// Package session runs statements, so the decisions this system has made can be
// watched working — in memory, or against a leaf on a disk.
//
// # Why it exists
//
// ★ A system nobody can run is a system nobody can check. Every capability in
// this repository was decidable and tested, and none of it was DEMONSTRABLE — a
// reader could work through twenty-two decision records and still not create a
// single fact. This closes that gap: write a statement, read it back, search it,
// count it.
//
// # What it is not
//
// ⚠ IT IS NOT THE STORAGE ENGINE, and it must never be mistaken for one. It
// USES one: [Open] backs a session with a leaf
// ([github.com/atvirokodosprendimai/sdev1/internal/core/leafstore]), so a fact
// written by one process is read by the next. What this package does is run
// statements; where the bytes live is that package's decision, not this one's.
//
// ⚠ And what it holds is still bounded by memory. [Open] rehydrates the WHOLE
// leaf, because search, faceting and traversal have to enumerate what a leaf
// holds and a read port deliberately cannot. That is honest for one leaf and one
// process; it is not how an engine reads. There is still no replica, no network
// and no second leaf.
//
// ⚠ [New] — a session with no store — is unchanged and is not a degraded mode.
// Datoms live in a map and vanish when the process exits. Every statement behaves
// identically either way, which is the property that keeps the two from becoming
// two different languages.
//
// ⚠ AND IT MUST NOT BECOME THE SPECIFICATION. When the real engine lands it has
// to agree with the RECORDS, not with this package. The way that is kept true is
// a constraint on what this package may contain: it builds only on packages the
// records already govern, and adds no rule of its own.
//
//   - Visibility over the two time axes is
//     [github.com/atvirokodosprendimai/sdev1/internal/core/temporal]'s.
//   - A datom's shape, and that a retraction is a datom rather than an absence,
//     is [github.com/atvirokodosprendimai/sdev1/internal/core/ports]'s.
//   - Transaction identity is [github.com/atvirokodosprendimai/sdev1/internal/core/tx]'s.
//   - The erasure boundary and the facet refusal are
//     [github.com/atvirokodosprendimai/sdev1/internal/core/search]'s.
//
// If a behaviour here is not traceable to one of those, it is a defect in this
// package rather than a decision.
//
// # What it does
//
// [Session.Run] takes one statement and applies it:
//
//   - ASSERT and RETRACT append a datom, at a transaction the SESSION mints.
//     ⚠ The instant never comes from the statement — the language makes that
//     unsayable, and this is the layer that must not quietly reintroduce it.
//   - READ filters datoms through the visibility predicate at the resolved
//     snapshot, so the two axes behave exactly as the defaults table says.
//   - SEARCH answers from an index fed on the WRITE path, so a search finds
//     facts that were asserted rather than facts something indexed by hand.
//   - MATCH SHAPE is refused by name. It needs a similarity metric chosen
//     against a corpus, and an empty result would read as "nothing matched",
//     which is the wrong answer to "this is not implemented".
//
// # Durability
//
// [Session.Seal] writes what has been asserted since the last seal into a
// segment; [Session.Close] releases the leaf and does NOT seal.
//
// ⚠ That asymmetry is ADR-020's, not a convenience: an acknowledged write is held
// in memory, so an unsealed tail is lost. Sealing on close would make the commit
// point depend on how a process happened to end.
//
// ★ Rehydration goes through the same path as a live write, so the datom map, the
// search index and the link resolver are populated by one piece of code. A
// rehydration that restored only the datoms would leave SEARCH answering nothing
// after a restart, with READ working and no error anywhere.
package session
