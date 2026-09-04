package temporal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

func leaf(t *testing.T, entity string) addr.LeafID {
	t.Helper()
	l, err := addr.Descend(addr.KeyOf(addr.TenantFromUint(1), entity), 1)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}
	return l
}

// at returns a transaction identifier committed at the given wall reading.
func at(t *testing.T, wall int64) tx.TxID {
	t.Helper()
	return tx.TxID{HLC: hlc.Timestamp{Wall: wall}, Leaf: leaf(t, "writer"), Seq: 1}
}

func ptr[T any](v T) *T { return &v }

const (
	past = int64(1_000)
	now  = int64(9_000)
)

// TestLoneInstantBindsValidTimeOnly is the falsifier ADR-002 names in its
// Enforced-by header, and the single most important assertion in this package.
//
// A caller supplying one instant is asking a BUSINESS-time question. Binding
// that instant to the transaction axis as well excludes every backdated write,
// because a backdated write commits now and is valid from the past. That is the
// defect a sibling project shipped past a green suite, and it is the reason the
// rule is written down rather than left to a default.
func TestLoneInstantBindsValidTimeOnly(t *testing.T) {
	q := ResolveQualifiers(Query{ValidAt: ptr(past)}, now)

	if q.ValidAt == nil {
		t.Fatal("a supplied instant did not reach ValidAt")
	}
	if *q.ValidAt != past {
		t.Errorf("ValidAt = %d, want the supplied instant %d", *q.ValidAt, past)
	}
	if q.AsOf != nil {
		t.Fatalf("a lone instant reached AsOf as %v — the transaction axis must stay OPEN. "+
			"Binding one instant to both axes is the defect this rule exists to prevent", q.AsOf)
	}
}

// TestBackdatedWriteIsVisibleAtItsValidTime is the same rule stated as the
// behaviour an operator sees: a fact recorded today but true since last year
// must be returned by a query about last year.
func TestBackdatedWriteIsVisibleAtItsValidTime(t *testing.T) {
	// Valid from the distant past, committed just now.
	validFrom, validTo := past, Forever
	committed := at(t, now)

	q := ResolveQualifiers(Query{ValidAt: ptr(past + 10)}, now)
	if !Visible(validFrom, validTo, committed, q) {
		t.Fatal("a backdated write is not visible at its own valid time — " +
			"this is exactly the case that returns nothing when one instant binds both axes")
	}

	// And it is correctly invisible BEFORE its validity begins.
	qBefore := ResolveQualifiers(Query{ValidAt: ptr(past - 1)}, now)
	if Visible(validFrom, validTo, committed, qBefore) {
		t.Error("the write is visible before its validity begins")
	}
}

// TestTransactionTimeDefaultsToOpen checks that with no transaction qualifier,
// no datom is excluded on the transaction axis however late it was committed.
func TestTransactionTimeDefaultsToOpen(t *testing.T) {
	q := ResolveQualifiers(Query{ValidAt: ptr(now)}, now)
	for _, wall := range []int64{1, now, now * 1000} {
		if !Visible(0, Forever, at(t, wall), q) {
			t.Errorf("a datom committed at %d was excluded with no transaction qualifier", wall)
		}
	}
}

