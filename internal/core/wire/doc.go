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
// # A request names a KEY, and that is not a detail
//
// A [Request] carries an [github.com/atvirokodosprendimai/sdev1/internal/core/addr.Key]
// and never a leaf identifier. ★★ THIS IS THE ONE DECISION IN ADR-045, and the
// obvious alternative silently breaks ADR-008.
//
// Naming the leaf is what a client would reach for first: it resolved a route, so
// it is holding a leaf already, and sending it saves the receiver a descent. What
// it costs is rule 4. A node that does not serve that leaf holds a name it does
// not recognise and NO WAY TO WORK OUT WHICH KEY PRODUCED IT — a leaf is a prefix
// of a hash, and hashes do not run backwards. It cannot answer, and it cannot
// redirect either, so the only thing left to send is an error. A stale client is
// then stuck being wrong, which is exactly the state ADR-008 rule 4 exists to
// make impossible.
//
// A key can be descended by ANY node to a leaf of its own. So the receiver
// computes the leaf, discovers it does not hold it, and says where it went —
// and the wrong node is the one that repairs the caller's map.
//
// # A length is a number a stranger chose
//
// [ReadFrame] refuses a frame claiming more than the declared bound BEFORE it
// allocates or reads the body. ⚠ Refusing afterwards is not the same property and
// is indistinguishable in the error: read-then-allocate is how one packet exhausts
// a node, so the allocation is the attack rather than the oversized message.
//
// ⚠ And the bound is DECLARED, never defaulted — [ErrNoFrameBound] on a
// non-positive one. "No bound" and "not configured yet" look identical from the
// outside, and the value that reads as safest is the unbounded one. [MaxFrame] is
// a value an operator may adopt; it is never applied on their behalf.
//
// # What it does not do
//
// No connections, no timeouts, no pooling and no authentication. The connection
// belongs to
// [github.com/atvirokodosprendimai/sdev1/internal/core/serve]; the rest is
// docs/adr/BACKLOG.md §18's.
//
// See docs/adr/ADR-043-response-envelope.md and
// docs/adr/ADR-045-a-leaf-is-served-over-a-stream.md.
package wire
