# ADR-037 Tasks

Implementation tasks for ADR-037: a prefetched block is a guess and a demanded
block is evidence, so eviction takes guesses first. See the parent ADR for the
decision.

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
| T1 | A cache that never becomes a store, and evicts guesses before evidence | done | — | five cache tests, then the prefetch and erasure suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `prefetch.Cache` | a read path (`BACKLOG.md` §12/§18) | none within this record |

## Notes

- ⚠ **THE CONSTRAINT `BACKLOG.md` §24 NAMES FIRST governs everything else.** A
  read must still work with every prefetched block evicted. If it stops working,
  the prefetch has become load-bearing — and that failure appears only under
  memory pressure, which is exactly when eviction happens. The defect and its
  trigger are the same event, so it is never seen in testing.
- ★ **Half the eviction question did not need a workload after all.** §24 is right
  that choosing among ARC, 2Q and CLOCK-Pro needs traffic to choose against. It is
  not right about the ordering underneath them: a prefetched block is a GUESS and a
  demanded block is EVIDENCE, and evicting evidence to keep guesses is wrong on
  every workload. That part is decidable now.
- ★ **A read PROMOTES a guess to evidence.** Without it, a perfectly prefetched
  sequential read keeps evicting the blocks it is about to use — prefetching would
  make things worse, and it would do so most on the workload it was built for.
- ★ **Deciding the blast radius lets the guessing stay deferred.** §24 says
  sequentiality detection is a heuristic and will sometimes be wrong. The useful
  question is what being wrong COSTS: bandwidth already counted against the read
  budget (ADR-018 rule 7), plus zero demanded evictions. Bounded, so the deferred
  detection is a deferred optimisation rather than a deferred risk.
- ⚠ **The bound is BYTES.** Blocks vary in size, so an entry count bounds nothing
  that matters. Same discipline as ADR-018 rule 5, and the same resource.
- ⚠ **An oversized block is REFUSED, not admitted by emptying the cache** — that
  trade gives up the whole working set for one block that may never be read again.
- ⚠ **Rule 3 is not ARC, and the record says so.** Two speculative entries are
  ordered only by recency, so a scan still evicts its own useful guesses. A real
  limit, and the one a workload would let us fix.
