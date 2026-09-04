package datom

import (
	"bytes"
	"encoding/binary"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// ★ Six of the ten tests here hand Decode bytes that Encode never produced. A
// round-trip test alone proves almost nothing: every field round-trips under a
// decoder that also happily decodes garbage, and the failure this record is about
// is only visible on input the encoder could not have written.

func testTx(wall int64, seq uint32) tx.TxID {
	return tx.TxID{
		HLC:  hlc.Timestamp{Wall: wall, Logical: 7},
		Leaf: addr.TenantFromUint(9).TenantSubtree(),
		Seq:  seq,
	}
}

func fact(entity, attribute string, value []byte) ports.Datom {
	return ports.Datom{
		Entity:    entity,
		Attribute: attribute,
		Value:     value,
		Valid:     temporal.Interval{From: 100, To: temporal.Forever},
		TxID:      testTx(1000, 1),
		Assert:    true,
	}
}

// sameDatom compares two datoms as FACTS. A nil value and an empty one are the
// same fact, which is the one normalisation this package makes.
func sameDatom(a, b ports.Datom) bool {
	return a.Entity == b.Entity &&
		a.Attribute == b.Attribute &&
		bytes.Equal(a.Value, b.Value) &&
		a.Valid == b.Valid &&
		a.TxID == b.TxID &&
		a.Assert == b.Assert &&
		a.IsReference == b.IsReference
}

func TestATruncatedRunIsRefusedRatherThanZeroFilled(t *testing.T) {
	// ⚠ ASSERTED datoms on purpose. A partially filled ports.Datom has Assert
	// false, which is a RETRACTION — so if any prefix of this run decodes, what
	// comes back withdraws a fact that was asserted, and reports success.
	run := []ports.Datom{
		fact("planet-3", "mass", []byte("5.97e24")),
		fact("planet-3", "orbits", []byte("star-1")),
		fact("planet-4", "mass", []byte("6.42e23")),
	}
	b, err := Encode(run)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	for n := 0; n < len(b); n++ {
		got, err := Decode(b[:n])
		if err == nil {
			t.Fatalf("Decode accepted the first %d of %d bytes and returned %d datoms", n, len(b), len(got))
		}
		if !errors.Is(err, ErrShortRun) {
			t.Errorf("Decode(b[:%d]) error = %v, want ErrShortRun", n, err)
		}
		if len(got) != 0 {
			t.Fatalf("Decode(b[:%d]) returned %d datoms alongside its error; a caller that reads "+
				"the slice before the error acts on a retraction nobody wrote", n, len(got))
		}
	}
}

func TestEveryFieldRoundTrips(t *testing.T) {
	big := make([]byte, 200_000)
	for i := range big {
		big[i] = byte(i * 7)
	}

	run := []ports.Datom{
		// An assertion with an unbounded end.
		fact("planet-3", "mass", []byte("5.97e24")),
		// A RETRACTION. Its Assert bit is the one a truncation would forge.
		{
			Entity: "planet-3", Attribute: "mass", Value: []byte("5.97e24"),
			Valid: temporal.Interval{From: 100, To: 900},
			TxID:  testTx(2000, 4), Assert: false,
		},
		// A REFERENCE. Same nine bytes as a name; only the bit tells them apart.
		{
			Entity: "planet-3", Attribute: "orbits", Value: []byte("planet-9"),
			Valid: temporal.Interval{From: 0, To: temporal.Forever},
			TxID:  testTx(3000, 1), Assert: true, IsReference: true,
		},
		// A retracted reference — both bits at once.
		{
			Entity: "planet-3", Attribute: "orbits", Value: []byte("planet-9"),
			Valid: temporal.Interval{From: 5, To: 6},
			TxID:  testTx(4000, 2), Assert: false, IsReference: true,
		},
		// An empty value, and a nil one: the same fact.
		fact("planet-3", "note", []byte{}),
		fact("planet-3", "other", nil),
		// A large value, and multi-byte names.
		fact("planet-3", "atlas", big),
		fact("žvaigždė-7", "masė", []byte("ø∞")),
		// Negative instants: the axes are signed and an instant before the epoch
		// is ordinary, not a sentinel.
		{
			Entity: "planet-3", Attribute: "ancient", Value: []byte("x"),
			Valid: temporal.Interval{From: -9000, To: -10},
			TxID:  testTx(-1, 0), Assert: true,
		},
	}

	b, err := Encode(run)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(b)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got) != len(run) {
		t.Fatalf("Decode returned %d datoms, want %d", len(got), len(run))
	}
	for i := range run {
		if !sameDatom(got[i], run[i]) {
			t.Errorf("datom %d did not survive the round trip:\n got %+v\nwant %+v", i, got[i], run[i])
		}
	}

	// The stated normalisation, asserted rather than assumed.
	for i, d := range got {
		if d.Value == nil {
			t.Errorf("datom %d decoded to a nil value; a zero-length value is an empty non-nil slice", i)
		}
	}
}

func TestAnUnboundedEndIsForeverNotZero(t *testing.T) {
	b, err := Encode([]ports.Datom{fact("planet-3", "mass", []byte("m"))})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// ⚠ Read straight out of the bytes. A decoder that substituted Forever for a
	// zero it found would pass a round-trip test and put an epoch-ended fact on
	// every disk.
	at := HeaderSize + 17
	if written := int64(binary.BigEndian.Uint64(b[at : at+8])); written != temporal.Forever {
		t.Fatalf("the encoded validity end is %d, want temporal.Forever (%d)", written, temporal.Forever)
	}

	got, err := Decode(b)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got[0].Valid.To != temporal.Forever {
		t.Errorf("decoded validity end = %d, want Forever (%d)", got[0].Valid.To, temporal.Forever)
	}
	if got[0].Valid.To == 0 {
		t.Errorf("an unbounded end decoded as zero: the fact now reads as having ended at the epoch")
	}
}

