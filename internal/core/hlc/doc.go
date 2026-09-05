// Package hlc implements a hybrid logical clock: a reading that stays close to
// wall-clock time, never moves backwards, and orders causally related events
// correctly across nodes without special hardware.
//
// # What it guarantees
//
// Successive readings from one [Clock] strictly increase, whatever the wall
// clock does. After a clock absorbs a remote timestamp with [Clock.Merge], its
// next reading strictly exceeds that remote one — which is what makes a message
// carrying a timestamp establish a happens-before relation between the sender's
// event and everything the receiver does afterwards.
//
// # What it does not guarantee
//
// It is not a synchronised clock and it does not bound skew. A reading is a
// hybrid of a wall reading and a counter; the wall half is an INPUT to the
// algorithm and never the ordering itself.
//
// # How it fails, and how it recovers
//
// A wall clock that jumps BACKWARDS is handled completely: the wall half stays
// pinned and the logical counter advances, so readings keep increasing and
// nothing is lost. This is the case a plain wall clock cannot survive, because
// two events would receive the same or inverted timestamps and an append-only
// log would record an order that never happened.
//
// A wall clock that jumps FORWARDS is the failure with no local recovery, and
// it is worth stating plainly. Monotonicity means a clock that has moved
// forward can never come back, so a node whose clock is badly fast drags every
// timestamp it touches — and, through [Clock.Merge], every clock it talks to —
// permanently ahead of true time. Nothing in this package detects or refuses
// that; bounding skew is a cluster-level policy and is deliberately not decided
// here. The operational consequence is that a cluster's timestamp quality is
// set by its worst clock, so a badly-skewed node must be found and removed by
// something outside this package.
//
// A frozen wall clock degrades gracefully: readings keep increasing through the
// logical counter alone, and the wall half resumes as soon as the clock moves.
//
// # Encoding
//
// [Timestamp.Encode] produces a fixed-width, byte-comparable form, so an index
// can order on it without decoding. Comparing two encodings as bytes yields the
// same order as [Timestamp.Compare].
//
// # Two absorb paths, and the difference matters
//
// A remote timestamp reaches this clock two ways, and they are bounded
// differently on purpose (ADR-042).
//
// ★ [Clock.Merge] is UNBOUNDED, and it is the right path for history read back
// from durable storage. A leaf written by a node whose clock was wrong carries
// those timestamps forever; refusing them would make committed data unreadable —
// a clock problem converted into data loss, over skew that already happened.
//
// ⚠ [Clock.Admit] is BOUNDED, and it is the right path at a network boundary. It
// checks a declared skew bound BEFORE merging, and a refusal leaves the clock
// byte-identical. That ordering is the whole decision: merging is irreversible —
// monotonicity is the property that forbids coming back — so a check performed
// afterwards reports damage rather than preventing it.
//
// ⚠ Skew is measured BY THE RECEIVER, against its own wall reading, and never
// self-reported: a node whose clock is wrong is exactly the node whose
// self-assessment is wrong.
//
// ⚠ And the honest limit — this measures the DIFFERENCE between two clocks, not
// either one's error. A receiver whose own clock is wrong refuses correct peers,
// confidently. Two clocks can only establish that they disagree; saying which is
// wrong needs a third party this system does not have.
//
// The decisions this package implements are recorded in
// docs/adr/ADR-002-transaction-identity.md and
// docs/adr/ADR-042-clock-skew-admission.md.
package hlc
