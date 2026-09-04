package datom

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// FormatVersion is the version of a run's layout.
//
// It is in the header from the first write rather than added when it is first
// needed: a format that acquires a version later has no way to describe what came
// before it.
const FormatVersion uint16 = 1

// HeaderSize is the fixed width of a run header: the format version and how many
// datoms follow.
const HeaderSize = 2 + 4

// FixedSize is the fixed part of one encoded datom, before its entity name,
// attribute name and value.
//
// flags(1) + entityLen(2) + attributeLen(2) + valueLen(4) + validFrom(8) +
// validTo(8) + txWall(8) + txLogical(4) + txSeq(4) + leafDepth(1) +
// leafPrefix(32).
//
// ⚠ 49 of these bytes are the transaction identifier, and 33 of those are the
// leaf. Eliding the leaf and taking it from the segment header would save them
// and is rejected in ADR-025: it makes a run readable only in the context of the
// segment around it. Shrinking it properly is the interning question, deferred
// whole to `BACKLOG.md` §12.
const FixedSize = 1 + 2 + 2 + 4 + 8 + 8 + 8 + 4 + 4 + 1 + 32

// MaxNameLen is the longest entity or attribute name a length prefix can express.
const MaxNameLen = 1<<16 - 1

// MaxValueLen is the longest value a length prefix can express.
const MaxValueLen = 1<<32 - 1

// Flag bits. ⚠ Both are STORED, never inferred: inferring IsReference from the
// shape of a value is what ADR-023 forbids, and inferring Assert from anything at
// all is what turns a truncation into a retraction.
const (
	flagAssert    byte = 1 << 0
	flagReference byte = 1 << 1

	// reservedFlags are the bits with no meaning in this version. A set one is
	// refused rather than masked off — within a known version it can only be
	// corruption, and masking returns a datom that decodes cleanly and means
	// something else.
	reservedFlags byte = ^(flagAssert | flagReference)
)

var (
	// ErrShortRun reports bytes that end before the run they claim to hold.
	//
	// ⚠ It is returned with NO datoms, not even the ones that decoded before the
	// damage. A caller that reads the slice before the error would otherwise act
	// on a datom whose Assert is false — a retraction of a fact nobody retracted.
	ErrShortRun = errors.New("datom: run ends early")

	// ErrUnknownVersion reports a run written by a build this one does not
	// understand. It is checked before anything after it is read.
	ErrUnknownVersion = errors.New("datom: unknown run format version")

	// ErrTooLong reports a name or value larger than its length prefix can
	// express. It is refused at encode time rather than wrapped into a smaller
	// number that would decode as a different, shorter fact.
	ErrTooLong = errors.New("datom: value or name exceeds what its length prefix can express")

	// ErrReservedFlag reports a flag bit with no meaning in this version.
	ErrReservedFlag = errors.New("datom: reserved flag bit is set")

	// ErrTrailingBytes reports bytes after the last datom of a run.
	//
	// Refused rather than ignored: a spliced or over-long buffer would otherwise
	// decode as though it were exactly right.
	ErrTrailingBytes = errors.New("datom: bytes after the end of the run")
)

// SizeOf returns what one datom costs on the wire, without encoding it.
//
// ★ It exists so a size bound counts the bytes that will ACTUALLY be written.
// Counting only values and ignoring the fixed part would under-report a run of
// many small facts by an order of magnitude — and many small facts is what a busy
// writer produces.
//
// ⚠ It excludes the run header, which is paid once per run rather than per datom.
// A caller summing this over n datoms and comparing against a byte budget is
// off by [HeaderSize], which is six bytes and does not move.
func SizeOf(d ports.Datom) int {
	return FixedSize + len(d.Entity) + len(d.Attribute) + len(d.Value)
}

// Encode writes a run of datoms.
//
// ⚠ EVERY field of every datom is written, whatever its value. Nothing is omitted
// for being zero, empty or false — an encoding that dropped a false Assert would
// save one bit and make a retraction indistinguishable from a truncation.
//
// The order given is the order written. Sorting belongs to whatever decides
// storage layout; doing it here would make the order a caller actually wrote
// unrecoverable, and would do it silently.
func Encode(datoms []ports.Datom) ([]byte, error) {
	size := HeaderSize
	for i, d := range datoms {
		if len(d.Entity) > MaxNameLen {
			return nil, fmt.Errorf("%w: datom %d has an entity of %d bytes and the prefix holds %d",
				ErrTooLong, i, len(d.Entity), MaxNameLen)
		}
		if len(d.Attribute) > MaxNameLen {
			return nil, fmt.Errorf("%w: datom %d has an attribute of %d bytes and the prefix holds %d",
				ErrTooLong, i, len(d.Attribute), MaxNameLen)
		}
		if uint64(len(d.Value)) > MaxValueLen {
			return nil, fmt.Errorf("%w: datom %d has a value of %d bytes and the prefix holds %d",
				ErrTooLong, i, len(d.Value), uint64(MaxValueLen))
		}
		size += FixedSize + len(d.Entity) + len(d.Attribute) + len(d.Value)
	}

	b := make([]byte, 0, size)
	b = binary.BigEndian.AppendUint16(b, FormatVersion)
	b = binary.BigEndian.AppendUint32(b, uint32(len(datoms)))

	for _, d := range datoms {
		var flags byte
		if d.Assert {
			flags |= flagAssert
		}
		if d.IsReference {
			flags |= flagReference
		}
		b = append(b, flags)
		b = binary.BigEndian.AppendUint16(b, uint16(len(d.Entity)))
		b = binary.BigEndian.AppendUint16(b, uint16(len(d.Attribute)))
		b = binary.BigEndian.AppendUint32(b, uint32(len(d.Value)))
		// ⚠ Both endpoints, always. An unbounded end is temporal.Forever written
		// out in full; leaving it implicit is how a fact acquires an end at the
		// epoch with nothing about it looking unusual.
		b = binary.BigEndian.AppendUint64(b, uint64(d.Valid.From))
		b = binary.BigEndian.AppendUint64(b, uint64(d.Valid.To))
		b = binary.BigEndian.AppendUint64(b, uint64(d.TxID.HLC.Wall))
		b = binary.BigEndian.AppendUint32(b, d.TxID.HLC.Logical)
		b = binary.BigEndian.AppendUint32(b, d.TxID.Seq)
		b = append(b, d.TxID.Leaf.Depth)
		b = append(b, d.TxID.Leaf.Prefix[:]...)
		b = append(b, d.Entity...)
		b = append(b, d.Attribute...)
		b = append(b, d.Value...)
	}
	return b, nil
}

