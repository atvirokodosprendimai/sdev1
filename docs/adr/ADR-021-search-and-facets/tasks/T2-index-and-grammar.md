# Task ADR-021-T2: Build the index, and give search a grammar

**Depends-on:** T1
**Depends-on note:** also blocked on work outside this record — see Status.
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `search.Index`, `search.Builder`, `ql.Search`, `search.Rank`
**Consumes:** `search.Posting`, `search.Visible`, `search.Facet` (T1); `subscribe.Subscription` from ADR-010; a storage engine (`docs/adr/BACKLOG.md` §12); a query evaluator (§20)
**Data dependency:** needs a running store — an index is a projection of the datom log, and there is no log to project
**Proof map:** v1
**Rests-on:** `an index rebuilt from the log reproducing the original exactly`, `a search result confirmed against the datoms rather than trusted`

## Status

⚠ **`pending`, and blocked on three things that do not exist.** Recorded rather
than started, with the reasons written down.

- **A storage engine** (`BACKLOG.md` §12). An index is a read model over the
  datom log. There is no log on a disk to project from.
- **A query evaluator** (`BACKLOG.md` §20). Rule 3 says a search result is a set
  of CANDIDATES, confirmed against the datoms before it is returned. Without an
  evaluator there is nothing to confirm against, and a search that skipped the
  confirmation would be exactly the authoritative index the record refuses.
- **A ranking function** (`BACKLOG.md` §27), which cannot be chosen without a
  corpus to choose it against.

★ **This is not the same as ADR-021 being unfinished.** The decision — that a
posting is sealed with the subject's key, that an undecryptable posting is absent
rather than counted, that a facet is exact or refused — is settled and proved by
T1's mutants against the real keystore. What waits here is machinery.

## Goal

Turn the settled meaning into a working index: built by subscription, rebuildable
from the log, and reachable from the language.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/search/index.go` | add | `Index`, `Builder` — the read model and how it is fed. |
| `internal/core/search/rank.go` | add | `Rank` — ordering the candidates. |
| `internal/core/ql/search.go` | add | `Search` statement: `SEARCH … IN … FACET BY … LIMIT …` plus a time clause. |
| `internal/core/search/index_test.go` | add | The tests below. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestRebuildFromTheLogReproducesTheIndex`, `TestASearchResultIsConfirmedAgainstTheDatoms`. Run the Acceptance fence and confirm it is red. [proof: acceptance]
2. [S2] Implement `Builder` as a consumer of ADR-010's subscription, so the index is fed by the same primitive as streaming backup and the console rather than a third way to follow the log. [proof: mutation]
3. [S3] Make a full rebuild from the log reproduce the index exactly, and test it by building twice. ⚠Rule 4 is worth nothing unproven: "the index is rebuildable" is the sentence that makes losing it a performance event rather than a data-loss event. [proof: mutation]
4. [S4] Confirm every candidate against the datoms before returning it. [proof: human: a reader confirms the search path reads the datoms for each candidate and drops those that no longer match, since an index fed by subscription is always behind and nothing else can detect it]
5. [S5] Add the `SEARCH` statement to the grammar, carrying a required `LIMIT`, an optional `FACET BY`, and a time clause. [proof: human: a reader confirms the statement is refused without a limit and that the time clause is the same one every other statement carries, rather than a second spelling]
6. [S6] Choose a ranking function against a real corpus, and record what it was measured on. [proof: human: a reader confirms the choice names the corpus and the date it was measured, because a ranker chosen without one is a preference rather than a decision]
7. [S7] Count search bytes against ADR-015's READ budget. [proof: human: a reader confirms search traffic is admitted through the same budget as other reads, since the link does not care which bytes are background and search is the largest amplifier a single request has]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/search/... -race -run 'TestRebuildFromTheLogReproducesTheIndex|TestASearchResultIsConfirmedAgainstTheDatoms' -count=1 2>&1 | tee /tmp/adr021-t2.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr021-t2.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestRebuildFromTheLogReproducesTheIndex` | `internal/core/search/index_test.go` | Building the index twice from the same log yields the same index, so it is genuinely a projection and losing it costs nothing but time | — | S2, S3 |
| `TestASearchResultIsConfirmedAgainstTheDatoms` | `internal/core/search/index_test.go` | A candidate whose datoms no longer match is dropped before the result is returned, so a stale index cannot return a wrong answer | — | S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The two tests above. |
| 2 — something selects it | The `SEARCH` statement is the only route into the index, and `Builder` the only thing that writes it. |
| 3 — the caller can discover it | `SEARCH` is part of the published grammar, so it appears in the query-language guide like every other statement. |
| 4 — it is used | `pending` — blocked on the storage engine and the evaluator. |

## Mutation Log

## Invariants

- The index is rebuildable from the log, exactly.
- No result is returned without being confirmed against the datoms.
- A `SEARCH` without a limit does not parse.

## Risks

- ⚠ **Confirming against the datoms is the rule that decays quietest**, because skipping it makes every search faster and the damage only appears on data that changed. A stale-index test is the only thing that notices.
- ⚠ **A rebuild test that builds once and compares to itself proves nothing.** The test builds twice from the same log and compares the two, which is the only shape that can fail.
- Ranking chosen without a corpus is a preference. S6 requires the corpus and the date to be recorded with the choice.
- ⚠ **Search is the largest fan-out a single request can cause** — one query can touch every leaf holding the tenant's subtree. Excluding it from the read budget would let one caller saturate the cluster while user queries are shed.

## Stop Condition

Stop and ask before returning a search result that has not been confirmed against
the datoms, however much faster it is. That turns the index into the authority,
which is the one thing rule 3 forbids — and the failure is invisible except on
data that has changed since the index saw it.

## Out of Scope

- Stemming, stop words and language detection (deferred: `docs/adr/BACKLOG.md` §27)
- Which attributes are indexed, and who decides (deferred: `docs/adr/BACKLOG.md` §27)
- The storage engine and the evaluator (deferred: `docs/adr/BACKLOG.md` §12 and §20)

## Verification Log
