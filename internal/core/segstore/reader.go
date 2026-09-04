package segstore

import (
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/segment"
)

// Reader reads one sealed segment.
//
// It is safe for concurrent use. Any number of goroutines may call [Reader.Get]
// and [Reader.Keys] at once, and none of them blocks another — a sealed segment
// is immutable, so there is nothing to serialise.
type Reader struct {
	// mu guards the MAPPING'S LIFETIME and never its contents.
	//
	// ⚠ It is not a lock on the data, and ADR-017's lock-free read path is not
	// weakened by it: readers take it shared and never contend, and the only
	// exclusive holder is Close. It exists because unmapping under an in-flight
	// read is not a stale answer but a signal — the one failure a reader cannot
	// catch, retry, or even attribute.
	mu    sync.RWMutex
	data  []byte
	hdr   segment.Header
	index []indexEntry
}

// Open maps a sealed segment and verifies that its index describes it.
//
// ⚠ Everything is checked before an offset from the index is used: the file is
// long enough, the trailer's magic and version are right, the index lies inside
// the block region, its checksum matches, and its entries are sorted, unique and
// in bounds. An index is a list of byte positions — a wrong one does not fail,
// it reads arbitrary bytes that look exactly like a block.
func Open(path string) (r *Reader, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	// Closed immediately on purpose. A mapping holds its own reference to the
	// file, so the descriptor is dead weight afterwards — and descriptors are a
	// bounded resource that a store holding many segments would exhaust first.
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()

	// ⚠ Before the mapping, not after. A file this short cannot hold a trailer, so
	// it is not a segment — and a zero-length mapping is refused by the kernel,
	// which would surface as EINVAL instead of the refusal it deserves.
	if size < int64(segment.HeaderSize+TrailerSize) {
		return nil, fmt.Errorf("%w: %s is %d bytes, shorter than a header and a trailer",
			ErrNotASegment, path, size)
	}

	data, err := mmapFile(f, int(size))
	if err != nil {
		return nil, fmt.Errorf("segstore: mapping %s: %w", path, err)
	}
	// Any failure below leaves a mapping nobody owns, and a leaked mapping is
	// invisible until the address space runs out.
	defer func() {
		if err != nil {
			_ = munmap(data)
		}
	}()

	t, err := decodeTrailer(data[size-TrailerSize:])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	blocksEnd := uint64(size) - TrailerSize
	if t.IndexOff < segment.HeaderSize || t.IndexLen > blocksEnd || t.IndexOff > blocksEnd-t.IndexLen {
		return nil, fmt.Errorf("%w: index at %d+%d does not fit in %d bytes of %s",
			ErrIndexCorrupt, t.IndexOff, t.IndexLen, blocksEnd, path)
	}
	raw := data[t.IndexOff : t.IndexOff+t.IndexLen]
	if got := segment.Checksum(raw); got != t.IndexSum {
		return nil, fmt.Errorf("%w: computed %08x, trailer records %08x for %s",
			ErrIndexCorrupt, got, t.IndexSum, path)
	}

	entries, err := decodeIndex(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := checkEntries(entries, t.IndexOff); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	hdr, err := segment.DecodeHeader(data[:segment.HeaderSize])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if int(hdr.Blocks) != len(entries) {
		return nil, fmt.Errorf("%w: header records %d blocks and the index holds %d in %s",
			ErrIndexCorrupt, hdr.Blocks, len(entries), path)
	}

	return &Reader{data: data, hdr: hdr, index: entries}, nil
}

// checkEntries verifies the index describes this file before any offset from it
// is followed.
//
// ⚠ The sortedness check is not tidiness. [Reader.Get] binary-searches, and a
// binary search over an unsorted list does not fail — it reports keys as missing
// that are right there, which reads as data loss rather than as corruption.
func checkEntries(entries []indexEntry, blocksEnd uint64) error {
	for i, e := range entries {
		if e.Span < segment.BlockHeaderSize {
			return fmt.Errorf("%w: block %q spans %d bytes, less than a block header",
				ErrIndexCorrupt, e.Key, e.Span)
		}
		if e.Offset < segment.HeaderSize || e.Offset > blocksEnd-uint64(e.Span) {
			return fmt.Errorf("%w: block %q at %d+%d lies outside the block region",
				ErrIndexCorrupt, e.Key, e.Offset, e.Span)
		}
		if i > 0 && entries[i-1].Key >= e.Key {
			return fmt.Errorf("%w: index is not sorted at %q", ErrIndexCorrupt, e.Key)
		}
	}
	return nil
}

// Leaf returns the leaf this segment was written for.
func (r *Reader) Leaf() (addr.LeafID, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.data == nil {
		return addr.LeafID{}, ErrClosed
	}
	return r.hdr.Leaf, nil
}

// Keys returns every key in the segment, sorted.
func (r *Reader) Keys() ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.data == nil {
		return nil, ErrClosed
	}
	out := make([]string, len(r.index))
	for i, e := range r.index {
		out[i] = e.Key
	}
	return out, nil
}

// Get returns the block stored under key, decoded.
//
// ★ The bytes are OWNED by the caller and outlive this Reader. They are never a
// view into the mapping: [segment.DecodeBlock] allocates its result, so the copy
// costs nothing here — and a view would be a dangling pointer the moment [Reader.Close]
// unmaps, working perfectly until then.
//
// ⚠ A missing key is [ErrNoSuchBlock], never an empty block.
func (r *Reader) Get(key string) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.data == nil {
		return nil, ErrClosed
	}

	i := sort.Search(len(r.index), func(i int) bool { return r.index[i].Key >= key })
	if i == len(r.index) || r.index[i].Key != key {
		return nil, fmt.Errorf("%w: %q", ErrNoSuchBlock, key)
	}
	e := r.index[i]

	h, err := segment.DecodeBlockHeader(r.data[e.Offset : e.Offset+segment.BlockHeaderSize])
	if err != nil {
		return nil, err
	}
	// The index and the block header record the same length by two different
	// routes. A disagreement means one of them describes a different file, and
	// following either would read bytes that are not this block.
	if want := uint64(segment.BlockHeaderSize) + uint64(h.StoredLen); want != uint64(e.Span) {
		return nil, fmt.Errorf("%w: block %q spans %d in the index and %d in its header",
			ErrIndexCorrupt, key, e.Span, want)
	}

	stored := r.data[e.Offset+segment.BlockHeaderSize : e.Offset+uint64(e.Span)]
	return segment.DecodeBlock(h, stored)
}

// Close releases the mapping. It is idempotent.
//
// ⚠ It waits for every in-flight read. Unmapping under one would not produce a
// stale answer but a signal, and blocks already returned are unaffected because
// they were never views into the mapping.
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.data == nil {
		return nil
	}
	data := r.data
	r.data = nil
	r.index = nil
	return munmap(data)
}
