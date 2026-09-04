package tail

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// entryFor builds an entry whose datoms are derived from its sequence number, so
// a reader can check that what it observed is INTERNALLY CONSISTENT. A torn read
// shows up as a mismatch between the identifier and the payload, which is
// exactly the fault that has no downstream remedy.
func entryFor(seq uint32) (tx.TxID, []ports.Datom) {
	id := tx.TxID{HLC: hlc.Timestamp{Wall: int64(seq) * 1000, Logical: seq}, Seq: seq}
	return id, []ports.Datom{
		{Entity: fmt.Sprintf("entity-%d", seq), Attribute: "n", Value: []byte(fmt.Sprint(seq)), Assert: true},
		{Entity: fmt.Sprintf("entity-%d", seq), Attribute: "m", Value: []byte(fmt.Sprint(seq)), Assert: true},
	}
}

// checkEntry reports what is wrong with an observed entry, or "".
func checkEntry(e Entry) string {
	seq := e.TxID.Seq
	if len(e.Datoms) != 2 {
		return fmt.Sprintf("entry %d carries %d datoms, want 2 — a partially written entry was published", seq, len(e.Datoms))
	}
	want := fmt.Sprintf("entity-%d", seq)
	for i, d := range e.Datoms {
		if d.Entity != want {
			return fmt.Sprintf("entry %d datom %d names %q, want %q — the identifier and the payload disagree",
				seq, i, d.Entity, want)
		}
	}
	if e.HLCSeq() != seq {
		return fmt.Sprintf("entry %d carries clock logical %d — the identifier is not internally consistent", seq, e.HLCSeq())
	}
	return ""
}

// HLCSeq is a test helper reading the logical counter this fixture writes.
func (e Entry) HLCSeq() uint32 { return e.TxID.HLC.Logical }

func mustTakeWriter(t *testing.T, tl *Tail) WriterToken {
	t.Helper()
	w, ok := tl.TakeWriter()
	if !ok {
		t.Fatal("the writer token was already taken")
	}
	return w
}

// TestPartialEntryIsNeverVisible runs a writer and readers together and checks
// every entry a reader observes is whole.
//
// Publication happens after the write, so a half-written entry is not protected
// — it is unreachable. This test is the falsifier for that ordering, and it runs
// under the race detector because the fault it is about only exists when a
// reader and a writer overlap.
func TestPartialEntryIsNeverVisible(t *testing.T) {
	const total = 5000
	tl := New()
	w := mustTakeWriter(t, tl)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				mark := tl.Watermark()
				var seen uint64
				var bad string
				tl.Walk(mark, func(e Entry) bool {
					if problem := checkEntry(e); problem != "" {
						bad = problem
						return false
					}
					seen++
					return true
				})
				if bad != "" {
					t.Error(bad)
					return
				}
				if seen != uint64(mark) {
					t.Errorf("walked %d entries against a watermark of %d", seen, mark)
					return
				}
			}
		}()
	}

	for seq := uint32(1); seq <= total; seq++ {
		id, datoms := entryFor(seq)
		if _, err := tl.Append(w, id, datoms); err != nil {
			t.Fatalf("Append %d: %v", seq, err)
		}
	}
	close(stop)
	wg.Wait()

	if got := tl.Watermark(); got != total {
		t.Errorf("watermark = %d after %d appends, want %d", got, total, total)
	}
}

// TestReadersAndWriterActuallyOverlap asserts the concurrency test above is
// testing concurrency.
//
// ⚠ A clean race-detector run proves nothing if the goroutines never ran at the
// same time. A concurrency test that quietly serialized is indistinguishable
// from one that is correct — both are green — so this asserts that a reader
// observed the watermark ADVANCE while it was running.
func TestReadersAndWriterActuallyOverlap(t *testing.T) {
	const total = 20000
	tl := New()
	w := mustTakeWriter(t, tl)

	var wg sync.WaitGroup
	observed := make(chan int, 1)
	started := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)
		distinct := map[Watermark]struct{}{}
		for i := 0; i < 200000; i++ {
			distinct[tl.Watermark()] = struct{}{}
			if len(distinct) > 8 {
				break
			}
		}
		observed <- len(distinct)
	}()

	<-started
	for seq := uint32(1); seq <= total; seq++ {
		id, datoms := entryFor(seq)
		if _, err := tl.Append(w, id, datoms); err != nil {
			t.Fatalf("Append %d: %v", seq, err)
		}
	}
	wg.Wait()

	if n := <-observed; n < 2 {
		t.Fatalf("a reader observed %d distinct watermark value(s) during %d concurrent appends; "+
			"the reader and the writer did not overlap, so every other concurrency assertion in "+
			"this package is vacuous for this run", n, total)
	}
}

