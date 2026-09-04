package session

import (
	"errors"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
)

// newTestSession returns a session on a clock the test advances by hand, so
// transaction identifiers are predictable.
func newTestSession() (*Session, func(int64)) {
	wall := int64(1000)
	s := New(addr.TenantFromUint(7), func() int64 { return wall })
	return s, func(to int64) { wall = to }
}

func mustRun(t *testing.T, s *Session, src string) Result {
	t.Helper()
	r, err := s.Run(src)
	if err != nil {
		t.Fatalf("Run(%q): %v", src, err)
	}
	return r
}

// TestAssertThenSelectReadsItBack is the loop this whole task exists to close.
// TestWhereFiltersForACaller is this defect stated where a caller meets it.
//
// ⚠ `Select.Where` parsed from ADR-011 onward and was evaluated nowhere, so
// `SELECT * FROM planet-7 WHERE name = "Mars"` returned every attribute of
// planet-7. No error, no warning, and no way to tell that the question asked was
// not the question answered.
func TestWhereFiltersForACaller(t *testing.T) {
	s, _ := newTestSession()
	mustRun(t, s, `ASSERT planet-7 name = "Terra"`)
	mustRun(t, s, `ASSERT planet-7 class = "terrestrial"`)

	narrow := mustRun(t, s, `SELECT * FROM planet-7 WHERE name = "Mars"`)
	if len(narrow.Rows) != 0 {
		t.Errorf("a predicate that matches nothing returned %d rows; the clause is being ignored",
			len(narrow.Rows))
	}

	// The control: a predicate that DOES match must still return the rows, or
	// "filters everything" would pass the assertion above.
	wide := mustRun(t, s, `SELECT * FROM planet-7 WHERE name = "Terra"`)
	if len(wide.Rows) != 2 {
		t.Errorf("a predicate that matches returned %d rows, want both attributes", len(wide.Rows))
	}

	// The published guide's shape: the predicate names an attribute the
	// projection does not return.
	guide := mustRun(t, s, `SELECT name FROM planet-7 WHERE class = "terrestrial"`)
	if len(guide.Rows) != 1 || guide.Rows[0].Attribute != "name" {
		t.Errorf("SELECT name ... WHERE class = ... returned %+v, want just the name row", guide.Rows)
	}
	miss := mustRun(t, s, `SELECT name FROM planet-7 WHERE class = "gas giant"`)
	if len(miss.Rows) != 0 {
		t.Errorf("the same query with a non-matching class returned %d rows", len(miss.Rows))
	}
}

func TestAssertThenSelectReadsItBack(t *testing.T) {
	s, _ := newTestSession()

	wrote := mustRun(t, s, `ASSERT planet-7 mass = 5972`)
	if wrote.Wrote == nil {
		t.Fatal("ASSERT produced no datom")
	}

	read := mustRun(t, s, `SELECT * FROM planet-7`)
	if len(read.Rows) != 1 {
		t.Fatalf("SELECT returned %d rows, want 1: %+v", len(read.Rows), read.Rows)
	}
	got := read.Rows[0]
	if got.Entity != "planet-7" || got.Attribute != "mass" || got.Value != "5972" {
		t.Fatalf("read back %+v", got)
	}

	// A projection narrows it, and an unknown attribute returns nothing rather
	// than everything.
	narrow := mustRun(t, s, `SELECT mass FROM planet-7`)
	if len(narrow.Rows) != 1 {
		t.Fatalf("SELECT mass returned %d rows", len(narrow.Rows))
	}
	none := mustRun(t, s, `SELECT radius FROM planet-7`)
	if len(none.Rows) != 0 {
		t.Fatalf("SELECT of an unwritten attribute returned %+v", none.Rows)
	}
}

