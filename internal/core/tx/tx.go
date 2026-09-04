package tx

import (
	"bytes"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
)

// TxID identifies one transaction, totally ordered across the whole cluster and
// strictly monotonic within its own leaf.
type TxID struct {
	HLC  hlc.Timestamp
	Leaf addr.LeafID
	Seq  uint32
}

// Compare orders by clock reading, then by leaf, then by sequence.
//
// The two tie-breakers are what make the order TOTAL rather than merely
// partial. Two leaves minting at the identical clock reading is ordinary on a
// busy cluster, and without a decision between them a query spanning both has
// no defined answer.
func (t TxID) Compare(other TxID) int {
	if c := t.HLC.Compare(other.HLC); c != 0 {
		return c
	}
	if c := compareLeaf(t.Leaf, other.Leaf); c != 0 {
		return c
	}
	switch {
	case t.Seq < other.Seq:
		return -1
	case t.Seq > other.Seq:
		return 1
	default:
		return 0
	}
}

// compareLeaf orders leaf identifiers by prefix bytes, then by depth.
//
// Unused prefix bytes are zero, so comparing the whole array is well defined,
// and depth breaks the tie between a shallow leaf and a deeper one sharing its
// bytes. The ordering lives here rather than in package addr because it is this
// record's rule about identity, not the addressing model's.
func compareLeaf(a, b addr.LeafID) int {
	if c := bytes.Compare(a.Prefix[:], b.Prefix[:]); c != 0 {
		return c
	}
	switch {
	case a.Depth < b.Depth:
		return -1
	case a.Depth > b.Depth:
		return 1
	default:
		return 0
	}
}

// String renders an identifier for a diagnostic.
func (t TxID) String() string {
	return fmt.Sprintf("%s@%s#%d", t.HLC, t.Leaf, t.Seq)
}
