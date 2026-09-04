package segstore

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/segment"
)

// FormatVersion is the version of the CONTAINER — how blocks, index and trailer
// are arranged in a file.
//
// It is separate from [segment.FormatVersion], which versions what one block is.
// Two versions rather than one because the two change for different reasons: a
// new codec or cipher changes a block without touching the container, and a new
// index encoding changes the container without touching a single block.
const FormatVersion uint16 = 1

// TrailerMagic marks the last bytes of a sealed segment.
//
// It is the first thing checked, and it is what makes a truncated or unrelated
// file a named refusal rather than a set of plausible byte offsets.
var TrailerMagic = [8]byte{'S', 'D', 'E', 'V', '1', 'S', 'E', 'G'}

// TrailerSize is the fixed width of the trailer: magic, version, where the index
// starts, how long it is, and a checksum over it.
//
// ★ Fixed width is the whole point. One seek to the end of a file of any size
// finds it, with no scan and no metadata kept beside the file — and a file too
// short to hold one cannot be mistaken for a segment.
const TrailerSize = 8 + 2 + 8 + 8 + 4

// maxKeyLen is what the index encoding can express for one key.
const maxKeyLen = 1<<16 - 1

var (
	// ErrNotASegment reports a file that does not end in a valid trailer: too
	// short, truncated, or never a segment at all.
	//
	// ★ It is deliberately distinct from [ErrIndexCorrupt]. "Incomplete" is what
	// a crash leaves and can be deleted without judgement; "corrupt" is a
	// complete segment whose contents disagree with themselves, and needs
	// someone to look.
	ErrNotASegment = errors.New("segstore: not a segment")

	// ErrIndexCorrupt reports an index that failed its checksum, or one whose
	// entries do not describe this file.
	//
	// ⚠ An index is a list of byte offsets. A wrong one does not fail — it reads
	// arbitrary bytes, and arbitrary bytes are indistinguishable from a block
	// until the block's own checksum says otherwise. Refusing here is what stops
	// that from being a read that merely looks odd.
	ErrIndexCorrupt = errors.New("segstore: index does not describe this segment")

	// ErrNoSuchBlock reports a key this segment does not hold.
	//
	// ⚠ It is NOT an empty block. A caller that treats absence as emptiness
	// writes over a fact it never read, and a block containing nothing is a
	// legitimate value that must stay distinguishable from one that is not there.
	ErrNoSuchBlock = errors.New("segstore: no such block")

	// ErrClosed reports a read attempted after [Reader.Close].
	//
	// ⚠ It exists because of the mapping. Without this check the read would touch
	// unmapped memory and arrive as a signal rather than an error, with no stack
	// naming the close that caused it.
	ErrClosed = errors.New("segstore: reader is closed")

	// ErrSealed reports a write attempted after [Writer.Seal].
	//
	// A sealed segment is published and immutable; the alternative to naming this
	// is a confusing failure from a closed file descriptor.
	ErrSealed = errors.New("segstore: segment is already sealed")

	// ErrDuplicateKey reports a key appended twice to one segment.
	//
	// ⚠ Refused rather than tolerated: the index is sorted and binary-searched,
	// so a second entry under the same key is a block that was written, is paid
	// for on disk, and can never be read. Silently unreachable data is worse than
	// a refusal at the moment it is created.
	ErrDuplicateKey = errors.New("segstore: key already in this segment")
)

// trailer is the fixed-width footer that says a file is a finished segment and
// where to find its index.
type trailer struct {
	Version  uint16
	IndexOff uint64
	IndexLen uint64
	IndexSum uint32
}

func (t trailer) encode() [TrailerSize]byte {
	var b [TrailerSize]byte
	copy(b[0:8], TrailerMagic[:])
	binary.BigEndian.PutUint16(b[8:10], t.Version)
	binary.BigEndian.PutUint64(b[10:18], t.IndexOff)
	binary.BigEndian.PutUint64(b[18:26], t.IndexLen)
	binary.BigEndian.PutUint32(b[26:30], t.IndexSum)
	return b
}