// TestAReadAtAPastInstantDoesNotSeeALaterWrite checks the two axes are honoured
// rather than reimplemented.
func TestAReadAtAPastInstantDoesNotSeeALaterWrite(t *testing.T) {
	s, _ := newTestSession()
	mustRun(t, s, `ASSERT planet-7 mass = 5972 VALID FROM 500`)

	before := mustRun(t, s, `SELECT * FROM planet-7 AS OF 100`)
	if len(before.Rows) != 0 {
		t.Fatalf("a read at instant 100 saw a fact valid from 500: %+v", before.Rows)
	}

	after := mustRun(t, s, `SELECT * FROM planet-7 AS OF 600`)
	if len(after.Rows) != 1 {
		t.Fatalf("a read at instant 600 did not see a fact valid from 500: %+v", after.Rows)
	}

	// A closed interval ends.
	mustRun(t, s, `ASSERT planet-9 class = 'giant' VALID FROM 100 TO 200`)
	inside := mustRun(t, s, `SELECT * FROM planet-9 AS OF 150`)
	if len(inside.Rows) != 1 {
		t.Fatalf("a read inside the interval saw nothing: %+v", inside.Rows)
	}
	outside := mustRun(t, s, `SELECT * FROM planet-9 AS OF 250`)
	if len(outside.Rows) != 0 {
		t.Fatalf("a read after the interval still saw the fact: %+v", outside.Rows)
	}
}

// TestTheSessionAssignsTransactionTime checks the axis a caller cannot set is
// assigned here and strictly increases.
func TestTheSessionAssignsTransactionTime(t *testing.T) {
	s, _ := newTestSession()

	first := mustRun(t, s, `ASSERT planet-7 mass = 5972`)
	second := mustRun(t, s, `ASSERT planet-7 radius = 6371`)

	if first.Wrote.TxID.Compare(second.Wrote.TxID) >= 0 {
		t.Fatalf("transaction identifiers did not increase: %v then %v",
			first.Wrote.TxID, second.Wrote.TxID)
	}

	// ⚠ Nothing a caller wrote reaches it. The statement below states a VALID
	// instant far in the past; the transaction must not follow it there.
	past := mustRun(t, s, `ASSERT planet-7 note = 'backdated' VALID FROM 1`)
	if past.Wrote.Valid.From != 1 {
		t.Fatalf("the stated validity was not honoured: %v", past.Wrote.Valid)
	}
	if past.Wrote.TxID.Compare(second.Wrote.TxID) <= 0 {
		t.Fatalf("a backdated VALID clause dragged the TRANSACTION identifier backwards: %v after %v.\n"+
			"Valid time is a claim about the world; transaction time is the record of when we were told, "+
			"and a caller who can move it makes every historical answer a claim rather than a record.",
			past.Wrote.TxID, second.Wrote.TxID)
	}

	// And a read does not consume one.
	before := s.minter.Mint()
	mustRun(t, s, `SELECT * FROM planet-7`)
	after := s.minter.Mint()
	if after.Seq != before.Seq+1 {
		t.Fatalf("a SELECT consumed %d transaction identifiers; reads must not mint",
			after.Seq-before.Seq-1)
	}
}

// TestAssertThenSearchFindsIt checks the index is fed on the WRITE path.
//
// ⚠ It touches the index only through ASSERT and SEARCH. An index populated by
// the test itself would prove nothing about what a write actually does.
func TestAssertThenSearchFindsIt(t *testing.T) {
	s, _ := newTestSession()

	mustRun(t, s, `ASSERT planet-7 description = 'a red dwarf star'`)
	mustRun(t, s, `ASSERT planet-9 description = 'a blue nebula'`)
	mustRun(t, s, `ASSERT planet-7 class = 'dwarf'`)
	mustRun(t, s, `ASSERT planet-9 class = 'nebula'`)

	found := mustRun(t, s, `SEARCH 'red' IN description LIMIT 10`)
	if len(found.Hits) != 1 {
		t.Fatalf("SEARCH 'red' returned %d hits, want 1: %+v", len(found.Hits), found.Hits)
	}
	if found.Hits[0].Posting.Subject != "planet-7" {
		t.Fatalf("SEARCH found %q", found.Hits[0].Posting.Subject)
	}

	// Facets come from the written values, not from a separate source.
	faceted := mustRun(t, s, `SEARCH 'a' IN description FACET BY class LIMIT 10`)
	if len(faceted.Facets) != 1 {
		t.Fatalf("expected one facet, got %+v", faceted.Facets)
	}
	if faceted.Facets[0].Total != 2 {
		t.Fatalf("facet total is %d, want 2", faceted.Facets[0].Total)
	}
}

