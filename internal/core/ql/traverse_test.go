package ql

import (
	"strings"
	"testing"
)

func mustTraverse(t *testing.T, src string) *Traverse {
	t.Helper()
	stmt, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	tr, ok := stmt.(*Traverse)
	if !ok {
		t.Fatalf("Parse(%q) returned %T, want *Traverse", src, stmt)
	}
	return tr
}

// TestTraverseCarriesOneTimeClause checks a walk cannot be asked to mix instants.
//
// ⚠ A shape query carries a qualifier per LEG, and the symmetry is genuinely
// tempting here. It must not exist: a per-hop clause would let a caller REQUEST
// a tree assembled from several instants — turning the defect ADR-023 exists to
// prevent into a documented feature.
func TestTraverseCarriesOneTimeClause(t *testing.T) {
	bare := mustTraverse(t, `TRAVERSE planet-7 DEPTH 3`)
	if bare.Root != "planet-7" || bare.Depth != 3 {
		t.Fatalf("parsed to %+v", bare)
	}
	if bare.Time.ValidAt != nil || bare.Time.AsOf != nil {
		t.Fatalf("a bare traversal carried a qualifier: %+v", bare.Time)
	}

	qualified := mustTraverse(t, `TRAVERSE planet-7 DEPTH 3 AS OF 1700000000 TRANSACTION 1700000500`)
	if qualified.Time.ValidAt == nil || *qualified.Time.ValidAt != 1700000000 {
		t.Fatalf("valid time is %v", qualified.Time.ValidAt)
	}
	if qualified.Time.AsOf == nil || qualified.Time.AsOf.HLC.Wall != 1700000500 {
		t.Fatalf("transaction bound is %v", qualified.Time.AsOf)
	}
	// It is the SAME clause type, so it resolves through the same table.
	if resolved := qualified.Time.Resolve(1700009999); resolved.ValidAt == nil {
		t.Fatal("Resolve left valid time unbound")
	}

	// ⚠ A per-hop qualifier must not parse. There is nowhere in the grammar to
	// put one, and this asserts that rather than assuming it.
	for _, src := range []string{
		`TRAVERSE planet-7 AS OF 100 DEPTH 3`,
		`TRAVERSE planet-7 DEPTH 3 AS OF 100 DEPTH 2`,
		`TRAVERSE planet-7 DEPTH 3 HOP AS OF 200`,
	} {
		if stmt, err := Parse(src); err == nil {
			t.Fatalf("Parse(%q) succeeded and returned %+v; a traversal must carry one clause for "+
				"the whole walk, or a caller can ask for a tree that never existed", src, stmt)
		}
	}
}

// TestTraverseRequiresADepth checks the bound is required, matching the walk.
func TestTraverseRequiresADepth(t *testing.T) {
	for _, src := range []string{
		`TRAVERSE planet-7`,
		`TRAVERSE planet-7 DEPTH 0`,
		`TRAVERSE planet-7 DEPTH -2`,
		`TRAVERSE planet-7 DEPTH deep`,
	} {
		_, err := Parse(src)
		if err == nil {
			t.Fatalf("Parse(%q) succeeded; an unbounded walk over a graph the caller does not "+
				"control is a scan they did not ask for", src)
		}
		if !strings.Contains(err.Error(), ErrNoDepth.Error()) {
			t.Fatalf("Parse(%q) failed with %q, which does not say the depth is the problem", src, err)
		}
	}
	if _, err := Parse(`TRAVERSE planet-7 DEPTH 1`); err != nil {
		t.Fatalf("a traversal with a positive depth was refused: %v", err)
	}
}

// TestAReferenceLiteralIsNotAString is the escape from inferring edges by shape.
//
// ⚠ It compares a reference and a quoted string with the SAME characters. A test
// using different text would pass even against a parser that ignored the marker.
func TestAReferenceLiteralIsNotAString(t *testing.T) {
	ref := mustWrite(t, `ASSERT planet-7 orbits = ->star-1`)
	if !ref.ValueIsReference {
		t.Fatal("`->star-1` did not parse as a reference")
	}
	if ref.Value != "star-1" {
		t.Fatalf("the reference target is %q, want star-1 — the marker is not part of the name", ref.Value)
	}
	if ref.ValueIsNumber {
		t.Fatal("a reference reported itself as a number")
	}

	// The same characters as text are NOT a reference.
	text := mustWrite(t, `ASSERT planet-7 note = 'star-1'`)
	if text.ValueIsReference {
		t.Fatal("a quoted string parsed as a reference; every value spelling an entity name would " +
			"become an accidental edge")
	}
	if text.Value != "star-1" {
		t.Fatalf("the literal is %q", text.Value)
	}

	// And a bare identifier that happens to name an entity is still a literal.
	bare := mustWrite(t, `ASSERT planet-7 note = star-1`)
	if bare.ValueIsReference {
		t.Fatal("a bare identifier parsed as a reference")
	}

	// A reference works on RETRACT too, and with a validity clause — the shared
	// tail means the two value forms cannot diverge.
	retract := mustWrite(t, `RETRACT planet-7 orbits = ->star-1 VALID FROM 500`)
	if !retract.ValueIsReference || retract.Op != OpRetract {
		t.Fatalf("retracting a link parsed to %+v", retract)
	}
	if retract.From == nil || *retract.From != 500 {
		t.Fatalf("the validity clause after a reference was lost: %v", retract.From)
	}

	// ⚠ And a reference must not smuggle in a transaction qualifier through the
	// second code path.
	if _, err := Parse(`ASSERT planet-7 orbits = ->star-1 TRANSACTION 900`); err == nil {
		t.Fatal("a reference write accepted a TRANSACTION clause; the two value paths share one " +
			"tail precisely so they cannot diverge on this")
	}

	// The marker needs a name after it.
	if _, err := Parse(`ASSERT planet-7 orbits = ->`); err == nil {
		t.Fatal("a dangling reference marker parsed")
	}
}
