package segment

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
)

func testLeaf(t *testing.T) addr.LeafID {
	t.Helper()
	l, err := addr.Descend(addr.KeyOf(addr.TenantFromUint(7), "segment-subject"), 2)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}
	return l
}

// TestBlockCarriesItsOwnCodec is the falsifier ADR-005 names in its Enforced-by
// header.
//
// A block must be interpretable from its own bytes. If the codec or cipher were
// held in configuration, a settings change would silently reinterpret data
// already written — the failure this corpus has rejected for the fan-out, the
// erasure scheme and the tenant width.
func TestBlockCarriesItsOwnCodec(t *testing.T) {
	for _, codec := range []CodecID{CodecIdentity, CodecZstd} {
		h := BlockHeader{
			Codec:     codec,
			Cipher:    CipherID(9),
			Stages:    StageCompressed | StageEncrypted,
			RawLen:    4096,
			StoredLen: 512,
			Checksum:  0xdeadbeef,
		}
		enc := h.Encode()
		got, err := DecodeBlockHeader(enc[:])
		if err != nil {
			t.Fatalf("DecodeBlockHeader: %v", err)
		}
		if got.Codec != codec {
			t.Errorf("codec = %d, want %d — a block that does not record its codec is only "+
				"readable by a process that happens to be configured the same way", got.Codec, codec)
		}
		if got.Cipher != CipherID(9) {
			t.Errorf("cipher = %d, want 9", got.Cipher)
		}
		if got != h {
			t.Errorf("header round trip lost a field: got %+v, want %+v", got, h)
		}
	}
}

// TestCorruptBlockIsRefused checks a flipped bit yields an error rather than the
// bytes. Without this an erasure decoder handed a rotten fragment returns wrong
// data with no error anywhere, because decoding assumes it knows which fragments
// are MISSING rather than which are wrong.
func TestCorruptBlockIsRefused(t *testing.T) {
	stored := []byte("the quick brown fox jumps over the lazy dog")
	h := BlockHeader{StoredLen: uint32(len(stored)), Checksum: Checksum(stored)}

	if err := h.Verify(stored); err != nil {
		t.Fatalf("intact block was refused: %v", err)
	}

	for i := range stored {
		rotten := append([]byte(nil), stored...)
		rotten[i] ^= 0x01
		if err := h.Verify(rotten); !errors.Is(err, ErrCorruptBlock) {
			t.Fatalf("a flipped bit at byte %d was not detected: %v", i, err)
		}
	}
}

// TestUnknownVersionIsRefused checks a segment written by a future release is
// refused explicitly, so an incompatible change becomes a migration rather than
// a misread; and that a segment header carries its version and leaf.
func TestUnknownVersionIsRefused(t *testing.T) {
	h := Header{Version: FormatVersion, Leaf: testLeaf(t), Blocks: 12}
	enc := h.Encode()

	got, err := DecodeHeader(enc[:])
	if err != nil {
		t.Fatalf("DecodeHeader: %v", err)
	}
	if got != h {
		t.Errorf("segment header round trip: got %+v, want %+v", got, h)
	}

	future := h.Encode()
	future[1] = 99
	if _, err := DecodeHeader(future[:]); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("a future version was not refused: %v", err)
	}

	if _, err := DecodeHeader(enc[:HeaderSize-1]); !errors.Is(err, ErrShortBuffer) {
		t.Errorf("a truncated header: error = %v, want ErrShortBuffer", err)
	}
}

