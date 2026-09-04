package tail

import (
	"errors"
	"fmt"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/lease"
)

// leafOne is the leaf every fencing test contends over.
func leafOne() addr.LeafID {
	var l addr.LeafID
	l.Prefix[0] = 0x0F
	l.Depth = 1
	return l
}

// grant takes the next epoch for the leaf, the way a handover would.
func grant(t *testing.T, r *lease.Registry, holder string) lease.Epoch {
	t.Helper()
	l, err := r.Grant(leafOne(), holder)
	if err != nil {
		t.Fatalf("Grant(%q): %v", holder, err)
	}
	return l.Epoch
}

func appendAt(t *testing.T, tl *Tail, e lease.Epoch, seq uint32) error {
	t.Helper()
	id, datoms := entryFor(seq)
	_, err := tl.Append(e, id, datoms)
	return err
}

// TestFencedOutWriterCannotAppend is the falsifier ADR-009 names in its
// Enforced-by header.
//
// ⚠ The old writer appends AFTER the new one has already published. A test that
// supersedes and then immediately writes proves less than it looks: the
// dangerous case is a writer that was PAUSED across the handover, does not know
// it, and wakes up believing it still owns the leaf. That is the ordering that
// actually happens in production, and it is the one here.
func TestFencedOutWriterCannotAppend(t *testing.T) {
	tl := New()
	reg := lease.NewRegistry()

	old := grant(t, reg, "node-a")
	if err := appendAt(t, tl, old, 1); err != nil {
		t.Fatalf("the original writer was refused: %v", err)
	}

	// node-a pauses here — a garbage collection, a stalled disk, a partition. It
	// is told nothing, and it does not release anything.
	fresh := grant(t, reg, "node-b")
	if fresh <= old {
		t.Fatalf("the handover took epoch %d, not above %d", fresh, old)
	}
	for seq := uint32(2); seq <= 5; seq++ {
		if err := appendAt(t, tl, fresh, seq); err != nil {
			t.Fatalf("the new writer was refused at %d: %v", seq, err)
		}
	}

	// node-a wakes up and writes, still believing it owns the leaf.
	err := appendAt(t, tl, old, 99)
	if !errors.Is(err, ErrFencedOut) {
		t.Fatalf("a superseded writer appended: error = %v, want ErrFencedOut — the refusal "+
			"must happen at the TAIL, because a writer that checks its own leadership and then "+
			"writes has a window between the two in which it loses it", err)
	}

	// Nothing it tried is in the tail.
	if got := tl.Watermark(); got != 5 {
		t.Errorf("watermark = %d, want 5 — the fenced-out write must not have landed", got)
	}
	seen := map[uint32]bool{}
	tl.Walk(tl.Watermark(), func(e Entry) bool { seen[e.TxID.Seq] = true; return true })
	if seen[99] {
		t.Error("the fenced-out writer's entry is in the tail")
	}
}

// TestTailRefusesAnEpochItHasSeenPast checks the refusal is permanent.
//
// A leaf that has moved on must not be draggable back by whichever writer was
// slowest — including after the holder of the newer epoch has itself gone quiet.
func TestTailRefusesAnEpochItHasSeenPast(t *testing.T) {
	tl := New()

	if err := appendAt(t, tl, lease.Epoch(5), 1); err != nil {
		t.Fatalf("the first epoch was refused: %v", err)
	}
	if got := tl.Epoch(); got != 5 {
		t.Fatalf("the tail records epoch %d, want 5", got)
	}

	// The epoch-5 holder stops. Nothing renews, nothing heartbeats, and the
	// refusal still stands — because it is about recency of CLAIM, not liveness.
	for _, older := range []lease.Epoch{lease.NoEpoch, 1, 2, 3, 4} {
		if err := appendAt(t, tl, older, 50); !errors.Is(err, ErrFencedOut) {
			t.Errorf("epoch %d after 5 was seen: error = %v, want ErrFencedOut", older, err)
		}
	}

	// Equal still works: one holder appends many times under one epoch.
	if err := appendAt(t, tl, lease.Epoch(5), 2); err != nil {
		t.Errorf("the holder's own epoch was refused on a later append: %v", err)
	}
	// And newer still works.
	if err := appendAt(t, tl, lease.Epoch(6), 3); err != nil {
		t.Errorf("a newer epoch was refused: %v", err)
	}
	// After which 5 is gone too.
	if err := appendAt(t, tl, lease.Epoch(5), 51); !errors.Is(err, ErrFencedOut) {
		t.Errorf("epoch 5 after 6 was seen: error = %v, want ErrFencedOut", err)
	}
	if got := tl.Watermark(); got != 3 {
		t.Errorf("watermark = %d, want 3", got)
	}
}

