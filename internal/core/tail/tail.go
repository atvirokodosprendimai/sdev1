package tail

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// ChunkSize is how many entries one chunk holds.
//
// It is a power of two so the offset within a chunk is a single byte and
// locating an entry is a shift and a mask — the same eight-bit step the address
// space uses to descend a level. It is a layout choice and not a shard count.
const ChunkSize = 256

// ErrWriterNotHeld reports an append attempted without the writer token.
//
// A leaf has one writer. Refusing an append from anywhere else makes that a
// property rather than a convention, and this package's correctness rests on it:
// two concurrent appenders would both compute the same slot.
var ErrWriterNotHeld = errors.New("tail: append attempted without the writer token")

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

// WriterToken is the right to append. Its zero value holds nothing, so an
// append with a token nobody took is refused rather than accepted by default.
type WriterToken struct {
	tail *Tail
}

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
	// taken records whether the writer token has been handed out.
	taken bool

	// index and high are the two published values. The order in which they are
	// written, and read, is the mechanism; see Append and Watermark.
	index atomic.Pointer[chunkIndex]
	high  atomic.Uint64
}

// New returns an empty tail.
func New() *Tail { return &Tail{} }

// TakeWriter hands out the writer token, once.
//
// A second caller gets false rather than a second token, because two writers
// would compute the same slot for different entries.
func (t *Tail) TakeWriter() (WriterToken, bool) {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if t.taken {
		return WriterToken{}, false
	}
	t.taken = true
	return WriterToken{tail: t}, true
}

// Append writes an entry and publishes it.
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
func (t *Tail) Append(w WriterToken, id tx.TxID, datoms []ports.Datom) (Watermark, error) {
	if w.tail != t {
		return 0, fmt.Errorf("%w: the token names %v, this tail is %p", ErrWriterNotHeld, w.tail, t)
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

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
