package erasure

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/segment"
)

func testLeaf(t *testing.T) addr.LeafID {
	t.Helper()
	l, err := addr.Descend(addr.KeyOf(addr.TenantFromUint(3), "erasure-subject"), 2)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}
	return l
}

// TestStripeCarriesItsOwnScheme is the falsifier ADR-006 names in its Enforced-by
// header.
//
// A stripe must be decodable under the scheme it was written with. If k and m
// were held in configuration, changing the cluster's configured scheme would
// make every existing stripe undecodable — the failure this corpus has already
// rejected for the fan-out, the codec and the tenant width.
func TestStripeCarriesItsOwnScheme(t *testing.T) {
	for _, scheme := range [][2]uint8{{8, 2}, {10, 4}, {4, 2}, {1, 1}, {200, 55}} {
		h := StripeHeader{
			DataShards:   scheme[0],
			ParityShards: scheme[1],
			FragmentSize: 512 * 1024,
			BlockLength:  4 << 20,
			Leaf:         testLeaf(t),
			BlockIndex:   7,
		}
		enc := h.Encode()
		got, err := DecodeStripeHeader(enc[:])
		if err != nil {
			t.Fatalf("RS(%d,%d): DecodeStripeHeader: %v", scheme[0], scheme[1], err)
		}
		if got.DataShards != scheme[0] || got.ParityShards != scheme[1] {
			t.Errorf("scheme decoded as RS(%d,%d), want RS(%d,%d) — a stripe that does not record "+
				"its own scheme is only readable while the configuration happens to match",
				got.DataShards, got.ParityShards, scheme[0], scheme[1])
		}
		if got.FragmentSize != h.FragmentSize || got.BlockLength != h.BlockLength {
			t.Errorf("sizes decoded as fragment %d / block %d, want %d / %d",
				got.FragmentSize, got.BlockLength, h.FragmentSize, h.BlockLength)
		}
		if got != h {
			t.Errorf("stripe header round trip lost a field:\n got %+v\nwant %+v", got, h)
		}
	}
}

// TestSchemeWiderThanTheFieldIsRefused checks the two ways a scheme can be
// unusable are refused at construction, by different names.
func TestSchemeWiderThanTheFieldIsRefused(t *testing.T) {
	// GF(2^8) admits at most 255 non-zero code positions.
	wide := StripeHeader{DataShards: 200, ParityShards: 56} // 256
	if err := wide.Validate(); !errors.Is(err, ErrSchemeTooWide) {
		t.Errorf("RS(200,56) is %d fragments: error = %v, want ErrSchemeTooWide",
			wide.Fragments(), err)
	}
	if edge := (StripeHeader{DataShards: 200, ParityShards: 55}); edge.Validate() != nil {
		t.Errorf("RS(200,55) is exactly %d fragments and must be allowed: %v",
			MaxCodePositions, edge.Validate())
	}

	// Zero parity tolerates nothing while still calling itself coded; zero data
	// has nothing to code.
	for _, bad := range []StripeHeader{
		{DataShards: 8, ParityShards: 0},
		{DataShards: 0, ParityShards: 2},
		{},
	} {
		if err := bad.Validate(); !errors.Is(err, ErrInvalidScheme) {
			t.Errorf("RS(%d,%d): error = %v, want ErrInvalidScheme",
				bad.DataShards, bad.ParityShards, err)
		}
	}

	// A decode refuses too, so an unusable scheme cannot enter through bytes.
	enc := wide.Encode()
	if _, err := DecodeStripeHeader(enc[:]); !errors.Is(err, ErrSchemeTooWide) {
		t.Errorf("decoding a too-wide header: error = %v, want ErrSchemeTooWide", err)
	}
}

// TestStripeHeaderIsFixedWidth pins the wire layout and checks a reader can
// stride.
//
// ⚠ It asserts the exact bytes rather than only that encode-then-decode is the
// identity. A round trip uses the SAME offsets on both sides, so it cannot see a
// symmetric layout bug — two fields written and read at each other's offsets
// round-trip perfectly and are wrong for every other reader of the format.
// Asserting len(encoded) == StripeHeaderSize would prove even less: with a
// fixed-size array return that is a tautology, true at any value of the
// constant.
func TestStripeHeaderIsFixedWidth(t *testing.T) {
	var leaf addr.LeafID
	for i := range leaf.Prefix {
		leaf.Prefix[i] = byte(i)
	}
	leaf.Depth = 5

	h := StripeHeader{
		DataShards:   8,
		ParityShards: 2,
		FragmentSize: 0x00080000,
		BlockLength:  0x00400000,
		Leaf:         leaf,
		BlockIndex:   0x11223344,
	}
	want := make([]byte, 0, StripeHeaderSize)
	want = append(want, 8, 2)                   // k, m
	want = append(want, 0x00, 0x08, 0x00, 0x00) // fragment size
	want = append(want, 0x00, 0x40, 0x00, 0x00) // block length
	for i := 0; i < addr.MaxDepth; i++ {        // leaf prefix
		want = append(want, byte(i))
	}
	want = append(want, 5)                      // leaf depth
	want = append(want, 0x11, 0x22, 0x33, 0x44) // block index
	if len(want) != StripeHeaderSize {
		t.Fatalf("this test's expected layout is %d bytes but StripeHeaderSize is %d; "+
			"the layout changed and the test was not updated with it", len(want), StripeHeaderSize)
	}
	got := h.Encode()
	if !bytes.Equal(got[:], want) {
		t.Errorf("the encoded layout changed\n got % x\nwant % x", got[:], want)
	}

	// Striding: headers written back to back are each decodable at a fixed
	// offset, which is what a fixed width is for.
	rng := rand.New(rand.NewSource(20260904))
	const n = 32
	headers := make([]StripeHeader, n)
	var stream []byte
	for i := range headers {
		headers[i] = randomHeader(rng)
		enc := headers[i].Encode()
		stream = append(stream, enc[:]...)
	}
	if len(stream) != n*StripeHeaderSize {
		t.Fatalf("%d headers occupy %d bytes, want %d — the width is not fixed",
			n, len(stream), n*StripeHeaderSize)
	}
	for i, want := range headers {
		at := i * StripeHeaderSize
		got, err := DecodeStripeHeader(stream[at:])
		if err != nil {
			t.Fatalf("header %d at offset %d: %v", i, at, err)
		}
		if got != want {
			t.Fatalf("header %d did not decode at its stride offset\n got %+v\nwant %+v",
				i, got, want)
		}
	}
}

