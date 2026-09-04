# ADR-028 Tasks

Implementation tasks for ADR-028: a tail seals on the first of a size bound or an
age bound, and the exposure it leaves is reported as a worst case. See the parent
ADR for the decision.

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
| T1 | State the bounds, and measure what is exposed | done | — | `go test ./internal/core/leafstore/... -race -run '…five tests…'`, then `datom`, then both packages whole |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `leafstore.Policy`, `leafstore.Exposure` | a caller that seals on a schedule (`BACKLOG.md` §15) | none within this record |

## Notes

- ★ **Two backlog entries are the same question from opposite sides.** §15 asks
  when the tail is sealed; §23 asks how long acknowledged data may stay unflushed.
  Data is exposed from acknowledgement until it is sealed, so **the sealing
  trigger IS the flush bound** — deciding them separately produces two constants
  somebody has to keep consistent by hand.
- ⚠ **THE TRAP, and §23 named it before there was anything to trap: reporting the
  window as an AVERAGE.** An average is smallest when the tail is fullest, because
  a burst of recent writes drags the mean down at exactly the moment the worst
  case is worst. So the number looks best when the risk is highest. The oldest
  unsealed datom is the number an operator wants during a power event.
- ⚠ Taking the NEWEST datom's age is worse and sounds more natural — it is what
  "how long since we wrote" means in conversation. It approaches zero as writes
  continue, so a leaf holding one acknowledged fact for an hour reports
  near-perfect safety as long as anything else is moving.
- **The policy is a PAIR and the first bound wins.** Size alone leaves a quiet
  tenant's write in memory indefinitely — and it is the tenant generating no load
  whose exposure nobody notices. Age alone makes segment sizes track the clock
  rather than the data.
- ⚠ **A zero bound is disabled and a policy with neither is REFUSED.** "Never
  seal" is legitimate and must be said out loud; a zero value that silently means
  never is what you get by configuring nothing, and then nothing is durable while
  everything reports success.
- **The age bound wins over any minimum segment size.** They conflict on a quiet
  leaf, and durability beats layout: a small segment costs space, an unbounded
  exposure costs data.
- ★ **A correction to §15:** it framed sealing as two publications and worried
  about a reader holding a snapshot from before the swap. Sealing MOVES data and
  drops none — ADR-026 merges by transaction over segments and tail alike, so the
  merged set is unchanged either side of a seal. The worry is real and belongs to
  COMPACTION, which does drop things.
- ⚠ **Nothing consults the policy yet.** No scheduler exists and this record does
  not add one: who asks, and how often, is a deployment decision. Stated here
  rather than left for someone to discover.
