// Package command builds the value a write commits: a transaction bound to one
// entity.
//
// # One entity, refused rather than discouraged
//
// A transaction touches exactly one entity, and an operation naming a second is
// rejected with [ErrCrossEntity] before anything is recorded.
//
// That single constraint is what removes distributed commit from the entire
// system. An entity resolves to one leaf, a leaf has one writer, and a
// transaction confined to one entity therefore needs no agreement with any other
// leaf. The moment a transaction may span entities it may span leaves, and every
// commit becomes a distributed one — a coordinator, a prepare phase, participant
// timeouts, and a recovery path for a coordinator that dies mid-transaction.
//
// It is a refusal rather than a convention because nothing in the data model
// distinguishes a value from a reference to another entity, so a cross-entity
// write is one line of caller code. A rule that is merely discouraged is
// permitted, and once permitted the cost is paid on every commit rather than on
// the ones that asked for it.
//
// # What this costs the caller, which is the honest half
//
// An operation spanning entities cannot be expressed as one transaction. It
// becomes several transactions plus, where it matters, a compensating one — and
// the intermediate states are VISIBLE to readers in between. A reader can
// observe the first half applied and the second not.
//
// That is real work pushed onto the domain, and it is the thing most likely to
// make this decision wrong. If a required operation genuinely cannot be
// expressed this way, that is the decision's stated falsifier rather than an
// inconvenience to work around, and [ErrCrossEntity] is a named error precisely
// so the first real case surfaces loudly instead of being routed around
// silently.
//
// # Retraction is a fact, not an absence
//
// Withdrawing a fact records a datom with its assert flag cleared. It never
// removes one. "This stopped being true" and "this was never recorded" are
// different statements, and only the first can be expressed by an absence in a
// store that overwrites — which this one does not.
//
// The decision this package implements is recorded in
// docs/adr/ADR-003-transaction-boundary.md.
package command