// TestStripeHeaderRoundTrips is a property test: encoding then decoding is the
// identity, so no field is silently dropped.
func TestStripeHeaderRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewSource(606))
	for i := 0; i < 2000; i++ {
		want := randomHeader(rng)
		enc := want.Encode()
		got, err := DecodeStripeHeader(enc[:])
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if got != want {
			t.Fatalf("case %d: round trip lost a field\n got %+v\nwant %+v", i, got, want)
		}
	}

	if _, err := DecodeStripeHeader(make([]byte, StripeHeaderSize-1)); !errors.Is(err, ErrShortBuffer) {
		t.Errorf("a truncated header: error = %v, want ErrShortBuffer", err)
	}
}

// randomHeader draws a header whose scheme is valid, since an invalid one is
// refused on decode and would test the refusal rather than the round trip.
func randomHeader(rng *rand.Rand) StripeHeader {
	k := 1 + rng.Intn(200)
	m := 1 + rng.Intn(MaxCodePositions-k)
	h := StripeHeader{
		DataShards:   uint8(k),
		ParityShards: uint8(m),
		FragmentSize: rng.Uint32(),
		BlockLength:  rng.Uint32(),
		BlockIndex:   rng.Uint32(),
	}
	rng.Read(h.Leaf.Prefix[:])
	h.Leaf.Depth = uint8(rng.Intn(addr.MaxDepth + 1))
	return h
}

// TestFragmentCarriesItsOwnChecksum checks a flipped bit in a fragment is
// detected, which is what lets a decoder treat the fragment as ABSENT rather
// than as data.
//
// Without it the code tolerates ⌊m/2⌋ faults instead of m, and a rotten fragment
// can produce a reconstruction that is wrong and reports success.
func TestFragmentCarriesItsOwnChecksum(t *testing.T) {
	payload := []byte("the quick brown fox jumps over the lazy dog")
	f := NewFragment(3, payload)

	if err := f.Verify(); err != nil {
		t.Fatalf("an intact fragment was refused: %v", err)
	}
	if f.Index != 3 {
		t.Errorf("index = %d, want 3 — the code solves for POSITIONS, so a fragment "+
			"that has lost its index is unusable even with intact bytes", f.Index)
	}

	for i := range payload {
		rotten := append([]byte(nil), payload...)
		rotten[i] ^= 0x01
		bad := Fragment{Index: f.Index, Checksum: f.Checksum, Bytes: rotten}
		if err := bad.Verify(); !errors.Is(err, segment.ErrCorruptBlock) {
			t.Fatalf("a flipped bit at byte %d was not detected: %v", i, err)
		}
	}

	// Two different fragments of the same stripe do not share a checksum by
	// accident of construction.
	other := NewFragment(4, []byte("a different payload entirely"))
	if other.Checksum == f.Checksum {
		t.Error("two unrelated fragments share a checksum; the checksum is not covering the bytes")
	}

	// The preamble round-trips, so a fragment read back off a disk knows its own
	// position and checksum.
	hdr := f.EncodeHeader()
	back, err := DecodeFragment(hdr[:], payload)
	if err != nil {
		t.Fatalf("DecodeFragment: %v", err)
	}
	if back.Index != f.Index || back.Checksum != f.Checksum {
		t.Errorf("fragment preamble round trip: got index %d checksum %08x, want %d %08x",
			back.Index, back.Checksum, f.Index, f.Checksum)
	}
	if err := back.Verify(); err != nil {
		t.Errorf("a fragment decoded from its own preamble failed verification: %v", err)
	}
	if _, err := DecodeFragment(hdr[:FragmentHeaderSize-1], payload); !errors.Is(err, ErrShortBuffer) {
		t.Errorf("a truncated fragment header: error = %v, want ErrShortBuffer", err)
	}
}
