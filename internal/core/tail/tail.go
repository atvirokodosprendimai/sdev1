package tail

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/atvirokodosprendimai/sdev1/internal/core/lease"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// ChunkSize is how many entries one chunk holds.
//
// It is a power of two so the offset within a chunk is a single byte and
// locating an entry is a shift and a mask — the same eight-bit step the address
// space uses to descend a level. It is a layout choice and not a shard count.
const ChunkSize = 256

// ErrFencedOut reports an append carrying an epoch older than one this tail has
// already seen.
//
// ★ The refusal happens HERE, at the resource, and that placement is the whole
// mechanism. A writer that asks "am I still the writer?", gets yes, and then
// appends has a window between the question and the write in which it can lose
// the leaf — to a garbage collection, a stalled disk, a partition — and the write
// lands anyway. The check and the write are not atomic and no amount of checking
// from the writer's side makes them so.
//
// So a writer that was paused across a handover comes back, appends under its
// old epoch, and is refused by the tail itself. It cannot corrupt anything; it
// can only fail, which is what it should do.
var ErrFencedOut = errors.New("tail: the epoch is older than one this tail has already seen")

// Entry is one published transaction: its identifier and the datoms it asserted.
//
// ⚠ An entry is never modified after it is published. That is what lets a reader
// walk the tail with no synchronization beyond its initial acquire-load.
type Entry struct {
	TxID   tx.TxID
	Datoms []ports.Datom
}

// Watermark is a published position: the number of entries that are complete and
// visible. It is the bound a reader walks against.
type Watermark uint64

// chunk is a fixed block of entry slots. Once allocated it is never moved, so a
// reader holding it through an older index still addresses valid memory.
type chunk struct {
	entries [ChunkSize]Entry
}

// chunkIndex is replaced wholesale on growth and never mutated, so a reader
// holding one is holding an immutable value.
type chunkIndex struct {
	chunks []*chunk
}

// Tail is the live end of a leaf's log: one writer, many readers, no reader-side
// lock.
type Tail struct {
	// writeMu serializes WRITERS with each other. No reader ever acquires it —
	// that is the property the guard in this package checks.
	writeMu sync.Mutex

	// highest is the greatest epoch this tail has observed. It is atomic rather
	// than guarded so that reading it costs nothing and cannot be mistaken for
	// something a reader must synchronize on.
	//
	// ⚠ Observing a higher epoch is IRREVERSIBLE: once seen, a lower one is
	// never accepted again, even if the holder of the higher epoch vanishes
	// immediately. A leaf that has moved on cannot be dragged back by whichever
	// writer was slowest.
	highest atomic.Uint64

	// index and high are the two published values. The order in which they are
	// written, and read, is the mechanism; see Append and Watermark.
	index atomic.Pointer[chunkIndex]
	high  atomic.Uint64
}

// New returns an empty tail.
func New() *Tail { return &Tail{} }

// Epoch reports the greatest epoch this tail has observed.
//
// It is a diagnostic. Nothing should decide whether it may write by asking this
// and then writing — that is the non-atomic pattern [ErrFencedOut] exists to
// make unnecessary. Pass the epoch to [Tail.Append] and let the tail refuse.
func (t *Tail) Epoch() lease.Epoch { return lease.Epoch(t.highest.Load()) }

// Append writes an entry and publishes it, under the caller's epoch.
//
// ⚠ The epoch is checked HERE and refused here. A leaf has one writer, and this
// is what makes that a property rather than a convention: a writer superseded
// while it was paused is refused at its next append with [ErrFencedOut], having
// been told nothing and having done no harm. There is no release and no expiry,
// because neither can tell a dead holder from a slow one.
//
// ★ The two steps are ordered and the order IS the mechanism: the entry is
// written COMPLETELY, and only then does the watermark advance. Reversed, a
// reader could observe an entry that is not finished — a torn read, returning
// data that never existed, which nothing downstream recovers from.
//
// The datoms are copied. A caller that kept its slice and mutated it would
// otherwise be mutating published state, which is the one thing this design does
// not allow. The bytes inside a datom's value are NOT copied and belong to the
// tail once appended.
func (t *Tail) Append(e lease.Epoch, id tx.TxID, datoms []ports.Datom) (Watermark, error) {
	if e == lease.NoEpoch {
		return 0, fmt.Errorf("%w: the zero epoch names no lease", ErrFencedOut)
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	// Equal is accepted — the current holder appends many times under one
	// epoch — and the FIRST epoch seen is accepted whatever its value, so a tail
	// never needs to be told where the counter started.
	if seen := lease.Epoch(t.highest.Load()); e < seen {
		return 0, fmt.Errorf("%w: epoch %d, and this tail has seen %d", ErrFencedOut, e, seen)
	}
	t.highest.Store(uint64(e))

	// Only the writer advances high, and it holds writeMu, so this load is exact
	// rather than merely recent.
	n := t.high.Load()
	ci, off := n/ChunkSize, n%ChunkSize

	idx := t.index.Load()
	if idx == nil || uint64(len(idx.chunks)) <= ci {
		// Grow by publishing a NEW index. Existing chunks are carried over by
		// pointer, so nothing a reader holds is moved or reallocated.
		next := &chunkIndex{chunks: make([]*chunk, 0, ci+1)}
		if idx != nil {
			next.chunks = append(next.chunks, idx.chunks...)
		}
		for uint64(len(next.chunks)) <= ci {
			next.chunks = append(next.chunks, &chunk{})
		}
		t.index.Store(next)
		idx = next
	}

	held := make([]ports.Datom, len(datoms))
	copy(held, datoms)

	// Write the entry completely...
	idx.chunks[ci].entries[off] = Entry{TxID: id, Datoms: held}

	// ...and only now publish it. This store is the release; a reader's load of
	// the watermark is the acquire.
	t.high.Add(1)
	return Watermark(n + 1), nil
}

// Watermark returns the published position.
//
// This is the ONLY synchronization a reader performs: one acquire-load. There is
// no lock here and there must never be one.
func (t *Tail) Watermark() Watermark { return Watermark(t.high.Load()) }

// Walk calls fn for each entry published at or before w, in order, stopping
// early if fn returns false.
//
// ⚠ The watermark is loaded by the caller and passed in, not re-read here. That
// is what makes a walk repeatable: the bound is fixed for the whole read, so a
// concurrent append is not in it however long the walk takes.
//
// ⚠ The two loads below are ordered deliberately. The caller's watermark was
// taken first and the chunk index is loaded second, because a writer publishes
// the index BEFORE advancing the watermark — so a watermark that covers a new
// chunk guarantees an index that contains it. Reading the index first would
// admit an index too short for the watermark held against it.
func (t *Tail) Walk(w Watermark, fn func(Entry) bool) {
	idx := t.index.Load()
	if idx == nil {
		return
	}
	for i := uint64(0); i < uint64(w); i++ {
		ci, off := i/ChunkSize, i%ChunkSize
		if ci >= uint64(len(idx.chunks)) {
			return
		}
		if !fn(idx.chunks[ci].entries[off]) {
			return
		}
	}
}
