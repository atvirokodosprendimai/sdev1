# ADR-044 Tasks

Implementation tasks for ADR-044: the one-entity boundary survives a real
registry, because the act is an entity — and bitemporality pays for the rest. See
the parent ADR for the decision.

**Source of truth:** the task files' headers. This README is a derived index —
when it disagrees with a task file, the task file wins.

## Execution Order

One task.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Fire ADR-003's falsifier at real registry records | done | — | four boundary tests over real fixtures, then the command, eval and leafstore suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | none — this task adds a test and no exported surface | — | none |

## Notes

- ★ **`BACKLOG.md` §8 was the one item genuinely blocked on the user**, and it was
  unblocked by them supplying a corpus: 548,547 Lithuanian public-procurement legal
  entities, 178 MB, examined 2026-09-05.
- ★★ **The corpus armed ADR-003's falsifier in the registry's OWN vocabulary.**
  277 entities carry a `legalStatus` naming an act that spans several companies —
  `Dalyvaujantis reorganizavime` (74), `Dalyvaujantis atskyrime` (56),
  `Reorganizuojamas` (139), and a handful of cross-border mergers and splits.
  ⚠ *Dalyvaujantis* means **participating**. A status whose entire content is "I am
  one party to an act involving others" is proof, in the domain's own words, that
  multi-entity operations are real here.
- ★ **And the boundary holds, because the ACT IS AN ENTITY.** A reorganisation has
  a date, a kind and participants; registering it is one transaction on the act,
  with `->participant` references. No cross-entity write.
- ⚠ **It is only USABLE because of ADR-035's inbound read, and nobody planned
  that.** "Which companies are in reorganisation 7" is `READ ->name FROM [reorg-7]`.
  Before that record existed — earlier the same day, for an unrelated reason — the
  normalised model was storable and unqueryable. **ADR-003's liveability rests on
  ADR-035.** A dependency nobody designed is one nobody is maintaining.
- ⚠ **The registry's own shape is denormalised** — status on each participant —
  and reproducing it takes two transactions. ★ Bitemporality pays for the missing
  atomicity: both datoms carry the act's real-world date as `Valid.From`, so a
  reader on the VALID axis sees a consistent world however the writes interleaved.
- ★★ **The general rule worth keeping: bitemporality substitutes for cross-entity
  atomicity exactly when the operation is a statement about the WORLD — which has
  its own instant — rather than about the SYSTEM.** A reorganisation happened on a
  date. The write order is a fact about the database, not about Lithuania.
- ⚠ **And the class where it FAILS, so the limit is known rather than
  discovered:** an invariant that must hold at every TRANSACTION instant — a
  balance transfer, a double-entry ledger, a conserved sum. There no real-world
  instant makes the intermediate state acceptable and ADR-003 genuinely breaks.
  This registry has no such invariant; it records what happened.
- ⚠ **One domain is one domain.** §8 is answered NARROWLY and the record says so.
