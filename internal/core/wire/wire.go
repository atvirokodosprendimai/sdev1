package wire

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/routing"
)

// FormatVersion is the version of the ENVELOPE — how a response's outcome and
// body are laid out. It is not the version of anything a body carries.
//
// ⚠ Extension goes through this number, which is REFUSED when unrecognised, and
// never through appended bytes, which are refused too. See [ErrTrailingBytes].
const FormatVersion uint16 = 1

// headerSize is the version and the outcome tag.
const headerSize = 2 + 1

var (
	// ErrShortFrame reports a frame too small to hold what it claims.
	ErrShortFrame = errors.New("wire: the frame is shorter than its contents claim")

	// ErrUnknownVersion reports an envelope version this build does not know.
	ErrUnknownVersion = errors.New("wire: unknown envelope version")

	// ErrUnknownOutcome reports an outcome tag that is not one of the three.
	//
	// ⚠ It is a refusal and never a default. A decoder that treated an
	// unrecognised outcome as "probably an answer" would have rebuilt the exact
	// flattening this package exists to prevent, arriving through the
	// forward-compatibility door instead of through the schema.
	ErrUnknownOutcome = errors.New("wire: unknown response outcome")

	// ErrTrailingBytes reports bytes after a complete frame.
	//
	// ★ This is what makes "a redirect has no payload field" true of the WIRE
	// rather than only of the struct. "Ignore what you do not understand" is
	// precisely how a payload smuggles itself into a redirect: append bytes, and a
	// permissive decoder hands one back while a tolerant caller reads what follows.
	ErrTrailingBytes = errors.New("wire: bytes remain after a complete response")
)

// Outcome names which of the three a response is.
//
// ⚠ The set is CLOSED. A fourth would have to fold into one of these, and the
// folding is the defect — ADR-008 rule 4 is that a stale route is answered with a
// redirect, never with an error and never with data, which is three answers.
type Outcome uint8

const (
	// OutcomeUnset is the zero value and is never valid on the wire.
	OutcomeUnset Outcome = iota
	// OutcomeAnswer: the node holds the leaf and this is the result.
	OutcomeAnswer
	// OutcomeRedirect: the node does not hold the leaf and says where it went.
	OutcomeRedirect
	// OutcomeRefusal: the node will not serve this, and says why.
	OutcomeRefusal
)

func (o Outcome) String() string {
	switch o {
	case OutcomeAnswer:
		return "answer"
	case OutcomeRedirect:
		return "redirect"
	case OutcomeRefusal:
		return "refusal"
	default:
		return "unset"
	}
}

// Response is exactly one of [Answer], [Redirect] or [Refusal].
//
// ★ The interface is SEALED by an unexported method, so those three are all there
// are and a type switch over them is exhaustive by construction. A fourth cannot
// be added from outside the package, which is what keeps [Outcome]'s closed set
// closed.
type Response interface {
	// Outcome names which of the three this is.
	Outcome() Outcome
	response()
}

// Answer is a result from the node that holds the leaf.
//
// Datoms is an encoded datom run — ADR-025's format, whose rules about versions,
// trailing bytes and returning nothing on error this envelope deliberately
// copies.
type Answer struct {
	Datoms []byte
}

func (*Answer) Outcome() Outcome { return OutcomeAnswer }
func (*Answer) response()        {}

// Redirect says where the leaf went.
//
// ⚠ IT HAS NO PAYLOAD FIELD, and that absence is the whole record. An
// optional-and-empty payload is a field a caller can read, and what it reads is a
// successful empty answer — so the stale route it was being redirected away from
// would have served a result. A field that does not exist cannot be read at all.
type Redirect struct {
	// Route is where the responding node believes the leaf now is, INCLUDING its
	// epoch. ⚠ Without the epoch a redirect cannot be ordered, and ADR-008 rule 5
	// is what stops two stale nodes bouncing a client between them forever —
	// keeping the redirect and losing the epoch is worse than losing both.
	Route routing.Route
}

func (*Redirect) Outcome() Outcome { return OutcomeRedirect }
func (*Redirect) response()        {}

// Refusal says the node will not serve this, and why.
//
// ⚠ Distinct from [Redirect]: ADR-008 rule 4 says a stale route is answered with
// a redirect and NEVER with an error, because refusing makes every topology change
// a fleet-wide outage while a redirect repairs the client.
type Refusal struct {
	Reason string
}

func (*Refusal) Outcome() Outcome { return OutcomeRefusal }
func (*Refusal) response()        {}

// Encode renders one response.
func Encode(r Response) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: nil", ErrUnknownOutcome)
	}

	out := make([]byte, headerSize)
	binary.BigEndian.PutUint16(out[0:2], FormatVersion)
	out[2] = byte(r.Outcome())

	switch v := r.(type) {
	case *Answer:
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(v.Datoms)))
		out = append(out, n[:]...)
		out = append(out, v.Datoms...)
	case *Redirect:
		body, err := encodeRoute(v.Route)
		if err != nil {
			return nil, err
		}
		out = append(out, body...)
	case *Refusal:
		if len(v.Reason) > 1<<16-1 {
			return nil, fmt.Errorf("wire: a refusal reason of %d bytes does not fit", len(v.Reason))
		}
		var n [2]byte
		binary.BigEndian.PutUint16(n[:], uint16(len(v.Reason)))
		out = append(out, n[:]...)
		out = append(out, v.Reason...)
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnknownOutcome, r)
	}
	return out, nil
}