func TestALengthIsCheckedBeforeItIsAllocated(t *testing.T) {
	b, err := Encode([]ports.Datom{{
		Entity: "", Attribute: "", Value: nil,
		Valid: temporal.Interval{From: 0, To: temporal.Forever},
		TxID:  testTx(1, 1), Assert: true,
	}})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(b) != HeaderSize+FixedSize {
		t.Fatalf("a datom with no names and no value encoded to %d bytes, want %d", len(b), HeaderSize+FixedSize)
	}

	// A gigabyte rather than the MaxUint32 a corrupt block could really carry:
	// the honest worst case is four times this, and a mutant that removed the
	// check would then be free to destabilise the machine running the fence.
	// A gigabyte is already a thousand times the ceiling asserted below.
	const bogus = 1 << 30
	binary.BigEndian.PutUint32(b[HeaderSize+5:HeaderSize+9], bogus)

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got, err := Decode(b)
	runtime.ReadMemStats(&after)

	if !errors.Is(err, ErrShortRun) {
		t.Fatalf("Decode of a run claiming a %d-byte value in %d bytes = %v, want ErrShortRun", bogus, len(b), err)
	}
	if len(got) != 0 {
		t.Errorf("Decode returned %d datoms with its error", len(got))
	}

	// ⚠ The error alone proves nothing here: it would be returned just as
	// truthfully AFTER the allocation happened. Heap growth is the only
	// observable difference between checking first and checking second.
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
		t.Errorf("Decode allocated %d bytes refusing a length it had not checked; want under 1 MiB", grew)
	}
}

func TestAnUnknownVersionIsRefused(t *testing.T) {
	b, err := Encode([]ports.Datom{fact("planet-3", "mass", []byte("m"))})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// Everything after the version stays well-formed on purpose: the refusal must
	// come from the version rather than from the damage.
	binary.BigEndian.PutUint16(b[0:2], FormatVersion+41)

	got, err := Decode(b)
	if !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("Decode of a run at an unknown version = %v, want ErrUnknownVersion", err)
	}
	if len(got) != 0 {
		t.Errorf("Decode returned %d datoms with its error", len(got))
	}
}

func TestAReservedFlagBitIsRefused(t *testing.T) {
	b, err := Encode([]ports.Datom{fact("planet-3", "mass", []byte("m"))})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	b[HeaderSize] |= 1 << 7

	got, err := Decode(b)
	if !errors.Is(err, ErrReservedFlag) {
		t.Fatalf("Decode of a datom with a reserved flag bit = %v, want ErrReservedFlag", err)
	}
	if len(got) != 0 {
		t.Errorf("Decode returned %d datoms with its error", len(got))
	}
}

func TestANameTooLongIsRefusedAtEncode(t *testing.T) {
	long := strings.Repeat("x", MaxNameLen+1)

	if _, err := Encode([]ports.Datom{fact(long, "mass", nil)}); !errors.Is(err, ErrTooLong) {
		t.Errorf("Encode with an over-long entity = %v, want ErrTooLong", err)
	}
	if _, err := Encode([]ports.Datom{fact("planet-3", long, nil)}); !errors.Is(err, ErrTooLong) {
		t.Errorf("Encode with an over-long attribute = %v, want ErrTooLong", err)
	}
	// The boundary itself is legal, so the refusal is at the right place rather
	// than one byte early.
	if _, err := Encode([]ports.Datom{fact(strings.Repeat("x", MaxNameLen), "mass", nil)}); err != nil {
		t.Errorf("Encode with a name of exactly MaxNameLen = %v, want it accepted", err)
	}
}

func TestAnEmptyRunIsNotATruncatedOne(t *testing.T) {
	b, err := Encode(nil)
	if err != nil {
		t.Fatalf("Encode(nil): %v", err)
	}
	if len(b) != HeaderSize {
		t.Fatalf("an empty run encoded to %d bytes, want a bare header of %d", len(b), HeaderSize)
	}

	got, err := Decode(b)
	if err != nil {
		t.Fatalf("Decode of an empty run: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("an empty run decoded to %d datoms", len(got))
	}

	// One byte short of a header is a different answer, and it must be an error.
	if _, err := Decode(b[:HeaderSize-1]); !errors.Is(err, ErrShortRun) {
		t.Errorf("Decode of a truncated header = %v, want ErrShortRun", err)
	}
}

func TestOrderIsPreserved(t *testing.T) {
	// Deliberately not EAVT order. An encoder that sorted would make the order a
	// caller actually wrote unrecoverable, and would do it without saying so.
	run := []ports.Datom{
		fact("zeta", "radius", []byte("3")),
		fact("alpha", "mass", []byte("1")),
		fact("mid", "orbits", []byte("2")),
		fact("alpha", "albedo", []byte("4")),
	}
	b, err := Encode(run)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(b)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for i := range run {
		if got[i].Entity != run[i].Entity || got[i].Attribute != run[i].Attribute {
			t.Fatalf("datom %d came back as %s/%s, want %s/%s",
				i, got[i].Entity, got[i].Attribute, run[i].Entity, run[i].Attribute)
		}
	}
}

func TestTrailingBytesAreRefused(t *testing.T) {
	b, err := Encode([]ports.Datom{fact("planet-3", "mass", []byte("m"))})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	got, err := Decode(append(b, 0x00))
	if !errors.Is(err, ErrTrailingBytes) {
		t.Fatalf("Decode of a run with a byte appended = %v, want ErrTrailingBytes", err)
	}
	if len(got) != 0 {
		t.Errorf("Decode returned %d datoms with its error", len(got))
	}
}
