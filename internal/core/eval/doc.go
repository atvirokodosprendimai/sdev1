// Package eval turns a parsed statement into rows.
//
// It is the evaluation half of docs/adr/BACKLOG.md §20, which waited on storage
// and no longer has to.
//
// # The rule this package exists to hold
//
// ⚠ A clause the parser accepts is EVALUATED or REFUSED BY NAME. It is never
// ignored.
//
// That is not a style preference. `Read.Where` was parsed from ADR-011 onward
// and evaluated nowhere, so this ran and answered:
//
//	READ * FROM planet-3 WHERE mass = "999"
//	  planet-3   mass         5
//	  planet-3   radius       6371
//
// Nothing matched. Every row came back. There was no error and no warning, and no
// way for the caller to tell that the question they asked was not the question
// answered. ★ A refusal is an answer a caller can act on; a filtered result that
// was never filtered is not.
//
// # How a READ is evaluated
//
//  1. The time clause is resolved ONCE.
//  2. The named entity is loaded ONCE, at that snapshot.
//  3. Datoms are filtered by ADR-002's visibility predicate, with the resolved
//     qualifiers passed straight through.
//  4. Each attribute reduces to its latest visible datom; a retraction SUPPRESSES
//     the attribute rather than being reported as a value.
//  5. The predicate is applied to the entity's WHOLE attribute set.
//  6. The projection is applied last.
//
// ⚠ Steps 5 and 6 are in that order and the obvious order is the other one.
// `READ name FROM planet-7 WHERE class = 'terrestrial'` is in the published
// guide, so a predicate must be able to name an attribute the projection does not
// return — narrowing first leaves nothing to test against, and the query silently
// returns nothing.
//
// # Two smaller rules, for the same reason as the big one
//
// ⚠ A comparison is NUMERIC only when the literal was written as a number. That
// makes it a property of the query text, readable where it was written; deciding
// it from the stored value would make the same statement change meaning when the
// data changes, since "10" < "9" is true as text and false as numbers.
//
// ⚠ A comparison that cannot be made is [ErrNotComparable], not false. "This is
// not a number" and "this is not greater than five" are different answers.
//
// # What it takes, and what it does not
//
// [Read] takes an INSTANT, not a clock. Reading a clock twice inside one
// statement is therefore not expressible — the defect ADR-023 fixed for
// traversal, arriving from a different direction.
//
// It takes a [ports.Reader], so writing is not expressible either, and it names
// no store: the same statement runs against a leaf, against memory, or against
// anything that reads.
//
// It does not plan, does not choose an index, and computes no similarity. Those
// are still deferred, with their reasons, in docs/adr/BACKLOG.md §15 and §20.
//
// See docs/adr/ADR-027-evaluator.md.
package eval