// Decode reads one response, and returns NOTHING on any error.
//
// ⚠ Nothing, deliberately: a partially decoded response is worse than none,
// because the part that decoded looks usable and most callers check the value
// before the error.
func Decode(b []byte) (Response, error) {
	if len(b) < headerSize {
		return nil, fmt.Errorf("%w: %d bytes, need at least %d", ErrShortFrame, len(b), headerSize)
	}
	if v := binary.BigEndian.Uint16(b[0:2]); v != FormatVersion {
		return nil, fmt.Errorf("%w: %d, this build knows %d", ErrUnknownVersion, v, FormatVersion)
	}

	body := b[headerSize:]
	switch Outcome(b[2]) {
	case OutcomeAnswer:
		if len(body) < 4 {
			return nil, fmt.Errorf("%w: an answer needs a length", ErrShortFrame)
		}
		n := int(binary.BigEndian.Uint32(body[0:4]))
		if len(body)-4 < n {
			return nil, fmt.Errorf("%w: an answer claims %d datom bytes and %d remain",
				ErrShortFrame, n, len(body)-4)
		}
		if len(body)-4 > n {
			return nil, fmt.Errorf("%w: %d byte(s) after an answer", ErrTrailingBytes, len(body)-4-n)
		}
		datoms := make([]byte, n)
		copy(datoms, body[4:4+n])
		return &Answer{Datoms: datoms}, nil

	case OutcomeRedirect:
		route, rest, err := decodeRoute(body)
		if err != nil {
			return nil, err
		}
		// ★ The refusal that makes this shape hold against bytes rather than only
		// against a struct definition.
		if len(rest) > 0 {
			return nil, fmt.Errorf("%w: %d byte(s) after a redirect", ErrTrailingBytes, len(rest))
		}
		return &Redirect{Route: route}, nil

	case OutcomeRefusal:
		if len(body) < 2 {
			return nil, fmt.Errorf("%w: a refusal needs a length", ErrShortFrame)
		}
		n := int(binary.BigEndian.Uint16(body[0:2]))
		if len(body)-2 < n {
			return nil, fmt.Errorf("%w: a refusal claims %d bytes and %d remain",
				ErrShortFrame, n, len(body)-2)
		}
		if len(body)-2 > n {
			return nil, fmt.Errorf("%w: %d byte(s) after a refusal", ErrTrailingBytes, len(body)-2-n)
		}
		return &Refusal{Reason: string(body[2 : 2+n])}, nil

	default:
		return nil, fmt.Errorf("%w: tag %d", ErrUnknownOutcome, b[2])
	}
}

// encodeRoute renders a route: depth, the significant prefix bytes, the epoch,
// and the next hops in order.
//
// ⚠ The hops' ORDER is part of the route — ADR-008 says two routes with the same
// hops in a different order are different routes, because a client tries them in
// sequence — so it is preserved rather than sorted.
func encodeRoute(r routing.Route) ([]byte, error) {
	depth := int(r.Prefix.Depth)
	if depth < 1 || depth > addr.MaxDepth {
		return nil, fmt.Errorf("wire: a route prefix of depth %d cannot be encoded", depth)
	}
	if len(r.NextHops) > 1<<16-1 {
		return nil, fmt.Errorf("wire: a route with %d next hops does not fit", len(r.NextHops))
	}

	out := make([]byte, 0, 1+depth+8+2)
	out = append(out, byte(depth))
	out = append(out, r.Prefix.Prefix[:depth]...)

	var epoch [8]byte
	binary.BigEndian.PutUint64(epoch[:], r.Epoch)
	out = append(out, epoch[:]...)

	var count [2]byte
	binary.BigEndian.PutUint16(count[:], uint16(len(r.NextHops)))
	out = append(out, count[:]...)

	for _, hop := range r.NextHops {
		if len(hop) > 1<<16-1 {
			return nil, fmt.Errorf("wire: a next hop of %d bytes does not fit", len(hop))
		}
		var n [2]byte
		binary.BigEndian.PutUint16(n[:], uint16(len(hop)))
		out = append(out, n[:]...)
		out = append(out, hop...)
	}
	return out, nil
}

// decodeRoute reads a route and returns whatever follows it, so the caller can
// refuse trailing bytes rather than silently ignoring them.
func decodeRoute(b []byte) (routing.Route, []byte, error) {
	if len(b) < 1 {
		return routing.Route{}, nil, fmt.Errorf("%w: a redirect needs a prefix depth", ErrShortFrame)
	}
	depth := int(b[0])
	if depth < 1 || depth > addr.MaxDepth {
		return routing.Route{}, nil, fmt.Errorf("%w: prefix depth %d", ErrShortFrame, depth)
	}
	if len(b) < 1+depth+8+2 {
		return routing.Route{}, nil, fmt.Errorf("%w: a redirect of depth %d needs %d bytes, has %d",
			ErrShortFrame, depth, 1+depth+8+2, len(b))
	}

	var route routing.Route
	copy(route.Prefix.Prefix[:], b[1:1+depth])
	route.Prefix.Depth = uint8(depth)

	at := 1 + depth
	route.Epoch = binary.BigEndian.Uint64(b[at : at+8])
	at += 8

	hops := int(binary.BigEndian.Uint16(b[at : at+2]))
	at += 2

	for i := 0; i < hops; i++ {
		if len(b)-at < 2 {
			return routing.Route{}, nil, fmt.Errorf("%w: next hop %d needs a length", ErrShortFrame, i)
		}
		n := int(binary.BigEndian.Uint16(b[at : at+2]))
		at += 2
		if len(b)-at < n {
			return routing.Route{}, nil, fmt.Errorf("%w: next hop %d claims %d bytes and %d remain",
				ErrShortFrame, i, n, len(b)-at)
		}
		route.NextHops = append(route.NextHops, string(b[at:at+n]))
		at += n
	}
	return route, b[at:], nil
}
