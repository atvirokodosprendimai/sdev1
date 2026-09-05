# ADR-036: Absence is a clause of its own, so "has this and lacks that" needs no boolean algebra

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-002-transaction-identity.md`, `docs/adr/ADR-011-query-language.md`, `docs/adr/ADR-027-evaluator.md`, `docs/adr/ADR-034-read-verb.md`, `docs/adr/ADR-035-inbound-read.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/ql/**`, `internal/core/eval/**`
**Enforced-by:** `internal/core/eval/without_test.go::TestAnExcludedAttributeIsNotAlsoRequired`
**Invalidates:** none — it adds a clause ADR-011 did not have; it narrows ADR-011's documented "one binding per leg" to legs that project, and says so in rule 6
**Served-path change:** `READ ->name FROM [staff] WHERE ->rank = 3 WITHOUT ->thirdname` runs. Until now nothing in the language could ask about an attribute an entity does NOT have.

## Context

The language can say `WHERE a = 'x'`. It cannot say "and has no `b`".

⚠ **And the obvious fix is the wrong one.** Absence looks like a predicate, so the
natural move is `WHERE NOT b` — at which point "has `a` = x AND lacks `b`" needs
`AND`, and ADR-011 deliberately has no boolean composition: *"Exactly one
predicate. There is no `AND`, no `OR`, and no parentheses."* Making absence a
predicate therefore forces boolean algebra into the grammar to express the one
question anybody actually asks.

★ **Absence is not a comparison, so it does not belong where comparisons go.** A
separate clause gives "matches this AND lacks that" for free, because two clauses
are conjoined by being two clauses — no operator, no precedence, no parentheses.

The same question exists in `MATCH SHAPE`, which has `REQUIRE` and `OPTIONAL` and
no way to say a subject must NOT carry an attribute.

## Existing Primitives Audit

- `internal/core/ql` (ADR-011, ADR-034, ADR-035): supplies the read grammar,
  `LegKind`, `Leg` and `BuildRow`. **Extended** — one keyword, one leg kind.
- `internal/core/eval` (ADR-027, ADR-035): supplies `latestVisible`, the drop
  rule and `ports.Carried`. **Reused as the DEFINITION of absence** — see rule 2.
  Nothing new decides what "does not have" means.
- `internal/core/temporal` (ADR-002): supplies visibility. **Reused unchanged**,
  and it is what makes absence snapshot-relative rather than absolute (rule 3).
- A `NOT` operator, or boolean composition: **rejected below.**
- A separate "is null" value: **rejected below.**

## Decision

**`WITHOUT a[, b]` is a clause of its own, in both statements, meaning the subject
must not CARRY those attributes at the snapshot.**

1. **`WITHOUT` is a CLAUSE, not a predicate, and not an operator.** ★ Two clauses
   conjoin by existing, so `WHERE ->rank = 3 WITHOUT ->thirdname` needs no `AND`.
   That is the whole reason for the shape of this decision: it delivers the
   conjunction everybody wants without opening boolean composition, which ADR-011
   closed on purpose.

2. **"Does not carry" is defined by `ports.Carried`, and by nothing new.** An
   attribute is absent when it is not in the entity's carried set at the snapshot.
   ★ That already means three different histories, and all three are correctly
   absent: never asserted; asserted and later RETRACTED; and asserted over a
   validity interval that does not cover the instant asked about.

3. ⚠ **Absence is SNAPSHOT-RELATIVE, never absolute.** `WITHOUT thirdname` asks
   "does not have one AT this instant", not "never had one". A caller who reads it
   as the second will be wrong about every entity that ever had the attribute
   retracted — and being able to ask the first is what makes retraction meaningful
   at all. It is documented in exactly those words rather than left to be inferred.

4. ⚠ **An excluded attribute is NEVER also required, and this is the trap.**
   ADR-035 rule 4 drops a member missing any attribute the statement NAMES. A
   `WITHOUT` attribute is named — in order to demand its absence — so applying the
   drop rule to it would make the clause unsatisfiable: every member would be
   dropped for lacking exactly the thing the caller asked them to lack. ★ The rule
   is that the drop applies to attributes a statement PROJECTS or COMPARES, and
   never to ones it EXCLUDES. That is the falsifier in `Enforced-by:`.

5. **In a shape query it is a third leg kind, `LegExcluded`, and it carries its own
   time clause like every other leg.** ⚠ ADR-011's central property is that time
   is a clause and can therefore attach per leg. A leg kind that could not carry
   one would be the first exception, and "did not have a thirdname AS OF 1900" is a
   real question rather than a hypothetical.

6. ⚠ **An excluded leg contributes NO BINDING to a result row.** It is a filter,
   and its answer is already carried by the row existing at all. ★ Binding it as
   `Unbound` would be actively wrong: `Unbound` means "an OPTIONAL leg matched
   nothing", and the two would become indistinguishable — one says the subject was
   asked for a value and had none, the other says it was required to have none.
   This narrows ADR-011's "one binding per leg" to "one per leg that projects",
   which is stated here because it is a change to a documented invariant.

7. **Every attribute in a `WITHOUT` obeys ADR-035 rule 3's marker.** `->a` inside
   an inbound read, bare outside one. One rule about what an attribute name means,
   applied everywhere an attribute name appears.

**What would falsify this.** `READ ->name FROM [staff] WITHOUT ->thirdname`
returning nothing over members that carry `name` and no `thirdname`. That is rule
4 broken, it is the failure the drop rule causes by default, and it is the
falsifier in `Enforced-by:`.

## Alternatives Considered

- **`WHERE NOT a`, absence as a predicate.** It is the first thing anybody
  writes. Rejected under rule 1: `WHERE` holds exactly ONE predicate by ADR-011,
  so "has `a` = x and lacks `b`" would require `AND` — and adding boolean
  composition to express the single most common form of this question is a large
  grammar change bought by a small notational preference.
- **Allow `AND` in `WHERE`, then absence is just another term.** More general,
  and eventually somebody will want it. Rejected as a much larger decision
  wearing this one's clothes: it needs precedence, parentheses, and an evaluator
  that plans term order. `BACKLOG.md` §20 owns it, and this record must not decide
  it by accident.
- **A null-like value, so `WHERE a = NULL` works.** Familiar from SQL. Rejected:
  this store has no nulls to compare — a datom either exists at a snapshot or does
  not — and inventing a value that means "no value" would put it in the store's
  vocabulary to serve the query language's.
- **`EXCLUDE` for shapes and `NOT` for reads.** Each reads well in its own
  statement. Rejected: it is one concept and would have two names, so every reader
  has to learn that they are the same thing. `WITHOUT` reads correctly in both.
- **Make an excluded leg bind `Unbound`,** keeping "one binding per leg" intact.
  Rejected under rule 6: it collides with what `Unbound` already means for an
  optional leg, and the collision is silent — both render identically.
- **Apply the drop rule to `WITHOUT` attributes for consistency.** It is what
  falls out of ADR-035's implementation untouched. Rejected under rule 4: the
  clause would be unsatisfiable, and it would fail by returning NOTHING, which
  looks exactly like a correct answer about data that does not exist.

## Component / Boundary Impact

No component boundaries move. `ql` gains a keyword and a leg kind, `eval` gains
one filter step. ★ The boundary worth naming is that absence is defined by
`ports.Carried` and not by this record: "what an entity currently has" already had
one implementation, and asking the negative question must not create a second.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `WITHOUT` keyword | new — reserved | T1 | callers |
| `ql.Read.Without` | new field — the excluded attributes | T1 | eval |
| `eval` absence filter | new — applied after the predicate, before projection | T1 | callers |
| `ql.LegExcluded` | new `LegKind` | T2 | callers, a future shape evaluator |
| `ql.BuildRow` | behaviour — an excluded leg that matched drops the row and binds nothing | T2 | a future shape evaluator |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `WITHOUT` keyword (T1) | T1 | T2 | No — T2 reuses the keyword T1 reserves |
| `ql.LegExcluded`, `ql.BuildRow` (T2) | T2 | a shape evaluator (`BACKLOG.md` §20) | ⚠ Yes for `BuildRow` — rule 6 narrows "one binding per leg" |

## Consequences

- **Positive:** "has this, lacks that" is expressible, and it cost one keyword
  rather than a boolean grammar.
- **Positive:** Absence reuses `ports.Carried`, so retraction and validity
  intervals are already handled correctly and no second notion of "has" exists.
- **Positive:** The shape query gains the missing third leg kind, and it keeps
  the per-leg time clause that is ADR-011's whole argument.
- **Negative:** ⚠ Two clauses that both filter, `WHERE` and `WITHOUT`, and a
  reader must know they conjoin. That is the price of not having `AND`, and it is
  cheaper than the alternative.
- **Negative:** ⚠ Rule 6 narrows a documented invariant. A consumer indexing
  `Row.Bindings` positionally against `ShapeQuery.Legs` would be wrong. Nothing
  does yet — the shape evaluator does not exist — which is why now is when this
  costs least.
- **Neutral:** `WITHOUT` on a read of one entity is allowed and means the same
  thing: return the entity's attributes only if it lacks the named ones.

## Out of Scope

- `AND`, `OR` and parentheses in `WHERE` (deferred: `docs/adr/BACKLOG.md` §20 — rule 1 exists precisely so this record does not decide it by accident)
- Comparing against absence, as in `WHERE a = NULL` (permanent: boundary: a datom exists at a snapshot or does not, and a value meaning "no value" would put the query language's vocabulary into the store's)
- Evaluating a shape query at all, including its metric (deferred: `docs/adr/BACKLOG.md` §20 — `BuildRow` decides the ROW rules and a similarity metric still needs real data)
- Indexing absence, so an excluded attribute could narrow a candidate set rather than filter one (deferred: `docs/adr/BACKLOG.md` §27 — and it must not change an answer, by ADR-035 rule 6)
- Absence across a traversal, as in "points at nothing" (deferred: `docs/adr/BACKLOG.md` §29)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The drop rule is applied to `WITHOUT` attributes | **High — it is what ADR-035's code does untouched** | Critical — the clause becomes unsatisfiable and fails by returning NOTHING, which is indistinguishable from a correct answer | Rule 4, and it is the record's falsifier rather than an ordinary test |
| Absence is read as "never had one" | High — that is what the English suggests | High — wrong about every entity whose attribute was retracted | Rule 3, documented in those words, with a test over a retracted attribute |
| An excluded leg binds `Unbound` | Med — it preserves a documented invariant | Med — it becomes indistinguishable from an optional leg that matched nothing | Rule 6, with a test asserting the binding count |
| Absence becomes a predicate, then `AND` follows | Med — `WHERE NOT b` is what everybody writes first | High — boolean composition arrives as a side effect rather than as a decision | Rule 1, and the alternative is recorded so a later proposal argues against a decision |
| A second definition of "does not have" appears in the evaluator | Med | High — it would drift from `ports.Carried`, and the two would disagree about retracted attributes | Rule 2: absence is the negation of the existing reduction, computed nowhere else |

## Rollback

Reverting removes a clause; no stored bytes change and no statement's meaning
changes, because absence was never expressible. ⚠ Rule 6 is the exception: a
consumer written against "one binding per leg" would have to be revisited, and
none exists yet.

## Follow-ups

- [ ] When `AND` is decided (`BACKLOG.md` §20), revisit whether `WITHOUT` should become an ordinary term — rule 1's argument is about what the grammar can express today, not about absence being special forever.
- [ ] When a shape evaluator exists (`BACKLOG.md` §20), confirm rule 6 survives contact with a real result set: an excluded leg must filter and never score, or a subject carrying the excluded attribute becomes "less similar" instead of not a candidate.