// TestDefaultsTableIsExhaustive walks all four combinations of supplied and
// omitted qualifiers and checks each resolves to the row ADR-002 rule 6 states.
func TestDefaultsTableIsExhaustive(t *testing.T) {
	u := at(t, 5_000)
	cases := []struct {
		name        string
		in          Query
		wantAsOf    *tx.TxID
		wantValidAt int64
	}{
		{"nothing", Query{}, nil, now},
		{"AS OF t", Query{ValidAt: ptr(past)}, nil, past},
		{"AS OF t TRANSACTION u", Query{ValidAt: ptr(past), AsOf: &u}, &u, past},
		{"TRANSACTION u", Query{AsOf: &u}, &u, now},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveQualifiers(c.in, now)
			if got.ValidAt == nil {
				t.Fatal("ValidAt is nil after resolution; it must always be bound")
			}
			if *got.ValidAt != c.wantValidAt {
				t.Errorf("ValidAt = %d, want %d", *got.ValidAt, c.wantValidAt)
			}
			switch {
			case c.wantAsOf == nil && got.AsOf != nil:
				t.Errorf("AsOf = %v, want it left open", got.AsOf)
			case c.wantAsOf != nil && got.AsOf == nil:
				t.Error("AsOf is open, want it bound to the supplied transaction")
			case c.wantAsOf != nil && got.AsOf.Compare(*c.wantAsOf) != 0:
				t.Errorf("AsOf = %v, want %v", got.AsOf, *c.wantAsOf)
			}
		})
	}
}

// TestVisibleRejectsOnEitherAxisIndependently checks neither condition is
// derived from the other: a datom failing only the business axis and one
// failing only the transaction axis are both excluded, and one passing both is
// included.
func TestVisibleRejectsOnEitherAxisIndependently(t *testing.T) {
	cutoff := at(t, 5_000)
	q := ResolveQualifiers(Query{ValidAt: ptr(int64(2_000)), AsOf: &cutoff}, now)

	early := at(t, 1_000) // within the transaction cutoff
	late := at(t, 8_000)  // beyond it

	if !Visible(1_000, 3_000, early, q) {
		t.Error("a datom passing both axes was excluded")
	}
	if Visible(4_000, 5_000, early, q) {
		t.Error("a datom failing only the BUSINESS axis was included")
	}
	if Visible(1_000, 3_000, late, q) {
		t.Error("a datom failing only the TRANSACTION axis was included")
	}
}

// TestIntervalIsHalfOpen checks the validity window is [From, To): a datom whose
// interval ends exactly at the query instant is excluded and one that starts
// exactly there is included, so adjacent intervals neither overlap nor gap.
func TestIntervalIsHalfOpen(t *testing.T) {
	q := ResolveQualifiers(Query{ValidAt: ptr(int64(500))}, now)

	if Visible(100, 500, at(t, 1), q) {
		t.Error("a datom whose interval ENDS at the query instant was included; the interval must be half-open")
	}
	if !Visible(500, 900, at(t, 1), q) {
		t.Error("a datom whose interval STARTS at the query instant was excluded")
	}
	// Two adjacent intervals must yield exactly one visible datom.
	visible := 0
	for _, iv := range []Interval{{From: 100, To: 500}, {From: 500, To: 900}} {
		if Visible(iv.From, iv.To, at(t, 1), q) {
			visible++
		}
	}
	if visible != 1 {
		t.Errorf("%d of two adjacent intervals are visible at one instant, want exactly 1", visible)
	}
}

// TestVisibleIsTheOnlyComparisonSite is a structural guard rather than a
// behavioural one.
//
// The defect this package exists to prevent was a CALLER passing one value into
// two parameters. Concentrating the comparison in one predicate is what makes
// that mistake reviewable in one place instead of at every call site, and this
// test is what keeps it concentrated as the codebase grows: no package outside
// this one may name both axes.
//
// It is coarse and could false-positive on an unrelated file mentioning both
// concepts. It is kept anyway — the defect it guards shipped in a sibling
// project past roughly 140 green tests, and a coarse guard that fires is worth
// more than an elegant one that does not exist.
func TestVisibleIsTheOnlyComparisonSite(t *testing.T) {
	root := filepath.Join("..", "..", "..", "internal")
	var offenders []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.Contains(filepath.ToSlash(path), "internal/core/temporal/") {
			return nil // this package is the sanctioned site
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		if strings.Contains(src, "AsOf") && strings.Contains(src, "ValidAt") {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("these files outside internal/core/temporal name BOTH time axes: %v\n"+
			"the two axes are compared in exactly one place, so that a caller passing one "+
			"instant into both parameters is reviewable in one file", offenders)
	}
}
