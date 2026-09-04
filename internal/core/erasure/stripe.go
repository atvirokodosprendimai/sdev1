package erasure

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
)

// MaxCodePositions is the largest number of fragments one stripe may have.
//
// It is a property of the arithmetic rather than a policy: the code operates
// over GF(2^8), which has 256 elements and therefore admits at most 255 non-zero
// code positions. There is no valid wider scheme to permit, so a scheme that
// exceeds it is refused at construction rather than misbehaving later.
const MaxCodePositions = 255

// StripeHeaderSize is the fixed width of an encoded stripe header.
const StripeHeaderSize = 1 + 1 + 4 + 4 + addr.MaxDepth + 1 + 4

var (
	// ErrSchemeTooWide reports k+m above [MaxCodePositions].
	ErrSchemeTooWide = errors.New("erasure: scheme has more fragments than the field allows")

	// ErrInvalidScheme reports a scheme with no data fragments or no parity.
	//
	// It is a separate error from [ErrSchemeTooWide] because "too wide" is not a
	// truthful name for a scheme with zero parity, and a scheme with zero parity
	// tolerates nothing while still calling itself coded.
	ErrInvalidScheme = errors.New("erasure: scheme needs at least one data and one parity fragment")

	// ErrShortBuffer reports a buffer too small to hold what it must decode.
	ErrShortBuffer = errors.New("erasure: buffer is too short")
)

// StripeHeader describes one stripe completely enough to decode it.
//
// ⚠ Every field needed to reconstruct the block is here rather than in
// configuration. Changing the cluster's configured scheme changes what the next
// write produces and reinterprets nothing already written.
type StripeHeader struct {
	// DataShards is k: how many fragments the block was split into.
	DataShards uint8
	// ParityShards is m: how many fragments were computed from those, and
	// therefore how many of the k+m may be lost.
	ParityShards uint8
	// FragmentSize is the length of every fragment, including the padding on
	// the last data fragment. All fragments are equal length; the code requires
	// it.
	FragmentSize uint32
	// BlockLength is the block's true length before padding.
	//
	// It is recorded rather than inferred so that removing the padding never
	// depends on knowing how a particular library pads. A change in that
	// behaviour cannot then alter what a decoded block contains.
	BlockLength uint32
	// Leaf is the leaf of the address space this stripe's block belongs to.
	Leaf addr.LeafID
	// BlockIndex is the block's position within its segment.
	BlockIndex uint32
}

// Fragments is the total number of fragments in the stripe, k+m.
func (h StripeHeader) Fragments() int {
	return int(h.DataShards) + int(h.ParityShards)
}

// Validate reports whether the scheme can be coded at all.
func (h StripeHeader) Validate() error {
	if h.DataShards == 0 || h.ParityShards == 0 {
		return fmt.Errorf("%w: got %d data and %d parity",
			ErrInvalidScheme, h.DataShards, h.ParityShards)
	}
	if n := h.Fragments(); n > MaxCodePositions {
		return fmt.Errorf("%w: %d data plus %d parity is %d, and the field allows %d",
			ErrSchemeTooWide, h.DataShards, h.ParityShards, n, MaxCodePositions)
	}
	return nil
}

// Encode returns the fixed-width, big-endian form of a stripe header.
func (h StripeHeader) Encode() [StripeHeaderSize]byte {
	var b [StripeHeaderSize]byte
	b[0] = h.DataShards
	b[1] = h.ParityShards
	binary.BigEndian.PutUint32(b[2:6], h.FragmentSize)
	binary.BigEndian.PutUint32(b[6:10], h.BlockLength)
	copy(b[10:42], h.Leaf.Prefix[:])
	b[42] = h.Leaf.Depth
	binary.BigEndian.PutUint32(b[43:47], h.BlockIndex)
	return b
}

// DecodeStripeHeader reverses [StripeHeader.Encode], refusing a scheme that
// cannot be coded rather than returning a header nothing can use.
func DecodeStripeHeader(b []byte) (StripeHeader, error) {
	if len(b) < StripeHeaderSize {
		return StripeHeader{}, fmt.Errorf("%w: got %d bytes, want %d for a stripe header",
			ErrShortBuffer, len(b), StripeHeaderSize)
	}
	h := StripeHeader{
		DataShards:   b[0],
		ParityShards: b[1],
		FragmentSize: binary.BigEndian.Uint32(b[2:6]),
		BlockLength:  binary.BigEndian.Uint32(b[6:10]),
		BlockIndex:   binary.BigEndian.Uint32(b[43:47]),
	}
	copy(h.Leaf.Prefix[:], b[10:42])
	h.Leaf.Depth = b[42]
	if err := h.Validate(); err != nil {
		return StripeHeader{}, err
	}
	return h, nil
}