// Decode reads a run written by [Encode].
//
// ⚠ On any error it returns NO datoms. See [ErrShortRun].
//
// Every length is checked against the bytes that remain before anything is
// allocated: a length prefix is a number a corrupt block chooses, and trusting
// one is how a flipped bit becomes a multi-gigabyte allocation.
func Decode(b []byte) ([]ports.Datom, error) {
	if len(b) < HeaderSize {
		return nil, fmt.Errorf("%w: %d bytes, too few for a run header of %d",
			ErrShortRun, len(b), HeaderSize)
	}
	// The version is read before anything else, so a run from a build this one
	// does not understand is refused rather than reinterpreted field by field.
	if version := binary.BigEndian.Uint16(b[0:2]); version != FormatVersion {
		return nil, fmt.Errorf("%w: run format %d, this build understands %d",
			ErrUnknownVersion, version, FormatVersion)
	}
	count := binary.BigEndian.Uint32(b[2:6])

	// ⚠ The count is a length like any other, and it sizes the slice below. The
	// smallest a datom can be is FixedSize, so the remaining bytes put a hard
	// ceiling on how many there can be.
	if uint64(count) > uint64(len(b)-HeaderSize)/FixedSize {
		return nil, fmt.Errorf("%w: run claims %d datoms and %d bytes cannot hold them",
			ErrShortRun, count, len(b))
	}

	out := make([]ports.Datom, 0, count)
	at := HeaderSize
	for i := uint32(0); i < count; i++ {
		if at+FixedSize > len(b) {
			return nil, fmt.Errorf("%w: ends inside the fixed part of datom %d", ErrShortRun, i)
		}

		flags := b[at]
		if flags&reservedFlags != 0 {
			return nil, fmt.Errorf("%w: datom %d carries flags %#02x and version %d defines only %#02x",
				ErrReservedFlag, i, flags, FormatVersion, flagAssert|flagReference)
		}

		entityLen := int(binary.BigEndian.Uint16(b[at+1 : at+3]))
		attributeLen := int(binary.BigEndian.Uint16(b[at+3 : at+5]))
		valueLen := binary.BigEndian.Uint32(b[at+5 : at+9])

		// ⚠ Summed in uint64 and compared with what remains BEFORE any of the
		// three is used to size anything. Three separate checks would each pass on
		// a buffer that cannot hold all three.
		body := uint64(entityLen) + uint64(attributeLen) + uint64(valueLen)
		remaining := uint64(len(b) - (at + FixedSize))
		if body > remaining {
			return nil, fmt.Errorf("%w: datom %d needs %d more bytes and %d remain",
				ErrShortRun, i, body, remaining)
		}

		d := ports.Datom{
			Valid: temporal.Interval{
				From: int64(binary.BigEndian.Uint64(b[at+9 : at+17])),
				To:   int64(binary.BigEndian.Uint64(b[at+17 : at+25])),
			},
			TxID: tx.TxID{
				HLC: hlc.Timestamp{
					Wall:    int64(binary.BigEndian.Uint64(b[at+25 : at+33])),
					Logical: binary.BigEndian.Uint32(b[at+33 : at+37]),
				},
				Seq: binary.BigEndian.Uint32(b[at+37 : at+41]),
			},
			Assert:      flags&flagAssert != 0,
			IsReference: flags&flagReference != 0,
		}
		d.TxID.Leaf.Depth = b[at+41]
		copy(d.TxID.Leaf.Prefix[:], b[at+42:at+FixedSize])

		// ⚠ Everything below COPIES out of b. A run may be sitting in a memory
		// mapping (ADR-024), where a returned sub-slice becomes a dangling pointer
		// the moment the segment is closed. Conversion to string copies; the value
		// is copied explicitly for the same reason.
		p := at + FixedSize
		d.Entity = string(b[p : p+entityLen])
		p += entityLen
		d.Attribute = string(b[p : p+attributeLen])
		p += attributeLen
		// A zero-length value becomes an empty non-nil slice. This is the one
		// normalisation: in Go nil and []byte{} differ, and as facts they do not.
		d.Value = make([]byte, valueLen)
		copy(d.Value, b[p:p+int(valueLen)])
		p += int(valueLen)

		out = append(out, d)
		at = p
	}

	if at != len(b) {
		return nil, fmt.Errorf("%w: %d bytes follow the last of %d datoms",
			ErrTrailingBytes, len(b)-at, count)
	}
	return out, nil
}
