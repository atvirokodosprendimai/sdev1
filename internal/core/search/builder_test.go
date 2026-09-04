package search

import (
	"context"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/crypt"
	"github.com/atvirokodosprendimai/sdev1/internal/core/lease"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/subscribe"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tail"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

func asserted(entity, attribute, value string, id tx.TxID) ports.Datom {
	return ports.Datom{
		Entity: entity, Attribute: attribute, Value: []byte(value),
		Valid: temporal.Interval{From: 0, To: temporal.Forever},
		TxID:  id, Assert: true,
	}
}

// logOf builds a tail holding the given transactions, and returns it with its
// published watermark.
func logOf(t *testing.T, entries ...[]ports.Datom) (*tail.Tail, tail.Watermark) {
	t.Helper()
	lg := tail.New()
	var w tail.Watermark
	for i, datoms := range entries {
		id := datoms[0].TxID
		got, err := lg.Append(lease.Epoch(i+1), id, datoms)
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		w = got
	}
	return lg, w
}

// answers renders an index's hits for a query, so two indexes can be compared by
// what they ANSWER rather than by how they are shaped.
func answers(t *testing.T, ks crypt.Keystore, ix *Index, text string) []string {
	t.Helper()
	res, err := ix.Search(ks, Query{Text: text, Limit: 20})
	if err != nil {
		t.Fatalf("Search(%q): %v", text, err)
	}
	out := make([]string, 0, len(res.Hits))
	for _, h := range res.Hits {
		out = append(out, h.Posting.Subject)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRebuildFromTheLogMatchesIncremental(t *testing.T) {
	ks := crypt.NewMemoryKeystore()
	log := [][]ports.Datom{
		{asserted("planet-3", "codename", "blue marble", txAt(100, 1))},
		{asserted("planet-4", "codename", "red planet", txAt(200, 1))},
		{
			asserted("star-1", "codename", "yellow dwarf", txAt(300, 1)),
			asserted("star-1", "orbits", "nothing at all", txAt(300, 2)),
		},
		{asserted("planet-3", "note", "a blue world", txAt(400, 1))},
	}

	// The write path: each datom indexed as it is asserted, which is what the
	// session does. It never sees a tail entry.
	incremental := NewIndex()
	for _, datoms := range log {
		for _, d := range datoms {
			for _, term := range TermsOf(d) {
				sealed, err := Seal(ks, Posting{Subject: d.Entity, Term: term, From: d.TxID})
				if err != nil {
					t.Fatalf("Seal: %v", err)
				}
				incremental.Add(term, sealed)
			}
		}
	}

	// The rebuild: the same facts reached by WALKING THE LOG through ADR-010's
	// subscription — a different driver, a cursor, and a sink.
	lg, watermark := logOf(t, log...)
	rebuilt := NewIndex()
	sub := &subscribe.Subscription{Sink: NewBuilder("search", ks, rebuilt)}
	if took := sub.Deliver(lg, watermark); took != len(log) {
		t.Fatalf("the builder accepted %d of %d entries", took, len(log))
	}

	if incremental.Terms() != rebuilt.Terms() {
		t.Errorf("rebuilt index holds %d terms, the incremental one %d",
			rebuilt.Terms(), incremental.Terms())
	}
	// ⚠ Compared by ANSWERS, not by internals: two indexes can hold the same
	// postings and rank differently, and it is the answer a caller gets.
	for _, query := range []string{"blue", "marble", "planet", "yellow", "nothing", "world"} {
		was, now := answers(t, ks, incremental, query), answers(t, ks, rebuilt, query)
		if !equalStrings(was, now) {
			t.Errorf("%q: incremental answered %v, rebuilt answered %v — an index rebuilt from "+
				"the log is not a projection of it, so losing one is data loss rather than a "+
				"performance event", query, was, now)
		}
	}
}

func TestRedeliveryDoesNotChangeTheAnswer(t *testing.T) {
	ks := crypt.NewMemoryKeystore()
	log := [][]ports.Datom{
		{asserted("planet-3", "codename", "blue marble", txAt(100, 1))},
		{asserted("planet-4", "codename", "blue giant", txAt(200, 1))},
	}
	lg, watermark := logOf(t, log...)

	ix := NewIndex()
	builder := NewBuilder("search", ks, ix)

	// ⚠ Delivery is AT LEAST ONCE. A sink that crashed after consuming and before
	// acknowledging sees the same entries again — modelled here by a second
	// subscription starting from a fresh cursor over the same log.
	first := &subscribe.Subscription{Sink: builder}
	if took := first.Deliver(lg, watermark); took != len(log) {
		t.Fatalf("first delivery accepted %d entries", took)
	}
	before := answers(t, ks, ix, "blue")
	terms := ix.Terms()
	scoresBefore := scoresFor(t, ks, ix, "blue marble")

	again := &subscribe.Subscription{Sink: builder}
	if took := again.Deliver(lg, watermark); took != len(log) {
		t.Fatalf("redelivery accepted %d entries", took)
	}

	if got := ix.Terms(); got != terms {
		t.Errorf("redelivery took the index from %d terms to %d", terms, got)
	}
	after := answers(t, ks, ix, "blue")
	if !equalStrings(before, after) {
		t.Errorf("redelivery changed the answer from %v to %v", before, after)
	}
	// ⚠ The sharper failure, and the one the subject list cannot see: duplicated
	// postings change term frequency, so the SCORES move while the same subjects
	// come back in the same order. The ranking is then wrong and every result
	// still looks right.
	for subject, was := range scoresBefore {
		now, held := scoresFor(t, ks, ix, "blue marble")[subject]
		if !held {
			t.Errorf("%s disappeared after redelivery", subject)
			continue
		}
		if now != was {
			t.Errorf("%s scored %.4f before redelivery and %.4f after; a posting has been "+
				"counted twice, so the ranking moved while the results still look right",
				subject, was, now)
		}
	}
}

// scoresFor maps each hit's subject to its score, so two runs can be compared on
// the number that ordering actually depends on.
func scoresFor(t *testing.T, ks crypt.Keystore, ix *Index, text string) map[string]float64 {
	t.Helper()
	res, err := ix.Search(ks, Query{Text: text, Limit: 20})
	if err != nil {
		t.Fatalf("Search(%q): %v", text, err)
	}
	out := make(map[string]float64, len(res.Hits))
	for _, h := range res.Hits {
		out[h.Posting.Subject] = h.Score
	}
	return out
}

// staleReader answers about the datoms as they are NOW, whatever the index still
// believes.
type staleReader struct{ datoms map[string][]ports.Datom }

func (s staleReader) Load(_ context.Context, entity string, _ ports.Snapshot) ([]ports.Datom, error) {
	return s.datoms[entity], nil
}

func (s staleReader) Attributes(_ context.Context, _ string, _ ports.Snapshot) ([]string, error) {
	return nil, nil
}

func TestACandidateIsConfirmedAgainstTheDatoms(t *testing.T) {
	ctx := context.Background()
	ks := crypt.NewMemoryKeystore()

	// All three subjects were indexed carrying "marble".
	log := [][]ports.Datom{
		{asserted("planet-3", "codename", "blue marble", txAt(100, 1))},
		{asserted("planet-9", "codename", "grey marble", txAt(200, 1))},
		{asserted("planet-5", "codename", "lost marble", txAt(250, 1))},
		{asserted("planet-2", "codename", "smooth marble", txAt(260, 1))},
	}
	lg, watermark := logOf(t, log...)
	ix := NewIndex()
	sub := &subscribe.Subscription{Sink: NewBuilder("search", ks, ix)}
	if took := sub.Deliver(lg, watermark); took != len(log) {
		t.Fatalf("the builder accepted %d entries", took)
	}

	res, err := ix.Search(ks, Query{Text: "marble", Limit: 20})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Hits) != 4 {
		t.Fatalf("the index returned %d candidates, want all four", len(res.Hits))
	}

	// ⚠ The datoms have moved on and the index has not — which is the normal
	// state of a derived index, not an exceptional one.
	//
	// planet-3 is unchanged. planet-9 was RENAMED. planet-5 was RETRACTED, and
	// that third case is the one that matters: a reader returns the visible
	// datoms INCLUDING the retraction, so the earlier assertion is still in the
	// list and still analyses to "marble". Confirming against the raw datoms
	// would confirm a fact that was withdrawn — which is exactly the failure this
	// function exists to prevent, arriving through the back door.
	// planet-2 is the fourth case: its prose is gone and all it carries now is a
	// REFERENCE to an entity that happens to be NAMED "marble". ⚠ A reference's
	// bytes are an entity name, not prose — analysing them as text would answer
	// "what links to this" with something that only looks like an answer, which
	// is what ADR-021 and ADR-023 both refuse. Confirming through the shared rule
	// drops it; confirming with a raw analysis keeps it.
	retracted := asserted("planet-5", "codename", "lost marble", txAt(400, 1))
	retracted.Assert = false
	reference := asserted("planet-2", "orbits", "marble", txAt(500, 1))
	reference.IsReference = true

	now := staleReader{datoms: map[string][]ports.Datom{
		"planet-3": {asserted("planet-3", "codename", "blue marble", txAt(100, 1))},
		"planet-9": {asserted("planet-9", "codename", "grey rock", txAt(300, 1))},
		"planet-5": {asserted("planet-5", "codename", "lost marble", txAt(250, 1)), retracted},
		"planet-2": {reference},
	}}

	confirmed, err := Confirm(ctx, now, ports.Snapshot{At: txAt(9999, 1), ValidAt: 1}, Analyze("marble"), res.Hits)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if len(confirmed) != 1 {
		kept := make([]string, len(confirmed))
		for i, c := range confirmed {
			kept[i] = c.Posting.Subject
		}
		t.Fatalf("Confirm kept %v of 4 candidates, want only planet-3 — a stale index is "+
			"returning a fact that is no longer there, indistinguishably from one that is", kept)
	}
	if confirmed[0].Posting.Subject != "planet-3" {
		t.Errorf("Confirm kept %q, want planet-3", confirmed[0].Posting.Subject)
	}
}

func TestARetractionIsNotIndexed(t *testing.T) {
	retracted := asserted("planet-3", "codename", "blue marble", txAt(200, 1))
	retracted.Assert = false
	reference := asserted("planet-3", "orbits", "star-1", txAt(300, 1))
	reference.IsReference = true

	// ⚠ Both exclusions live in TermsOf, so the write path and a rebuild cannot
	// disagree about them.
	if terms := TermsOf(retracted); len(terms) != 0 {
		t.Errorf("a retraction produced terms %v; the fact it withdraws must stop being findable", terms)
	}
	if terms := TermsOf(reference); len(terms) != 0 {
		t.Errorf("a reference produced terms %v; its bytes are an entity name, not prose", terms)
	}
	if terms := TermsOf(asserted("planet-3", "codename", "blue marble", txAt(100, 1))); len(terms) != 2 {
		t.Errorf("an ordinary assertion produced %v, want both words", terms)
	}
}