// TestRetractedFactIsNotReturned checks a retraction suppresses the value while
// remaining a datom.
func TestRetractedFactIsNotReturned(t *testing.T) {
	s, _ := newTestSession()

	mustRun(t, s, `ASSERT planet-7 class = 'terrestrial'`)
	if got := mustRun(t, s, `SELECT * FROM planet-7`); len(got.Rows) != 1 {
		t.Fatalf("the asserted fact was not readable: %+v", got.Rows)
	}

	retracted := mustRun(t, s, `RETRACT planet-7 class = 'terrestrial'`)
	if retracted.Wrote == nil {
		t.Fatal("RETRACT produced no datom")
	}
	// ⚠ A retraction IS a datom, never an absence — "this stopped being true" and
	// "this was never recorded" are different facts.
	if retracted.Wrote.Assert {
		t.Fatal("a retraction was recorded as an assertion")
	}

	after := mustRun(t, s, `SELECT * FROM planet-7`)
	if len(after.Rows) != 0 {
		t.Fatalf("a retracted fact was still returned: %+v", after.Rows)
	}
}

// TestTraverseWalksLinksAtOneInstant is the session-level check that a hierarchy
// can be walked as it stood at an instant.
//
// ⚠ The graph is RESHAPED between the two instants: planet-7 orbits star-1 from
// the start, and star-1's own link changes. A walk at the earlier instant must
// return the earlier shape, not a mixture.
func TestTraverseWalksLinksAtOneInstant(t *testing.T) {
	s, _ := newTestSession()

	mustRun(t, s, `ASSERT planet-7 orbits = ->star-1 VALID FROM 100`)
	mustRun(t, s, `ASSERT star-1 within = ->cluster-old VALID FROM 100 TO 200`)
	mustRun(t, s, `ASSERT star-1 within = ->cluster-new VALID FROM 200`)

	early := mustRun(t, s, `TRAVERSE planet-7 DEPTH 2 AS OF 150`)
	names := reached(early)
	if len(names) != 2 || names[0] != "star-1" || names[1] != "cluster-old" {
		t.Fatalf("a walk at 150 reached %v, want [star-1 cluster-old] — an answer containing "+
			"cluster-new is a tree assembled from two instants", names)
	}

	late := mustRun(t, s, `TRAVERSE planet-7 DEPTH 2 AS OF 250`)
	if n := reached(late); len(n) != 2 || n[1] != "cluster-new" {
		t.Fatalf("a walk at 250 reached %v, want the later shape", n)
	}

	// The depth bound applies through the language too.
	shallow := mustRun(t, s, `TRAVERSE planet-7 DEPTH 1 AS OF 150`)
	if n := reached(shallow); len(n) != 1 || n[0] != "star-1" {
		t.Fatalf("DEPTH 1 reached %v, want [star-1]", n)
	}
}

// TestOnlyReferencesAreFollowed checks a literal that spells an entity name is
// not an edge.
func TestOnlyReferencesAreFollowed(t *testing.T) {
	s, _ := newTestSession()

	mustRun(t, s, `ASSERT planet-7 note = 'star-1'`)   // a literal
	mustRun(t, s, `ASSERT planet-7 orbits = ->star-1`) // a reference
	mustRun(t, s, `ASSERT star-1 name = 'Sol'`)

	got := mustRun(t, s, `TRAVERSE planet-7 DEPTH 1`)
	if n := reached(got); len(n) != 1 || n[0] != "star-1" {
		t.Fatalf("walked to %v, want [star-1] once — a literal spelling an entity name must not "+
			"be followed, or every identifier-looking value becomes an accidental edge", n)
	}

	// A retracted link stops being followed, while remaining a datom.
	mustRun(t, s, `RETRACT planet-7 orbits = ->star-1`)
	after := mustRun(t, s, `TRAVERSE planet-7 DEPTH 1`)
	if n := reached(after); len(n) != 0 {
		t.Fatalf("a retracted link was still followed: %v", n)
	}
}

// reached renders a traversal result's entities in order.
func reached(r Result) []string {
	out := make([]string, 0, len(r.Reached))
	for _, p := range r.Reached {
		out = append(out, p.Entity)
	}
	return out
}

// TestUnsupportedStatementIsNamed checks an unimplemented statement says so.
func TestUnsupportedStatementIsNamed(t *testing.T) {
	s, _ := newTestSession()

	_, err := s.Run(`MATCH SHAPE LIKE planet-7 REQUIRE mass SIMILARITY jaccard >= 0.8`)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("MATCH SHAPE returned %v, want ErrUnsupported — an empty result would read as "+
			"'nothing matched', which is the wrong answer to 'this is not implemented'", err)
	}
	if !strings.Contains(err.Error(), "similarity") {
		t.Fatalf("the refusal %q does not say what is missing", err)
	}
}
