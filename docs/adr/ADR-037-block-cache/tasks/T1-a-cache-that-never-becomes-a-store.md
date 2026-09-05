# Task ADR-037-T1: A cache that never becomes a store, and evicts guesses before evidence

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `prefetch.BlockID`, `prefetch.Arrival`, `prefetch.Demanded`, `prefetch.Speculative`, `prefetch.Cache`, `prefetch.NewCache`, `prefetch.ErrBlockTooLarge`
**Consumes:** `prefetch.Budget` from ADR-018 (the bytes-not-entries discipline)
**Data dependency:** hermetic — the cache is in-memory and every test constructs its own
**Proof map:** v1
**Rests-on:** `a read answering the same thing with the cache emptied`, `eviction taking speculative entries before demanded ones`, `a speculative entry that is read becoming demanded`, `the bound being bytes rather than entries`

## Goal

Give a prefetch somewhere to put its result, and make the two ways it could go
wrong — becoming load-bearing, and letting a scan evict everyone's working set —
structurally impossible rather than merely avoided.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/prefetch/cache.go` | add | `Cache`, `BlockID`, `Arrival`, and the eviction order. |
| `internal/core/prefetch/cache_test.go` | add | The tests below. |
| `internal/core/prefetch/doc.go` | modify | Why a guess is evicted before evidence, and why the cache may never be load-bearing. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestEvictingEverythingChangesNoAnswer`, `TestAScanCannotEvictAnotherReadersWorkingSet`, `TestAReadPromotesAGuessToEvidence`, `TestTheBoundIsBytesNotEntries`, `TestABlockLargerThanTheCacheIsRefused`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Implement `Cache` bounded in BYTES, admitting an entry only when it fits after eviction. ⚠Not entries: blocks vary in size, and the same count is generous for small ones and an out-of-memory kill for large ones. [proof: mutation]
3. [S3] Record each entry's `Arrival` — `Demanded` or `Speculative` — set by the caller at `Put`, because only the caller knows whether a read needed the block or a prefetch guessed it. [proof: mutation]
4. [S4] Evict SPECULATIVE entries before DEMANDED ones, least-recently-used within each class. ★A guess before evidence, and it is right on every workload — which is why it can be decided without one, unlike the choice between ARC and 2Q. [proof: mutation]
5. [S5] Promote a speculative entry to demanded when it is READ. ⚠Without this a perfectly prefetched sequential read keeps evicting the blocks it is about to use, and prefetching makes things worse rather than better. [proof: mutation]
6. [S6] Refuse an entry larger than the whole cache with `ErrBlockTooLarge`, rather than emptying the cache to admit it. ⚠Admitting it trades the entire working set for one block that may never be read again. [proof: mutation]
7. [S7] Provide `EvictAll`, and use it to prove rule 1: the same sequence of reads answers identically with the cache emptied between every one. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/prefetch/... -race -run 'TestEvictingEverythingChangesNoAnswer|TestAScanCannotEvictAnotherReadersWorkingSet|TestAReadPromotesAGuessToEvidence|TestTheBoundIsBytesNotEntries|TestABlockLargerThanTheCacheIsRefused' -count=1 2>&1 | tee /tmp/adr037-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr037-t1a.out \
  && go test ./internal/core/prefetch/... ./internal/core/erasure/... -race -count=1 2>&1 | tee /tmp/adr037-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr037-t1b.out
```

The second command carries `erasure` because the cache's sizing vocabulary comes
from the stripe header ADR-018 plans against, and a change there that altered what
a block costs would silently change what this cache holds.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestEvictingEverythingChangesNoAnswer` | `internal/core/prefetch/cache_test.go` | **The falsifier ADR-037 names in `Enforced-by:`.** A sequence of reads served through a backing source plus the cache returns exactly what the same sequence returns with `EvictAll` called before every single read. ★ It is the cache being emptied MID-sequence that matters, not before it — a cache emptied first was never exercised | — | S7 |
| `TestAScanCannotEvictAnotherReadersWorkingSet` | `internal/core/prefetch/cache_test.go` | A reader's demanded blocks fill part of the cache; a scan then puts far more speculative blocks than fit. Every demanded block survives, and the scan's own earliest guesses are what went. ⚠ Under plain LRU the demanded blocks would be gone, so this is the test that separates rule 3 from the obvious implementation | — | S4 |
| `TestAReadPromotesAGuessToEvidence` | `internal/core/prefetch/cache_test.go` | A speculative entry that is `Get`-ed survives a subsequent flood of speculative puts, while an un-read speculative entry put at the same moment does not. ★ Two entries of identical age and class differing only in having been read — otherwise the test would pass on recency alone | — | S5 |
| `TestTheBoundIsBytesNotEntries` | `internal/core/prefetch/cache_test.go` | A cache holding one large block admits fewer entries than the same cache holding small ones, and `Bytes` never exceeds the bound. ⚠ An entry-count bound passes any test that uses uniform sizes, so the sizes here differ by an order of magnitude | — | S2 |
| `TestABlockLargerThanTheCacheIsRefused` | `internal/core/prefetch/cache_test.go` | Putting an oversized block returns `ErrBlockTooLarge` AND leaves the existing entries intact — the second half is the point, since a refusal that still emptied the cache would have done the damage anyway | — | S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests. |
| 2 — something selects it | `NewCache` is the only constructor and `Put`/`Get` the only ways in and out. |
| 3 — the caller can discover it | `Arrival` is a required argument to `Put`, so a caller cannot store a block without saying how it arrived. |
| 4 — it is used | ⚠ **Nothing fetches yet**, so nothing fills this on a served path — there is no transport (`BACKLOG.md` §12/§18). ADR-018 plans the fetch and this holds its result; the two meet when a read path exists. Recorded rather than implied. |

