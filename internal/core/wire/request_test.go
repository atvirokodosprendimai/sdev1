package wire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
)

// TestARequestNamesAKeyNotALeaf is ADR-045 rule 1.
//
// ★ The second half is the point. That a key round-trips is ordinary; that ANY
// node can descend it to a leaf of its own is what makes a redirect computable at
// the receiver — and a leaf name would give the receiver nothing to compute from.
func TestARequestNamesAKeyNotALeaf(t *testing.T) {
	key := addr.KeyOf(addr.TenantFromUint(7), "planet-3")

	frame, err := EncodeRequest(Request{Key: key, Statement: "READ * FROM planet-3", Now: 1700})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	got, err := DecodeRequest(frame)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	if got.Key != key {
		t.Fatalf("key = %x, want %x", got.Key, key)
	}
	if got.Statement != "READ * FROM planet-3" || got.Now != 1700 {
		t.Errorf("request = %+v", got)
	}

	// ★ A node that has never seen this request can descend the key itself. That
	// is the whole reason the key travels: the receiver computes the leaf, so it
	// can tell whether it holds it and, if not, say where it went.
	for _, depth := range []uint8{1, 2, 4} {
		leaf, err := addr.Descend(got.Key, depth)
		if err != nil {
			t.Fatalf("a receiver could not descend the key at depth %d: %v", depth, err)
		}
		if leaf.Depth != depth {
			t.Errorf("descended to depth %d, want %d", leaf.Depth, depth)
		}
		// It agrees with what the sender would have computed, so both sides mean
		// the same leaf without either naming one.
		want, err := addr.Descend(key, depth)
		if err != nil {
			t.Fatalf("Descend: %v", err)
		}
		if leaf != want {
			t.Errorf("receiver descended to %+v, sender to %+v", leaf, want)
		}
	}
}

// TestAnOversizedFrameIsRefusedNotAllocated is ADR-045 rule 4.
//
// ⚠ "Refused" is not the same as "refused before allocating", and only the second
// is worth anything. The fixture offers a header claiming a huge body and then
// ENDS — a reader that sized a buffer and read first would block or fail on the
// body; one that checks the length first returns promptly with the bound error.
func TestAnOversizedFrameIsRefusedNotAllocated(t *testing.T) {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], 1<<31) // 2GB claimed, nothing supplied

	got, err := ReadFrame(bytes.NewReader(header[:]), 1024)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("a frame claiming 2GB against a 1KB bound = %v, want ErrFrameTooLarge.\n"+
			"A length prefix is the one field a stranger fully controls; a reader that sizes a "+
			"buffer from it and then complains has already done the damage.", err)
	}
	if got != nil {
		t.Errorf("a refused frame returned %d bytes alongside its error", len(got))
	}

	// ⚠ And it did not attempt the body: the reader was given ONLY a header, so a
	// reader that tried would have surfaced an unexpected-EOF instead.
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		t.Error("the reader attempted the body before checking the bound")
	}

	// Writing an oversized frame is refused too, so a node cannot emit what its
	// peer is obliged to reject.
	if err := WriteFrame(io.Discard, make([]byte, 2048), 1024); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("WriteFrame over the bound = %v, want ErrFrameTooLarge", err)
	}
}

// TestAFrameBoundIsRequired is ADR-045 rule 4's declaration half.
func TestAFrameBoundIsRequired(t *testing.T) {
	for _, bound := range []int{0, -1, -1024} {
		if _, err := ReadFrame(bytes.NewReader(nil), bound); !errors.Is(err, ErrNoFrameBound) {
			t.Errorf("ReadFrame with bound %d = %v, want ErrNoFrameBound", bound, err)
		}
		if err := WriteFrame(io.Discard, nil, bound); !errors.Is(err, ErrNoFrameBound) {
			t.Errorf("WriteFrame with bound %d = %v, want ErrNoFrameBound", bound, err)
		}
	}

	// ⚠ A frame EXACTLY at the bound is accepted; one byte over is not. An
	// off-by-one in the refusing direction rejects traffic an operator declared
	// acceptable.
	var buf bytes.Buffer
	exact := make([]byte, 64)
	if err := WriteFrame(&buf, exact, 64); err != nil {
		t.Fatalf("a frame exactly at the bound was refused: %v", err)
	}
	if got, err := ReadFrame(&buf, 64); err != nil {
		t.Errorf("reading a frame exactly at the bound: %v", err)
	} else if len(got) != 64 {
		t.Errorf("read %d bytes, want 64", len(got))
	}

	var over bytes.Buffer
	if err := WriteFrame(&over, make([]byte, 65), 128); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	if _, err := ReadFrame(&over, 64); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("a 65-byte frame against a 64-byte bound = %v, want ErrFrameTooLarge", err)
	}
}

// TestARequestRoundTripsThroughAStream checks the codec and the framing compose,
// and that ADR-043's refusals apply inside a frame unchanged.
func TestARequestRoundTripsThroughAStream(t *testing.T) {
	key := addr.KeyOf(addr.TenantFromUint(3), "staff")
	original := Request{Key: key, Statement: "READ ->name FROM [staff]", Now: 42}

	payload, err := EncodeRequest(original)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	var stream bytes.Buffer
	if err := WriteFrame(&stream, payload, MaxFrame); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	body, err := ReadFrame(&stream, MaxFrame)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	got, err := DecodeRequest(body)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if got != original {
		t.Errorf("round trip gave %+v, want %+v", got, original)
	}
	if stream.Len() != 0 {
		t.Errorf("%d bytes left in the stream after one frame", stream.Len())
	}

	// ⚠ ADR-043's refusals, inside the frame: trailing bytes and an unknown
	// version are refused here exactly as they are for a response.
	if r, err := DecodeRequest(append(append([]byte(nil), payload...), 0x00)); !errors.Is(err, ErrTrailingBytes) {
		t.Errorf("trailing bytes in a request = %v (%+v), want ErrTrailingBytes", err, r)
	}
	future := append([]byte(nil), payload...)
	future[0], future[1] = 0xff, 0xff
	if _, err := DecodeRequest(future); !errors.Is(err, ErrUnknownVersion) {
		t.Errorf("an unknown request version = %v, want ErrUnknownVersion", err)
	}
	if _, err := DecodeRequest(payload[:4]); !errors.Is(err, ErrShortFrame) {
		t.Errorf("a truncated request = %v, want ErrShortFrame", err)
	}
}
