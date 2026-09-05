# ADR-044: The one-entity boundary survives a real registry, because the ACT is an entity — and bitemporality pays for the rest

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-002-transaction-identity.md`, `docs/adr/ADR-003-transaction-boundary.md`, `docs/adr/ADR-023-links-and-traversal.md`, `docs/adr/ADR-035-inbound-read.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/command/**`
**Enforced-by:** `internal/core/command/domain_test.go::TestARealMultiEntityActFitsTheBoundary`
**Invalidates:** none — ADR-003 states the boundary and names its own falsifier; `BACKLOG.md` §8 has said "nothing has tested it" since
**Served-path change:** None. This tests a decision that was already made, against a domain that already exists.

## Context

ADR-003 confines a transaction to one entity, and that single constraint removes
distributed commit from the whole system. `BACKLOG.md` §8 names the price of that
leverage: *"Its falsifier is correspondingly large: the decision fails if a
legitimate domain operation cannot be expressed within one entity. Nothing has
tested it."*

`command.ErrCrossEntity`'s own comment says the same, forward-looking: it exists
*"so the first genuine case of a domain needing a multi-entity transaction
surfaces as a specific refusal rather than as a confusing failure somewhere
downstream."*

**The corpus.** 548,547 Lithuanian public-procurement legal entities
(`juridiniai.jsonl`, 178 MB, examined 2026-09-05). Twelve attributes: five present
on every record — `id`, `name`, `registrationDate`, `legalForm`,
`registrationStatus` — and seven sparse, from `location` at 15% down to
`legalStatus` at 0.4%.

★ **And the corpus arms the falsifier in the registry's own vocabulary.** 277
entities carry a `legalStatus` naming a legal act that spans SEVERAL companies:

| Status | Meaning | Count |
|---|---|---|
| `Reorganizuojamas` | being reorganised | 139 |
| `Dalyvaujantis reorganizavime` | **participating in** a reorganisation | 74 |
| `Dalyvaujantis atskyrime` | **participating in** a separation | 56 |
| `Jungiama peržengiant 1 valstyb` | merged across a state border | 3 |
| `Skaidoma peržengiant 1 valst` / `JA jung peržengiant 1 valst` / `Inic. EB jungimosi būdu` / `Dalyvaujanti 1 valst jungimesi` | cross-border split, merger, EC merger, participation | 1 each |

⚠ **"Dalyvaujantis" means "participating".** A status whose whole content is *"I
am one party to an act involving others"* is proof, in the domain's own words,
that multi-entity operations are real here — and that the registry needs to say so
about each participant separately. That is exactly the shape §8 warned about.

## Existing Primitives Audit

- `internal/core/command` (ADR-003): supplies `Transaction` and `ErrCrossEntity`.
  **Tested, not changed** — this record adds no mechanism.
- `internal/core/link` / ADR-023: supplies the typed reference. **Reused as the
  modelling primitive** — an act names its participants with references.
- `internal/core/eval` / ADR-035: supplies the inbound read. ★ **Reused, and it
  turns out to be load-bearing for ADR-003** — see rule 2. It was built earlier
  today for an unrelated reason.
- `internal/core/temporal` (ADR-002): supplies the valid axis. **Reused as the
  substitute for cross-entity atomicity** — see rule 3.
- The corpus itself: **cited, never committed.** `*.jsonl` is ignored; the test
  carries a handful of real records copied in as fixtures, so it runs without the
  178 MB file.

## Decision

**The boundary holds for this domain. It holds because a multi-entity ACT is
itself an entity, it is queryable only because of ADR-035, and where the registry
denormalises, bitemporality is what pays for the missing atomicity.**

1. ★ **A multi-entity legal act is an ENTITY, and registering it is ONE
   transaction.** A reorganisation has a date, a kind and participants. Model it
   as an entity carrying `->participant` references and the act commits within the
   boundary — no cross-entity write, no distributed commit.

2. ⚠ **That model is only USABLE because of ADR-035's inbound read, and nobody
   planned that.** With the act as the entity, "which companies are in
   reorganisation 7" is `READ ->name FROM [reorg-7]`. ★ Before that record existed
   this shape was storable and unqueryable — you could write the normalised form
   and then only get at it by already knowing every participant's identifier.
   **So ADR-003's liveability rests on ADR-035**, which was built for a different
   reason on the same day. Recorded because a dependency nobody designed is one
   nobody is maintaining.

3. ⚠ **The registry's OWN shape is denormalised — `legalStatus` sits on each
   participant — and reproducing it takes two transactions.** They are not atomic
   on the transaction axis. ★ Bitemporality is what makes that acceptable: both
   datoms carry the act's real-world date as `Valid.From`, so a reader asking
   about the VALID axis sees a consistent world whether or not the writes
   interleaved. The tearing exists only on the transaction axis, which is the
   audit axis.

4. ★★ **The general rule, and it is the finding worth keeping: bitemporality
   substitutes for cross-entity atomicity exactly when the operation is a
   statement about the WORLD — which has its own instant — rather than about the
   SYSTEM.** A company reorganisation happened on a date. Both facts about it are
   true from that date. Their write order is a fact about the database, not about
   Lithuania.

5. ⚠ **And the class where the substitution FAILS, stated so the boundary's limit
   is known rather than discovered: an invariant that must hold at every
   TRANSACTION instant.** A balance transfer, a double-entry ledger, "the sum
   across these two entities is constant" — there the atomicity is a property of
   the system, no real-world instant makes the intermediate state acceptable, and
   ADR-003 would genuinely fail. **This registry has no such invariant**, and
   neither does procurement generally: it records what happened, it does not
   maintain a conserved quantity.

6. **So §8 is answered for this domain, and answered NARROWLY.** ★ One domain
   tested, not "domains". The record states which one, what it contains, and the
   one property a future domain must be checked for — rule 5 — rather than
   claiming the boundary is universally liveable.

7. ⚠ **A SECOND FINDING THE CORPUS PRODUCED, unrelated to the boundary: every
   registry identifier is ALL-NUMERIC**, and a bare numeric cannot be an entity
   name because it lexes as a number. ★ The language already has the escape —
   ADR-021 added backticks so a keyword could still be an attribute — and it
   covers this: `` READ legalStatus FROM `111756039` ``. Recorded because it was
   found by running the language at real data rather than by reading it, a domain
   whose primary keys are integers is not unusual, and the guide currently
   presents quoting as being about KEYWORDS rather than about identifiers in
   general.

**What would falsify this.** A legitimate operation from this corpus that cannot
be expressed within one entity. The test models a real reorganisation from real
records and commits it as one transaction; that is the falsifier in
`Enforced-by:`.

## Alternatives Considered

- **Declare §8 answered on the argument alone.** The reasoning above is
  self-contained. Rejected: §8's complaint is precisely that nothing had been
  *tested*, and a decision whose falsifier has never been fired at real data is
  the thing this corpus refuses everywhere else.
- **Model each participant's status as the atomic unit and accept two
  transactions.** It matches the registry's own denormalised shape exactly.
  Rejected as the PRIMARY model under rule 1 — it needs the very cross-entity
  atomicity in question — but kept as rule 3's secondary shape, because the source
  data really is denormalised and bitemporality genuinely covers it.
- **Relax ADR-003 to allow a transaction over a bounded set of entities.** It
  would model the act directly, on the participants. Rejected: it is the one
  constraint that removes distributed commit from the entire system, and this
  domain does not need it relaxed. ⚠ Rule 5 says what WOULD justify revisiting,
  which is a stronger position than "not yet".
- **Commit the corpus so the test reads it directly.** The test would then be
  against all 548,547 records. Rejected: 178 MB of somebody else's crawl is not
  this project's source, and a test that needs it cannot run anywhere. Real
  records are copied in as fixtures and the corpus is cited by shape, size and
  date.
- **Test against synthetic data shaped like a registry.** Reproducible and small.
  Rejected: it would have invented the multi-entity case, and the whole value here
  is that the domain produced one nobody was looking for — `Dalyvaujantis` is a
  word the registry needed and this project did not predict.

## Component / Boundary Impact

No component changes. `internal/core/command` gains a test; nothing gains an
export.

⚠ The boundary this confirms is ADR-003's, and it is confirmed for one domain.
Rule 5 is what makes that a claim rather than an extrapolation.

## Wiring & Contract Changes

None — implementation-internal only. This record adds a test and changes no
contract, no export and no behaviour.

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| None — one task, and it produces no surface | T1 | — | No |

## Consequences

- **Positive:** §8's largest untested claim has been fired at a real domain, and
  it held. The corpus is named, sized and dated so the test can be repeated.
- **Positive:** Rule 5 turns "we have not found a counterexample" into "here is
  the property a counterexample would have", which is checkable against the next
  domain rather than re-argued.
- **Positive:** The EAVT model is vindicated incidentally: seven of twelve
  attributes are sparse, from 15% down to 0.4%, so a relational schema here is
  mostly nulls.
- **Negative:** ⚠ **One domain is one domain.** Registry and procurement data
  record what happened; they maintain no conserved quantity. A domain that does —
  rule 5's class — is untested and would be the real trial.
- **Negative:** ⚠ Rule 2's dependency is real and was unplanned: ADR-003's
  liveability rests on ADR-035's inbound read. Removing or weakening that read
  makes the normalised model unqueryable and would reopen this.
- **Neutral:** No code changed. The decision was already right; it is now
  evidenced.

## Out of Scope

- Testing a domain with a cross-entity INVARIANT (deferred: `docs/adr/BACKLOG.md` §8 — rule 5 names the property; ledgers and balance transfers are the class, and none is to hand)
- Modelling the procurement CONTRACTS themselves — buyer, supplier, award (deferred: `docs/adr/BACKLOG.md` §8 — this corpus holds legal entities only, and the contract corpus is a separate crawl)
- Ingesting the corpus at scale, or measuring what 548,547 entities cost (deferred: `docs/adr/BACKLOG.md` §12 — a storage-engine question, not a boundary one)
- Committing the corpus (permanent: boundary: 178 MB of somebody else's crawl is not this project's source; a record cites its shape, size and date, never its bytes)
- Changing ADR-003 (permanent: boundary: this record tests that decision and does not amend it)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| "One domain held" is read as "the boundary is liveable" | **High — it is the conclusion everyone wants** | High — the next domain is adopted without the check, and rule 5's class is exactly where it breaks | Rule 6, and rule 5 states the property to test rather than the verdict |
| ADR-035's inbound read is weakened later | Med — it looks like a query convenience | High — the normalised model becomes unqueryable and ADR-003's liveability goes with it | Rule 2, recorded so the dependency is visible from both ends |
| The denormalised shape is used without the shared valid-from | Med — it is two ordinary writes | High — the two facts then disagree on the valid axis too, and bitemporality stops covering the missing atomicity | Rule 3, with a test asserting the shared `Valid.From` |
| The corpus is committed | Med — it is sitting in the working tree | Med — 178 MB of a third-party crawl in the history, permanently | `*.jsonl` is ignored, and the test uses copied fixtures |
| The fixtures drift from the corpus | Low | Low — the record's counts become stale | Counts are dated in Context, and the queries that produced them are simple enough to re-run |

## Rollback

Nothing to roll back: no code changed and no contract moved. ⚠ What could be lost
is the finding — rule 4's substitution rule and rule 5's limit are the durable
output, and they live only in this record.

## Follow-ups

- [ ] When a contract corpus is available (buyer, supplier, award), model an award and check rule 1 against a relationship whose participants are on DIFFERENT sides rather than peers — a reorganisation's participants are symmetric and an award's are not.
- [ ] If a domain with a cross-entity invariant is ever adopted (rule 5's class), test it BEFORE building on ADR-003, not after. That is the case where the boundary genuinely fails, and it is cheap to detect and expensive to discover.
- [ ] Re-run the corpus census if `juridiniai.jsonl` is refreshed; the 277 multi-entity statuses are dated 2026-09-05 and are a snapshot of an active registry.
