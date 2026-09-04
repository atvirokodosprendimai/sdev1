package tail

import (
	"math"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// latest is a bound above every identifier this fixture mints, for the cases
// that are about the watermark rather than about the bound.
var latest = tx.TxID{HLC: hlc.Timestamp{Wall: math.MaxInt64, Logical: math.MaxUint32}, Seq: math.MaxUint32}

func fill(t *testing.T, tl *Tail, w WriterToken, from, to uint32) {
	t.Helper()
	for seq := from; seq <= to; seq++ {
		id, datoms := entryFor(seq)
		if _, err := tl.Append(w, id, datoms); err != nil {
			t.Fatalf("Append %d: %v", seq, err)
		}
	}
}

func read(tl *Tail, s Snapshot) []uint32 {
	var out []uint32
	tl.Read(s, func(e Entry) bool {
		out = append(out, e.TxID.Seq)
		return true
	})
	return out
}

// TestSnapshotExcludesLaterTransactions checks the BOUND does work the watermark
// does not.
//
// Every entry here is published, so the watermark admits all of them. Only the
// transaction bound separates what the reader asked for from what happens to
// exist, which is why a snapshot is the pair and not either one alone.
func TestSnapshotExcludesLaterTransactions(t *testing.T) {
	tl := New()
	w := mustTakeWriter(t, tl)
	fill(t, tl, w, 1, 10)

	if got := tl.Watermark(); got != 10 {
		t.Fatalf("watermark = %d, want 10 — every entry must be published for this test to be about the bound", got)
	}

	boundAt5, _ := entryFor(5)
	s := tl.Snapshot(boundAt5)
	if s.Watermark != 10 {
		t.Fatalf("snapshot watermark = %d, want 10", s.Watermark)
	}

	got := read(tl, s)
	if len(got) != 5 {
		t.Fatalf("read %d entries under a bound of 5 with 10 published, want 5: %v", len(got), got)
	}
	for i, seq := range got {
		if seq != uint32(i+1) {
			t.Fatalf("position %d holds sequence %d, want %d", i, seq, i+1)
		}
	}

	// The bound is inclusive at its own identifier and exclusive above it.
	boundAt1, _ := entryFor(1)
	if got := read(tl, tl.Snapshot(boundAt1)); len(got) != 1 || got[0] != 1 {
		t.Errorf("a bound at the first entry read %v, want exactly [1]", got)
	}

	// A bound below everything reads nothing, rather than defaulting to all.
	below := tx.TxID{HLC: hlc.Timestamp{Wall: 0, Logical: 0}}
	if got := read(tl, tl.Snapshot(below)); len(got) != 0 {
		t.Errorf("a bound below every entry read %v, want nothing", got)
	}

	// And an unbounded read sees everything published.
	if got := read(tl, tl.Snapshot(latest)); len(got) != 10 {
		t.Errorf("an unbounded snapshot read %d entries, want 10", len(got))
	}
}

// TestReadIsBoundedByTheSnapshot checks the WATERMARK does work the bound does
// not: entries appended after the snapshot was taken are absent however long the
// read runs, and however high the bound is.
func TestReadIsBoundedByTheSnapshot(t *testing.T) {
	tl := New()
	w := mustTakeWriter(t, tl)
	fill(t, tl, w, 1, 6)

	// The bound admits everything, so only the watermark can exclude anything.
	s := tl.Snapshot(latest)
	before := read(tl, s)
	if len(before) != 6 {
		t.Fatalf("snapshot read %d entries, want 6", len(before))
	}

	fill(t, tl, w, 7, 40)

	after := read(tl, s)
	if len(after) != 6 {
		t.Fatalf("the same snapshot read %d entries after 34 more were appended, want 6 — "+
			"a snapshot must be a fixed prefix, not a live view", len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("the snapshot changed at position %d: %d became %d", i, before[i], after[i])
		}
	}

	// Repeated reads of one snapshot are identical, which is repeatable reads
	// falling out of the design rather than being built.
	for i := 0; i < 4; i++ {
		if got := read(tl, s); len(got) != 6 {
			t.Fatalf("read %d of the same snapshot returned %d entries, want 6", i, len(got))
		}
	}

	// A snapshot taken now does see them.
	if got := read(tl, tl.Snapshot(latest)); len(got) != 40 {
		t.Errorf("a fresh snapshot read %d entries, want 40", len(got))
	}
}
