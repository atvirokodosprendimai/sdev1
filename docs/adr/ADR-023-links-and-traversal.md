# ADR-023: A link is a typed datom, and a traversal resolves every hop at one instant

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-002-transaction-identity.md`, `docs/adr/ADR-003-transaction-boundary.md`, `docs/adr/ADR-007-crypto-shredding.md`, `docs/adr/ADR-011-query-language.md`, `docs/adr/ADR-016-tenant-prefix.md`, `docs/adr/ADR-022-write-statements.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/link/**`
**Enforced-by:** `internal/core/link/link_test.go::TestEveryHopResolvesAtOneInstant`
**Invalidates:** none — it fills a gap. ADR-003 made the entity the transaction boundary and said nothing about how one entity refers to another, so a value that IS a reference has been indistinguishable from a string that looks like one
**Served-path change:** An entity can refer to another, and a caller can walk the resulting graph as it stood at any instant — where before a reference was untyped bytes that nothing could follow.

## Context

`ports.Datom.Value` is `[]byte`. That is enough to store a reference and not
enough to *know* one: `"planet-9"` as a name and `"planet-9"` as a link are the
same nine bytes, so nothing can traverse, nothing can validate, and nothing can
tell a dangling reference from an ordinary string.

So the store has no relationships. It also, therefore, has no hierarchies — a
taxonomy is a graph of links, and there are none.

★ **The interesting part is not the value type.** Tagging a value as a reference
is a small change. What makes this a decision worth recording is what happens
when you *walk* those references in a store where everything is bitemporal.

⚠ **A TRAVERSAL THAT RESOLVES EACH HOP AT ITS OWN "NOW" PRODUCES A TREE THAT
NEVER EXISTED.** Ask for a category hierarchy as it stood last March, and the
natural implementation reads the root at March, then reads its children with a
fresh read — at today's instant. The result is March's root with today's
children: a shape that was never true at any moment, assembled from two. Nothing
about it looks wrong. Every node in it is real, every edge in it existed at some
point, and the tree as a whole is fiction.

★ That failure is invisible precisely where it matters most — in the historical
queries a bitemporal store exists to answer.

## Existing Primitives Audit

- `internal/core/ports` (ADR-003): supplies `Datom`. **Extended by one field
  rather than joined by a second structure** — a link IS a datom, so it inherits
  bitemporality, retraction and the transaction boundary for free. A separate
  edge table would need every one of those decided again.
- `internal/core/temporal` (ADR-002): supplies `Visible` and `Query`. **Reused
  whole and made the single instant** every hop of a traversal resolves at.
- `internal/core/ql` (ADR-011, ADR-022): supplies the statements. **Extended**:
  a link is written with the same `ASSERT` and read with the same clause.
- `internal/core/crypt` (ADR-007): **relied on for what a link must NOT do** — see
  rule 6. A reference is plaintext structure beside encrypted content.
- A graph database or an edge store: **none.** ⚠ Adopting one would mean links
  living outside the datom log, which loses bitemporality, retraction, erasure and
  the tenant boundary in a single step — every property this record gets for free
  by refusing to.

## Decision

**A link is a datom whose value is typed as a reference, and any walk over links
resolves every hop at one instant.**

1. **A value carries a kind: literal or reference.** ⚠ The kind is stored, not
   inferred. Guessing from the shape of the bytes — "it looks like an entity
   name" — makes every string that resembles an identifier into an accidental
   edge, and the guess changes as data does.

2. **A link is an ordinary datom.** It is bitemporal, retractable, bound to one
   entity, and inside the tenant subtree, because it is not a new kind of thing.
   ★ A separate edge store would have to re-decide all four, and would get at
   least one of them wrong.

3. **A traversal takes ONE snapshot and uses it for every hop.** ⚠ This is the
   record. Resolving hop *n+1* at a fresh instant assembles a tree that never
   existed, out of parts that each did.

4. **A traversal is depth-BOUNDED and the bound is required.** An unbounded walk
   over a graph a caller does not control is a full scan they did not ask for.

5. **A cycle is detected and reported, never silently truncated.** ⚠ Truncating
   returns a partial answer that looks complete. And cycles are not hypothetical
   here: a hierarchy is edited over time, so a link added in March and another in
   April can form a loop that exists only at instants between them — visible in a
   historical query and in no current one.

6. **A dangling reference is a normal answer, not an error.** ★ The target may
   have been retracted, may not exist yet at the instant asked about, or may have
   been ERASED. ⚠ That last case is why this cannot be an error: distinguishing
   "target missing" from "target erased" would rebuild the existence oracle
   ADR-007 removed. All three look identical.

7. **A traversal never crosses a tenant.** The subtree is the boundary, and a
   reference naming an entity outside it does not resolve.

**What would falsify this.** A traversal whose hops resolve at different
instants. That is the falsifier in `Enforced-by:`, it is checkable today with no
storage engine, and it is exactly what a reasonable implementation does by
default — each hop is a read, and a read defaults to now.

## Alternatives Considered

- **Infer references from the value's shape.** No schema change, works
  immediately. Rejected under rule 1: every string resembling an identifier
  becomes an edge, the graph changes when unrelated data does, and there is no
  way to store a string that merely looks like a name.
- **A separate edge store, or a graph database beside the log.** The mature
  answer, and it would bring traversal algorithms for free. Rejected under rule 2:
  edges outside the datom log lose bitemporality, retraction, erasure and the
  tenant boundary in one move — and every one of those is a property this record
  otherwise gets without asking.
- **Resolve each hop at the current instant.** Simplest, and what a naive walk
  does. Rejected under rule 3: it fabricates trees that never existed, and only in
  historical queries, where nothing looks wrong.
- **Let a traversal run unbounded, stopping when it runs out of graph.** Fine on
  small data. Rejected under rule 4: on a graph the caller does not control it is
  an unbounded scan, and search taught this system what one request fanning out
  costs.
- **Truncate on a cycle and return what was found.** Always returns something.
  Rejected under rule 5: a partial answer indistinguishable from a complete one is
  worse than a refusal, and a caller cannot tell.
- **Error on a dangling reference.** Catches data problems early. Rejected under
  rule 6: an erased target and a missing one must be indistinguishable, or `stat`
  on a graph becomes the oracle ADR-007 spent a record removing.
- **A dedicated `LINK` statement.** Reads well. Rejected: a link is a datom, so
  `ASSERT` already says it. A second write verb would imply a second kind of
  thing and invite it to drift into having its own rules.

## Component / Boundary Impact

One new component, `internal/core/link`, owning what a reference is and what
walking one means. It has one reason to change: how this store expresses that one
entity refers to another.

⚠ The boundary: it TYPES and TRAVERSES. It stores nothing and reads no storage —
`Walk` is handed a resolver and a snapshot and returns the reachable set. Keeping
the walk separate from the fetch is what makes the same-instant rule provable
today, with no engine.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `link.Kind` | new — literal or reference, and no third | T1 | T2 |
| `link.Value` | new — bytes plus their kind | T1 | T2 |
| `link.Ref` | new — a reference to an entity | T1 | T2 |
| `link.Resolver` | new — what a walk asks for one entity's links at a snapshot | T1 | T2 |
| `link.Walk` / `link.Path` | new — a bounded, single-instant traversal | T1 | callers |
| `link.ErrDepthRequired` / `link.ErrCycle` | new sentinels | T1 | callers |
| `ql` reference literals | new, `pending` — writing a link in the language | T2 | callers |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `link.Kind`, `link.Value`, `link.Ref`, `link.Walk` | T1 | T2 | No — T2 is written against T1 |

## Consequences

- **Positive:** Hierarchies become free. A taxonomy is links, links are datoms,
  and datoms are bitemporal — so "what did this hierarchy look like in March" is
  a traversal at an instant rather than a feature.
- **Positive:** Retracting a link is retracting a datom, and erasing a subject
  removes its links from every answer by the same key destruction.
- **Positive:** The same-instant rule makes historical traversals trustworthy,
  which is the only reason to have them.
- **Negative:** A walk costs a read per hop per level, and the depth bound is the
  only thing standing between a caller and a large one.
- **Negative:** Typed values change what a datom's value IS, which is a format
  change — cheap now, expensive once data exists. That is why it is being decided
  now rather than when someone asks for graphs.
- **Neutral:** Nothing writes a link through the language yet. The model and the
  walk are decidable and the syntax is T2.

## Out of Scope

- Writing a link in the query language, and a traversal statement (deferred: `docs/adr/BACKLOG.md` §29)
- Querying by inbound edges — "what points AT this" (deferred: `docs/adr/BACKLOG.md` §29)
- Storing links durably (deferred: `docs/adr/BACKLOG.md` §12)
- Enforcing that a reference target exists at write time (permanent: boundary: an entity may be referred to before it is asserted, and requiring otherwise would make write order significant across a boundary the store does not coordinate)
- Telling an erased target from a missing one (permanent: boundary: they must be indistinguishable, or a traversal becomes the existence oracle crypto-shredding exists to remove)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Each hop is resolved at its own instant | High — every hop is a read, and a read defaults to now | Critical — historical traversals silently return trees that never existed | The falsifier walks a graph whose shape CHANGED between two instants and asserts the answer matches one of them exactly, never a mixture |
| References are inferred from value shape | Med | High — accidental edges appear and change as unrelated data does | The kind is a stored field; a test asserts a literal that looks like an entity name is not followed |
| A cycle truncates instead of reporting | Med | High — a partial answer that looks complete | `ErrCycle` names the entity the walk returned to |
| A dangling reference is raised as an error | Med | Critical — rebuilds the existence oracle for erased subjects | Rule 6, and a test asserts a retracted target, an absent target and an erased target are byte-identical answers |
| Links are put in a separate edge store for traversal speed | Med | High — loses bitemporality, retraction, erasure and the tenant boundary at once | Rule 2, and the alternatives section says what each one costs |

## Rollback

`link.Kind` widens what a datom's value carries, so data written with a reference
kind needs that kind to be readable — this is a format change and reverting it
after data exists is a migration. Nothing else here persists: the walk is a pure
function. Deciding it now, before there is data, is the whole reason it is being
decided now.

## Follow-ups

- [ ] When links reach the language (`BACKLOG.md` §29), confirm a traversal statement carries ONE time clause for the whole walk rather than one per hop — a per-hop qualifier would make rule 3 sayably violable, which is worse than it being merely implementable.
- [ ] When storage exists (`BACKLOG.md` §12), confirm a walk's reads all use one snapshot rather than each taking a fresh one; the rule is easy to hold in a pure function and easy to lose behind a cache.
- [ ] Measure what a depth bound costs on a real hierarchy before choosing a default for the language — rule 4 requires the bound and does not say what a sensible one is, and guessing without a shape to measure would be a constant nobody wrote down.
