package segment

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// BlockHeaderSize is the fixed width of an encoded block header. Fixed width is
// what lets a reader stride over headers without decoding bodies, and it bounds
// how small a block can usefully be — the header is paid once per block.
const BlockHeaderSize = 2 + 2 + 1 + 1 + 4 + 4 + 4

// checksumTable is the Castagnoli polynomial, chosen because it is what the
// standard library accelerates and because this is a detection code for
// accidental corruption rather than a defence against a deliberately altered
// block.
var checksumTable = crc32.MakeTable(crc32.Castagnoli)

// BlockHeader describes one block completely enough to read it.
//
// ⚠ Every field needed to decode the block is here rather than in
// configuration. That is the property the whole format rests on: a settings
// change can never reinterpret bytes already written.
type BlockHeader struct {
	Codec     CodecID
	Cipher    CipherID
	Stages    Stage
	RawLen    uint32
	StoredLen uint32
	Checksum  uint32
}

// Checksum computes the block checksum over stored bytes.
//
// It covers the STORED bytes — after compression and encryption — because those
// are the bytes that sit on a disk and can rot. A checksum over the raw bytes
// would report health it never checked.
func Checksum(stored []byte) uint32 {
	return crc32.Checksum(stored, checksumTable)
}

// Verify reports whether stored bytes match the header's checksum.
//
// It is what turns silent corruption into a detected fault. Without it, an
// erasure decoder handed a present-but-rotten fragment returns wrong data with
// no error anywhere, because decoding assumes it knows which fragments are
// missing rather than which are wrong.
func (h BlockHeader) Verify(stored []byte) error {
	if got := Checksum(stored); got != h.Checksum {
		return fmt.Errorf("%w: computed %08x, header records %08x over %d stored bytes",
			ErrCorruptBlock, got, h.Checksum, len(stored))
	}
	return nil
}

// Encode returns the fixed-width, big-endian form of a block header.
func (h BlockHeader) Encode() [BlockHeaderSize]byte {
	var b [BlockHeaderSize]byte
	putUint16(b[0:2], uint16(h.Codec))
	putUint16(b[2:4], uint16(h.Cipher))
	b[4] = byte(h.Stages)
	b[5] = 0 // reserved, so the header stays a round width if a flag byte is added
	putUint32(b[6:10], h.RawLen)
	putUint32(b[10:14], h.StoredLen)
	putUint32(b[14:18], h.Checksum)
	return b
}

// DecodeBlockHeader reverses [BlockHeader.Encode].
func DecodeBlockHeader(b []byte) (BlockHeader, error) {
	if len(b) < BlockHeaderSize {
		return BlockHeader{}, fmt.Errorf("%w: got %d bytes, want %d for a block header",
			ErrShortBuffer, len(b), BlockHeaderSize)
	}
	return BlockHeader{
		Codec:     CodecID(binary.BigEndian.Uint16(b[0:2])),
		Cipher:    CipherID(binary.BigEndian.Uint16(b[2:4])),
		Stages:    Stage(b[4]),
		RawLen:    binary.BigEndian.Uint32(b[6:10]),
		StoredLen: binary.BigEndian.Uint32(b[10:14]),
		Checksum:  binary.BigEndian.Uint32(b[14:18]),
	}, nil
}

func putUint16(b []byte, v uint16) { binary.BigEndian.PutUint16(b, v) }
func putUint32(b []byte, v uint32) { binary.BigEndian.PutUint32(b, v) }
func getUint32(b []byte) uint32    { return binary.BigEndian.Uint32(b) }
