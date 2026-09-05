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
// # How an inbound READ is evaluated
//
// `READ ->a FROM [e]` reads the entities that point at e (ADR-035). The order of
// operations is the decision, and each step is a rule:
//
//  1. Candidates are PROPOSED by [ports.Inbound]. A reader that is not one is
//     REFUSED with [ErrNoInboundIndex] — never answered with an empty result.
//  2. Each candidate is loaded and CONFIRMED to still carry an asserted
//     reference to e at the snapshot.
//  3. A member missing any attribute the statement names, or failing the
//     comparison, is DROPPED entirely.
//  4. Survivors are ordered by entity name, then paged.
//
// ⚠ Step 2 is not a formality. A candidate list is a proposal, and the one thing
// an index that only appends gets wrong is a RETRACTED reference — it never
// un-proposes. Because meaning rests on the datoms, a scan is a correct
// implementation and an index is an optimisation that cannot change an answer.
//
// ⚠ Step 3 is an inner join, and it is deliberately the OPPOSITE of `OPTIONAL`
// in a shape query, where an unmatched leg keeps the row with an unbound binding.
// A shape query asks how much a subject RESEMBLES a pattern, so a partial match
// is an answer; a table read asks which members SATISFY a condition, so it is not.
//
// ⚠ Step 4 pages over MEMBERS and comes after step 3. Paging over rows would cut
// a member in half; paging before the drop would give unpredictable page sizes;
// and paging without a total order repeats and skips while still looking like a
// page.
//
// ⚠ It costs one scan plus one load per candidate. That N+1 is the price of
// step 2 and is recorded rather than optimised away.
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
