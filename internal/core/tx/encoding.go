package tx

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
)

// leafEncodedSize is a leaf identifier on the wire: every prefix byte, then the
// depth. The whole prefix is written even though only the first Depth bytes are
// meaningful, because a fixed width is what lets an index stride over records
// without a length prefix, and the unused bytes are zero so the encoding stays
// comparable.
const leafEncodedSize = 32 + 1

// EncodedSize is the width of a [TxID] on the wire.
const EncodedSize = hlc.EncodedSize + leafEncodedSize + 4

// ErrShortEncoding reports a buffer that is not [EncodedSize] bytes.
var ErrShortEncoding = errors.New("tx: encoded identifier is the wrong size")

// Encode returns the fixed-width, byte-comparable form.
//
// The field order matches [TxID.Compare] — clock, then leaf, then sequence —
// and every field is big-endian, so comparing two encodings with bytes.Compare
// yields the same order as comparing the values. That is what lets a segment
// index sort on the bytes without decoding them.
func (t TxID) Encode() [EncodedSize]byte {
	var b [EncodedSize]byte
	clock := t.HLC.Encode()
	n := copy(b[0:], clock[:])
	n += copy(b[n:], t.Leaf.Prefix[:])
	b[n] = t.Leaf.Depth
	n++
	binary.BigEndian.PutUint32(b[n:], t.Seq)
	return b
}

// Decode reverses [TxID.Encode].
func Decode(b [EncodedSize]byte) (TxID, error) {
	var clock [hlc.EncodedSize]byte
	copy(clock[:], b[0:hlc.EncodedSize])
	ts, err := hlc.Decode(clock)
	if err != nil {
		return TxID{}, fmt.Errorf("tx: decode clock: %w", err)
	}

	var leaf addr.LeafID
	off := hlc.EncodedSize
	copy(leaf.Prefix[:], b[off:off+32])
	leaf.Depth = b[off+32]
	off += leafEncodedSize

	return TxID{
		HLC:  ts,
		Leaf: leaf,
		Seq:  binary.BigEndian.Uint32(b[off:]),
	}, nil
}

// DecodeSlice reverses [TxID.Encode] for a slice of unknown length.
func DecodeSlice(b []byte) (TxID, error) {
	if len(b) != EncodedSize {
		return TxID{}, fmt.Errorf("%w: got %d bytes, want %d", ErrShortEncoding, len(b), EncodedSize)
	}
	var fixed [EncodedSize]byte
	copy(fixed[:], b)
	return Decode(fixed)
}
