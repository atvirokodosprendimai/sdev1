# ADR-022: The language asserts and retracts; valid time is the caller's and transaction time is never

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-002-transaction-identity.md`, `docs/adr/ADR-003-transaction-boundary.md`, `docs/adr/ADR-007-crypto-shredding.md`, `docs/adr/ADR-010-subscribe-and-purge.md`, `docs/adr/ADR-011-query-language.md`, `docs/adr/ADR-013-agent-tool-surface.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/ql/write.go`, `internal/core/ql/write_test.go`, `cmd/sdev1-ql/**`, `internal/core/session/**`
**Enforced-by:** `internal/core/ql/write_test.go::TestAWriteCannotSetTransactionTime`
**Invalidates:** none — it fills ADR-011's stated gap. That record deferred write statements because nothing evaluated them; the STATEMENTS are decidable regardless, and leaving the language read-only meant the only way to put a fact in was a Go call
**Served-path change:** A caller can state a fact, and state when it was true, in the same language they read it with — instead of the language being read-only and writes being reachable only from Go.

## Context

The store has had a write path since ADR-003: `command.Transaction` asserts and
retracts, `ports.Datom` carries an `Assert` flag, and validity is an interval.
The language has had none of it. You can ask this system anything and tell it
nothing.

That gap is not neutral. It made the query language look like the contract while
the real contract was a Go API nobody documented, and it left ADR-013's agent
surface read-only by accident rather than by decision — the record says
"read-only is a consequence, the language has no write statement", which is true
and was never meant to be permanent.

There is one decision here that cannot be changed later, and it is not which
keywords to use.

⚠ **BITEMPORALITY MEANS A CALLER SETS VALID TIME. IT MUST NEVER MEAN THEY SET
TRANSACTION TIME.** Valid time is a claim about the world — "this was true from
March" — and backdating it is the ordinary, correct use of the axis. Transaction
time is the record of *when this system was told*, and it is the only thing that
makes the store auditable. A caller who can set it can claim to have known
something earlier than they did, and **no query can detect it**, because every
query's evidence is the value that was forged.

★ If transaction time is ever settable, every historical answer this system gives
becomes a claim rather than a record — retroactively, for data already stored.

## Existing Primitives Audit

- `internal/core/command` (ADR-003): supplies `Transaction`, `Assert`, `Retract`
  and the one-entity boundary. **Reused whole** — this record adds a way to *say*
  what that package already does, and adds no second write path.
- `internal/core/ports` (ADR-003): supplies `Datom` with its `Assert` flag.
  **Relied on**: a retraction is a datom with the flag cleared, never an absent
  one, and the statements here mirror that rather than inventing a third state.
- `internal/core/temporal` (ADR-002): supplies `Interval` and `Forever`.
  **Reused** so a validity interval means the same thing written as read.
- `internal/core/ql` (ADR-011): supplies the parser, `Statement` and the lexer.
  **Extended** — a write is a statement like any other, so `Parse` stays the one
  entry point.
- `internal/core/tx` (ADR-002): supplies `TxID`. **Deliberately NOT reachable
  from a write statement.** That absence is rule 3 and it is the whole record.

## Decision

**`ASSERT` and `RETRACT` are the only write verbs; a write may state when a fact
was true and may never state when it was recorded.**

1. **There is no `UPDATE` and no `DELETE`, and there never will be.** An update is
   a new assertion at a later transaction; a deletion is a retraction; an erasure
   is the destruction of a key (ADR-007). ⚠ The harm of a CRUD verb is not at the
   API — it is that it describes a data model this store does not have, so
   everything a caller then infers about history, retraction and erasure is
   wrong, and nothing reports it.

2. **A write names exactly one entity, refused AT PARSE TIME.** ADR-003 made the
   entity the transaction boundary. ⚠ Enforcing it only at commit means a caller
   writes a statement, sees it parse, and learns at the end that the shape was
   never allowed — the grammar simply does not admit a second entity.

3. **`VALID FROM … TO …` sets valid time. There is no syntax for transaction
   time, and it is absent rather than ignored.** ⚠ Not "accepted and overridden":
   a `TRANSACTION` clause that parsed on a write would be written by somebody, who
   would believe it worked. The system assigns transaction time, always.

4. **An omitted `VALID` clause means "from this transaction's own instant, until
   further notice".** ★That is a default derived from the write itself rather than
   a constant nobody wrote down — and the alternative, requiring an explicit
   timestamp on every ordinary write, would force callers to invent one from their
   own clock, which is exactly the skew ADR-002's hybrid clock exists to survive.

5. **`VALID FROM t` with no `TO` is an open interval.** "Until further notice",
   not "until now" — a fact with no stated end has not ended.

6. **A retraction's interval says when the fact STOPPED holding**, and defaults
   like an assertion's. ⚠ Retracting a fact *as if it had never been true*
   requires stating `VALID FROM` explicitly, so an omitted clause can never
   rewrite history by accident.

7. **A write is a statement, so `Parse` remains the single entry point** and the
   agent surface's rule — every tool compiles to a statement — keeps holding when
   a write tool is added.

**What would falsify this.** A write statement that accepts a transaction
qualifier. That is the falsifier in `Enforced-by:`, it is checkable today with no
storage engine, and it is the mistake a reasonable implementer makes — the read
statements all take `TRANSACTION u`, so making writes symmetrical looks like
consistency.

## Alternatives Considered

- **`INSERT` / `UPDATE` / `DELETE`, because everybody knows them.** Genuinely
  lowers the learning cost, and an agent would guess them correctly on the first
  try. Rejected under rule 1: they would be guessed correctly and *understood*
  incorrectly. A caller who believes `UPDATE` overwrites will reason wrongly about
  every historical query, and about what erasure does.
- **Let a caller supply the transaction time, for backfill and migration.** The
  strongest case against rule 3: importing history from another system genuinely
  wants to say "this was recorded then". Rejected — a migration wants valid time
  backdated, which rule 3 already allows, and its *recording* time honestly is
  now. Allowing otherwise makes every audit answer unfalsifiable, permanently and
  for data already stored.
- **Accept `TRANSACTION` on a write and ignore it.** Kinder to a caller who
  writes it by symmetry. Rejected under rule 3: silently ignoring an instruction
  is worse than refusing it, because the caller believes it took effect.
- **Require `VALID` on every write.** Removes a default entirely. Rejected under
  rule 4: it forces every ordinary write to invent a timestamp from the caller's
  own clock, which is the skew problem moved to the worst possible place.
- **Let one statement write several entities, refusing at commit.** Fewer round
  trips. Rejected under rule 2: it makes a shape that can never succeed look
  acceptable right up to the end.
- **A separate write API rather than statements in the language.** Arguably
  cleaner separation. Rejected under rule 7: it gives the store two contracts, and
  the agent surface's guarantee — every tool compiles to a statement — would have
  to be re-decided for writes.

## Component / Boundary Impact

No new component. `internal/core/ql` gains two statements, which is where the
language lives; the write path they describe is ADR-003's and unchanged.

⚠ The boundary: this record decides what a write MEANS and what may be said. It
executes nothing. Turning a parsed write into datoms on a disk needs a storage
engine, and the statement's meaning is decidable and provable without one.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `ql.Write` | new — the statement: op, entity, attribute, value, validity | T1 | callers |
| `ql.WriteOp` | new — `OpAssert` and `OpRetract`, and no third | T1 | callers |
| `ql.ErrNoWriteEntity` / `ql.ErrTransactionTimeIsNotYours` | new sentinels | T1 | callers |
| `ql.Write.Interval` | new — the validity as written, or the open default | T1 | callers |
| `session.Session` | new — an in-memory store that applies writes and answers reads | T2 | `cmd/sdev1-ql` |
| `cmd/sdev1-ql` | new — run statements and see what they do | T2 | anyone checking what exists |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `ql.Write`, `ql.WriteOp`, `ql.Write.Interval` | T1 | T2 | No — T2 is written against T1 |

## Consequences

- **Positive:** The language becomes the whole contract. Nothing has to reach for
  a Go API to put a fact in.
- **Positive:** ADR-013's agent surface can gain a write tool without re-deciding
  anything: it compiles to a statement like every other tool.
- **Positive:** Backdating valid time — the reason to be bitemporal at all — is
  finally sayable.
- **Negative:** Callers arriving from a CRUD store must learn two verbs that do
  not map onto four. That cost is deliberate: the mapping would be a lie.
- **Negative:** Refusing to let a migration set transaction time makes importing
  another system's history lossy on that axis. It is the right loss — the imported
  record honestly *was* recorded here, now.
- **Neutral:** Nothing executes. The statements are decidable and their execution
  is not.

## The session, and what it is not

T2 builds `internal/core/session` and `cmd/sdev1-ql`: an **in-memory** session
that applies writes, answers `SELECT` and `SEARCH`, and prints what it did.

★ It exists because a system nobody can run is a system nobody can check. Every
capability in this corpus was decidable and tested, and none of it was
*demonstrable* — you could read twenty-two records and still not create a fact.

⚠ **It is NOT the storage engine, and must never be mistaken for one.** It holds
datoms in a map, loses them on exit, spans no leaf, and does not go near a disk.
What it IS: a complete, honest implementation of the meanings this corpus already
decided — visibility over the two axes, the erasure boundary, the facet refusal —
composed so a person can watch them work.

⚠ **And it must not become the specification by accident.** When the real engine
lands it has to agree with the records, not with this. The way that is kept true
is that the session builds only on the packages the records govern and adds no
rule of its own.

## Out of Scope

- Executing a write against durable storage (deferred: `docs/adr/BACKLOG.md` §28)
- Writing several attributes in one statement (deferred: `docs/adr/BACKLOG.md` §28)
- A write tool on the agent surface (deferred: `docs/adr/BACKLOG.md` §25)
- Typed values and references between entities (deferred: `docs/adr/ADR-023-links-and-traversal.md`)
- Who may write what (deferred: `docs/adr/BACKLOG.md` §11)
- Setting transaction time, ever (permanent: boundary: it is the record of when this system was told, and a caller who can forge it makes every historical answer a claim rather than a record — retroactively, for data already stored)
- `UPDATE` and `DELETE` (permanent: boundary: the store appends; an update is an assertion and a deletion is a retraction, so a verb implying in-place mutation would describe a data model this system does not have)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A `TRANSACTION` clause is added to writes for symmetry with reads | High — every read statement takes one, so writes look inconsistent without it | Critical — every audit answer becomes forgeable, retroactively | The falsifier asserts the clause does not parse on a write, and that the error names why rather than saying "unexpected token" |
| `UPDATE` or `DELETE` is added because callers ask for them | Med | High — the caller's model of history and erasure silently diverges from the store's | Rule 1, and the write verbs are a closed set with no third value |
| A missing `VALID` clause is treated as "since the beginning of time" | Med | High — a write would silently claim a fact was always true | Rule 4 defaults to the transaction's own instant, and the test asserts the default is not the zero instant |
| A retraction with no interval rewrites history | Med | High — the fact would read as never having been true | Rule 6: an omitted clause retracts from the transaction's instant, and "never true" must be stated |
| A write statement admits two entities and fails at commit | Low | Med — a shape that can never succeed looks acceptable until the end | Rule 2: the grammar takes one entity and there is nowhere to put a second |

## Rollback

No persistent state and no format — the statements parse and nothing executes
them yet, so a revert is a code revert. Once writes execute, the datoms they
produce are ordinary datoms and outlive the syntax that made them.

## Follow-ups

- [ ] When writes execute (`BACKLOG.md` §28), confirm the transaction time is assigned by the minter and is not reachable from anything a caller supplies — rule 3 is only as strong as the layer that honours it.
- [ ] When the agent surface gains a write tool (`BACKLOG.md` §25), confirm it is named for what the store does. ADR-013 already refuses `update` and `delete` at registration; this is the record that makes that refusal have something to point at.
- [ ] Decide whether one statement may write several attributes of one entity (`BACKLOG.md` §28). It stays within ADR-003's boundary and is purely a grammar question, deferred only to keep this record about time rather than about syntax.
