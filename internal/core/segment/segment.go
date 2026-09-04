package segment

import (
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
)

// FormatVersion is the segment format this build understands. A segment
// declaring any other version is refused rather than partially read, so an
// incompatible change becomes a migration instead of a corruption.
const FormatVersion uint16 = 1

// DefaultBlockSize is a starting figure rather than a measured optimum: large
// enough for a compressor to find redundancy and for an erasure fragment to be
// meaningful, small enough that a random read does not pull an unreasonable
// amount.
//
// It is a DEFAULT and not a constant of the format. Every block header records
// its own lengths, so changing this reinterprets nothing already written.
const DefaultBlockSize = 4 << 20

var (
	// ErrUnknownVersion reports a segment written by a different release.
	ErrUnknownVersion = errors.New("segment: unknown format version")

	// ErrCorruptBlock reports a checksum mismatch. It is returned instead of
	// the bytes, because returning them would be returning wrong data as fact.
	ErrCorruptBlock = errors.New("segment: block failed its checksum")

	// ErrShortBuffer reports a buffer too small to hold what it must decode.
	ErrShortBuffer = errors.New("segment: buffer is too short")
)

// Stage is a bitmask recording which pipeline stages a block passed through.
//
// The order is fixed — compress, encrypt, code — so the flags say WHICH stages
// ran rather than in what order. A reader applies their inverses in the reverse
// of the fixed order and never has to guess.
type Stage uint8

const (
	// StageCompressed means a codec was applied.
	StageCompressed Stage = 1 << iota
	// StageEncrypted means a cipher was applied, after compression.
	StageEncrypted
	// StageCoded means the block was erasure-coded, after encryption.
	StageCoded
)

// Has reports whether a stage ran.
func (s Stage) Has(other Stage) bool { return s&other != 0 }

// String renders the stages for a diagnostic, in pipeline order.
func (s Stage) String() string {
	out := ""
	for _, st := range []struct {
		flag Stage
		name string
	}{
		{StageCompressed, "compressed"},
		{StageEncrypted, "encrypted"},
		{StageCoded, "coded"},
	} {
		if s.Has(st.flag) {
			if out != "" {
				out += "+"
			}
			out += st.name
		}
	}
	if out == "" {
		return "raw"
	}
	return out
}

// CipherID names the cipher a block was encrypted with. Zero means none.
//
// It is recorded per block for the same reason the codec is: a block encrypted
// under one scheme is only readable by something that knows which.
type CipherID uint16

// CipherNone means the block was not encrypted.
const CipherNone CipherID = 0

// Header is a segment's own header: what format it is, which leaf it belongs to,
// and how many blocks it holds.
type Header struct {
	Version uint16
	Leaf    addr.LeafID
	Blocks  uint32
}

// HeaderSize is the fixed width of an encoded segment header.
const HeaderSize = 2 + 32 + 1 + 4

// Encode returns the fixed-width form of a segment header.
func (h Header) Encode() [HeaderSize]byte {
	var b [HeaderSize]byte
	putUint16(b[0:2], h.Version)
	copy(b[2:34], h.Leaf.Prefix[:])
	b[34] = h.Leaf.Depth
	putUint32(b[35:39], h.Blocks)
	return b
}

// DecodeHeader reverses [Header.Encode], refusing an unknown version before
// reading anything that follows.
func DecodeHeader(b []byte) (Header, error) {
	if len(b) < HeaderSize {
		return Header{}, fmt.Errorf("%w: got %d bytes, want %d for a segment header",
			ErrShortBuffer, len(b), HeaderSize)
	}
	version := uint16(b[0])<<8 | uint16(b[1])
	if version != FormatVersion {
		return Header{}, fmt.Errorf("%w: %d, this build understands %d",
			ErrUnknownVersion, version, FormatVersion)
	}
	var h Header
	h.Version = version
	copy(h.Leaf.Prefix[:], b[2:34])
	h.Leaf.Depth = b[34]
	h.Blocks = getUint32(b[35:39])
	return h, nil
}
