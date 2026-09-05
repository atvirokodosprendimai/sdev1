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
// # What the boundary cost when it met a real registry
//
// The constraint above removes distributed commit from the whole system, and its
// falsifier is correspondingly large: it fails if a legitimate domain operation
// cannot be expressed within one entity. That was untested until a real corpus
// arrived — 548,547 Lithuanian public-procurement legal entities, examined
// 2026-09-05 (ADR-044).
//
// ★ The corpus armed the falsifier in the registry's own vocabulary. 277 entities
// carry a status naming a legal act that spans several companies, including 131
// whose status is literally "participating" — Dalyvaujantis reorganizavime,
// Dalyvaujantis atskyrime. A status whose entire content is "I am one party to an
// act involving others" is the domain saying multi-entity operations are real.
//
// ★ The boundary held, because the ACT IS AN ENTITY. A reorganisation has a date,
// a kind and participants, so registering it is a single-entity write carrying
// references to the companies involved.
//
// ⚠ And it is only USABLE because of ADR-035's inbound read: "which companies are
// in this reorganisation" is a read from the act. Before that existed the
// normalised model was storable and unqueryable, so this package's liveability
// rests on a record built for an unrelated reason. A dependency nobody designed
// is one nobody is maintaining.
//
// ⚠ Where the registry DENORMALISES — a status on each participant — reproducing
// it takes two transactions, and bitemporality pays for the atomicity this
// boundary does not provide: both facts carry the act's real-world date as
// Valid.From, so a reader on the VALID axis sees a consistent world however the
// writes interleaved.
//
// ★★ The rule that generalises: bitemporality substitutes for cross-entity
// atomicity exactly when the operation is a statement about the WORLD — which has
// its own instant — rather than about the SYSTEM.
//
// ⚠ And the class where it does NOT, so the limit is known rather than
// discovered: an invariant that must hold at every TRANSACTION instant — a
// balance transfer, a double-entry ledger, a conserved sum. There no real-world
// instant makes the intermediate state acceptable, and this boundary genuinely
// fails. A registry records what happened; it conserves nothing.
//
// The decisions this package implements are recorded in
// docs/adr/ADR-003-transaction-boundary.md and
// docs/adr/ADR-044-the-boundary-against-a-real-domain.md.
package command
