package search

import (
	"errors"
	"reflect"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/crypt"
)

// doc is one subject and the text indexed for it.
type doc struct {
	subject string
	text    string
}

// build indexes every doc, in the order given.
func build(t *testing.T, ks crypt.Keystore, docs []doc) *Index {
	t.Helper()
	ix := NewIndex()
	for _, d := range docs {
		for _, term := range Analyze(d.text) {
			s, err := Seal(ks, Posting{Subject: d.subject, Term: term, From: txAt(100, 1)})
			if err != nil {
				t.Fatalf("Seal(%q): %v", d.subject, err)
			}
			ix.Add(term, s)
		}
	}
	return ix
}

// corpus is shaped so that rarity and term-count pull in DIFFERENT directions.
//
// ⚠ planet-3 deliberately does NOT contain "star". An earlier version gave it
// "blue star", which made it the only subject matching BOTH query terms — so it
// won on term count and `TestRarerTermsScoreHigher` passed with every term
// weighted equally. The mutant that flattened the weights survived, which is how
// the weak fixture was found. With "star" removed, the rare-term subject matches
// exactly as many query terms as the common-term ones and can only win on rarity.
var corpus = []doc{
	{subject: "planet-1", text: "red dwarf star"},
	{subject: "planet-2", text: "red giant star"},
	{subject: "planet-3", text: "blue nebula"},
	{subject: "planet-4", text: "dwarf dwarf dwarf"},
}

