package search

import (
	"errors"
	"reflect"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/crypt"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// txAt builds a transaction identifier for a test.
func txAt(wall int64, seq uint32) tx.TxID {
	return tx.TxID{
		HLC:  hlc.Timestamp{Wall: wall},
		Leaf: addr.LeafID{Depth: 1},
		Seq:  seq,
	}
}

// sealFor seals one posting for a subject against a REAL keystore.
func sealFor(t *testing.T, ks crypt.Keystore, subject, term string) Sealed {
	t.Helper()
	s, err := Seal(ks, Posting{Subject: subject, Term: Term(term), From: txAt(100, 1)})
	if err != nil {
		t.Fatalf("Seal(%q, %q): %v", subject, term, err)
	}
	return s
}

// TestAShreddedSubjectHasNoPostings is ADR-021's falsifier.
//
// ⚠ It runs against the REAL keystore and calls its real Shred. A fake that
// returned "key missing" would only prove this code handles a missing key; it
// would not prove that shredding a subject PRODUCES that condition, which is the
// entire claim.
//
// ⚠ It also seals postings for TWO subjects and asserts the survivor is still
// returned. Without that, a Visible that dropped everything — or one that
// returned nothing at all — would pass.
func TestAShreddedSubjectHasNoPostings(t *testing.T) {
	ks := crypt.NewMemoryKeystore()

	doomed := sealFor(t, ks, "planet-7", "dwarf")
	survivor := sealFor(t, ks, "planet-9", "dwarf")

	// Before: both are visible. Without this the test could pass against an
	// implementation that never returned anything.
	before := Visible(ks, []Sealed{doomed, survivor})
	if len(before) != 2 {
		t.Fatalf("before shredding, Visible returned %d postings, want 2", len(before))
	}

	id, ok := ks.Resolve("planet-7")
	if !ok {
		t.Fatal("the subject has no handle to shred")
	}
	if _, err := ks.Shred(id, "erasure request"); err != nil {
		t.Fatalf("Shred: %v", err)
	}

	after := Visible(ks, []Sealed{doomed, survivor})
	if len(after) != 1 {
		t.Fatalf("after shredding, Visible returned %d postings, want 1 — an index that still "+
			"answers for an erased subject is the fastest way in the system to find the person "+
			"somebody asked to erase", len(after))
	}
	if after[0].Subject != "planet-9" {
		t.Fatalf("the surviving posting is for %q, want planet-9", after[0].Subject)
	}
}

// TestAnUndecryptablePostingIsNotCounted checks the erased subject is absent from
// every number the result exposes, not merely from the values.
func TestAnUndecryptablePostingIsNotCounted(t *testing.T) {
	ks := crypt.NewMemoryKeystore()
	doomed := sealFor(t, ks, "planet-7", "dwarf")
	survivor := sealFor(t, ks, "planet-9", "dwarf")

	id, _ := ks.Resolve("planet-7")
	if _, err := ks.Shred(id, "erasure request"); err != nil {
		t.Fatalf("Shred: %v", err)
	}

	got := Visible(ks, []Sealed{doomed, survivor})
	if len(got) != 1 {
		t.Fatalf("Visible returned %d postings, want 1", len(got))
	}

	// The answer must be identical to one where the erased posting was never
	// offered at all. If the two differ in any observable way, the difference IS
	// the disclosure.
	never := Visible(ks, []Sealed{survivor})
	if !reflect.DeepEqual(got, never) {
		t.Fatalf("a shredded posting is observable: with it %v, without it %v", got, never)
	}

	// Match must not leak it through the limit either.
	matched, err := Match(ks, []Sealed{doomed, survivor}, 10)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if len(matched) != 1 {
		t.Fatalf("Match returned %d postings, want 1", len(matched))
	}
}

// TestVisibleReportsNoWithheldCount asserts the property structurally.
//
// ★ A function that cannot return the number cannot leak it. Checking the
// signature rather than a behaviour is what makes this survive somebody deciding
// that a withheld count would be useful diagnostics.
func TestVisibleReportsNoWithheldCount(t *testing.T) {
	ft := reflect.TypeOf(Visible)
	if ft.NumOut() != 1 {
		t.Fatalf("Visible returns %d values, want exactly 1 (the postings): a second return is "+
			"almost certainly a count of what was dropped, which is an oracle for the existence "+
			"of erased subjects", ft.NumOut())
	}
	if got, want := ft.Out(0), reflect.TypeOf([]Posting(nil)); got != want {
		t.Fatalf("Visible returns %v, want %v", got, want)
	}
}

// TestAnalyzeIsDeterministic checks an index and a query analyse text alike.
func TestAnalyzeIsDeterministic(t *testing.T) {
	cases := []struct {
		text string
		want []Term
	}{
		{"Red Dwarf", []Term{"red", "dwarf"}},
		{"red-dwarf", []Term{"red", "dwarf"}},
		{"  RED   dwarf!! ", []Term{"red", "dwarf"}},
		{"planet7", []Term{"planet7"}},
		{"", nil},
		{"!!!", nil},
	}
	for _, tc := range cases {
		got := Analyze(tc.text)
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("Analyze(%q) = %v, want %v", tc.text, got, tc.want)
		}
		// Same input, same output — an index and a query must agree or a term is
		// stored in one form and looked up in another, and the search silently
		// returns nothing.
		if again := Analyze(tc.text); !reflect.DeepEqual(again, got) {
			t.Fatalf("Analyze(%q) is not deterministic: %v then %v", tc.text, got, again)
		}
	}
}

