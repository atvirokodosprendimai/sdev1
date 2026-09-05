# ADR-037: A prefetched block is a guess and a demanded block is evidence, so eviction takes guesses first

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-015-admission-control.md`, `docs/adr/ADR-018-read-ahead.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/prefetch/**`
**Enforced-by:** `internal/core/prefetch/cache_test.go::TestEvictingEverythingChangesNoAnswer`
**Invalidates:** none — it fills `BACKLOG.md` §24's "cache" and "eviction" bullets, which ADR-018 deferred
**Served-path change:** A fetched block can be held and found again, so a prefetch can actually pay off. Until now ADR-018 could plan a prefetch and nothing could keep its result.

## Context

ADR-018 decides WHICH fragments a read asks for. `BACKLOG.md` §24 lists what is
missing past that, and the three parts interact.

⚠ **The constraint §24 names first is the one that governs everything else:** a
read must still work with every prefetched block evicted. If it stops working, the
prefetch has become load-bearing — and the failure appears only under memory
pressure, which is precisely when eviction happens. The bug and its trigger arrive
together, so it is never seen in testing and always seen in production.

★ **The eviction question looked like it needed a workload, and half of it does
not.** §24 says least-recently-used is wrong for a sequential scan, which evicts
exactly what it is about to read, and that choosing a scan-resistant policy "needs
a workload to choose against". True of choosing among ARC, 2Q and CLOCK-Pro. Not
true of the ordering underneath them: **a prefetched block is a GUESS and a
demanded block is EVIDENCE.** Evicting evidence in order to keep guesses is wrong
on every workload, so it can be decided now.

★ **And that decides what a WRONG prefetch costs, which is the part of "when to
prefetch" worth deciding.** §24 says detection is a heuristic and will be wrong
sometimes — so the useful question is not how to guess better, it is how much a
bad guess is allowed to cost. Bounding the blast radius lets the guessing stay
deferred without the exposure being open-ended.

## Existing Primitives Audit

- `internal/core/prefetch` (ADR-018): supplies `Budget`, `Plan`, `Window` and the
  bytes-not-entries discipline. **Extended in the same package** — a cache with a
  different notion of "how much" from the plan that fills it would be two budgets
  for one resource.
- `internal/core/erasure` (ADR-006): supplies the stripe header a window is sized
  from. **Not touched** — the cache holds assembled BLOCKS, and how a block was
  reconstructed is not its business.
- ADR-015's shedding: **referenced, not modified.** ADR-018 rule 7 already counts
  prefetch bytes against the read budget, which is what stops a wrong guess
  competing with user queries for the link.
- An off-the-shelf LRU: **rejected below.**
- Sequentiality detection: **deferred**, with rule 6 bounding what its being
  wrong may cost.

## Decision

**The cache is bounded in bytes, every entry is either DEMANDED or SPECULATIVE,
eviction takes speculative first, and a speculative entry that is read becomes
demanded.**

1. ⚠ **A read must work with every entry evicted, and that is the record's
   falsifier.** The cache is a cache and never a store: nothing may be reachable
   only through it. ★ Stated first because every other rule here is an
   optimisation, and an optimisation that becomes load-bearing fails under memory
   pressure — which is when eviction happens, so the defect and its trigger are
   the same event.

2. **Every entry records HOW IT ARRIVED: demanded, or speculative.** A demanded
   block was asked for by a read that needed it. A speculative one was pulled by a
   prefetch that guessed it would be.

3. ★ **Eviction takes SPECULATIVE entries before DEMANDED ones, least-recently-
   used within each class.** A guess is evicted before evidence. ⚠ This is what
   makes a sequential scan survivable: a scan fills the cache with speculative
   blocks, and plain LRU would let those evict the working set of every other
   reader on the node. Here a scan can only evict its own guesses until it runs
   out of them.

4. **A speculative entry that is READ becomes demanded.** ★ That is the promotion
   that makes a correct prefetch pay: the guess was right, so it stops being a
   guess. Without it, a perfectly prefetched sequential read would keep evicting
   the blocks it is about to use, and the prefetch would make things worse.

5. **The bound is BYTES, not entries.** ⚠ Blocks vary in size, so an entry count
   bounds nothing that matters — the same limit is generous for small blocks and
   an out-of-memory kill for large ones. This is ADR-018 rule 5's discipline, and
   it is the same resource, so it is measured the same way.

6. ⚠ **What a WRONG prefetch costs is bounded, and stated: bandwidth already
   counted against the read budget (ADR-018 rule 7), plus ZERO demanded
   evictions.** ★ It cannot slow another reader down by taking their working set,
   because rule 3 will not let it. This is why sequentiality detection can stay
   deferred: the blast radius of guessing badly is decided here even though the
   guessing is not.

7. **An entry larger than the whole cache is REFUSED, not admitted by evicting
   everything.** ⚠ Admitting it would empty the cache to hold one block that
   itself may never be read again, which is the worst outcome available: the
   working set is gone and nothing replaced it.

**What would falsify this.** A read that returns a different answer, or fails,
after the cache is emptied mid-read. That is the falsifier in `Enforced-by:`, and
it is the failure that only ever appears under memory pressure.

## Alternatives Considered

- **Plain LRU.** It is the obvious answer and every library ships one. Rejected
  under rule 3: §24 names the defect directly — a sequential scan evicts exactly
  what it is about to read, and worse, it evicts what OTHER readers were about to
  read. A scan is not a rare workload for a store like this.
- **A published scan-resistant policy — ARC, 2Q, CLOCK-Pro.** Better than what
  rule 3 does, on the workloads their papers measure. Rejected as premature and
  recorded as such: §24 is right that CHOOSING among them needs a workload, and
  there is none. Rule 3 is the part that is workload-independent, and it is a
  strict improvement on LRU that does not pretend to be the final answer.
- **Segregate by a fixed byte ratio — speculative may use at most X% of the
  cache.** It also bounds a scan's damage. Rejected: it needs a number nobody can
  justify, and it wastes capacity whenever the split is wrong in either direction.
  Rule 3 gets the same protection out of an ORDER, which needs no constant.
- **Let the prefetch write into the same class as a demanded read.** Simplest, one
  class, no promotion. Rejected under rules 3 and 6: the cache could then no longer
  tell a guess from evidence, which is the one distinction the whole policy rests
  on, and a wrong prefetch would cost other readers their working set.
- **Bound the cache by entry count.** Cheap and easy to reason about. Rejected
  under rule 5: it bounds the wrong thing, and it is the same mistake ADR-018 rule
  5 already refused for the prefetch budget.
- **Admit an oversized entry by evicting everything.** It honours the caller's
  request. Rejected under rule 7: it trades the whole working set for one block
  that may never be read again.
- **Decide sequentiality detection here too.** It would close §24 completely.
  Rejected: it is a heuristic and needs a workload, and inventing one now would fix
  a detection strategy against traffic nobody has seen. Rule 6 bounds what its
  absence — and later, its errors — may cost.

## Component / Boundary Impact

No new component. The cache lives in `internal/core/prefetch` beside the planner
that fills it, because they measure one resource and a second notion of "how much"
would eventually disagree with the first.

⚠ The boundary: the cache decides what to KEEP. It does not fetch, does not
reconstruct, and does not decide when to prefetch. It is handed blocks and told
how they arrived.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `prefetch.BlockID` | new — a block's identity | T1 | callers |
| `prefetch.Arrival` / `prefetch.Demanded` / `prefetch.Speculative` | new — how an entry arrived | T1 | callers |
| `prefetch.Cache` / `prefetch.NewCache` | new — the bounded cache | T1 | a read path |
| `prefetch.Cache.Put` / `Get` / `Evict` / `EvictAll` / `Bytes` / `Len` | new | T1 | a read path |
| `prefetch.ErrBlockTooLarge` | new sentinel — rule 7 | T1 | callers |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `prefetch.Cache` (T1) | T1 | a read path (`BACKLOG.md` §12/§18) | No |

## Consequences

- **Positive:** A prefetch can finally pay off — ADR-018 could plan one and
  nothing could keep the result.
- **Positive:** A sequential scan can no longer evict another reader's working
  set, and that holds without anyone choosing a cache-replacement policy.
- **Positive:** Rule 6 bounds what a bad prefetch costs, so the deferred detection
  heuristic is a deferred OPTIMISATION rather than a deferred risk.
- **Negative:** ⚠ Rule 3 is not ARC. Two speculative entries are ordered only by
  recency, so a scan still evicts its own useful guesses. That is a real limit and
  it is the one a workload would let us fix.
- **Negative:** The promotion in rule 4 means a scan that reads everything once
  ends with a cache full of demanded blocks it will never read again. ⚠ Recorded:
  it is the cost of not distinguishing "read once" from "read repeatedly", which is
  exactly what the deferred policies do and what needs a workload.
- **Neutral:** Nothing fetches yet, so nothing fills this cache on a served path.

## Out of Scope

- Deciding WHEN to prefetch, and detecting sequentiality (deferred: `docs/adr/BACKLOG.md` §24 — a heuristic that needs a workload; rule 6 bounds what being wrong costs)
- Choosing among ARC, 2Q and CLOCK-Pro (deferred: `docs/adr/BACKLOG.md` §24 — rule 3 is the workload-independent part, and the rest needs traffic to choose against)
- Distinguishing "read once" from "read repeatedly" (deferred: `docs/adr/BACKLOG.md` §24 — the same choice, and the reason rule 4's cost is recorded rather than fixed)
- The nearest-`k` versus load-spread tension (deferred: `docs/adr/BACKLOG.md` §22 — ADR-018 recorded it, and the answer depends on whether the cluster is nearer its bandwidth ceiling or its latency target)
- Actually fetching a fragment, or reconstructing a block (deferred: `docs/adr/BACKLOG.md` §12/§18 — there is no transport)
- Sharing one cache across tenants, and what that means for isolation (deferred: `docs/adr/BACKLOG.md` §22)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The cache becomes load-bearing | Med — it is what happens when a read path starts trusting a hit | **Critical** — the failure appears only under memory pressure, which is exactly when eviction happens, so the defect and its trigger are the same event | Rule 1, and it is the record's falsifier: empty the cache mid-read and the answer must not change |
| Plain LRU is used because a library was to hand | High — every language ships one | High — a scan evicts other readers' working sets | Rule 3, with a test that scans past the cache size and checks the demanded entries survived |
| Speculative entries are never promoted | Med — promotion is easy to leave out | High — a correct prefetch would keep evicting the blocks it is about to read, making prefetching worse than none | Rule 4, with a test that reads a speculative entry and then fills the cache |
| The bound is entries rather than bytes | Med — it is simpler | High — the same limit is generous for small blocks and fatal for large ones | Rule 5 |
| An oversized block empties the cache | Med — admitting it looks like honouring the caller | Med — the working set is gone and one block that may never be read again replaced it | Rule 7, with a named refusal |

## Rollback

Removing the cache removes a speed-up, not a capability — rule 1 is exactly the
property that makes that true, so it is worth re-reading before assuming it still
holds. ⚠ If rule 1 has been broken by then, rollback becomes a data-availability
change rather than a performance one, and that is the signal that this record was
violated rather than superseded.

## Follow-ups

- [ ] When there is real traffic (`BACKLOG.md` §24), measure whether rule 3 is enough before reaching for ARC — the ordering may already capture most of the benefit, and a policy nobody can explain is worse than one that is merely imperfect.
- [ ] When a read path exists (`BACKLOG.md` §12/§18), re-check rule 1 against it: the moment something reads a hit without a fallback, this record is broken and the test that proves it must run on that path rather than only on the cache.
- [ ] Revisit rule 4's cost once "read once" and "read repeatedly" can be told apart; a scan currently ends with a cache full of promoted blocks it will not use again.
