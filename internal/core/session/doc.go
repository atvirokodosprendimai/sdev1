// Package session runs statements against an in-memory store, so the decisions
// this system has made can be watched working.
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
// ⚠ IT IS NOT THE STORAGE ENGINE, and it must never be mistaken for one. Datoms
// live in a map. They vanish when the process exits. There is no segment, no
// leaf, no replica, no disk and no network. Nothing here is durable, and nothing
// here scales.
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
//   - SELECT filters datoms through the visibility predicate at the resolved
//     snapshot, so the two axes behave exactly as the defaults table says.
//   - SEARCH answers from an index fed on the WRITE path, so a search finds
//     facts that were asserted rather than facts something indexed by hand.
//   - MATCH SHAPE is refused by name. It needs a similarity metric chosen
//     against a corpus, and an empty result would read as "nothing matched",
//     which is the wrong answer to "this is not implemented".
package session