// TestLeafAcceptsWritesAfterHandover is the failure `FAILURES.md` catalogued as
// unrecoverable and open, now closed.
//
// Before ADR-009 the writer token was handed out once and never taken back, so a
// leaf whose writer process died was readable forever and writable never — a
// permanent outage caused by a transient fault.
func TestLeafAcceptsWritesAfterHandover(t *testing.T) {
	tl := New()
	reg := lease.NewRegistry()

	first := grant(t, reg, "node-a")
	for seq := uint32(1); seq <= 3; seq++ {
		if err := appendAt(t, tl, first, seq); err != nil {
			t.Fatalf("append %d: %v", seq, err)
		}
	}

	// node-a's process is lost. Nothing releases, nothing is cleaned up, nothing
	// is told — which is what a killed process looks like from here.

	// Reads never stopped working; that half was always fine.
	read := 0
	tl.Walk(tl.Watermark(), func(Entry) bool { read++; return true })
	if read != 3 {
		t.Fatalf("%d entries readable after the writer was lost, want 3", read)
	}

	// And now writes come back, which is the part that used to be impossible.
	second := grant(t, reg, "node-b")
	for seq := uint32(4); seq <= 8; seq++ {
		if err := appendAt(t, tl, second, seq); err != nil {
			t.Fatalf("the replacement writer was refused at %d: %v — this is the failure the "+
				"catalogue recorded as unrecoverable, and it must not still be one", seq, err)
		}
	}
	if got := tl.Watermark(); got != 8 {
		t.Errorf("watermark = %d after a handover and five more appends, want 8", got)
	}

	// Handover works repeatedly, so a leaf is not merely rescuable once.
	for i, holder := range []string{"node-c", "node-d", "node-e"} {
		e := grant(t, reg, holder)
		if err := appendAt(t, tl, e, uint32(9+i)); err != nil {
			t.Errorf("handover %d to %q: %v", i, holder, err)
		}
	}
}

// TestPublishedEntriesSurviveHandover checks fencing costs nothing that was
// already committed.
func TestPublishedEntriesSurviveHandover(t *testing.T) {
	tl := New()
	reg := lease.NewRegistry()

	first := grant(t, reg, "node-a")
	const before = 40
	for seq := uint32(1); seq <= before; seq++ {
		if err := appendAt(t, tl, first, seq); err != nil {
			t.Fatalf("append %d: %v", seq, err)
		}
	}
	snapshot := tl.Watermark()

	second := grant(t, reg, "node-b")
	for seq := uint32(before + 1); seq <= before+20; seq++ {
		if err := appendAt(t, tl, second, seq); err != nil {
			t.Fatalf("post-handover append %d: %v", seq, err)
		}
	}

	// Everything published before the handover is still there, in order, whole.
	var seen []uint32
	whole := true
	tl.Walk(snapshot, func(e Entry) bool {
		seen = append(seen, e.TxID.Seq)
		if len(e.Datoms) != 2 || e.Datoms[0].Entity != fmt.Sprintf("entity-%d", e.TxID.Seq) {
			whole = false
			return false
		}
		return true
	})
	if !whole {
		t.Fatal("an entry published before the handover is incomplete afterwards")
	}
	if len(seen) != before {
		t.Fatalf("%d entries survive the handover, want %d", len(seen), before)
	}
	for i, seq := range seen {
		if seq != uint32(i+1) {
			t.Fatalf("position %d holds sequence %d, want %d — the handover reordered entries",
				i, seq, i+1)
		}
	}

	// A snapshot taken before the handover is still a fixed prefix afterwards.
	if got := len(seen); uint64(got) != uint64(snapshot) {
		t.Errorf("the pre-handover watermark walks %d entries, want %d", got, snapshot)
	}
}
