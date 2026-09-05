package prefetch

import (
	"errors"
	"fmt"
	"testing"
)

func blockID(blob string, i uint32) BlockID { return BlockID{Blob: blob, Index: i} }

// source is the backing store a read falls back to. It is the thing that makes
// the cache optional rather than load-bearing.
type source map[BlockID][]byte

func (s source) read(c *Cache, id BlockID) ([]byte, bool) {
	if block, hit := c.Get(id); hit {
		return block, true
	}
	block, held := s[id]
	if !held {
		return nil, false
	}
	// A demanded read: the caller needed this block, so it is evidence.
	_ = c.Put(id, block, Demanded)
	return block, true
}

// TestEvictingEverythingChangesNoAnswer is ADR-037's falsifier.
//
// ⚠ The cache is emptied DURING the sequence, before every single read, not once
// before it starts. A test that clears the cache and then reads exercises an
// empty cache and proves only that a fallback exists — not that a partially warm
// cache and a cold one agree, which is the property that matters.
func TestEvictingEverythingChangesNoAnswer(t *testing.T) {
	src := source{}
	for i := uint32(0); i < 12; i++ {
		src[blockID("blob", i)] = []byte(fmt.Sprintf("block-%d", i))
	}

	// A read sequence with repeats, so the cache is genuinely hit part of the way
	// through rather than only ever missing.
	sequence := []uint32{0, 1, 2, 0, 3, 1, 4, 5, 2, 6, 0, 7, 8, 3, 9, 10, 11, 5}

	warm := NewCache(64)
	var withCache [][]byte
	for _, i := range sequence {
		block, ok := src.read(warm, blockID("blob", i))
		if !ok {
			t.Fatalf("block %d is missing from the source", i)
		}
		withCache = append(withCache, block)
	}

	// ⚠ Every read must return the SOURCE's bytes, not merely the same bytes as
	// the other run. Comparing the two runs against each other alone would pass
	// with a cache that lies identically in both — a `Get` reporting a hit it does
	// not hold would make both runs agree on the wrong answer.
	for n, i := range sequence {
		if want := src[blockID("blob", i)]; string(withCache[n]) != string(want) {
			t.Fatalf("read %d returned %q, want %q from the source", n, withCache[n], want)
		}
	}

	// ★ The same sequence, with everything evicted before each read. If any
	// answer differs, something became reachable only through the cache.
	cold := NewCache(64)
	for n, i := range sequence {
		cold.EvictAll()
		// ⚠ And the eviction must actually have happened. Without this the whole
		// comparison below is vacuous against a no-op EvictAll: the "cold" run
		// would simply be a second warm one, and it would agree perfectly.
		if got := cold.Len(); got != 0 {
			t.Fatalf("EvictAll left %d entries; the cold run is not cold, so nothing below "+
				"is being tested", got)
		}
		block, ok := src.read(cold, blockID("blob", i))
		if !ok {
			t.Fatalf("read %d of block %d failed with the cache emptied; the cache has become "+
				"load-bearing, and this failure would only ever appear under memory pressure", n, i)
		}
		if string(block) != string(withCache[n]) {
			t.Fatalf("read %d gave %q with a warm cache and %q with an emptied one",
				n, withCache[n], block)
		}
		if want := src[blockID("blob", i)]; string(block) != string(want) {
			t.Fatalf("read %d returned %q from an emptied cache, want %q", n, block, want)
		}
	}

	// And a mid-sequence eviction of ONE entry is equally invisible.
	partial := NewCache(64)
	for n, i := range sequence {
		partial.Evict(blockID("blob", sequence[0]))
		block, _ := src.read(partial, blockID("blob", i))
		if string(block) != string(withCache[n]) {
			t.Fatalf("read %d differed after evicting one entry: %q vs %q",
				n, block, withCache[n])
		}
	}
}

// TestAScanCannotEvictAnotherReadersWorkingSet is ADR-037 rule 3.
//
// ⚠ This is the test that fails under plain LRU, which is the implementation
// somebody will reach for. It puts far more speculative blocks than the cache can
// hold, so the eviction ORDER is actually exercised.
func TestAScanCannotEvictAnotherReadersWorkingSet(t *testing.T) {
	const blockSize = 10
	c := NewCache(10 * blockSize)
	block := make([]byte, blockSize)

	// A reader's working set: four blocks it actually asked for.
	working := []BlockID{
		blockID("reader", 0), blockID("reader", 1),
		blockID("reader", 2), blockID("reader", 3),
	}
	for _, id := range working {
		if err := c.Put(id, block, Demanded); err != nil {
			t.Fatalf("Put(%v): %v", id, err)
		}
	}

	// A scan now prefetches thirty blocks through a cache that holds ten.
	for i := uint32(0); i < 30; i++ {
		if err := c.Put(blockID("scan", i), block, Speculative); err != nil {
			t.Fatalf("Put(scan %d): %v", i, err)
		}
	}

	// ★ Every demanded block survives. Under LRU they would be the OLDEST entries
	// and would have gone first.
	for _, id := range working {
		if _, held := c.Get(id); !held {
			t.Errorf("the scan evicted demanded block %v.\n"+
				"A guess must be evicted before evidence — otherwise a sequential scan takes "+
				"the working set of every other reader on the node.", id)
		}
	}

	// The scan's own earliest guesses are what went, and its latest survive.
	if _, held := c.Get(blockID("scan", 0)); held {
		t.Error("the scan's oldest speculative block survived; within a class, eviction is " +
			"least-recently-used")
	}
	if _, held := c.Get(blockID("scan", 29)); !held {
		t.Error("the scan's newest speculative block was evicted")
	}

	if c.Bytes() > 10*blockSize {
		t.Errorf("the cache holds %d bytes, over its %d bound", c.Bytes(), 10*blockSize)
	}
}

