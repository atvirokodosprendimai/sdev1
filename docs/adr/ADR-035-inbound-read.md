# ADR-035: An entity that things point at is a table, and reading it is a bounded set rather than a scan

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-003-transaction-boundary.md`, `docs/adr/ADR-011-query-language.md`, `docs/adr/ADR-021-search-and-facets.md`, `docs/adr/ADR-023-links-and-traversal.md`, `docs/adr/ADR-027-evaluator.md`, `docs/adr/ADR-034-read-verb.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/ql/**`, `internal/core/eval/**`, `internal/core/ports/ports.go`
**Enforced-by:** `internal/core/eval/inbound_test.go::TestAMemberMissingEitherAttributeIsSkipped`
**Invalidates:** none — it fills `BACKLOG.md` §29's "Inbound edges" bullet, which ADR-023 deferred and named as "a different index, not a different walk"
**Served-path change:** `READ ->name FROM [catalogue] WHERE ->status = 'live' LIMIT 20 OFFSET 40` runs. Until now every read named exactly one entity, so a caller had to already know the identifier of everything they wanted.

## Context

Every read this language can express names ONE entity. `READ * FROM planet-7`
answers about `planet-7` and nothing else. ⚠ That means a caller has to already
know the identifier of everything they want, and there has been no way to ask
"what is there" — `BACKLOG.md` §20 defers enumeration because "list all entities"
needs a planner and cross-leaf routing that do not exist.

★ **An entity that N things point AT is already the bounded set that enumeration
was missing.** `ASSERT alice member = ->staff` makes `staff` a thing to ask about:
not "every entity", which is unbounded and unroutable, but "the entities whose
links land here", which is addressable because the target is addressable. That is
what a table is, and this store already had one — it simply had no way to say so.

ADR-023 deferred exactly this and named its shape: *"Inbound edges. 'What points
at this' is a different index, not a different walk."*

⚠ **The honest limit, stated first.** The referrers are separate entities, so their
keys put them wherever the trie puts them — a complete inbound answer across a
cluster is a cross-leaf question and needs the routing `BACKLOG.md` §18 and §20
defer. This record decides what the statement MEANS and implements it against one
reader. It does not pretend that scales yet.

## Existing Primitives Audit

- `internal/core/ql` (ADR-011, ADR-034): supplies the lexer, `Read` and
  `Predicate`. **Extended** — `[entity]`, `->attribute`, `LIMIT` and `OFFSET`, all
  on the statement that already exists rather than a new verb.
- `internal/core/eval` (ADR-027): supplies evaluation, `latestVisible`, and the
  order of predicate-then-projection. **Extended, and its rules reused unchanged**
  — a member is reduced by the same `ports.Carried` everything else uses.
- `internal/core/ports` (ADR-003): supplies `Reader`, `Datom`, `Snapshot`.
  **A second, separate port is added** — see rule 7 for why not on `Reader`.
- `internal/core/link` (ADR-023): supplies the typed reference and `Walk`.
  **Reused as the definition of an edge** — `Datom.IsReference` is what makes a
  value a link, and this record adds no second notion of one.
- `internal/core/search` (ADR-021): supplies the index-proposes /
  datoms-confirm pattern, and `Confirm`. **The pattern is reused, not the code** —
  see rule 6.
- A new index, or a new storage structure: **none.** See rule 6.
- A general join: **rejected below**, and refused by the parser rather than
  half-implemented.

## Decision

**`FROM [e]` reads the entities that point at `e`; every attribute in such a
statement is a MEMBER's attribute and says so with `->`; a member missing any
attribute the statement names is DROPPED; and paging is over members, after the
drop, in a deterministic order.**

1. **`FROM [e]` names a SET: every entity carrying an asserted reference whose
   value is `e`.** ★ The brackets are the whole idea — `FROM e` reads one entity
   and `FROM [e]` reads the things that point at it. One character apart because
   they are one concept apart, and both are addressed by the same identifier.

2. **`->a` marks an attribute of a MEMBER.** In `READ ->name FROM [staff]`, `name`
   is each member's attribute, not `staff`'s.

3. ⚠ **Inside an inbound read a bare attribute name is REFUSED, and outside one
   `->a` is refused.** A bare name would have to mean the index entity's OWN
   attribute — which is a join, and this record does not do joins. ★ Refusing it
   is what keeps the meaning available: if `a` and `->a` were synonyms today,
   adding the join later would silently change what already-written statements
   mean. A refusal now is cheaper than a migration then.

4. ⚠ **A member missing ANY attribute the statement names contributes NO rows.**
   Missing the projected attribute, missing the predicate's attribute, or failing
   the comparison — all three drop the member, and they drop it entirely rather
   than emitting it with a hole. ★ This is an inner join and it is deliberately
   **the opposite of ADR-011's `OPTIONAL` leg**, where an unmatched leg yields an
   unbound binding and keeps the row. Two statements, two rules, both chosen: a
   shape query asks how much a subject RESEMBLES a pattern, so a partial match is
   an answer; a table read asks which members SATISFY a condition, so a partial
   match is not one.

5. ⚠ **`LIMIT` and `OFFSET` page over MEMBERS, applied AFTER rule 4, over a
   deterministic order — members sorted by entity name.** Three traps in one
   sentence, each of which produces a plausible wrong answer:
   - Paging over ROWS would make `LIMIT 10` return ten attribute-values, cutting
     a member in half, which is not what any caller means by ten results.
   - Paging BEFORE the drop makes page sizes unpredictable — `OFFSET 10` would
     skip ten candidates of which some were never going to appear.
   - Paging with NO order is not paging. Without a total order, `LIMIT 10 OFFSET
     10` can repeat a member it already returned and skip one it never did.

   ⚠ And paging is coherent only WITHIN one snapshot. A caller paging across a
   moving present will see members shift between pages; pinning `AS OF` /
   `TRANSACTION` is the fix, and it is the caller's to apply.

6. ★ **The datoms decide; any index only proposes.** The set is DEFINED as the
   entities carrying an asserted reference to `e` at the snapshot, and every
   candidate is confirmed against its own datoms before it contributes a row.
   ⚠ This is ADR-021's rule, and it is here because an inbound index is a cache
   that goes stale exactly when a reference is RETRACTED: an index that appended
   would keep proposing a member whose edge was withdrawn. Because meaning rests
   on the datoms, a scan is a CORRECT implementation and an index is a later
   optimisation that cannot change an answer.

7. **The capability is a SEPARATE port, `ports.Inbound`, not a method on
   `ports.Reader`.** ⚠ `Reader` is entity-addressed: `Load` answers about an
   identifier a caller already holds. "What points at this" is a scan, and a
   reader serving one entity — a routed remote reader, say — cannot answer it.
   Putting it on `Reader` would make every future implementation claim a
   capability it may not have.

8. ⚠ **A reader that cannot answer is a REFUSAL, never an empty result.**
   `ErrNoInboundIndex`. "Nothing points at this" and "I cannot tell you what points
   at this" are different answers, and returning the first for the second is the
   same defect as ADR-027's discarded `WHERE`: a narrow question getting a
   confident wrong answer with no error.

**What would falsify this.** A member of `[e]` that carries no `lastname`, or no
`name`, appearing in the result of `READ ->name FROM [e] WHERE ->lastname = 'a'`.
That is the falsifier in `Enforced-by:`.

## Alternatives Considered

- **A new verb, or `TRAVERSE … INBOUND`.** It keeps `READ` untouched. Rejected:
  ADR-023 already established that this is a different INDEX and not a different
  walk, and ADR-011's whole argument is that a family of verbs is what this
  language refuses. `FROM [e]` is the same question as `FROM e` over a different
  source, which is what a clause is for.
- **Make `a` and `->a` synonyms inside an inbound read.** Friendlier, and one
  fewer refusal. Rejected under rule 3: it spends the notation that a join will
  need, so adding a join later would change what existing statements mean —
  silently, since both spellings would already parse.
- **Keep the member and leave the missing attribute unbound**, matching
  `OPTIONAL`. Consistent-looking. Rejected under rule 4: it is the right rule for a
  resemblance query and the wrong one for a table read, and a table read is what
  this is. Recorded as a deliberate difference so the inconsistency is not read as
  an oversight.
- **Page over rows rather than members.** It is what a naive implementation does,
  because rows are what the evaluator already produces. Rejected under rule 5:
  `LIMIT 10` would cut a member's attributes in half.
- **Return members in whatever order the store yields.** Cheapest. Rejected under
  rule 5: paging over an unstable order can repeat and skip, and it does so
  quietly — the result still looks like a page.
- **Add `Referrers` to `ports.Reader`.** One port, no type assertion, and every
  reader implements it. Rejected under rule 7: it claims a scan capability on
  behalf of every future reader, including ones that serve a single entity.
- **Build the inbound index first.** It is what makes this fast. Rejected as
  premature under rule 6: the index cannot change an answer, so it can be added
  without reopening this record — and building it now would fix a structure
  against a storage layer whose enumeration story (`BACKLOG.md` §20) is undecided.
- **Wait for routing before deciding any of this.** Honest about the limit.
  Rejected: the limit is about how far an answer reaches, not about what the
  question means, and deciding the meaning now is what lets the routing work
  implement something rather than invent it.

## Component / Boundary Impact

No new component. `ql` gains source and page syntax, `eval` gains the inbound
evaluation, `ports` gains one interface. The boundary that matters is rule 7's:
reading one entity and scanning for referrers are different capabilities and are
different types, so nothing can accidentally require the second by asking for the
first.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `[entity]` source syntax | new — FROM names a set | T1 | callers |
| `->attribute` in a projection or predicate | new — a member's attribute | T1 | callers |
| `LIMIT` / `OFFSET` on a read | new — paging over members | T1 | callers |
| `OFFSET` keyword | new — reserved | T1 | callers |
| `ql.Read.Inbound`, `ql.Read.Page` | new fields | T1 | eval, session |
| `ql.Page` | new — a limit and an offset as written | T1 | eval |
| `ql.ErrJoinNotSupported` | new sentinel — a bare attribute in an inbound read | T1 | callers |
| `ports.Inbound` | new port — `Referrers` | T2 | eval, leafstore, session |
| `eval.ErrNoInboundIndex` | new sentinel — the reader cannot scan | T2 | callers |
| `leafstore.Store.Referrers` | new — the scan, over a real leaf | T2 | eval |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `ql.Read.Inbound`, `ql.Read.Page`, `ql.Page` (T1) | T1 | T2 | No — new fields on an existing type |
| `ports.Inbound`, `leafstore.Store.Referrers` (T2) | T2 | a served path | No |

## Consequences

- **Positive:** There is finally a way to ask "what is there" that does not
  require knowing every identifier first, and it is bounded by an address rather
  than being a scan of everything.
- **Positive:** It needed no new storage. A reference was already a datom, so the
  set is a question about datoms the store already holds.
- **Positive:** Rule 6 means an index can be added later as pure optimisation, and
  a wrong index is a slow answer rather than a wrong one.
- **Negative:** ⚠ It is **N+1 reads** — one scan for candidates, then one load per
  member to confirm and project. That is the cost of rule 6 and it is real. Stated
  rather than discovered.
- **Negative:** ⚠ A complete answer across leaves needs routing that does not
  exist. One reader means one leaf or one session, and a cluster-wide inbound read
  is deferred with `BACKLOG.md` §18/§20.
- **Negative:** `->` now appears on both sides of the language — as a reference
  VALUE in a write and as a member's attribute in a read. Position disambiguates
  completely (right of `=` in a write, left of it or after the verb in a read), so
  it is a readability cost rather than a grammar ambiguity. Named so it is not
  rediscovered as a bug.
- **Neutral:** Rule 4 differs from `OPTIONAL`. Both are deliberate.

## Out of Scope

- Joining a member's attributes with the index entity's own (deferred: `docs/adr/BACKLOG.md` §20 — rule 3 reserves `->` versus bare precisely so this stays addable)
- An inbound INDEX, as opposed to the scan that defines the answer (deferred: `docs/adr/BACKLOG.md` §27 — rule 6 makes it a pure optimisation)
- A cluster-wide inbound read across leaves (deferred: `docs/adr/BACKLOG.md` §18 — the referrers are separate entities and land on separate leaves, so it needs routing)
- `ORDER BY` (deferred: `docs/adr/BACKLOG.md` §20 — rule 5 fixes ONE order because paging requires one, and choosing among orders is a cost-model question)
- Filtering on ABSENCE — "members with no `thirdname`" (deferred: `docs/adr/BACKLOG.md` §20 — it is one rule shared with `MATCH SHAPE` and belongs in one record of its own)
- More than one predicate, or a boolean combination of them (deferred: `docs/adr/BACKLOG.md` §20)
- Enumerating entities that point at NOTHING (permanent: boundary: that is "every entity", which is the unbounded enumeration this record exists to avoid needing)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A retracted reference keeps proposing a member | High — it is exactly what a cached index does | High — a read returns a member whose edge was withdrawn, and it looks like a live answer | Rule 6: the datoms decide, and confirmation is not optional |
| Paging is applied before the drop, or over rows | High — both are what falls out of the obvious implementation | Med — pages have unpredictable sizes, or a member is cut in half | Rule 5, with a test that pages a set where some members are dropped |
| Members come back in an unstable order | High — map iteration is the default in Go | High — paging silently repeats and skips, and the result still looks like a page | Rule 5: sorted by entity name, with a test |
| A reader that cannot scan returns nothing | Med — an empty slice is the easy zero value | High — "nothing points here" for "I cannot tell", which is ADR-027's discarded-`WHERE` defect again | Rule 8: a named refusal |
| `a` and `->a` quietly become synonyms | Med — it looks like a friendly convenience | Med — the join notation is spent, and adding a join later changes existing statements | Rule 3, refused at parse time with a named sentinel |
| The N+1 cost is discovered in production | Med | Med | Stated in Consequences, and rule 6 says why it is the price of correctness |

## Rollback

Removing it removes a capability rather than changing one: no stored bytes change,
because the set was always derivable from datoms already written. ⚠ The one
irreversible part is rule 3's notation — statements written with `->` would have
to be rewritten, which is the same reason ADR-034 was worth doing early.

## Follow-ups

- [ ] When routing exists (`BACKLOG.md` §18), decide what a cross-leaf inbound read costs and whether it is offered at all — a bounded set that fans out to every leaf is bounded in name only.
- [ ] When an inbound index is built (`BACKLOG.md` §27), prove rule 6 holds by running the same statements against the index and against the scan and comparing — an index that can change an answer has broken this record.
- [ ] Revisit rule 5's single order if `ORDER BY` arrives; the order chosen here exists to make paging honest, not because entity name is the interesting one.