// decodeTrailer reverses [trailer.encode].
//
// It refuses an unknown container version with [segment.ErrUnknownVersion] rather
// than a sentinel of its own: the corpus already has one name for "this is the
// right kind of thing, written by a future you do not understand", and a second
// would only make a caller check twice.
func decodeTrailer(b []byte) (trailer, error) {
	if len(b) != TrailerSize {
		return trailer{}, fmt.Errorf("%w: %d trailer bytes, want %d", ErrNotASegment, len(b), TrailerSize)
	}
	if string(b[0:8]) != string(TrailerMagic[:]) {
		return trailer{}, fmt.Errorf("%w: the last %d bytes are not a trailer", ErrNotASegment, TrailerSize)
	}
	t := trailer{
		Version:  binary.BigEndian.Uint16(b[8:10]),
		IndexOff: binary.BigEndian.Uint64(b[10:18]),
		IndexLen: binary.BigEndian.Uint64(b[18:26]),
		IndexSum: binary.BigEndian.Uint32(b[26:30]),
	}
	if t.Version != FormatVersion {
		return trailer{}, fmt.Errorf("%w: segment container version %d, this build understands %d",
			segment.ErrUnknownVersion, t.Version, FormatVersion)
	}
	return t, nil
}

// indexEntry locates one block: where it starts and how far it runs.
//
// Span covers the block's header AND its stored bytes, so the index can be bounds
// checked against the file without touching the block region — which on a mapped
// file means without faulting in every page at open time.
type indexEntry struct {
	Key    string
	Offset uint64
	Span   uint32
}

// encodeIndex writes entries in the order given. The caller sorts.
func encodeIndex(entries []indexEntry) []byte {
	size := 4
	for _, e := range entries {
		size += 2 + len(e.Key) + 8 + 4
	}
	b := make([]byte, 0, size)
	b = binary.BigEndian.AppendUint32(b, uint32(len(entries)))
	for _, e := range entries {
		b = binary.BigEndian.AppendUint16(b, uint16(len(e.Key)))
		b = append(b, e.Key...)
		b = binary.BigEndian.AppendUint64(b, e.Offset)
		b = binary.BigEndian.AppendUint32(b, e.Span)
	}
	return b
}

// decodeIndex reverses [encodeIndex].
//
// Every length is checked against what remains, because this runs on bytes that
// have already passed a checksum and could still have been written by a different
// version of this package.
func decodeIndex(b []byte) ([]indexEntry, error) {
	if len(b) < 4 {
		return nil, fmt.Errorf("%w: %d index bytes, too few for a count", ErrIndexCorrupt, len(b))
	}
	n := binary.BigEndian.Uint32(b[0:4])
	at := 4

	// A count is four attacker-agnostic bytes but still a length: allocating from
	// it before the body is read would let a corrupt segment ask for gigabytes.
	// The smallest an entry can be is 14 bytes, so the count has a hard ceiling.
	if uint64(n) > uint64(len(b)-4)/14 {
		return nil, fmt.Errorf("%w: index claims %d entries in %d bytes", ErrIndexCorrupt, n, len(b))
	}

	entries := make([]indexEntry, 0, n)
	for i := uint32(0); i < n; i++ {
		if at+2 > len(b) {
			return nil, fmt.Errorf("%w: index ends inside entry %d", ErrIndexCorrupt, i)
		}
		keyLen := int(binary.BigEndian.Uint16(b[at : at+2]))
		at += 2
		if at+keyLen+12 > len(b) {
			return nil, fmt.Errorf("%w: index ends inside entry %d", ErrIndexCorrupt, i)
		}
		key := string(b[at : at+keyLen])
		at += keyLen
		off := binary.BigEndian.Uint64(b[at : at+8])
		at += 8
		span := binary.BigEndian.Uint32(b[at : at+4])
		at += 4
		entries = append(entries, indexEntry{Key: key, Offset: off, Span: span})
	}
	if at != len(b) {
		return nil, fmt.Errorf("%w: %d bytes left over after %d index entries", ErrIndexCorrupt, len(b)-at, n)
	}
	return entries, nil
}
