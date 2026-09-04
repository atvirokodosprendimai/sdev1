// Package ports declares the boundary between the write path and the read
// models, as interfaces and nothing else.
//
// # The asymmetry is the point
//
// A read model is handed [Reader]. The write path is handed [Store]. A read
// model therefore cannot write — not because a document forbids it, but because
// it was never given anything that can.
//
// That distinction matters more than it looks. "The read side must not write"
// is a rule, and a rule holds until somebody is in a hurry; "the read side has
// no write method" is a compile error, and it holds at three in the morning. So
// this package exists to move one rule out of prose and into the type system,
// and it holds interfaces only, because an implementation here would give
// something to reach past.
//
// # Why the write path does not read a projection
//
// [Store] carries both halves for one reason: the write path answers its own
// questions. Command validation — does this entity exist, what is the current
// value, is this a duplicate — is answered from the writer's own state, never
// from a read model.
//
// A write that consults a projection has made its correctness depend on a
// component that is deliberately stale and separately scaled. A lagging replica
// then stops being a slow read and becomes a source of WRONG WRITES, which is
// the failure mode the read/write split exists to remove. The cost of avoiding
// it is real: the same fact ends up indexed twice, once minimally for the
// writer and once richly per query shape. That duplication is the price and it
// is deliberate.
//
// # How it fails
//
// The failure this package guards is silent and gradual. Nothing breaks the day
// a read model acquires a writable port; it simply becomes possible for a
// projection to write, and some later change does. By then the coupling is load
// bearing and the split is decorative. That is why the guard is structural and
// why a companion test scans every package for a read-side dependency on a
// writable port.
//
// # A notification carries an identifier, never state
//
// [Publisher] takes an identifier. A consumer re-reads for itself, so two
// notifications racing for the same subject both re-read and the later answer
// wins, correctly. Publishing rendered state would let an older render arrive
// last and be applied.
//
// The decision this package implements is recorded in
// docs/adr/ADR-003-transaction-boundary.md.
package ports
