package leafstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
)

// leafOfThree builds a leaf sealed three times, holding superseded and retracted
// facts as well as current ones.
func leafOfThree(t *testing.T, dir string) {
	t.Helper()
	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sealEach(t, s,
		[]ports.Datom{
			assertion("planet-3", "mass", "first", 100),
			assertion("star-1", "codename", "yellow", 110),
		},
		[]ports.Datom{
			assertion("planet-3", "mass", "second", 200),
			retraction("star-1", "codename", "yellow", 210),
		},
		[]ports.Datom{
			assertion("planet-3", "radius", "6371", 300),
			assertion("planet-9", "mass", "1.9e27", 310),
		},
	)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// historyOf renders every entity's full history, so two leaves can be compared on
// everything they hold rather than on what a query would return.
func historyOf(t *testing.T, s *Store) map[string][]string {
	t.Helper()
	entities, err := s.Entities()
	if err != nil {
		t.Fatalf("Entities: %v", err)
	}
	out := make(map[string][]string, len(entities))
	for _, e := range entities {
		history, err := s.History(e)
		if err != nil {
			t.Fatalf("History(%q): %v", e, err)
		}
		out[e] = values(history)
	}
	return out
}

func sameHistory(a, b map[string][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, held := b[k]
		if !held || !equal(av, bv) {
			return false
		}
	}
	return true
}

func TestAnInterruptedCompactionDoesNotDuplicate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	leafOfThree(t, dir)

	// What the leaf answers before anything is compacted.
	before, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	want := historyOf(t, before)
	inputs := segmentFiles(t, dir)
	if err := before.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(inputs) != 3 {
		t.Fatalf("want 3 segments, got %d", len(inputs))
	}

	// Compact, then put the inputs BACK. ⚠ This is exactly the state a crash
	// between publishing the merged segment and removing its inputs leaves — and
	// it leaves it permanently, which is why an ordering alone is not enough.
	saved := make(map[string][]byte, len(inputs))
	for _, name := range inputs {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		saved[name] = raw
	}

	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Compact(ctx); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for name, raw := range saved {
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	if got := len(segmentFiles(t, dir)); got != 4 {
		t.Fatalf("the interrupted state holds %d segments, want the merge plus its 3 inputs", got)
	}

	after, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = after.Close() }()

	if got := historyOf(t, after); !sameHistory(got, want) {
		t.Fatalf("a leaf carrying an interrupted compaction answers differently:\n want %v\n  got %v\n"+
			"every datom is in two segments at once, and without deduplication every read of this "+
			"leaf counts each of them twice — forever, because nothing cleans the overlap up", want, got)
	}
}

func TestCompactionChangesNoAnswer(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	leafOfThree(t, dir)

	before, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// ⚠ HISTORY, not a query. A READ resolves to the latest visible datom, so
	// dropping every superseded fact would leave queries answering identically
	// while the past became unanswerable — which is what this store is for.
	want := historyOf(t, before)
	if err := before.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()
	if err := s.Compact(ctx); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if got := historyOf(t, s); !sameHistory(got, want) {
		t.Errorf("compaction changed what the leaf holds:\n want %v\n  got %v\n"+
			"compaction is a layout operation and drops no fact — removal is the purge's job",
			want, got)
	}
}

func TestCompactionPublishesBeforeRemoving(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	leafOfThree(t, dir)

	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	before := segmentFiles(t, dir)
	if err := s.Compact(ctx); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	after := segmentFiles(t, dir)

	if len(after) != 1 {
		t.Fatalf("after compaction the leaf holds %v, want one segment", after)
	}
	if s.Segments() != 1 {
		t.Errorf("the store still holds %d open segments", s.Segments())
	}
	// ⚠ The surviving file must be the NEW one. If an input had been kept and the
	// merge discarded, the count would still be wrong — but if the merge had been
	// written over an input's name, this is what would catch it.
	for _, old := range before {
		if after[0] == old {
			t.Errorf("the surviving segment %s is one of the inputs; the merge did not publish "+
				"under a name of its own", after[0])
		}
	}
	// And the facts are all still there.
	history, err := s.History("planet-3")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 3 {
		t.Errorf("planet-3 holds %d datoms after compaction, want 3", len(history))
	}
}

func TestTwoTransactionsAreNotOneDatom(t *testing.T) {
	// Identical in entity, attribute and value; different transactions. ⚠ Two
	// facts, and a deduplication keyed on anything less than full equality would
	// silently merge them.
	first := assertion("planet-3", "mass", "5", 100)
	second := assertion("planet-3", "mass", "5", 200)

	got := deduplicate([]ports.Datom{first, second})
	if len(got) != 2 {
		t.Errorf("two assertions from different transactions collapsed to %d; they are two facts, "+
			"and the identity must include the transaction", len(got))
	}

	// The same datom twice IS one datom.
	if got := deduplicate([]ports.Datom{first, first}); len(got) != 1 {
		t.Errorf("the same datom twice stayed as %d", len(got))
	}

	// ⚠ Equal in every field EXCEPT the value is not a repeat. That is a
	// malformed write, and hiding one of them would be this store repairing data
	// it does not understand.
	odd := first
	odd.Value = []byte("6")
	if got := deduplicate([]ports.Datom{first, odd}); len(got) != 2 {
		t.Errorf("two datoms disagreeing about their value collapsed to %d", len(got))
	}

	// A retraction and the assertion it withdraws differ in one bool.
	withdrawn := first
	withdrawn.Assert = false
	if got := deduplicate([]ports.Datom{first, withdrawn}); len(got) != 2 {
		t.Errorf("an assertion and its retraction collapsed to %d", len(got))
	}

	// As do two datoms differing only in their validity interval.
	moved := first
	moved.Valid = temporal.Interval{From: 5, To: 9}
	if got := deduplicate([]ports.Datom{first, moved}); len(got) != 2 {
		t.Errorf("two datoms differing only in validity collapsed to %d", len(got))
	}
}

func TestShouldCompactDoesNotCompact(t *testing.T) {
	dir := t.TempDir()
	leafOfThree(t, dir)

	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	segments := s.Segments()
	for i := 0; i < 3; i++ {
		if !s.ShouldCompact(Policy{MaxSegments: 2}) {
			t.Fatal("a leaf of three segments is not due under a bound of two")
		}
	}
	if s.Segments() != segments {
		t.Errorf("ShouldCompact compacted: %d became %d", segments, s.Segments())
	}
	if got := len(segmentFiles(t, dir)); got != segments {
		t.Errorf("ShouldCompact changed the directory: %d files, want %d", got, segments)
	}

	// A zero bound disables compaction rather than making everything due.
	if s.ShouldCompact(Policy{}) {
		t.Error("a policy with no segment bound reported a compaction due")
	}
}