// TestIndexBuiltTwiceAnswersIdentically checks the index is a projection rather
// than a thing with a history.
func TestIndexBuiltTwiceAnswersIdentically(t *testing.T) {
	ks := crypt.NewMemoryKeystore()

	first, err := build(t, ks, corpus).Search(ks, Query{Text: "red star", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	second, err := build(t, ks, corpus).Search(ks, Query{Text: "red star", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if !sameOrder(first, second) {
		t.Fatalf("two indexes built from the same postings answered differently:\n %v\n %v",
			order(first), order(second))
	}
}

// TestRankingDoesNotDependOnMapOrder is the determinism guard.
//
// ⚠ Go randomises map iteration deliberately. A ranking that reads a map in
// iteration order returns a different answer per process, and every test that
// runs inside ONE binary passes anyway — which is exactly how a per-process
// random seed survived in placement until it was found by hand.
//
// Two shapes are needed and neither is sufficient alone: repeating the query
// exercises iteration randomness within a process, and building the index in a
// different insertion order exercises the accumulation.
func TestRankingDoesNotDependOnMapOrder(t *testing.T) {
	ks := crypt.NewMemoryKeystore()
	ix := build(t, ks, corpus)

	want, err := ix.Search(ks, Query{Text: "red star dwarf", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for i := 0; i < 200; i++ {
		got, err := ix.Search(ks, Query{Text: "red star dwarf", Limit: 10})
		if err != nil {
			t.Fatalf("Search iteration %d: %v", i, err)
		}
		if !sameOrder(want, got) {
			t.Fatalf("iteration %d returned a different order:\n want %v\n got  %v",
				i, order(want), order(got))
		}
	}

	// Reversed insertion order must not change the answer either.
	reversed := make([]doc, len(corpus))
	for i, d := range corpus {
		reversed[len(corpus)-1-i] = d
	}
	other, err := build(t, ks, reversed).Search(ks, Query{Text: "red star dwarf", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !sameOrder(want, other) {
		t.Fatalf("insertion order changed the ranking:\n want %v\n got  %v", order(want), order(other))
	}
}

// TestAShreddedSubjectIsAbsentFromRankedResults checks erasure reaches a ranked
// answer, not only a raw posting list.
func TestAShreddedSubjectIsAbsentFromRankedResults(t *testing.T) {
	ks := crypt.NewMemoryKeystore()
	ix := build(t, ks, corpus)

	before, err := ix.Search(ks, Query{Text: "red", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(before.Hits) != 2 {
		t.Fatalf("before shredding, %d hits for \"red\", want 2 (planet-1 and planet-2)", len(before.Hits))
	}

	id, ok := ks.Resolve("planet-1")
	if !ok {
		t.Fatal("planet-1 has no handle to shred")
	}
	if _, err := ks.Shred(id, "erasure request"); err != nil {
		t.Fatalf("Shred: %v", err)
	}

	after, err := ix.Search(ks, Query{Text: "red", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(after.Hits) != 1 {
		t.Fatalf("after shredding, %d hits, want 1 — a ranked search that still answers for an "+
			"erased subject is the fastest lookup in the system for the person somebody asked to erase",
			len(after.Hits))
	}
	if after.Hits[0].Posting.Subject != "planet-2" {
		t.Fatalf("the surviving hit is %q, want planet-2", after.Hits[0].Posting.Subject)
	}
}

// TestAShreddedSubjectIsAbsentFromFacets checks the erasure survives the
// COUNTING path too.
//
// ⚠ Faceting the candidates and filtering for display afterwards is the natural
// implementation, and it leaks an erased subject as a number — the same
// disclosure arriving through a different door.
func TestAShreddedSubjectIsAbsentFromFacets(t *testing.T) {
	ks := crypt.NewMemoryKeystore()
	ix := build(t, ks, corpus)

	values := map[string]map[string]string{
		"class": {
			"planet-1": "dwarf",
			"planet-2": "giant",
		},
	}
	q := Query{Text: "red", Limit: 10, Facets: []string{"class"}, FacetBound: 100, Values: values}

	before, err := ix.Search(ks, q)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(before.Facets) != 1 || before.Facets[0].Total != 2 {
		t.Fatalf("before shredding, facets are %+v, want one facet totalling 2", before.Facets)
	}

	id, _ := ks.Resolve("planet-1")
	if _, err := ks.Shred(id, "erasure request"); err != nil {
		t.Fatalf("Shred: %v", err)
	}

	after, err := ix.Search(ks, q)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(after.Facets) != 1 {
		t.Fatalf("after shredding, %d facets, want 1", len(after.Facets))
	}
	f := after.Facets[0]
	if f.Total != 1 {
		t.Fatalf("the facet total is %d, want 1 — an erased subject counted in a facet is disclosed "+
			"as a number even though it never appears as a result", f.Total)
	}
	for _, c := range f.Counts {
		if c.Value == "dwarf" {
			t.Fatalf("the erased subject's value %q is still counted: %+v", c.Value, f.Counts)
		}
	}
}

// TestResultHonoursTheLimit checks a search returns at most what was asked for,
// and returns the best of them.
func TestResultHonoursTheLimit(t *testing.T) {
	ks := crypt.NewMemoryKeystore()
	ix := build(t, ks, corpus)

	full, err := ix.Search(ks, Query{Text: "star dwarf", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(full.Hits) < 2 {
		t.Fatalf("the corpus produced %d hits, too few to test a limit", len(full.Hits))
	}

	capped, err := ix.Search(ks, Query{Text: "star dwarf", Limit: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(capped.Hits) != 2 {
		t.Fatalf("limit 2 returned %d hits", len(capped.Hits))
	}
	// The kept ones must be the top of the unlimited answer, not an arbitrary two.
	for i, h := range capped.Hits {
		if h.Posting.Subject != full.Hits[i].Posting.Subject {
			t.Fatalf("limited hit %d is %q, want %q — the limit must keep the BEST results",
				i, h.Posting.Subject, full.Hits[i].Posting.Subject)
		}
	}

	if _, err := ix.Search(ks, Query{Text: "star", Limit: 0}); !errors.Is(err, ErrNoLimit) {
		t.Fatalf("a search with no limit returned %v, want ErrNoLimit", err)
	}
}

// TestRarerTermsScoreHigher checks ranking does the one thing that makes it
// worth having.
func TestRarerTermsScoreHigher(t *testing.T) {
	ks := crypt.NewMemoryKeystore()
	// "star" is in two documents; "blue" is in one, and that one has no "star".
	ix := build(t, ks, corpus)

	got, err := ix.Search(ks, Query{Text: "blue star", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got.Hits) < 2 {
		t.Fatalf("%d hits, too few for rarity and term count to disagree", len(got.Hits))
	}
	// ⚠ Every hit here matches exactly ONE of the two query terms, so term count
	// cannot decide the winner and only the weighting can. Assert the rare-term
	// subject scores STRICTLY higher, not merely that it sorts first — equal
	// scores would sort planet-1 first anyway, and a strict comparison is what
	// tells a real weighting from a tie broken alphabetically.
	for _, h := range got.Hits[1:] {
		if got.Hits[0].Score <= h.Score {
			t.Fatalf("the top hit %q scores %v, not strictly above %q at %v — with every term "+
				"weighted equally these tie, and the order is then alphabetical rather than ranked",
				got.Hits[0].Posting.Subject, got.Hits[0].Score, h.Posting.Subject, h.Score)
		}
	}
	if got.Hits[0].Posting.Subject != "planet-3" {
		t.Fatalf("the top hit is %q, want planet-3 — it is the only subject matching the RARE term, "+
			"and if a common term outranks a rare one the ranking is doing nothing useful\nhits: %v",
			got.Hits[0].Posting.Subject, order(got))
	}
}

// TestAnEmptyQueryIsRefused checks a query with no terms does not match
// everything.
func TestAnEmptyQueryIsRefused(t *testing.T) {
	ks := crypt.NewMemoryKeystore()
	ix := build(t, ks, corpus)

	for _, text := range []string{"", "   ", "!!!", "--"} {
		if _, err := ix.Search(ks, Query{Text: text, Limit: 10}); !errors.Is(err, ErrNoQuery) {
			t.Fatalf("Search(%q) returned %v, want ErrNoQuery — an empty query matching the whole "+
				"corpus is the most expensive thing a caller can ask for by accident", text, err)
		}
	}
}

// order renders a result's subjects for a failure message.
func order(r Result) []string {
	out := make([]string, 0, len(r.Hits))
	for _, h := range r.Hits {
		out = append(out, h.Posting.Subject)
	}
	return out
}

// sameOrder compares two results by subject order and score.
func sameOrder(a, b Result) bool {
	if !reflect.DeepEqual(order(a), order(b)) {
		return false
	}
	for i := range a.Hits {
		if a.Hits[i].Score != b.Hits[i].Score {
			return false
		}
	}
	return true
}
