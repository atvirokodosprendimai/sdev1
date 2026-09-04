package tx

import (
	"sync"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
)

// Minter issues transaction identifiers for one leaf.
//
// One minter per leaf, and the sequence it holds is safe only because a leaf
// has exactly one writer. If two processes ever mint for the same leaf they can
// produce colliding identifiers, and nothing here would notice: both look
// valid, and the log records two events at one position. The guard is the
// single-writer property upstream, not anything in this type.
type Minter struct {
	mu    sync.Mutex
	leaf  addr.LeafID
	clock *hlc.Clock
	seq   uint32
}

// NewMinter returns a Minter issuing identifiers for one leaf from one clock.
func NewMinter(leaf addr.LeafID, clock *hlc.Clock) *Minter {
	return &Minter{leaf: leaf, clock: clock}
}

// Leaf returns the leaf this minter issues for.
func (m *Minter) Leaf() addr.LeafID {
	return m.leaf
}

// Mint issues the next identifier, strictly greater than every identifier this
// minter has already issued.
//
// The clock reading and the sequence advance under one lock so they cannot be
// interleaved by a concurrent caller: two identifiers carrying the same reading
// and the same sequence would be indistinguishable, which is the one outcome
// the type exists to prevent.
func (m *Minter) Mint() TxID {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	return TxID{
		HLC:  m.clock.Now(),
		Leaf: m.leaf,
		Seq:  m.seq,
	}
}

// Observe absorbs an identifier received from another leaf, advancing this
// minter's clock past it so that everything minted afterwards is ordered after
// the remote transaction.
//
// This is how causality crosses a leaf boundary: a node that has seen a remote
// transaction cannot subsequently mint an identifier that appears to precede
// it.
func (m *Minter) Observe(remote TxID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clock.Merge(remote.HLC)
}
