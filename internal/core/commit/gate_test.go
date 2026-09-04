package commit

import (
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tail"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

func idAt(seq uint32) tx.TxID {
	return tx.TxID{HLC: hlc.Timestamp{Wall: int64(seq) * 1000, Logical: seq}, Seq: seq}
}

func datomsFor(seq uint32) []ports.Datom {
	return []ports.Datom{{Entity: "e", Attribute: "a", Assert: true, Value: []byte{byte(seq)}}}
}

func newGate(t *testing.T) (*Gate, *tail.Tail) {
	t.Helper()
	tl := tail.New()
	g, err := NewGate(tl, mustCondition(t, 3, 3), epoch)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	return g, tl
}

// visible reports whether an entry can be READ through the tail.
//
// ⚠ Reading rather than checking a flag is the point. A flag a reader must
// remember to consult is a flag some reader will not consult, and an
// implementation that published uncommitted entries and marked them would pass a
// flag-based test.
func visible(tl *tail.Tail, id tx.TxID) bool {
	found := false
	tl.Walk(tl.Watermark(), func(e tail.Entry) bool {
		if e.TxID == id {
			found = true
			return false
		}
		return true
	})
	return found
}

// TestUncommittedEntryIsUnreachable checks a pending write is absent rather than
// merely unmarked.
func TestUncommittedEntryIsUnreachable(t *testing.T) {
	g, tl := newGate(t)
	id := idAt(1)

	if err := g.Write(id, datomsFor(1)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if visible(tl, id) {
		t.Fatal("an uncommitted entry is readable; the watermark is what makes an entry " +
			"reachable at all, so an uncommitted one must be absent rather than flagged")
	}
	if tl.Watermark() != 0 {
		t.Errorf("the watermark is %d after a write and no acknowledgement, want 0", tl.Watermark())
	}
	if g.Committed(id) {
		t.Error("an unacknowledged entry reports itself committed")
	}
	if g.Pending() != 1 {
		t.Errorf("Pending = %d, want 1", g.Pending())
	}

	// Two acknowledgements is one short of the floor of three: still absent.
	for _, d := range []string{"feed-a", "feed-b"} {
		if _, err := g.Acknowledge(id, Ack{Node: d, Domain: d, Epoch: epoch}); err != nil {
			t.Fatalf("Acknowledge: %v", err)
		}
	}
	if visible(tl, id) {
		t.Fatal("an entry one domain short of the floor is readable")
	}

	// The third commits it.
	if _, err := g.Acknowledge(id, Ack{Node: "c", Domain: "feed-c", Epoch: epoch}); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if !visible(tl, id) {
		t.Fatal("a committed entry is not readable")
	}
}

// TestWatermarkAdvancesOnlyOnCommit checks the watermark moves on the
// acknowledgement that satisfies the condition and on no earlier one — and that
// entries publish IN ORDER.
//
// ⚠ Acknowledging one replica at a time is what makes an early advance visible.
// Acknowledging everything at once could not distinguish an implementation that
// published on the first.
func TestWatermarkAdvancesOnlyOnCommit(t *testing.T) {
	g, tl := newGate(t)
	id := idAt(1)
	if err := g.Write(id, datomsFor(1)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	for i, d := range []string{"feed-a", "feed-b"} {
		if _, err := g.Acknowledge(id, Ack{Node: d, Domain: d, Epoch: epoch}); err != nil {
			t.Fatalf("Acknowledge %d: %v", i, err)
		}
		if got := tl.Watermark(); got != 0 {
			t.Fatalf("after %d of 3 acknowledgements the watermark is %d, want 0", i+1, got)
		}
	}
	if n, err := g.Acknowledge(id, Ack{Node: "c", Domain: "feed-c", Epoch: epoch}); err != nil || n != 1 {
		t.Fatalf("the satisfying acknowledgement published %d entries (err=%v), want 1", n, err)
	}
	if got := tl.Watermark(); got != 1 {
		t.Errorf("the watermark is %d after the commit, want 1", got)
	}

	// ⚠ ORDER. A later entry that is fully acknowledged must NOT publish while an
	// earlier one is still short, because the watermark's meaning is a stable
	// PREFIX — publishing past a gap would let a reader see a later write without
	// an earlier one and call it a prefix.
	early, late := idAt(2), idAt(3)
	for _, id := range []tx.TxID{early, late} {
		if err := g.Write(id, datomsFor(id.Seq)); err != nil {
			t.Fatalf("Write %v: %v", id, err)
		}
	}
	for _, d := range []string{"feed-a", "feed-b", "feed-c"} {
		if _, err := g.Acknowledge(late, Ack{Node: d, Domain: d, Epoch: epoch}); err != nil {
			t.Fatalf("Acknowledge late: %v", err)
		}
	}
	if visible(tl, late) {
		t.Fatal("a fully acknowledged LATER entry published while an earlier one was still " +
			"pending; the watermark then names something that is not a prefix")
	}
	if got := tl.Watermark(); got != 1 {
		t.Errorf("the watermark moved to %d on an out-of-order commit, want 1", got)
	}

	// Completing the earlier one releases both, in order.
	n := 0
	for _, d := range []string{"feed-a", "feed-b", "feed-c"} {
		got, err := g.Acknowledge(early, Ack{Node: d, Domain: d, Epoch: epoch})
		if err != nil {
			t.Fatalf("Acknowledge early: %v", err)
		}
		n += got
	}
	if n != 2 {
		t.Errorf("completing the earlier entry published %d entries, want 2 — it and the later "+
			"one that was waiting behind it", n)
	}
	var order []uint32
	tl.Walk(tl.Watermark(), func(e tail.Entry) bool { order = append(order, e.TxID.Seq); return true })
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Errorf("the published order is %v, want 1 2 3", order)
	}
}

// TestOneDefinitionOfCommitted checks the gate's answer and the tail's
// reachability never disagree, in any state.
//
// ⚠ Two definitions of committed drift, and the drift shows up only under
// partial failure — which is exactly when nobody is reading test output. So they
// are compared at every step rather than at the end.
func TestOneDefinitionOfCommitted(t *testing.T) {
	g, tl := newGate(t)

	ids := []tx.TxID{idAt(1), idAt(2)}
	for _, id := range ids {
		if err := g.Write(id, datomsFor(id.Seq)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	check := func(stage string) {
		t.Helper()
		for _, id := range ids {
			if g.Committed(id) != visible(tl, id) {
				t.Fatalf("%s: %v reports committed=%v but readable=%v — two definitions of "+
					"committed have drifted, and the one a reader uses is not the one the "+
					"writer waited for", stage, id, g.Committed(id), visible(tl, id))
			}
		}
	}

	check("after writing")
	for i, id := range ids {
		for j, d := range []string{"feed-a", "feed-b", "feed-c"} {
			if _, err := g.Acknowledge(id, Ack{Node: d, Domain: d, Epoch: epoch}); err != nil {
				t.Fatalf("Acknowledge: %v", err)
			}
			check("entry " + string(rune('1'+i)) + " ack " + string(rune('1'+j)))
		}
	}
	check("after all acknowledgements")

	// Both ended committed, so the agreement above is not agreement on "nothing
	// is committed".
	for _, id := range ids {
		if !g.Committed(id) {
			t.Errorf("%v never committed", id)
		}
	}
}

// TestPendingEntriesAreCountable checks the exposure window is readable.
func TestPendingEntriesAreCountable(t *testing.T) {
	g, _ := newGate(t)

	if got := g.Pending(); got != 0 {
		t.Errorf("a fresh gate has %d pending, want 0", got)
	}

	ids := []tx.TxID{idAt(1), idAt(2), idAt(3)}
	for i, id := range ids {
		if err := g.Write(id, datomsFor(id.Seq)); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if got := g.Pending(); got != i+1 {
			t.Errorf("after %d writes Pending = %d, want %d", i+1, got, i+1)
		}
	}

	// Committing the first drops the count by one and no more.
	for _, d := range []string{"feed-a", "feed-b", "feed-c"} {
		if _, err := g.Acknowledge(ids[0], Ack{Node: d, Domain: d, Epoch: epoch}); err != nil {
			t.Fatalf("Acknowledge: %v", err)
		}
	}
	if got := g.Pending(); got != 2 {
		t.Errorf("after committing one of three, Pending = %d, want 2", got)
	}

	// Why says what an uncommitted entry is waiting for.
	if err := g.Why(ids[1]); err == nil {
		t.Error("Why reported nothing for an uncommitted entry")
	}
	if err := g.Why(ids[0]); err != nil {
		t.Errorf("Why on a committed entry: %v, want nil", err)
	}

	// A duplicate write is refused rather than silently replacing a pending one.
	if err := g.Write(ids[1], datomsFor(9)); err == nil {
		t.Error("writing an already-pending identifier was accepted")
	}
}

// TestLateAcknowledgementStillCommits checks an acknowledgement arriving after
// the condition was met changes nothing.
func TestLateAcknowledgementStillCommits(t *testing.T) {
	g, tl := newGate(t)
	id := idAt(1)
	if err := g.Write(id, datomsFor(1)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	for _, d := range []string{"feed-a", "feed-b", "feed-c"} {
		if _, err := g.Acknowledge(id, Ack{Node: d, Domain: d, Epoch: epoch}); err != nil {
			t.Fatalf("Acknowledge: %v", err)
		}
	}
	mark := tl.Watermark()
	if mark != 1 {
		t.Fatalf("the watermark is %d after committing one entry, want 1", mark)
	}

	// Two more replies arrive for an entry that already committed.
	for _, d := range []string{"feed-d", "feed-e"} {
		published, err := g.Acknowledge(id, Ack{Node: d, Domain: d, Epoch: epoch})
		if err != nil {
			t.Fatalf("late Acknowledge: %v", err)
		}
		if published != 0 {
			t.Errorf("a late acknowledgement published %d entries, want 0", published)
		}
	}
	if got := tl.Watermark(); got != mark {
		t.Errorf("a late acknowledgement moved the watermark from %d to %d", mark, got)
	}
	if !g.Committed(id) {
		t.Error("a late acknowledgement un-committed the entry")
	}

	// The entry appears exactly once, so nothing was appended twice.
	count := 0
	tl.Walk(tl.Watermark(), func(e tail.Entry) bool {
		if e.TxID == id {
			count++
		}
		return true
	})
	if count != 1 {
		t.Errorf("the entry appears %d times in the tail, want 1", count)
	}

	// Acknowledging something that was never written is refused.
	if _, err := g.Acknowledge(idAt(99), Ack{Node: "x", Domain: "feed-x", Epoch: epoch}); err == nil {
		t.Error("an acknowledgement for an unwritten entry was accepted")
	}
}
