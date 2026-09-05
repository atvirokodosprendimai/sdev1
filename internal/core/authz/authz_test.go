package authz

import (
	"context"
	"errors"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/leafstore"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

var acme = addr.TenantFromUint(7)

func at(wall int64) tx.TxID {
	return tx.TxID{HLC: hlc.Timestamp{Wall: wall}, Seq: 1}
}

// reader serves datoms for one principal, filtered by the snapshot the way a
// real reader does.
type reader struct{ datoms []ports.Datom }

func (r reader) Load(_ context.Context, entity string, snap ports.Snapshot) ([]ports.Datom, error) {
	q := snap.Query()
	var out []ports.Datom
	for _, d := range r.datoms {
		if d.Entity != entity {
			continue
		}
		if temporal.Visible(d.Valid.From, d.Valid.To, d.TxID, q) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (r reader) Attributes(_ context.Context, _ string, _ ports.Snapshot) ([]string, error) {
	return nil, nil
}

func snapshotAt(instant int64) ports.Snapshot {
	return ports.Snapshot{At: at(1 << 40), ValidAt: instant}
}

func mustGrant(t *testing.T, principal string, tenant addr.TenantID, can Capability, wall int64) ports.Datom {
	t.Helper()
	d, err := GrantDatom(principal, tenant, can, at(wall), wall)
	if err != nil {
		t.Fatalf("GrantDatom: %v", err)
	}
	return d
}

func mustRevoke(t *testing.T, principal string, tenant addr.TenantID, can Capability, wall int64) ports.Datom {
	t.Helper()
	d, err := RevokeDatom(principal, tenant, can, at(wall), wall)
	if err != nil {
		t.Fatalf("RevokeDatom: %v", err)
	}
	return d
}

func TestARevokedGrantCannotReadThePast(t *testing.T) {
	ctx := context.Background()

	// Granted at 100, revoked at 200.
	r := reader{datoms: []ports.Datom{
		mustGrant(t, "alice", acme, Read, 100),
		mustRevoke(t, "alice", acme, Read, 200),
	}}

	// ⚠ FIRST: the audit path still shows the grant was live at 150. That is what
	// makes this a test of the LEAK rather than of ordinary revocation — the
	// information is right there, and the decision must ignore it.
	history, err := History(ctx, r, "alice", snapshotAt(150))
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) == 0 || !history[0].Granted {
		t.Fatalf("the grant's history does not show it live at 150: %+v", history)
	}

	// NOW: the current set, at 300, after the revocation.
	now, err := Load(ctx, r, "alice", snapshotAt(300))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := now.Allow(acme, Read); !errors.Is(err, ErrNotGranted) {
		t.Fatalf("a revoked principal is still allowed: %v", err)
	}

	// ★ And the whole point: there is no call that permits the past. `Allow`
	// takes a tenant and a capability and nothing else, so a caller holding the
	// instant 150 has nowhere to put it. The set built from the PRESENT is the
	// only set there is.
	//
	// The closest a caller can come is building a stale set deliberately — and
	// even that is a decision they made about which present to load, not an
	// authorization against the past.
	stale, err := Load(ctx, r, "alice", snapshotAt(150))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := stale.Allow(acme, Read); err != nil {
		t.Fatalf("sanity: a set loaded at 150 should hold the grant, got %v", err)
	}
	if err := now.Allow(acme, Read); !errors.Is(err, ErrNotGranted) {
		t.Error("loading a stale set changed what the current one answers")
	}
}

func TestAnAbsentGrantSetRefuses(t *testing.T) {
	ctx := context.Background()

	// ⚠ An empty set must refuse. A default of permission fails open exactly when
	// the thing that would say no is unreachable.
	empty, err := Load(ctx, reader{}, "alice", snapshotAt(100))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := empty.Allow(acme, Read); !errors.Is(err, ErrNotGranted) {
		t.Errorf("an empty grant set allowed a read: %v", err)
	}

	// And a grant must not be accidentally broad: the wrong tenant and the wrong
	// capability both refuse, or Allow is really answering "does this principal
	// have any grant at all".
	held := reader{datoms: []ports.Datom{mustGrant(t, "alice", acme, Read, 100)}}
	set, err := Load(ctx, held, "alice", snapshotAt(200))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := set.Allow(acme, Read); err != nil {
		t.Fatalf("the held grant was refused: %v", err)
	}
	if err := set.Allow(addr.TenantFromUint(9), Read); !errors.Is(err, ErrNotGranted) {
		t.Errorf("a read grant for tenant 7 permitted tenant 9: %v", err)
	}
	if err := set.Allow(acme, Write); !errors.Is(err, ErrNotGranted) {
		t.Errorf("a read grant permitted a write: %v", err)
	}
}

func TestTheReservedTenantCannotBeGranted(t *testing.T) {
	ctx := context.Background()

	// Refused at the WRITING end.
	if _, err := GrantDatom("alice", SystemTenant, Read, at(100), 100); !errors.Is(err, ErrReservedTenant) {
		t.Errorf("GrantDatom for the system tenant = %v, want ErrReservedTenant", err)
	}

	// ⚠ And at the READING end. Refusing only where a grant is built leaves a
	// hand-made datom effective, and these are ordinary writes anyone with write
	// access to the system tenant could make.
	forged := ports.Datom{
		Entity:    "alice",
		Attribute: attributeFor(SystemTenant, Read),
		Value:     []byte(Read),
		Valid:     temporal.Interval{From: 0, To: temporal.Forever},
		TxID:      at(100), Assert: true,
	}
	set, err := Load(ctx, reader{datoms: []ports.Datom{forged}}, "alice", snapshotAt(200))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := set.Allow(SystemTenant, Read); !errors.Is(err, ErrReservedTenant) {
		t.Errorf("a hand-made grant for the system tenant was honoured: %v", err)
	}
}

func TestHistoryAnswersWhoHadAccessWithoutAuthorizing(t *testing.T) {
	ctx := context.Background()
	r := reader{datoms: []ports.Datom{
		mustGrant(t, "alice", acme, Read, 100),
		mustRevoke(t, "alice", acme, Read, 200),
	}}

	// "Who had access in March": asked AT 150, the answer is the grant alone —
	// the revocation is valid from 200 and had not happened yet.
	inMarch, err := History(ctx, r, "alice", snapshotAt(150))
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(inMarch) != 1 || !inMarch[0].Granted {
		t.Fatalf("History at 150 = %+v, want just the grant", inMarch)
	}
	if inMarch[0].Tenant != acme || inMarch[0].Capability != Read || inMarch[0].From != 100 {
		t.Errorf("the record does not name what was granted, or when: %+v", inMarch[0])
	}

	// Asked at 300, both records are visible: the grant is still valid and the
	// revocation has taken effect.
	today, err := History(ctx, r, "alice", snapshotAt(300))
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(today) != 2 || !today[0].Granted || today[1].Granted {
		t.Fatalf("History at 300 = %+v, want the grant then the revocation", today)
	}

	// ★ THE POINT: the audit path says alice HAD access at 150, and the decision
	// says she does not have it now. Both are true, and only the second one gates
	// a read — including a read ABOUT 150.
	now, err := Load(ctx, r, "alice", snapshotAt(300))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := now.Allow(acme, Read); !errors.Is(err, ErrNotGranted) {
		t.Errorf("the audit answer leaked into the decision: %v", err)
	}

	// And it returns RECORDS. There is no decision here to mistake for
	// permission, which is what keeps the audit question from becoming a way to
	// authorize.
}

func TestAGrantIsADatomAndRevocationIsARetraction(t *testing.T) {
	ctx := context.Background()

	grant := mustGrant(t, "alice", acme, Read, 100)
	revoke := mustRevoke(t, "alice", acme, Read, 200)
	if !grant.Assert {
		t.Error("a grant is not an assertion")
	}
	if revoke.Assert {
		t.Error("a revocation is not a retraction; 'this stopped applying' and 'this never " +
			"existed' are different facts and only the first is a revocation")
	}
	if grant.Attribute != revoke.Attribute {
		t.Errorf("a revocation names a different attribute (%q) from the grant (%q), so it "+
			"withdraws nothing", revoke.Attribute, grant.Attribute)
	}

	// ★ And it is an ORDINARY datom: it round-trips through a real leaf with no
	// special handling, which is the whole argument for grants being datoms.
	dir := t.TempDir()
	store, err := leafstore.Open(dir, addr.TenantFromUint(0).TenantSubtree())
	if err != nil {
		t.Fatalf("leafstore.Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Append(ctx, grant, revoke); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := store.Seal(ctx); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	set, err := Load(ctx, store, "alice", snapshotAt(300))
	if err != nil {
		t.Fatalf("Load from a leaf: %v", err)
	}
	if err := set.Allow(acme, Read); !errors.Is(err, ErrNotGranted) {
		t.Errorf("the revocation did not survive a round trip through a leaf: %v", err)
	}
}