// TestHeaderIsFixedWidth pins the wire layout and checks a reader can stride.
//
// ⚠ It asserts the exact bytes rather than only that encode-then-decode is the
// identity. A round trip uses the SAME offsets on both sides, so it cannot see a
// symmetric layout bug — two fields written and read at each other's offsets
// round-trip perfectly and produce garbage for every other reader of the format.
// These are the bytes that go on a disk, so they are what the test names.
func TestHeaderIsFixedWidth(t *testing.T) {
	h := BlockHeader{
		Codec:     0x0102,
		Cipher:    0x0304,
		Stages:    StageCompressed | StageCoded, // 0b101
		RawLen:    0x11223344,
		StoredLen: 0x55667788,
		Checksum:  0x99aabbcc,
	}
	want := []byte{
		0x01, 0x02, // codec, big-endian
		0x03, 0x04, // cipher
		0x05,                   // stages
		0x00,                   // reserved
		0x11, 0x22, 0x33, 0x44, // raw length
		0x55, 0x66, 0x77, 0x88, // stored length
		0x99, 0xaa, 0xbb, 0xcc, // checksum
	}
	if len(want) != BlockHeaderSize {
		t.Fatalf("this test's expected layout is %d bytes but BlockHeaderSize is %d; "+
			"the layout changed and the test was not updated with it", len(want), BlockHeaderSize)
	}
	got := h.Encode()
	if !bytes.Equal(got[:], want) {
		t.Errorf("the encoded layout changed\n got % x\nwant % x", got[:], want)
	}

	// Striding: headers written back to back are each decodable at a fixed
	// offset, which is the whole point of a fixed width.
	rng := rand.New(rand.NewSource(20260904))
	const n = 64
	headers := make([]BlockHeader, n)
	var stream []byte
	for i := range headers {
		headers[i] = BlockHeader{
			Codec:     CodecID(rng.Intn(1 << 16)),
			Cipher:    CipherID(rng.Intn(1 << 16)),
			Stages:    Stage(rng.Intn(8)),
			RawLen:    rng.Uint32(),
			StoredLen: rng.Uint32(),
			Checksum:  rng.Uint32(),
		}
		enc := headers[i].Encode()
		stream = append(stream, enc[:]...)
	}
	if len(stream) != n*BlockHeaderSize {
		t.Fatalf("%d headers occupy %d bytes, want %d — the width is not fixed",
			n, len(stream), n*BlockHeaderSize)
	}
	for i, want := range headers {
		at := i * BlockHeaderSize
		got, err := DecodeBlockHeader(stream[at:])
		if err != nil {
			t.Fatalf("header %d at offset %d: %v", i, at, err)
		}
		if got != want {
			t.Fatalf("header %d did not decode at its stride offset\n got %+v\nwant %+v",
				i, got, want)
		}
	}
}

// TestStageFlagsRecordThePipeline checks the flags say which stages ran, so a
// reader applies their inverses without being told and cannot guess the order.
func TestStageFlagsRecordThePipeline(t *testing.T) {
	none := Stage(0)
	if none.Has(StageCompressed) || none.Has(StageEncrypted) || none.Has(StageCoded) {
		t.Error("the zero stage set reports a stage as having run")
	}
	if got := none.String(); got != "raw" {
		t.Errorf("empty stages render as %q, want %q", got, "raw")
	}

	all := StageCompressed | StageEncrypted | StageCoded
	for _, s := range []Stage{StageCompressed, StageEncrypted, StageCoded} {
		if !all.Has(s) {
			t.Errorf("stage %v not reported in the full set", s)
		}
	}
	// The rendering is in pipeline order, which is the order a reader must undo
	// in reverse.
	if got, want := all.String(), "compressed+encrypted+coded"; got != want {
		t.Errorf("stages render as %q, want %q — the order is the pipeline's", got, want)
	}

	// A round trip through the header preserves them.
	h := BlockHeader{Stages: StageCompressed | StageCoded}
	enc := h.Encode()
	back, err := DecodeBlockHeader(enc[:])
	if err != nil {
		t.Fatalf("DecodeBlockHeader: %v", err)
	}
	if back.Stages != h.Stages {
		t.Errorf("stages = %v, want %v", back.Stages, h.Stages)
	}
}

// TestHeaderRoundTrips is a property test: encoding then decoding a block header
// is the identity, so no field is silently dropped.
func TestHeaderRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 2000; i++ {
		want := BlockHeader{
			Codec:     CodecID(rng.Intn(1 << 16)),
			Cipher:    CipherID(rng.Intn(1 << 16)),
			Stages:    Stage(rng.Intn(8)),
			RawLen:    rng.Uint32(),
			StoredLen: rng.Uint32(),
			Checksum:  rng.Uint32(),
		}
		enc := want.Encode()
		got, err := DecodeBlockHeader(enc[:])
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if got != want {
			t.Fatalf("case %d: round trip lost a field\n got %+v\nwant %+v", i, got, want)
		}
	}
}

// TestCodecIdentityIsAlwaysAvailable checks a build with no compression
// dependency can still read and write, and that a codec's interface is narrow
// enough that it cannot reach the header or the filesystem.
func TestCodecIdentityIsAlwaysAvailable(t *testing.T) {
	c, err := LookupCodec(CodecIdentity)
	if err != nil {
		t.Fatalf("the identity codec is not registered: %v", err)
	}
	if c.Name() != "identity" {
		t.Errorf("codec 0 is %q, want identity", c.Name())
	}
	// Registering over an existing identifier is refused rather than silently
	// overwriting, so a collision fails at startup rather than at read time.
	if err := RegisterCodec(CodecIdentity, identityCodec{}); !errors.Is(err, ErrDuplicateCodec) {
		t.Errorf("re-registering codec 0: error = %v, want ErrDuplicateCodec", err)
	}
	if len(RegisteredCodecs()) < 1 {
		t.Error("no codecs registered at all")
	}
}

