package prefetch

import (
	"errors"
	"fmt"
	"sync"
)

// ErrBlockTooLarge reports a block that does not fit in the whole cache.
//
// ⚠ It is a REFUSAL rather than an admission that empties the cache. Admitting it
// would trade the entire working set for one block that may never be read again,
// which is the worst outcome available: the useful entries are gone and nothing
// took their place.
var ErrBlockTooLarge = errors.New("prefetch: the block is larger than the whole cache")

// BlockID identifies one block of one blob.
type BlockID struct {
	// Blob is what the block belongs to.
	Blob string
	// Index is the block's position in it.
	Index uint32
}

func (b BlockID) String() string { return fmt.Sprintf("%s#%d", b.Blob, b.Index) }

// Arrival says how an entry came to be in the cache.
//
// ★ This is the distinction the whole eviction policy rests on. A DEMANDED block
// was asked for by a read that needed it; a SPECULATIVE one was pulled by a
// prefetch that guessed it would be. One is evidence and the other is a guess,
// and a guess is evicted first — which is true on every workload, and is why it
// can be decided without one.
type Arrival int

const (
	// Demanded: a read asked for this block.
	Demanded Arrival = iota
	// Speculative: a prefetch guessed this block would be wanted.
	Speculative
)

func (a Arrival) String() string {
	if a == Speculative {
		return "speculative"
	}
	return "demanded"
}

// entry is one cached block.
type entry struct {
	id      BlockID
	block   []byte
	arrival Arrival
	// used is a monotonic counter, not a clock. Two entries touched in the same
	// nanosecond must still order, and a clock cannot promise that.
	used uint64
}

// Cache holds fetched blocks so a prefetch can pay off.
//
// ⚠ IT IS A CACHE AND NEVER A STORE. Nothing may be reachable only through it: a
// read must still work with every entry evicted. ★ That constraint is first
// because every other rule here is an optimisation, and an optimisation that
// becomes load-bearing fails under memory pressure — which is exactly when
// eviction happens, so the defect and its trigger are the same event and it is
// never seen in testing.
//
// ⚠ The bound is BYTES, not entries. Blocks vary in size, so an entry count
// bounds nothing that matters: the same limit is generous for small blocks and an
// out-of-memory kill for large ones. It is ADR-018 rule 5's discipline over the
// same resource, so it is measured the same way.
type Cache struct {
	mu    sync.Mutex
	limit int64
	bytes int64
	// tick orders entries by use. See [entry.used].
	tick    uint64
	entries map[BlockID]*entry
}

// NewCache returns a cache bounded to limit bytes.
func NewCache(limit int64) *Cache {
	return &Cache{limit: limit, entries: make(map[BlockID]*entry)}
}

// Put admits a block, evicting as needed to make room.
//
// ⚠ arrival is the caller's to state, because only the caller knows whether a
// read needed this block or a prefetch guessed it. There is no default: a cache
// that inferred it would have to guess the one thing its policy depends on.
//
// A block larger than the whole cache is [ErrBlockTooLarge] and nothing is
// evicted — see that error for why.
func (c *Cache) Put(id BlockID, block []byte, arrival Arrival) error {
	size := int64(len(block))

	c.mu.Lock()
	defer c.mu.Unlock()

	if size > c.limit {
		return fmt.Errorf("%w: %d bytes into a cache of %d", ErrBlockTooLarge, size, c.limit)
	}

	// Replacing an existing entry frees its bytes first, or the same block put
	// twice would be counted twice and evict on the second write.
	if prior, held := c.entries[id]; held {
		c.bytes -= int64(len(prior.block))
		delete(c.entries, id)
	}

	c.evictLocked(size)

	c.tick++
	c.entries[id] = &entry{id: id, block: block, arrival: arrival, used: c.tick}
	c.bytes += size
	return nil
}

// Get returns a block if it is held, and PROMOTES a speculative entry to
// demanded.
//
// ★ The promotion is what makes a correct prefetch pay: the guess was right, so
// it stops being a guess. ⚠ Without it, a perfectly prefetched sequential read
// would keep evicting the very blocks it is about to use — prefetching would make
// things worse, and worst on the workload it exists for.
func (c *Cache) Get(id BlockID) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, held := c.entries[id]
	if !held {
		return nil, false
	}
	c.tick++
	e.used = c.tick
	e.arrival = Demanded
	return e.block, true
}

// Evict removes one block if it is held, and reports whether it was.
func (c *Cache) Evict(id BlockID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, held := c.entries[id]
	if !held {
		return false
	}
	c.bytes -= int64(len(e.block))
	delete(c.entries, id)
	return true
}

// EvictAll empties the cache.
//
// ★ It exists so that the property in [Cache]'s comment is TESTABLE rather than
// merely asserted: a read that answers differently after this is a read that had
// become dependent on the cache.
func (c *Cache) EvictAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[BlockID]*entry)
	c.bytes = 0
}

// Bytes is what the cache currently holds. It never exceeds the bound.
func (c *Cache) Bytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

// Len is how many blocks are held.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// evictLocked frees space until `need` more bytes fit.
//
// ★ SPECULATIVE ENTRIES GO FIRST, least-recently-used within each class. A guess
// is evicted before evidence.
//
// ⚠ This is what makes a sequential scan survivable. Under plain LRU a scan fills
// the cache with speculative blocks and evicts the working set of every other
// reader on the node — the defect `BACKLOG.md` §24 names. Here a scan can only
// evict its own guesses until it runs out of them.
//
// ⚠ It is not ARC. Two speculative entries are ordered only by recency, so a scan
// still evicts its own useful guesses; choosing better than that needs a workload
// nobody has, and this ordering does not.
func (c *Cache) evictLocked(need int64) {
	for c.bytes+need > c.limit {
		victim := c.victimLocked()
		if victim == nil {
			// Unreachable while need <= limit, which Put guarantees. Returning
			// rather than looping is what keeps that a bug rather than a hang.
			return
		}
		c.bytes -= int64(len(victim.block))
		delete(c.entries, victim.id)
	}
}

// victimLocked picks the next entry to evict: the least recently used
// speculative entry, or the least recently used demanded one when no speculative
// entry remains.
func (c *Cache) victimLocked() *entry {
	var best *entry
	for _, e := range c.entries {
		if best == nil || betterVictim(e, best) {
			best = e
		}
	}
	return best
}

// betterVictim reports whether a should be evicted before b.
//
// Class dominates recency: any speculative entry is evicted before any demanded
// one, however recently the speculative one was touched.
func betterVictim(a, b *entry) bool {
	if a.arrival != b.arrival {
		return a.arrival == Speculative
	}
	return a.used < b.used
}
