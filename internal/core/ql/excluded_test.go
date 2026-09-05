package ql

import (
	"testing"
)

// shapeWithAllThreeLegKinds is the fixture these tests share: one required, one
// optional and one excluded leg, in one query.
//
// ★ All three in ONE row is the point. An excluded leg binding nothing is only
// distinguishable from an optional leg binding UNBOUND when both are present —
// with either alone, the two rules render identically.
const shapeWithAllThreeLegKinds = `MATCH SHAPE LIKE planet-7
  REQUIRE mass
  OPTIONAL nickname
  WITHOUT retired
  SIMILARITY jaccard >= 0.8`

// TestAnExcludedLegDropsASubjectThatHasIt checks the mirror of a required leg.
func TestAnExcludedLegDropsASubjectThatHasIt(t *testing.T) {
	legs := parseShapeQuery(t, shapeWithAllThreeLegKinds).Legs
	if len(legs) != 3 {
		t.Fatalf("parsed %d legs, want 3: %+v", len(legs), legs)
	}
	if legs[2].Kind != LegExcluded || legs[2].Attribute != "retired" {
		t.Fatalf("third leg = %+v, want retired excluded", legs[2])
	}

	// Carries the excluded attribute → dropped, however well it matches the rest.
	// ⚠ It matches BOTH other legs, so nothing but the exclusion can explain the
	// drop.
	if _, ok := BuildRow("planet-9", legs, map[string]string{
		"mass": "5", "nickname": "Nine", "retired": "yes",
	}); ok {
		t.Error("a subject carrying the excluded attribute was kept; an excluded leg is the " +
			"mirror of a required one")
	}

	// Lacks it → kept, with the other legs bound as before.
	row, ok := BuildRow("planet-3", legs, map[string]string{"mass": "5", "nickname": "Terra"})
	if !ok {
		t.Fatal("a subject lacking the excluded attribute was dropped; the exclusion is a " +
			"filter, and this subject passes it")
	}
	if row.Subject != "planet-3" {
		t.Errorf("subject = %q", row.Subject)
	}

	// ⚠ Both halves. A rule of "always drop" and a rule of "never drop" each pass
	// one of these two assertions.
	if _, ok := BuildRow("planet-4", legs, map[string]string{"nickname": "Mars"}); ok {
		t.Error("a subject missing the REQUIRED leg was kept; adding an excluded leg must not " +
			"change what the other kinds mean")
	}
}

// TestAnExcludedLegBindsNothing is ADR-036 rule 6.
func TestAnExcludedLegBindsNothing(t *testing.T) {
	legs := parseShapeQuery(t, shapeWithAllThreeLegKinds).Legs

	// The subject has `mass`, no `nickname`, and no `retired`.
	row, ok := BuildRow("planet-3", legs, map[string]string{"mass": "5"})
	if !ok {
		t.Fatal("the row was dropped")
	}

	// Three legs, TWO bindings: the excluded one contributes none.
	if len(row.Bindings) != 2 {
		t.Fatalf("row has %d bindings for 3 legs, want 2: %v\n"+
			"An excluded leg is a filter — its answer is already carried by the row existing "+
			"at all.", len(row.Bindings), row.Bindings)
	}

	// ★ THE DISTINCTION, and it needs both legs in one row to be visible. The
	// OPTIONAL leg matched nothing and IS present as unbound; the EXCLUDED leg
	// also matched nothing and is ABSENT. Binding the excluded one as unbound
	// would make "had no value to give" and "was required to have none" identical.
	nickname, held := row.Get("nickname")
	if !held {
		t.Fatal("the optional leg is missing; it must bind UNBOUND rather than disappear")
	}
	if nickname.IsBound() {
		t.Errorf("the optional leg is bound to %v, want unbound", nickname)
	}
	if _, held := row.Get("retired"); held {
		t.Error("the excluded leg produced a binding. It would be indistinguishable from the " +
			"optional leg above, which says the opposite thing.")
	}

	// A bound optional leg still binds, so the rule above is about the excluded
	// leg rather than about this row being unusual.
	full, ok := BuildRow("planet-3", legs, map[string]string{"mass": "5", "nickname": "Terra"})
	if !ok {
		t.Fatal("the row was dropped")
	}
	if len(full.Bindings) != 2 {
		t.Errorf("row has %d bindings, want 2", len(full.Bindings))
	}
	if got, _ := full.Get("nickname"); !got.IsBound() {
		t.Error("the optional leg did not bind its value")
	}
}

// TestAnExcludedLegCarriesItsOwnTimeClause checks the per-leg qualifier reaches
// the third leg kind too.
//
// ★ ADR-011's central property is that time is a CLAUSE, which is why it can
// attach per leg. A leg kind that could not take one would be the first
// exception to the thing that record exists to hold — and "did not have a
// nickname AS OF 1900" is a real question, not a hypothetical.
func TestAnExcludedLegCarriesItsOwnTimeClause(t *testing.T) {
	q := parseShapeQuery(t, `MATCH SHAPE LIKE planet-7
  REQUIRE mass
  WITHOUT retired AS OF 1600000000 TRANSACTION 1650000000
  SIMILARITY jaccard >= 0.8`)

	if len(q.Legs) != 2 {
		t.Fatalf("parsed %d legs, want 2", len(q.Legs))
	}
	excluded := q.Legs[1]
	if excluded.Kind != LegExcluded {
		t.Fatalf("second leg kind = %v, want excluded", excluded.Kind)
	}
	if excluded.Time.ValidAt == nil || *excluded.Time.ValidAt != 1600000000 {
		t.Errorf("the leg's AS OF = %v, want 1600000000", excluded.Time.ValidAt)
	}
	if excluded.Time.AsOf == nil || excluded.Time.AsOf.HLC.Wall != 1650000000 {
		t.Errorf("the leg's TRANSACTION = %v, want 1650000000", excluded.Time.AsOf)
	}

	// ⚠ It landed on the LEG and not on the query. The two are different
	// questions, and a parser that hoisted it would silently apply one leg's
	// qualifier to the whole shape.
	if q.Time.ValidAt != nil || q.Time.AsOf != nil {
		t.Errorf("the query carries %+v; the qualifier belongs to the leg that wrote it", q.Time)
	}

	// Several excluded legs, each with its own clause or none.
	many := parseShapeQuery(t, `MATCH SHAPE LIKE planet-7
  WITHOUT retired AS OF 1600000000, decommissioned
  SIMILARITY jaccard >= 0.8`)
	if len(many.Legs) != 2 {
		t.Fatalf("parsed %d legs, want 2", len(many.Legs))
	}
	if many.Legs[0].Time.ValidAt == nil || many.Legs[1].Time.ValidAt != nil {
		t.Errorf("the clause did not stay on the leg that wrote it: %+v", many.Legs)
	}

	// The kind names itself, so a diagnostic can say which rule dropped a row.
	if got := LegExcluded.String(); got != "excluded" {
		t.Errorf("LegExcluded.String() = %q, want \"excluded\"", got)
	}
}