// TestEncodeDecodeRoundTrips is the property: encode then decode returns the
// original bytes, across generated payloads and every registered codec.
func TestEncodeDecodeRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, codec := range RegisteredCodecs() {
		for i := 0; i < 200; i++ {
			raw := make([]byte, rng.Intn(8192))
			// Mix compressible and incompressible payloads: an all-random
			// corpus never exercises a compressor's normal case.
			if i%2 == 0 {
				for j := range raw {
					raw[j] = byte(j % 7)
				}
			} else {
				rng.Read(raw)
			}

			h, stored, err := EncodeBlock(raw, codec)
			if err != nil {
				t.Fatalf("codec %d case %d: EncodeBlock: %v", codec, i, err)
			}
			got, err := DecodeBlock(h, stored)
			if err != nil {
				t.Fatalf("codec %d case %d: DecodeBlock: %v", codec, i, err)
			}
			if !bytes.Equal(got, raw) {
				t.Fatalf("codec %d case %d: round trip changed %d bytes into %d",
					codec, i, len(raw), len(got))
			}
		}
	}
}

// TestDecodeResolvesCodecFromHeader checks a block decodes with NO configuration
// supplied — the property that keeps a settings change from reinterpreting data.
func TestDecodeResolvesCodecFromHeader(t *testing.T) {
	raw := bytes.Repeat([]byte("compress me please "), 500)

	hz, sz, err := EncodeBlock(raw, CodecZstd)
	if err != nil {
		t.Fatalf("EncodeBlock(zstd): %v", err)
	}
	hi, si, err := EncodeBlock(raw, CodecIdentity)
	if err != nil {
		t.Fatalf("EncodeBlock(identity): %v", err)
	}

	// The compressed form must actually be smaller, or the codec is not running.
	if len(sz) >= len(si) {
		t.Errorf("zstd stored %d bytes and identity stored %d; the codec did not compress",
			len(sz), len(si))
	}

	// Both decode through the SAME call with no codec argument anywhere.
	for _, c := range []struct {
		name   string
		h      BlockHeader
		stored []byte
	}{{"zstd", hz, sz}, {"identity", hi, si}} {
		got, err := DecodeBlock(c.h, c.stored)
		if err != nil {
			t.Fatalf("%s: DecodeBlock: %v", c.name, err)
		}
		if !bytes.Equal(got, raw) {
			t.Errorf("%s: decoded bytes differ from the original", c.name)
		}
	}
}

// TestUnregisteredCodecIsRefused checks a header naming a codec this build lacks
// yields a named error rather than the stored bytes returned as the value.
func TestUnregisteredCodecIsRefused(t *testing.T) {
	stored := []byte("stored bytes")
	h := BlockHeader{
		Codec:     CodecID(4242),
		RawLen:    uint32(len(stored)),
		StoredLen: uint32(len(stored)),
		Checksum:  Checksum(stored),
	}
	got, err := DecodeBlock(h, stored)
	if !errors.Is(err, ErrUnknownCodec) {
		t.Fatalf("an unregistered codec: error = %v, want ErrUnknownCodec", err)
	}
	if got != nil {
		t.Error("bytes were returned alongside the error; a caller could mistake stored bytes for the value")
	}
	if _, _, err := EncodeBlock(stored, CodecID(4242)); !errors.Is(err, ErrUnknownCodec) {
		t.Errorf("encoding with an unregistered codec: error = %v, want ErrUnknownCodec", err)
	}
}

// TestDecodeVerifiesTheChecksum checks a flipped bit is caught BEFORE the codec
// runs, so rotten bytes never reach a decompressor.
func TestDecodeVerifiesTheChecksum(t *testing.T) {
	raw := bytes.Repeat([]byte("payload "), 200)
	h, stored, err := EncodeBlock(raw, CodecZstd)
	if err != nil {
		t.Fatalf("EncodeBlock: %v", err)
	}

	rotten := append([]byte(nil), stored...)
	rotten[len(rotten)/2] ^= 0x80

	got, err := DecodeBlock(h, rotten)
	if !errors.Is(err, ErrCorruptBlock) {
		t.Fatalf("a corrupt block: error = %v, want ErrCorruptBlock — rotten bytes must not "+
			"reach the decompressor, which would fail confusingly or produce plausible garbage", err)
	}
	if got != nil {
		t.Error("bytes were returned alongside the corruption error")
	}

	// A truncated block is caught too, even if the checksum somehow matched.
	if _, err := DecodeBlock(h, stored[:len(stored)-1]); !errors.Is(err, ErrCorruptBlock) {
		t.Errorf("a truncated block: error = %v, want ErrCorruptBlock", err)
	}
}
