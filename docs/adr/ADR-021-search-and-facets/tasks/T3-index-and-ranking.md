# Task ADR-021-T3: An index you can actually search, and a ranking that is the same everywhere

**Depends-on:** T1, T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `search.Index`, `search.NewIndex`, `search.Index.Add`, `search.Index.Postings`, `search.Index.Terms`, `search.Index.Search`, `search.Query`, `search.Result`, `search.Scored`, `search.Rank`, `search.ErrNoQuery`
**Consumes:** `search.Posting`, `search.Sealed`, `search.Visible`, `search.Facet`, `search.Analyze` (T1); `ql.Search` (T2); `crypt.Keystore` from ADR-007
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `rarer terms outranking common ones`, `ranking that does not depend on map iteration order`, `a shredded subject absent from a ranked result and from its facets`, `a result honouring the query's limit`

## Goal

Turn the settled posting model into something that actually answers a query, in
memory, with no cluster — so search is usable and provable before it is durable.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/search/index.go` | add | `Index`, `Query`, `Result`, `Scored`, `Search`. |
| `internal/core/search/rank.go` | add | `Rank` — deterministic scoring over term and document frequency. |
| `internal/core/search/index_test.go` | add | The tests below. |
| `docs/QUERY-LANGUAGE.md` | modify | What a search returns, and that results are candidates. |

★ **This is deliberately IN MEMORY.** Persisting an index needs the storage
engine and confirming a candidate needs the evaluator, but neither is needed to
decide — or to prove — what a search MEANS. Splitting them is what lets the
erasure property and the determinism property be tested today.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestIndexBuiltTwiceAnswersIdentically`, `TestRankingDoesNotDependOnMapOrder`, `TestAShreddedSubjectIsAbsentFromRankedResults`, `TestAShreddedSubjectIsAbsentFromFacets`, `TestResultHonoursTheLimit`, `TestRarerTermsScoreHigher`, `TestAnEmptyQueryIsRefused`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Index` as term to sealed postings, and `Add` as the only way one enters. ★The dictionary holds TERMS, never subjects — the subject lives inside the sealed posting, which is what keeps erasure working.
3. [S3] Implement `Rank` over term frequency and inverse document frequency, ordering by score and breaking ties by subject so the order is TOTAL. ⚠A ranking that leaves ties unbroken is non-deterministic in practice, because the candidate order comes from a map. [proof: mutation]
4. [S4] Make every iteration over index state ordered before it can affect a result. ⚠Go randomises map iteration deliberately, and a ranking that reads a map in iteration order produces a different answer per process — the exact defect found in placement on 2026-09-04, where a per-process seed passed every in-process test. [proof: mutation]
5. [S5] Route results through `Visible`, so a shredded subject is absent from a ranked answer for the same reason it is absent from a raw one. [proof: mutation]
6. [S6] Compute facets over the VISIBLE subjects only, so an erased subject cannot be counted. ⚠A facet computed over candidates rather than over visible results would leak an erased subject as a number, which is the disclosure ADR-021 rule 2 forbids arriving through a different door. [proof: mutation]
7. [S7] Honour the query's limit, and refuse an empty query by name rather than matching everything.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/search/... -race -run 'TestIndexBuiltTwice|TestRankingDoesNotDependOnMapOrder|TestAShreddedSubjectIsAbsentFrom|TestResultHonoursTheLimit|TestRarerTermsScoreHigher|TestAnEmptyQueryIsRefused' -count=1 2>&1 | tee /tmp/adr021-t3a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr021-t3a.out \
  && go test ./internal/core/search/... ./internal/core/crypt/... ./internal/core/ql/... -race -count=1 2>&1 | tee /tmp/adr021-t3b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr021-t3b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestIndexBuiltTwiceAnswersIdentically` | `internal/core/search/index_test.go` | Two indexes built from the same postings in the same order return the same ranked answer, so the index is a projection rather than a thing with a history | — | S2, S3 |
| `TestRankingDoesNotDependOnMapOrder` | `internal/core/search/index_test.go` | The same query over the same index returns the identical order on many repeated runs, and postings added in a different order still rank the same — the check that catches a ranking reading a Go map in iteration order | — | S3, S4 |
| `TestAShreddedSubjectIsAbsentFromRankedResults` | `internal/core/search/index_test.go` | Shredding a subject removes it from a ranked search, with the survivor still returned so an implementation that dropped everything would fail | — | S5 |
| `TestAShreddedSubjectIsAbsentFromFacets` | `internal/core/search/index_test.go` | The erased subject is absent from the facet counts too, and the totals match the visible set — a facet over candidates would leak it as a number | — | S6 |
| `TestResultHonoursTheLimit` | `internal/core/search/index_test.go` | A search returns at most the requested number of results, and the ones it returns are the highest-scoring | — | S7 |
| `TestRarerTermsScoreHigher` | `internal/core/search/index_test.go` | A subject matching a rare term outranks one matching only a common term, which is the whole reason ranking exists rather than returning candidates in any order | — | S3 |
| `TestAnEmptyQueryIsRefused` | `internal/core/search/index_test.go` | A query that analyses to no terms is refused by name rather than matching everything | — | S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The seven tests above. |
| 2 — something selects it | `Index.Search` is the only path from a query to a result, and `Add` the only way a posting enters an index. |
| 3 — the caller can discover it | `Query` and `Result` are documented in `docs/QUERY-LANGUAGE.md` alongside the statement that produces them. |
| 4 — it is used | In memory, by anything holding an index. Persisting one is T4. |

