// Package chaos breaks the rest of this system on purpose and records what does
// not come back.
//
// # The catalogue is the deliverable
//
// The tests here are not the point. `docs/adr/FAILURES.md` is: a written record
// of what this system survives and, more usefully, what it does not. That second
// half is what an operator actually needs at three in the morning, and it is the
// half nobody writes down — because every other document in a project describes
// what is supposed to happen.
//
// Every fault registered here has exactly one entry there, and every entry names
// a registered fault. Both directions are checked. Checking only the first
// catches a new fault nobody wrote up; checking the second catches a fault that
// quietly stopped being injected while its entry still reads as current, which is
// the direction that rots.
//
// # Three dispositions, and no fourth
//
// A fault RECOVERS, is UNRECOVERABLE BY DESIGN, or is UNRECOVERABLE AND OPEN.
//
// The middle one is a correct answer rather than a failure. Losing more fragments
// than a stripe has parity destroys the block; there is no recovery, and a system
// that produced something anyway would be inventing. Saying so is what keeps the
// OPEN entries countable — without the distinction, twenty entries tell a reader
// nothing about which two matter.
//
// There is deliberately no fourth value. A fourth is how "we are looking into it"
// enters a catalogue, after which nothing is countable again.
//
// # A seed matters more than realism
//
// Every schedule is a pure function of one integer, printed on failure and
// replayable. An unreproducible failure is a report rather than a bug: it costs
// whoever tries to confirm it a day, it gets muted, and the muted one is usually
// the real one.
//
// This is why in-process injection is the primary suite rather than a composed
// cluster. Most fault CLASSES — a lost fragment, a flipped bit, a stopped writer,
// a breached floor — are expressible over values, and expressed that way they are
// deterministic and cost nothing. The classes that genuinely need separate
// processes are partitions, host clock skew, disk exhaustion and crash-restart,
// and those are the composed suite's job.
//
// # What this package does not decide
//
// It injects and observes. What SHOULD happen under a fault is owned by the
// record that made the promise — the durability policy for the floor, the erasure
// record for coding tolerance, the read-path record for the tail. A chaos package
// carrying its own expectations would be a second statement of every guarantee,
// and the two would drift apart without anything noticing.
package chaos