// TestFacetCountsAreExact checks the counts are the true counts.
func TestFacetCountsAreExact(t *testing.T) {
	subjects := []string{"a", "b", "c", "d", "e"}
	values := map[string]string{
		"a": "terrestrial",
		"b": "gas giant",
		"c": "terrestrial",
		"d": "terrestrial",
		// "e" has no class at all, and must be counted under no value.
	}

	got, err := Facet("class", subjects, values, 10)
	if err != nil {
		t.Fatalf("Facet: %v", err)
	}
	want := FacetResult{
		Attribute: "class",
		Counts:    []Count{{Value: "terrestrial", N: 3}, {Value: "gas giant", N: 1}},
		Total:     5,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Facet = %+v, want %+v", got, want)
	}

	// ⚠ A subject with no value is not a subject with an empty value. Merging
	// them is the same conflation an unbound binding exists to prevent.
	for _, c := range got.Counts {
		if c.Value == "" {
			t.Fatal(`a subject with no value was counted under ""; "has no class" and "has an empty class" are different facts`)
		}
	}
}

// TestAFacetOverItsBoundIsRefused checks the refusal is total.
func TestAFacetOverItsBoundIsRefused(t *testing.T) {
	subjects := []string{"a", "b", "c", "d", "e"}
	values := map[string]string{"a": "x", "b": "x", "c": "y", "d": "y", "e": "z"}

	got, err := Facet("class", subjects, values, 3)
	if !errors.Is(err, ErrFacetTooWide) {
		t.Fatalf("Facet over its bound returned %v, want ErrFacetTooWide — an unlabelled estimate "+
			"is a lie, and a facet count is exactly the number somebody reconciles against a total", err)
	}
	// ⚠ No partial counts. A truncated breakdown looks like a complete one.
	if len(got.Counts) != 0 || got.Total != 0 {
		t.Fatalf("a refused facet still returned counts: %+v", got)
	}

	// Positive control: the same data inside the bound counts normally, so the
	// refusal above is about the bound and not about the input.
	if _, err := Facet("class", subjects, values, 5); err != nil {
		t.Fatalf("the same facet within its bound was refused: %v", err)
	}

	if _, err := Facet("class", subjects, values, 0); !errors.Is(err, ErrNoFacetBound) {
		t.Fatalf("a facet with no bound returned %v, want ErrNoFacetBound", err)
	}
}

// TestASearchWithoutALimitIsRefused checks a search is always bounded.
func TestASearchWithoutALimitIsRefused(t *testing.T) {
	ks := crypt.NewMemoryKeystore()
	sealed := []Sealed{sealFor(t, ks, "planet-7", "dwarf")}

	for _, limit := range []int{0, -1} {
		if _, err := Match(ks, sealed, limit); !errors.Is(err, ErrNoLimit) {
			t.Fatalf("Match with limit %d returned %v, want ErrNoLimit — an unranked, unlimited "+
				"search is a full scan with extra steps", limit, err)
		}
	}

	got, err := Match(ks, sealed, 1)
	if err != nil {
		t.Fatalf("Match with a positive limit was refused: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Match returned %d postings, want 1", len(got))
	}
}

// TestPostingCarriesItsTransactionRange checks a posting survives the round trip
// with the range that makes a time-qualified search possible.
func TestPostingCarriesItsTransactionRange(t *testing.T) {
	ks := crypt.NewMemoryKeystore()
	until := txAt(500, 9)

	for _, p := range []Posting{
		{Subject: "planet-7", Term: "dwarf", From: txAt(100, 1)},
		{Subject: "planet-7", Term: "dwarf", From: txAt(100, 1), Until: &until},
	} {
		sealed, err := Seal(ks, p)
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}
		got, err := Open(ks, sealed)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if got.Subject != p.Subject || got.Term != p.Term {
			t.Fatalf("round trip changed the posting: %+v, want %+v", got, p)
		}
		if got.From.Compare(p.From) != 0 {
			t.Fatalf("From did not survive: %v, want %v", got.From, p.From)
		}
		switch {
		case p.Until == nil && got.Until != nil:
			t.Fatalf("a still-current posting came back retracted at %v", *got.Until)
		case p.Until != nil && got.Until == nil:
			t.Fatal("a retracted posting came back as still current — an index that forgets when a " +
				"posting stopped holding cannot answer a time-qualified search")
		case p.Until != nil && got.Until.Compare(*p.Until) != 0:
			t.Fatalf("Until did not survive: %v, want %v", *got.Until, *p.Until)
		}
	}
}