## Mutation Log

- 2026-09-04 · fda1ea4* · mutant killed · exit 1 · `internal/core/search/rank.go` · drops the tie-break, so subjects with equal scores come back in whatever order the score map happened to yield. Go randomises map iteration deliberately, which makes the ranking differ between runs and between processes — and the sort still looks correct, because the scores really are ordered. This is the same class of defect placement shipped with a per-process seed · acceptance-sha256:af920c4a9502b30d10436b29c3b5a79336df0bb7cc095f0ddc1b30f843d460ae · covers:ranking that does not depend on map iteration order
- 2026-09-04 · fda1ea4* · mutant killed · exit 1 · `internal/core/search/index.go` · keeps a placeholder for a posting that would not decrypt instead of dropping it, so an erased subject reappears in the ranked answer and in the facet counts. Bypassing Visible in favour of an inline open-and-handle loop is how the erasure guarantee gets lost: the rule lives in one function, and code that does the decrypt itself is not covered by it · acceptance-sha256:af920c4a9502b30d10436b29c3b5a79336df0bb7cc095f0ddc1b30f843d460ae · covers:a shredded subject absent from a ranked result and from its facets
- 2026-09-04 · fda1ea4* · mutant killed · exit 1 · `internal/core/search/rank.go` · ignores the limit and returns every match, which is invisible on a small corpus and unbounded on a real one. The caller asked for a bounded cost and gets whatever the index holds, which is the full scan the required LIMIT exists to prevent · acceptance-sha256:af920c4a9502b30d10436b29c3b5a79336df0bb7cc095f0ddc1b30f843d460ae · covers:a result honouring the query's limit
- 2026-09-04 · fda1ea4* · mutant inconclusive · exit 1 · `internal/core/search/rank.go` · weights every term equally, so a subject matching a term the whole corpus shares outranks one matching a term nothing else has. The search still returns results, still returns them deterministically, and still honours the limit — it just ranks by how MANY terms matched rather than by how much each one distinguishes, which is the difference between a ranking and an arbitrary order that happens to be stable · acceptance-sha256:af920c4a9502b30d10436b29c3b5a79336df0bb7cc095f0ddc1b30f843d460ae · covers:rarer terms outranking common ones
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-04 · fda1ea4* · mutant survived · exit 0 · `internal/core/search/rank.go` · weights every term equally, so a subject matching a term the whole corpus shares outranks one matching a term nothing else has. The search still returns results, still returns them deterministically, and still honours the limit — it just ranks by how MANY terms matched rather than by how much each one distinguishes, which is the difference between a ranking and an arbitrary order that happens to be stable · acceptance-sha256:af920c4a9502b30d10436b29c3b5a79336df0bb7cc095f0ddc1b30f843d460ae · covers:rarer terms outranking common ones
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-04 · fda1ea4* · mutant killed · exit 1 · `internal/core/search/rank.go` · weights every term equally, so a term the whole corpus shares counts for as much as one nothing else has. The search still returns results, still deterministically, still within its limit — it just ranks by how MANY terms matched rather than by how much each one distinguishes. This mutant SURVIVED on the first attempt because the fixture gave the rare-term subject both query terms, so it won on count rather than rarity; the fixture was corrected and the claim is bound to a case where the two disagree · acceptance-sha256:af920c4a9502b30d10436b29c3b5a79336df0bb7cc095f0ddc1b30f843d460ae · covers:rarer terms outranking common ones

## Invariants

- The same postings and the same query give the same ranked answer, always.
- No shredded subject appears in a result or in a facet count.
- A result never exceeds its limit.

## Risks