// TestAReadPromotesAGuessToEvidence is ADR-037 rule 4.
func TestAReadPromotesAGuessToEvidence(t *testing.T) {
	const blockSize = 10
	c := NewCache(4 * blockSize)
	block := make([]byte, blockSize)

	// ★ TWO speculative entries put back to back, so they differ only in whether
	// one of them is READ. Without the pair, a test would pass on recency alone:
	// a promoted entry is also the most recently used one.
	read := blockID("guess", 1)
	unread := blockID("guess", 2)
	if err := c.Put(read, block, Speculative); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := c.Put(unread, block, Speculative); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// The guess turns out to be right.
	if _, held := c.Get(read); !held {
		t.Fatal("the speculative block was not there to read")
	}

	// A flood of further guesses, more than the cache holds.
	for i := uint32(0); i < 20; i++ {
		if err := c.Put(blockID("flood", i), block, Speculative); err != nil {
			t.Fatalf("Put(flood %d): %v", i, err)
		}
	}

	if _, held := c.Get(read); !held {
		t.Error("a speculative block that was READ was evicted by later guesses.\n" +
			"Reading it is what turns the guess into evidence — without the promotion, a " +
			"correctly prefetched sequential read keeps evicting the blocks it is about to use.")
	}
	if _, held := c.Get(unread); held {
		t.Error("an un-read speculative block survived the flood; it is still a guess, and " +
			"guesses go first")
	}
}

// TestTheBoundIsBytesNotEntries is ADR-037 rule 5.
//
// ⚠ Sizes differ by an order of magnitude. An entry-count bound passes any test
// whose blocks are all the same size, which is what makes uniform fixtures the
// wrong ones here.
func TestTheBoundIsBytesNotEntries(t *testing.T) {
	const limit = 1000

	small := NewCache(limit)
	for i := uint32(0); i < 50; i++ {
		if err := small.Put(blockID("small", i), make([]byte, 10), Demanded); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	large := NewCache(limit)
	for i := uint32(0); i < 50; i++ {
		if err := large.Put(blockID("large", i), make([]byte, 200), Demanded); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	// ★ Same limit, same number of puts, and a different number of entries held —
	// which is only true if the bound is bytes.
	if small.Len() <= large.Len() {
		t.Errorf("a cache of 10-byte blocks held %d entries and one of 200-byte blocks held %d; "+
			"with a byte bound the small blocks must fit in greater number",
			small.Len(), large.Len())
	}
	if got := small.Bytes(); got > limit {
		t.Errorf("small cache holds %d bytes, over its %d bound", got, limit)
	}
	if got := large.Bytes(); got > limit {
		t.Errorf("large cache holds %d bytes, over its %d bound", got, limit)
	}

	// Re-putting the same block does not double-count it.
	c := NewCache(limit)
	for i := 0; i < 5; i++ {
		if err := c.Put(blockID("same", 0), make([]byte, 100), Demanded); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	if got := c.Bytes(); got != 100 {
		t.Errorf("one block put five times accounts for %d bytes, want 100", got)
	}
}

// TestABlockLargerThanTheCacheIsRefused is ADR-037 rule 7.
func TestABlockLargerThanTheCacheIsRefused(t *testing.T) {
	const limit = 100
	c := NewCache(limit)

	held := blockID("kept", 0)
	if err := c.Put(held, make([]byte, 50), Demanded); err != nil {
		t.Fatalf("Put: %v", err)
	}

	err := c.Put(blockID("huge", 0), make([]byte, limit+1), Demanded)
	if !errors.Is(err, ErrBlockTooLarge) {
		t.Fatalf("Put of an oversized block = %v, want ErrBlockTooLarge", err)
	}

	// ★ THE SECOND HALF, and the one that matters: a refusal that still emptied
	// the cache would have done the damage it was refusing to do.
	if _, still := c.Get(held); !still {
		t.Error("refusing an oversized block still evicted the existing entries; the whole " +
			"working set was traded for a block that was not even admitted")
	}
	if got := c.Bytes(); got != 50 {
		t.Errorf("the cache holds %d bytes after a refused put, want 50", got)
	}

	// A block exactly the size of the cache fits, so the boundary is not off by
	// one in the direction that refuses valid work.
	exact := NewCache(limit)
	if err := exact.Put(blockID("exact", 0), make([]byte, limit), Demanded); err != nil {
		t.Errorf("a block exactly the size of the cache was refused: %v", err)
	}
}