// TestSnapshotIsRepeatable checks a watermark loaded once is a stable prefix:
// reading it twice gives the same answer, and later appends are not in it.
func TestSnapshotIsRepeatable(t *testing.T) {
	tl := New()
	w := mustTakeWriter(t, tl)

	for seq := uint32(1); seq <= 10; seq++ {
		id, datoms := entryFor(seq)
		if _, err := tl.Append(w, id, datoms); err != nil {
			t.Fatalf("Append %d: %v", seq, err)
		}
	}

	mark := tl.Watermark()
	if mark != 10 {
		t.Fatalf("watermark = %d, want 10", mark)
	}
	first := collect(tl, mark)

	// Appends made AFTER the watermark was taken must not appear in it, however
	// many times it is read.
	for seq := uint32(11); seq <= 30; seq++ {
		id, datoms := entryFor(seq)
		if _, err := tl.Append(w, id, datoms); err != nil {
			t.Fatalf("Append %d: %v", seq, err)
		}
	}

	for i := 0; i < 5; i++ {
		again := collect(tl, mark)
		if len(again) != len(first) {
			t.Fatalf("read %d of the same watermark returned %d entries, first returned %d",
				i, len(again), len(first))
		}
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("read %d differs at entry %d: %d vs %d", i, j, again[j], first[j])
			}
		}
	}

	// A fresh watermark does see them.
	if got := len(collect(tl, tl.Watermark())); got != 30 {
		t.Errorf("a watermark taken after the later appends walks %d entries, want 30", got)
	}
}

func collect(tl *Tail, w Watermark) []uint32 {
	var out []uint32
	tl.Walk(w, func(e Entry) bool {
		out = append(out, e.TxID.Seq)
		return true
	})
	return out
}

// TestChunkGrowthDoesNotMoveEntries checks entries written before the chunk index
// grows are still readable afterwards, at the same positions.
//
// Growth publishes a NEW index carrying the existing chunks by pointer. If it
// reallocated or moved them, a reader holding an older index would be addressing
// memory that no longer means what it did.
func TestChunkGrowthDoesNotMoveEntries(t *testing.T) {
	tl := New()
	w := mustTakeWriter(t, tl)

	// Fill past several chunk boundaries.
	const total = ChunkSize*3 + 7
	for seq := uint32(1); seq <= total; seq++ {
		id, datoms := entryFor(seq)
		if _, err := tl.Append(w, id, datoms); err != nil {
			t.Fatalf("Append %d: %v", seq, err)
		}
	}

	got := collect(tl, tl.Watermark())
	if len(got) != total {
		t.Fatalf("walked %d entries, want %d — an entry was lost across a chunk boundary", len(got), total)
	}
	for i, seq := range got {
		if seq != uint32(i+1) {
			t.Fatalf("position %d holds sequence %d, want %d — entries moved during growth", i, seq, i+1)
		}
	}

	// A watermark taken mid-first-chunk still walks the same entries after three
	// more chunks have been added.
	early := Watermark(5)
	for seq := uint32(total + 1); seq <= total+ChunkSize*2; seq++ {
		id, datoms := entryFor(seq)
		if _, err := tl.Append(w, id, datoms); err != nil {
			t.Fatalf("Append %d: %v", seq, err)
		}
	}
	stale := collect(tl, early)
	if len(stale) != 5 {
		t.Fatalf("an old watermark walked %d entries after growth, want 5", len(stale))
	}
	for i, seq := range stale {
		if seq != uint32(i+1) {
			t.Errorf("after growth, old position %d holds sequence %d, want %d", i, seq, i+1)
		}
	}
}

// TestAppendRequiresTheWriterToken checks the single-writer assumption is a
// property rather than a convention.
func TestAppendRequiresTheWriterToken(t *testing.T) {
	tl := New()
	id, datoms := entryFor(1)

	// The zero token holds nothing.
	if _, err := tl.Append(WriterToken{}, id, datoms); err == nil {
		t.Error("an append with the zero token succeeded; the single-writer assumption is only a convention")
	} else if !isWriterNotHeld(err) {
		t.Errorf("zero token: error = %v, want ErrWriterNotHeld", err)
	}

	w := mustTakeWriter(t, tl)
	if _, err := tl.Append(w, id, datoms); err != nil {
		t.Fatalf("the holder of the token was refused: %v", err)
	}

	// The token is handed out once.
	if _, ok := tl.TakeWriter(); ok {
		t.Error("a second writer token was handed out; two writers would compute the same slot")
	}

	// A token for another tail does not work here.
	other := New()
	ow := mustTakeWriter(t, other)
	if _, err := tl.Append(ow, id, datoms); !isWriterNotHeld(err) {
		t.Errorf("another tail's token: error = %v, want ErrWriterNotHeld", err)
	}
	if got := tl.Watermark(); got != 1 {
		t.Errorf("watermark = %d after one accepted and three refused appends, want 1", got)
	}
}

func isWriterNotHeld(err error) bool {
	return errors.Is(err, ErrWriterNotHeld)
}