- ⚠ **Go randomises map iteration deliberately, and a ranking that reads a map in iteration order is non-deterministic per process.** That is exactly the defect found in placement on 2026-09-04, where a per-process random seed passed every in-process determinism test because a test binary is one process. The test here runs the same query many times AND builds the index in two different insertion orders, because either alone can pass against a broken implementation.
- ⚠ **Ties are where determinism actually breaks.** Two subjects with equal scores will be ordered by whatever the candidate slice happened to hold. The ranking breaks ties by subject so the order is total.
- ⚠ **A facet computed over CANDIDATES rather than over VISIBLE results leaks an erased subject as a number.** It is the natural implementation — facet the matched set, then filter for display — and it is the same disclosure ADR-021 rule 2 forbids, arriving through the counting path instead of the result path.
- The scoring function is term frequency against inverse document frequency, chosen for being explainable and deterministic rather than for being good. Whether it ranks WELL needs a corpus, and that is deferred rather than claimed.
- ⚠ **THE RARITY MUTANT SURVIVED ON ITS FIRST RUN, AND THE FIXTURE WAS THE REASON.** `TestRarerTermsScoreHigher` queried `blue star` against a corpus where the rare-term subject also carried `star` — so it matched BOTH query terms and won on term COUNT, not on rarity. Flattening every weight to 1.0 left it winning anyway, and the test passed with the mechanism it names removed. The fixture now gives that subject `blue nebula`, so every hit matches exactly one query term and only the weighting can decide; the test also asserts the top score is STRICTLY higher, because equal scores would sort alphabetically and look like a ranking. Both rows stay in the log.
- ★ **The general shape is worth carrying:** a test can assert the right outcome for the wrong reason, and mutation is what tells the two apart. The outcome was correct on both runs — what changed is whether the named mechanism produced it.
- ⚠ **`TestIndexBuiltTwiceAnswersIdentically` is weaker than its name suggests, and it is NOT a declared claim for that reason.** It builds twice in the SAME order, so any deterministic implementation passes it — which makes it a near-duplicate of the determinism test rather than an independent check. The property that genuinely discriminates is the reversed-insertion-order half of `TestRankingDoesNotDependOnMapOrder`. The test stays because it is cheap and would catch a truly stateful index, but nothing in `Rests-on` claims it proves anything on its own. Noticed while choosing a mutant for it and failing to find one that a deterministic implementation would survive.
- Results are CANDIDATES. Confirming them against the datoms is T4, and until then a caller must not treat a result as proof the entity still matches.

## Stop Condition

Stop and ask before letting any result order depend on map iteration, however
convenient. A search that returns a different order per process is the same class
of defect as placement disagreeing between clients, and it passes every test that
runs inside one binary.

## Out of Scope

- Persisting the index (this record's T4)
- Confirming candidates against the datoms (T4, and `docs/adr/BACKLOG.md` §20)
- Choosing a ranking function against a real corpus (deferred: `docs/adr/BACKLOG.md` §27)
- Stemming, stop words and language detection (deferred: `docs/adr/BACKLOG.md` §27)

## Verification Log
- 2026-09-04 · fda1ea4* · exit 0 · `set -o pipefail …` · acceptance-sha256:af920c4a9502b30d10436b29c3b5a79336df0bb7cc095f0ddc1b30f843d460ae · ms:4143
- 2026-09-04 · fda1ea4* · exit 0 · `set -o pipefail …` · acceptance-sha256:af920c4a9502b30d10436b29c3b5a79336df0bb7cc095f0ddc1b30f843d460ae · ms:4350
- 2026-09-04 · fda1ea4* · exit 0 · `set -o pipefail …` · acceptance-sha256:af920c4a9502b30d10436b29c3b5a79336df0bb7cc095f0ddc1b30f843d460ae · ms:3947
- 2026-09-04 · fda1ea4* · exit 0 · `set -o pipefail …` · acceptance-sha256:af920c4a9502b30d10436b29c3b5a79336df0bb7cc095f0ddc1b30f843d460ae · ms:3964
- 2026-09-04 · fda1ea4* · exit 0 · `set -o pipefail …` · acceptance-sha256:af920c4a9502b30d10436b29c3b5a79336df0bb7cc095f0ddc1b30f843d460ae · ms:4062
- 2026-09-04 · fda1ea4* · exit 0 · `set -o pipefail …` · acceptance-sha256:af920c4a9502b30d10436b29c3b5a79336df0bb7cc095f0ddc1b30f843d460ae · ms:3794
- 2026-09-04 · fda1ea4* · exit 0 · `set -o pipefail …` · acceptance-sha256:af920c4a9502b30d10436b29c3b5a79336df0bb7cc095f0ddc1b30f843d460ae · ms:3998
- 2026-09-04 · fda1ea4* · exit 0 · `set -o pipefail …` · acceptance-sha256:af920c4a9502b30d10436b29c3b5a79336df0bb7cc095f0ddc1b30f843d460ae · ms:4090
- 2026-09-04 · fda1ea4* · exit 0 · `set -o pipefail …` · acceptance-sha256:af920c4a9502b30d10436b29c3b5a79336df0bb7cc095f0ddc1b30f843d460ae · ms:4011
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:af920c4a9502b30d10436b29c3b5a79336df0bb7cc095f0ddc1b30f843d460ae · ms:4003