## Mutation Log

- 2026-09-05 · e3240c0* · mutant killed · exit 1 · `internal/core/prefetch/cache.go` · makes eviction plain least-recently-used, ignoring how an entry arrived — so a sequential scan evicts the working set of every other reader on the node, which is the exact defect BACKLOG §24 names and the implementation anyone reaching for an off-the-shelf LRU would get · acceptance-sha256:d9c72799572094514ea783b4e8d73f913d4bbbd9812a58b8215aa158dbe1b2d1 · covers:eviction taking speculative entries before demanded ones
- 2026-09-05 · e3240c0* · mutant killed · exit 1 · `internal/core/prefetch/cache.go` · drops the promotion, so a speculative block that was READ stays a guess — a perfectly prefetched sequential read then keeps evicting the very blocks it is about to use, and prefetching makes things worse exactly on the workload it exists for · acceptance-sha256:d9c72799572094514ea783b4e8d73f913d4bbbd9812a58b8215aa158dbe1b2d1 · covers:a speculative entry that is read becoming demanded
- 2026-09-05 · e3240c0* · mutant killed · exit 1 · `internal/core/prefetch/cache.go` · bounds the cache by an entry count instead of by bytes, so the same limit is generous for small blocks and an out-of-memory kill for large ones — the mistake ADR-018 rule 5 already refused for the prefetch budget, arriving in the cache that spends it · acceptance-sha256:d9c72799572094514ea783b4e8d73f913d4bbbd9812a58b8215aa158dbe1b2d1 · covers:the bound being bytes rather than entries
- 2026-09-05 · e3240c0* · mutant killed · exit 1 · `internal/core/prefetch/cache.go` · reports a HIT for a block the cache does not hold, handing back nothing while claiming it is the block — a caller that trusts the hit then reads an empty block instead of falling back to the source, which is the cache becoming load-bearing in the worst way: silently, and with a wrong answer rather than a failure · acceptance-sha256:d9c72799572094514ea783b4e8d73f913d4bbbd9812a58b8215aa158dbe1b2d1 · covers:a read answering the same thing with the cache emptied

## Invariants

- Nothing is reachable only through the cache.
- A speculative entry is evicted before any demanded one.
- Reading a speculative entry makes it demanded.
- The bound is bytes, and `Bytes()` never exceeds it.

## Risks

- ⚠ **Rule 1's test must empty the cache DURING the sequence, not before it.** A test that clears the cache and then reads exercises an empty cache, which proves only that the fallback exists — not that a partially warm cache and a cold one agree.
- ⚠ **AND COMPARING THE TWO RUNS AGAINST EACH OTHER IS NOT ENOUGH — the first version of this test did exactly that and was weaker than it looked.** Designing the mutants exposed it: a cache that LIES IDENTICALLY in both runs passes a warm-versus-cold comparison, because both runs agree on the same wrong answer. Two mutants would have survived — a `Get` reporting a hit it does not hold, and an `EvictAll` that evicts nothing, which makes the "cold" run a second warm one and the whole comparison vacuous. ★ Fixed before running them, by asserting every read against the SOURCE's bytes and asserting `Len() == 0` after each `EvictAll`. A comparison between two runs of the same code tests consistency, not correctness; at least one side must be pinned to something outside it.
- ⚠ **`TestAScanCannotEvictAnotherReadersWorkingSet` is the one that fails under plain LRU**, which is the implementation somebody will reach for. It must use more speculative puts than the cache can hold, or the eviction order is never exercised.
- ⚠ **Promotion is easy to test by accident.** A promoted entry is also the most recently used one, so a test that only checks it survived passes on recency. The test pairs it with an un-read speculative entry of the same age.
- ⚠ **A byte bound passes an entry-count test whenever the sizes are uniform.** The sizes must differ enough that the two bounds disagree.
- Nothing fills this cache on a served path, so the task adds a mechanism and not a behaviour. Recorded on the parent record as a consequence.

## Stop Condition

Stop and ask before letting anything read a cache hit without a fallback path.
That is rule 1, and breaking it converts every future eviction from a slower read
into a failed one — visible only under memory pressure, which is when eviction
happens.

## Out of Scope

- Deciding when to prefetch (deferred: `docs/adr/BACKLOG.md` §24)
- ARC, 2Q or CLOCK-Pro (deferred: `docs/adr/BACKLOG.md` §24)
- Fetching or reconstructing anything (deferred: `docs/adr/BACKLOG.md` §12/§18)
- Cross-tenant cache isolation (deferred: `docs/adr/BACKLOG.md` §22)

## Verification Log
- 2026-09-05 · e3240c0* · exit 0 · `set -o pipefail …` · acceptance-sha256:d9c72799572094514ea783b4e8d73f913d4bbbd9812a58b8215aa158dbe1b2d1 · ms:3741
- 2026-09-05 · e3240c0* · exit 0 · `set -o pipefail …` · acceptance-sha256:d9c72799572094514ea783b4e8d73f913d4bbbd9812a58b8215aa158dbe1b2d1 · ms:3716
- 2026-09-05 · e3240c0* · exit 0 · `set -o pipefail …` · acceptance-sha256:d9c72799572094514ea783b4e8d73f913d4bbbd9812a58b8215aa158dbe1b2d1 · ms:3769
- 2026-09-05 · e3240c0* · exit 0 · `set -o pipefail …` · acceptance-sha256:d9c72799572094514ea783b4e8d73f913d4bbbd9812a58b8215aa158dbe1b2d1 · ms:3746
- 2026-09-05 · e3240c0* · exit 0 · `set -o pipefail …` · acceptance-sha256:d9c72799572094514ea783b4e8d73f913d4bbbd9812a58b8215aa158dbe1b2d1 · ms:3690
