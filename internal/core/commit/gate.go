package commit

import (
	"fmt"
	"sync"

	"github.com/atvirokodosprendimai/sdev1/internal/core/lease"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tail"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// Gate holds writes until they commit, and then publishes them.
//
// ★ It adds NO visibility mechanism. The tail's watermark already makes an
// unpublished entry unreachable rather than half-visible, so all this does is
// decide when the watermark advances — which makes the watermark the commit
// point rather than a second definition beside it.
type Gate struct {
	mu        sync.Mutex
	tail      *tail.Tail
	condition Condition
	epoch     lease.Epoch

	// order is the sequence writes arrived in. Entries publish in this order and
	// no other; see Acknowledge.
	order []tx.TxID
	held  map[tx.TxID]*held
}

type held struct {
	datoms    []ports.Datom
	acks      []Ack
	committed bool
}

// NewGate returns a gate over a tail.
func NewGate(t *tail.Tail, c Condition, e lease.Epoch) (*Gate, error) {
	if t == nil {
		return nil, fmt.Errorf("commit: a gate needs a tail to publish into")
	}
	if c.DomainLevel == "" {
		return nil, fmt.Errorf("commit: a gate needs a condition with a domain level")
	}
	return &Gate{tail: t, condition: c, epoch: e, held: map[tx.TxID]*held{}}, nil
}

// Write records an entry as pending. It is NOT visible.
func (g *Gate) Write(id tx.TxID, datoms []ports.Datom) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.held[id]; ok {
		return fmt.Errorf("commit: %v is already pending", id)
	}
	kept := make([]ports.Datom, len(datoms))
	copy(kept, datoms)
	g.held[id] = &held{datoms: kept}
	g.order = append(g.order, id)
	return nil
}

// Acknowledge records one replica's acknowledgement and publishes whatever that
// makes committable.
//
// ⚠ Entries publish IN ORDER. An entry whose own condition is met stays pending
// while an earlier entry has not committed, because the watermark's meaning is a
// stable PREFIX — publishing past a gap would let a reader see a later write
// without an earlier one and call it a prefix.
//
// So one acknowledgement can commit several entries at once: the one it
// satisfied, plus every later entry that was already satisfied and waiting.
func (g *Gate) Acknowledge(id tx.TxID, a Ack) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	h, ok := g.held[id]
	if !ok {
		return 0, fmt.Errorf("commit: %v is not pending", id)
	}
	if !h.committed {
		h.acks = append(h.acks, a)
	}
	return g.drain()
}

// drain publishes every leading entry whose condition is met, and stops at the
// first that is not. The caller holds the lock.
func (g *Gate) drain() (int, error) {
	published := 0
	for _, id := range g.order {
		h := g.held[id]
		if h.committed {
			continue
		}
		if err := g.condition.Satisfied(h.acks, g.epoch); err != nil {
			// The prefix stops here. Later entries wait however satisfied they
			// are, because the watermark means a prefix.
			return published, nil
		}
		if _, err := g.tail.Append(g.epoch, id, h.datoms); err != nil {
			return published, fmt.Errorf("commit: publishing %v: %w", id, err)
		}
		h.committed = true
		published++
	}
	return published, nil
}

// Committed reports whether an entry has been published.
//
// ★ There is exactly one definition of committed, and this reads the same state
// the watermark was advanced from. Two definitions would drift, and the drift
// would show only under partial failure — which is when nobody is reading test
// output.
func (g *Gate) Committed(id tx.TxID) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	h, ok := g.held[id]
	return ok && h.committed
}

// Pending is how many entries have been written and not yet committed.
//
// It is the exposure window an operator needs during a degradation: how much has
// been accepted by this node and acknowledged to nobody.
func (g *Gate) Pending() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, h := range g.held {
		if !h.committed {
			n++
		}
	}
	return n
}

// Why reports why an entry has not committed, or nil if it has.
func (g *Gate) Why(id tx.TxID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	h, ok := g.held[id]
	if !ok {
		return fmt.Errorf("commit: %v is not pending", id)
	}
	if h.committed {
		return nil
	}
	return g.condition.Satisfied(h.acks, g.epoch)
}
