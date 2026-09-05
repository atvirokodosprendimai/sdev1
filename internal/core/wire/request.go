package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
)

var (
	// ErrFrameTooLarge reports a frame claiming more bytes than the declared
	// bound allows.
	//
	// ⚠ It is raised BEFORE the body is read. A length prefix is a number a
	// stranger chose, and read-then-allocate is how one packet exhausts a node's
	// memory — the refusal is worth nothing if it happens after the allocation it
	// was meant to prevent.
	ErrFrameTooLarge = errors.New("wire: the frame claims more bytes than the bound allows")

	// ErrNoFrameBound reports an undeclared maximum frame size.
	//
	// ★ There is deliberately no default. "No bound" and "not configured yet" are
	// indistinguishable from the outside, and the value that looks safest —
	// unbounded — is the one that lets a stranger choose an allocation.
	ErrNoFrameBound = errors.New("wire: a maximum frame size must be declared and positive")
)

// lengthPrefix is the width of a frame's length field.
const lengthPrefix = 4

// MaxFrame is a frame bound an operator MAY adopt, and is applied to nothing.
//
// ⚠ It is not a default. [ReadFrame] and [WriteFrame] take the bound as an
// argument and refuse a non-positive one, so this constant is reachable only by a
// caller who wrote its name down — which is the difference between a value someone
// chose and a value that happened to them.
//
// A statement is text and a datom run is bounded by ADR-025, so a megabyte is
// generous for both; it is a starting point for a node's configuration, not a
// measurement.
const MaxFrame = 1 << 20

// Request is what a caller wants, and where the answer lives is NOT part of it.
//
// ★★ IT NAMES A KEY, NEVER A LEAF. That is ADR-045's whole decision. Naming the
// leaf is the obvious design — a client that resolved a route already holds one —
// and it destroys ADR-008: a node that does not serve that leaf **cannot compute a
// redirect from a leaf name**, because it holds a name it does not recognise and
// no way to work out which key produced it. A key can be descended by ANY node to
// a leaf of its own, which is what makes the redirect computable at the receiver.
//
// ⚠ And the redirect is the whole of ADR-008 rule 4: a stale route is answered
// with a redirect, never with an error and never with data.
type Request struct {
	// Key is what is being asked about. Every node can descend it.
	Key addr.Key
	// Statement is the query text, in the language ADR-011 and ADR-034 define.
	Statement string
	// Now is the caller's business instant, so the evaluator resolves the time
	// clause against a stated moment rather than reading a clock at the far end.
	//
	// ⚠ It is carried rather than taken locally for ADR-023's reason: a clock
	// read at the server would make one statement span two moments if it were
	// ever read twice.
	Now int64
}

// EncodeRequest renders a request.
//
// The layout mirrors a response: a version, then the body, then nothing.
func EncodeRequest(r Request) ([]byte, error) {
	if len(r.Statement) > 1<<32-1 {
		return nil, fmt.Errorf("wire: a statement of %d bytes does not fit", len(r.Statement))
	}

	out := make([]byte, 2, 2+len(r.Key)+8+4+len(r.Statement))
	binary.BigEndian.PutUint16(out[0:2], FormatVersion)
	out = append(out, r.Key[:]...)

	var now [8]byte
	binary.BigEndian.PutUint64(now[:], uint64(r.Now))
	out = append(out, now[:]...)

	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(r.Statement)))
	out = append(out, n[:]...)
	out = append(out, r.Statement...)
	return out, nil
}

// DecodeRequest reads a request, returning NOTHING on any error.
//
// ⚠ It applies the same three refusals a response gets (ADR-043): an unknown
// version, a short body, and TRAILING BYTES. The last matters here for the same
// reason it matters there — "ignore what you do not understand" is how something
// nobody agreed to travels alongside something everybody did.
func DecodeRequest(b []byte) (Request, error) {
	const head = 2 + len(addr.Key{}) + 8 + 4
	if len(b) < head {
		return Request{}, fmt.Errorf("%w: %d bytes, need at least %d", ErrShortFrame, len(b), head)
	}
	if v := binary.BigEndian.Uint16(b[0:2]); v != FormatVersion {
		return Request{}, fmt.Errorf("%w: %d, this build knows %d", ErrUnknownVersion, v, FormatVersion)
	}

	var r Request
	at := 2
	copy(r.Key[:], b[at:at+len(r.Key)])
	at += len(r.Key)

	r.Now = int64(binary.BigEndian.Uint64(b[at : at+8]))
	at += 8

	n := int(binary.BigEndian.Uint32(b[at : at+4]))
	at += 4

	if len(b)-at < n {
		return Request{}, fmt.Errorf("%w: a statement claims %d bytes and %d remain",
			ErrShortFrame, n, len(b)-at)
	}
	if len(b)-at > n {
		return Request{}, fmt.Errorf("%w: %d byte(s) after a request", ErrTrailingBytes, len(b)-at-n)
	}
	r.Statement = string(b[at : at+n])
	return r, nil
}

// WriteFrame writes one length-prefixed frame.
//
// The bound is checked on the way out too, so a node cannot emit a frame its
// peer is obliged to refuse.
func WriteFrame(w io.Writer, payload []byte, maxFrame int) error {
	if maxFrame <= 0 {
		return fmt.Errorf("%w: got %d", ErrNoFrameBound, maxFrame)
	}
	if len(payload) > maxFrame {
		return fmt.Errorf("%w: %d bytes, bound is %d", ErrFrameTooLarge, len(payload), maxFrame)
	}

	var n [lengthPrefix]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(payload)))
	if _, err := w.Write(n[:]); err != nil {
		return fmt.Errorf("wire: writing a frame length: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("wire: writing a frame body: %w", err)
	}
	return nil
}

// ReadFrame reads one length-prefixed frame.
//
// ⚠ THE BOUND IS CHECKED BEFORE THE BODY IS READ OR ANY BUFFER IS SIZED. The
// length is the one field a stranger fully controls, so a reader that sizes a
// buffer from it and then complains has already done the damage: the allocation
// is the attack, not the oversized message.
func ReadFrame(r io.Reader, maxFrame int) ([]byte, error) {
	if maxFrame <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrNoFrameBound, maxFrame)
	}

	var n [lengthPrefix]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return nil, fmt.Errorf("wire: reading a frame length: %w", err)
	}
	size := int(binary.BigEndian.Uint32(n[:]))

	// ★ Here, before make() and before any further read.
	if size > maxFrame {
		return nil, fmt.Errorf("%w: the frame claims %d bytes, bound is %d",
			ErrFrameTooLarge, size, maxFrame)
	}

	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("wire: reading a frame body of %d bytes: %w", size, err)
	}
	return body, nil
}
