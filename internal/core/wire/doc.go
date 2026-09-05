// Package wire is what one response looks like as bytes.
//
// It encodes and decodes exactly one thing — a response — and it moves no bytes,
// holds no connection and knows nothing about a request. That is why it is
// testable with no network, which is the same reason
// [github.com/atvirokodosprendimai/sdev1/internal/core/segment] refuses to touch
// a filesystem.
//
// # The property this package exists to keep
//
// ⚠ ADR-008 rule 4 is the whole of that record: a stale route is answered with a
// REDIRECT, never with an error and never with data. It is held in Go's type
// system — [github.com/atvirokodosprendimai/sdev1/internal/core/routing.Redirect]
// and `routing.Destination` are different types, and the first carries nothing a
// caller could read as an answer.
//
// ★ A wire format is where that property is normally lost, and losing it is the
// DEFAULT outcome rather than an unlikely one. The ordinary way to design this
// message is a struct with a payload and an optional redirect field. Under every
// mainstream schema language a missing field decodes to a zero value, so a client
// that receives a redirect and reads the payload gets an empty SUCCESSFUL
// answer — no error, no tag, nothing to notice. The stale route it was being
// redirected away from has just served a result.
//
// # Three outcomes, and a redirect has nowhere to put data
//
// A [Response] is exactly one of [Answer], [Redirect] or [Refusal]. The interface
// is sealed, so those three are all there are and a type switch over them is
// exhaustive by construction.
//
// ⚠ [Redirect] HAS NO PAYLOAD FIELD. Not empty — absent. An optional-and-empty
// payload is a field a caller can read, and what it reads is a successful empty
// answer; a field that does not exist cannot be read at all.
//
// # Three refusals, because the struct is not the wire
//
// A shape is only half the property. These make it hold against bytes:
//
//   - An unknown outcome tag is [ErrUnknownOutcome]. A decoder that guessed
//     "probably an answer" would have rebuilt the flattening through the
//     forward-compatibility door.
//   - An unknown version is [ErrUnknownVersion], as ADR-025 does for a datom run.
//   - ⚠ TRAILING BYTES are [ErrTrailingBytes], and this is the important one.
//     "Ignore what you do not understand" is precisely how a payload smuggles
//     itself into a redirect: append bytes, and a permissive decoder hands back a
//     redirect while a tolerant caller finds data after it.
//
// ⚠ And a decode error returns NOTHING. A partially decoded response is worse
// than none, because the part that decoded looks usable.
//
// # What it does not do
//
// No framing, no connections, no timeouts, and no request — those are
// docs/adr/BACKLOG.md §18's, and none of them carries a correctness property that
// is lost by waiting. This one does, which is why it is settled first.
//
// ⚠ Nothing sends or receives a response yet: there is no transport. This is a
// shape with no wire, tested against itself — the same position ADR-025 was in,
// and for the same reason, because the format has to be right before any byte
// moves.
//
// See docs/adr/ADR-043-response-envelope.md.
package wire
