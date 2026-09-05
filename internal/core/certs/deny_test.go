package certs_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/authz"
	"github.com/atvirokodosprendimai/sdev1/internal/core/certs"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/leafstore"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// denials is a real reserved-tenant leaf holding denial datoms.
//
// ★ A real `leafstore`, not a fake reader. The whole reason a denial is a datom
// rather than a CRL is that it travels the path everything else does; a fake here
// would prove the wiring against something the wiring does not use.
type denials struct {
	store *leafstore.Store
	seq   uint32
}

func newDenials(t *testing.T) *denials {
	t.Helper()

	store, err := leafstore.Open(t.TempDir(), authz.SystemTenant.TenantSubtree())
	if err != nil {
		t.Fatalf("opening the denial leaf: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &denials{store: store}
}

func (d *denials) append(t *testing.T, datom ports.Datom) {
	t.Helper()

	ctx := context.Background()
	if err := d.store.Append(ctx, datom); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := d.store.Seal(ctx); err != nil {
		t.Fatalf("Seal: %v", err)
	}
}

func (d *denials) id(at int64) tx.TxID {
	d.seq++
	return tx.TxID{HLC: hlc.Timestamp{Wall: at}, Seq: d.seq}
}

// TestADenialNamesASerialNotAPrincipal is ADR-047 rule 8.
//
// ★ Two certificates for the SAME principal. Denying one must not touch the
// other — that is the whole reason for denying by serial: a leaked key is one
// certificate, and its holder has to be able to carry on with a new one.
func TestADenialNamesASerialNotAPrincipal(t *testing.T) {
	const now = int64(1_700_000_000)

	a := mint(t, "cluster-ca")
	leaked := issue(t, a, "reader-1")
	replacement := issue(t, a, "reader-1")

	if leaked.Serial == replacement.Serial {
		t.Fatal("two issuances produced the same serial, so this test cannot distinguish them")
	}

	d := newDenials(t)
	datom, err := certs.DenyDatom(leaked.Serial, leaked.NotAfter, "key on a lost laptop", d.id(now), now)
	if err != nil {
		t.Fatalf("DenyDatom: %v", err)
	}
	d.append(t, datom)

	ctx := context.Background()
	denied, reason, err := certs.Denied(ctx, d.store, leaked.Serial, now+1)
	if err != nil {
		t.Fatalf("Denied(leaked): %v", err)
	}
	if !denied {
		t.Error("the denied certificate is not denied")
	}
	if reason != "key on a lost laptop" {
		t.Errorf("reason = %q, want the one that was written", reason)
	}

	// ★★ The same principal's OTHER certificate is unaffected.
	denied, _, err = certs.Denied(ctx, d.store, replacement.Serial, now+1)
	if err != nil {
		t.Fatalf("Denied(replacement): %v", err)
	}
	if denied {
		t.Error("denying one certificate denied another belonging to the same principal.\n" +
			"A leaked key is ONE certificate. Denying the name would punish the legitimate " +
			"holder and block their reissuance — and \"this person may no longer read\" is " +
			"a grant retraction, which says it better.")
	}
}

// TestUndenyingIsARetraction is ADR-047 rule 7's withdrawal half.
func TestUndenyingIsARetraction(t *testing.T) {
	const (
		denied  = int64(1_700_000_000)
		allowed = denied + 500
	)

	a := mint(t, "cluster-ca")
	i := issue(t, a, "reader-1")
	d := newDenials(t)

	deny, err := certs.DenyDatom(i.Serial, i.NotAfter, "suspected", d.id(denied), denied)
	if err != nil {
		t.Fatalf("DenyDatom: %v", err)
	}
	d.append(t, deny)

	allow, err := certs.AllowDatom(i.Serial, i.NotAfter, d.id(allowed), allowed)
	if err != nil {
		t.Fatalf("AllowDatom: %v", err)
	}
	// ⚠ Asserted on Assert:false, not on absence. A deletion would also make the
	// serial leave the set — and would lose the fact that it was ever denied,
	// which is what an auditor comes looking for.
	if allow.Assert {
		t.Error("AllowDatom asserts rather than retracts, so the denial is being deleted " +
			"rather than withdrawn")
	}
	if allow.Entity != deny.Entity || allow.Attribute != deny.Attribute {
		t.Errorf("the retraction names %s/%s, the denial named %s/%s",
			allow.Entity, allow.Attribute, deny.Entity, deny.Attribute)
	}
	d.append(t, allow)

	ctx := context.Background()
	still, _, err := certs.Denied(ctx, d.store, i.Serial, allowed+1)
	if err != nil {
		t.Fatalf("Denied: %v", err)
	}
	if still {
		t.Error("the certificate is still denied after the denial was withdrawn")
	}

	// ★ And it WAS denied, in the past. The history survives, which is the
	// difference between a retraction and a delete.
	was, _, err := certs.Denied(ctx, d.store, i.Serial, allowed-1)
	if err != nil {
		t.Fatalf("Denied(before the withdrawal): %v", err)
	}
	if !was {
		t.Error("the record that it was denied is gone, so the retraction behaved as a delete")
	}
}

// TestADenialOutlivesTheCertificateItDenies is ADR-047 rule 10.
//
// ⚠ A denial swept early silently RE-ADMITS the certificate, and nothing about
// that looks like a change in access. The expiry is in the certificate, so the
// retention rule is derivable rather than guessed — and the denial must carry it.
func TestADenialOutlivesTheCertificateItDenies(t *testing.T) {
	const now = int64(1_700_000_000)

	a := mint(t, "cluster-ca")
	i := issue(t, a, "reader-1")
	d := newDenials(t)

	datom, err := certs.DenyDatom(i.Serial, i.NotAfter, "leaked", d.id(now), now)
	if err != nil {
		t.Fatalf("DenyDatom: %v", err)
	}
	if datom.Valid.To != i.NotAfter.Unix() {
		t.Errorf("the denial ends at %d, the certificate at %d.\n"+
			"Ending sooner re-admits the certificate; ending later fills the reserved "+
			"tenant with facts about certificates nobody can present.",
			datom.Valid.To, i.NotAfter.Unix())
	}
	d.append(t, datom)

	ctx := context.Background()

	// In force for the certificate's whole remaining life.
	for _, at := range []int64{now + 1, i.NotAfter.Unix() - 1} {
		denied, _, err := certs.Denied(ctx, d.store, i.Serial, at)
		if err != nil {
			t.Fatalf("Denied(%d): %v", at, err)
		}
		if !denied {
			t.Errorf("the certificate is not denied at %d, which is before it expires", at)
		}
	}

	// ⚠ And a denial with no end is REFUSED, rather than defaulting to forever.
	if _, err := certs.DenyDatom(i.Serial, time.Time{}, "", d.id(now), now); !errors.Is(err, certs.ErrNoSerial) {
		t.Errorf("a denial with no expiry = %v, want a refusal", err)
	}
	if _, err := certs.DenyDatom("", i.NotAfter, "", d.id(now), now); !errors.Is(err, certs.ErrNoSerial) {
		t.Errorf("a denial naming no serial = %v, want ErrNoSerial", err)
	}
}

// TestASerialIsReadTheSameWayItIsWritten guards the one spelling.
//
// ⚠ A denial written as one representation and looked up as another denies
// nothing — silently, because both sides look right. This is the failure that
// leaves an operator certain a key is revoked when it is not.
func TestASerialIsReadTheSameWayItIsWritten(t *testing.T) {
	a := mint(t, "cluster-ca")
	i := issue(t, a, "reader-1")

	for _, spelling := range []string{
		i.Serial,
		strings.ToUpper(i.Serial),
		strings.TrimLeft(i.Serial, "0"),
		insertEvery(i.Serial, ":", 2),
	} {
		got, err := certs.ParseSerial(spelling)
		if err != nil {
			t.Fatalf("ParseSerial(%q): %v", spelling, err)
		}
		if got != i.Serial {
			t.Errorf("ParseSerial(%q) = %q, want %q — a denial written one way and read "+
				"another denies nothing", spelling, got, i.Serial)
		}
	}

	if _, err := certs.ParseSerial("not-hex"); !errors.Is(err, certs.ErrNoSerial) {
		t.Errorf("ParseSerial of a non-serial = %v, want ErrNoSerial", err)
	}
	if _, err := certs.ParseSerial(""); !errors.Is(err, certs.ErrNoSerial) {
		t.Errorf("ParseSerial of nothing = %v, want ErrNoSerial", err)
	}
}

// TestADenialLivesInItsOwnEntitySpace guards the `cert:` prefix.
//
// ⚠ This test exists because a mutant SURVIVED without it. Dropping the prefix —
// filing a denial under the bare serial — round-trips perfectly, because writing
// and reading go through the same function, so every other test here passes. What
// it loses is the separation: a denial would then share a namespace with ordinary
// entities, and the denial set would become a function of whatever else happens to
// be stored in the reserved tenant.
//
// ★ A property that only shows up when something OTHER than this API writes.
func TestADenialLivesInItsOwnEntitySpace(t *testing.T) {
	const now = int64(1_700_000_000)

	a := mint(t, "cluster-ca")
	i := issue(t, a, "reader-1")
	d := newDenials(t)

	// The prefix is part of the contract, not an internal detail.
	if got := certs.Entity(i.Serial); got == i.Serial {
		t.Fatalf("Entity(%s) = %s, which is the bare serial", i.Serial, got)
	}

	// A datom filed under the BARE serial, as some unrelated writer might.
	d.append(t, ports.Datom{
		Entity:    i.Serial,
		Attribute: "certificate:denied",
		Value:     []byte("not a real denial"),
		Valid:     forever(now),
		TxID:      d.id(now),
		Assert:    true,
	})

	denied, _, err := certs.Denied(context.Background(), d.store, i.Serial, now+1)
	if err != nil {
		t.Fatalf("Denied: %v", err)
	}
	if denied {
		t.Error("a datom stored under the bare serial denied the certificate.\n" +
			"Denials must live in their own entity space, or the denial set becomes a " +
			"function of whatever else is in the reserved tenant.")
	}
}

// forever is a validity interval with no stated end.
func forever(from int64) temporal.Interval {
	return temporal.Interval{From: from, To: temporal.Forever}
}

func insertEvery(s, sep string, n int) string {
	var out strings.Builder
	for i, r := range s {
		if i > 0 && i%n == 0 {
			out.WriteString(sep)
		}
		out.WriteRune(r)
	}
	return out.String()
}
