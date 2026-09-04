package erasure

import (
	"encoding/binary"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/segment"
)

// FragmentHeaderSize is the fixed width of a fragment's own preamble: its index
// within the stripe, and the checksum of its bytes.
const FragmentHeaderSize = 1 + 4

// Fragment is one piece of a stripe.
//
// ★ The checksum is the point. A code with m parity fragments corrects m
// fragments known to be MISSING, but only ⌊m/2⌋ that are present and WRONG,
// because locating a fault costs as much redundancy as repairing it. Verifying
// each fragment turns every error into an erasure before decoding starts, which
// restores the full m tolerance and removes the case where reconstruction
// returns wrong bytes and reports success.
type Fragment struct {
	// Index is the fragment's position in the stripe. Positions below k are
	// data; the rest are parity. The code solves for positions, so a fragment
	// that has lost its index is unusable even if its bytes are intact.
	Index uint8
	// Checksum covers Bytes.
	Checksum uint32
	// Bytes is the fragment's payload.
	Bytes []byte
}

// NewFragment builds a fragment and computes its checksum.
//
// It uses the segment format's checksum rather than a second implementation:
// one polynomial, one place to change it. Two checksum functions would
// eventually disagree, and the disagreement would present as corruption.
func NewFragment(index uint8, b []byte) Fragment {
	return Fragment{Index: index, Checksum: segment.Checksum(b), Bytes: b}
}

// Verify reports whether the fragment's bytes still match its checksum.
//
// It returns [segment.ErrCorruptBlock] so that a caller handling storage faults
// has one error to test for, whether the fault was found in a block or in a
// fragment of one.
func (f Fragment) Verify() error {
	if got := segment.Checksum(f.Bytes); got != f.Checksum {
		return fmt.Errorf("%w: fragment %d computed %08x, header records %08x over %d bytes",
			segment.ErrCorruptBlock, f.Index, got, f.Checksum, len(f.Bytes))
	}
	return nil
}

// EncodeHeader returns the fixed-width, big-endian form of a fragment's
// preamble. The payload follows it unchanged.
func (f Fragment) EncodeHeader() [FragmentHeaderSize]byte {
	var b [FragmentHeaderSize]byte
	b[0] = f.Index
	binary.BigEndian.PutUint32(b[1:5], f.Checksum)
	return b
}

// DecodeFragment reverses [Fragment.EncodeHeader], taking the payload that
// follows it.
func DecodeFragment(header []byte, payload []byte) (Fragment, error) {
	if len(header) < FragmentHeaderSize {
		return Fragment{}, fmt.Errorf("%w: got %d bytes, want %d for a fragment header",
			ErrShortBuffer, len(header), FragmentHeaderSize)
	}
	return Fragment{
		Index:    header[0],
		Checksum: binary.BigEndian.Uint32(header[1:5]),
		Bytes:    payload,
	}, nil
}
