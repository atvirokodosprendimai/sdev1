# Task ADR-021-T4: Rebuild the index from the log, and confirm what it returns

**Depends-on:** T3
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `search.TermsOf`, `search.Builder`, `search.NewBuilder`, `search.Builder.Name`, `search.Builder.Consume`, `search.Builder.Highest`, `search.Confirm`
**Consumes:** `search.Index`, `search.Seal`, `search.Analyze` (T3); `subscribe.Subscription` and `subscribe.Sink` from ADR-010; `tail.Entry` from ADR-017; `ports.Reader` from ADR-003
**Data dependency:** hermetic — a tail the test builds, and a reader it controls
**Proof map:** v1
**Rests-on:** `an index rebuilt from the log answering as the write path's does`, `a candidate confirmed against the datoms rather than trusted`, `an index fed by an at-least-once stream being idempotent`, `one rule deciding which datoms are indexed`

## Status

★ **Unblocked, and done.** This task was `pending` on two things that now exist:
a storage engine (`BACKLOG.md` §12, closed by ADR-024/025/026) and a query
evaluator (§20, closed for `SELECT` by ADR-027). `ports.Reader` is what
confirmation reads through, and it has real implementations behind it now.

⚠ **An earlier version of this task ALSO claimed the grammar and the index were
blocked on the storage engine.** They were not — parsing is parsing and an index
is a data structure — and over-deferring them left search looking further away
than it was. That correction is kept here because the same mistake was available
twice.

## Goal

Make the index a projection of the log — rebuildable, idempotent under
redelivery — and make its answers confirmed rather than believed.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/search/builder.go` | add | `TermsOf`, and `Builder`: an index fed by ADR-010's subscription. |
| `internal/core/search/confirm.go` | add | `Confirm`: drop candidates whose datoms no longer match. |
| `internal/core/search/builder_test.go` | add | The tests below. |
| `internal/core/session/session.go` | modify | The write path indexes through `TermsOf` rather than its own copy of the rule. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestRebuildFromTheLogMatchesIncremental`, `TestRedeliveryDoesNotChangeTheAnswer`, `TestACandidateIsConfirmedAgainstTheDatoms`, `TestARetractionIsNotIndexed`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Extract `TermsOf` — which datoms produce terms — into ONE function, and route the session's write path through it. ⚠Two copies of that rule drift, and when they do a rebuilt index answers differently from the one it replaced with nothing failing. [proof: mutation]
3. [S3] Implement `Builder` as a `subscribe.Sink`, so the index follows the log through the same primitive as streaming backup and the console rather than a third way of following it. [proof: mutation]
4. [S4] Make a rebuild from the log answer as the write path's index does, and test it by building both and comparing their ANSWERS rather than their internals. ⚠"The index is rebuildable" is the sentence that makes losing it a performance event rather than a data-loss event, and it is worth nothing unproven. [proof: mutation]
5. [S5] Make the builder IDEMPOTENT under redelivery, with a high-water transaction. ⚠Delivery is at-least-once by ADR-010's own contract, and an index that simply appended would hold each posting twice — which changes what terms SCORE without changing what they mean, so the results stay plausible and the ranking is wrong. [proof: mutation]
6. [S6] Index an entry ATOMICALLY: build its postings, then add them. ⚠A seal failing partway through an entry would leave some postings added and the high-water mark unmoved, so redelivery duplicates exactly what the mark exists to prevent.
7. [S7] Implement `Confirm` so a candidate whose datoms no longer carry any searched term is dropped before the result is returned. [proof: mutation]
8. [S8] Keep an index shard inside one tenant subtree. [proof: human: a reader confirms the index is a field of a `Session`, that a `Session` is bound to one tenant at construction and never rebound, and that no path exists by which two tenants' postings reach one index — it is structural rather than filtered afterwards]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/search/... -race -run 'TestRebuildFromTheLogMatchesIncremental|TestRedeliveryDoesNotChangeTheAnswer|TestACandidateIsConfirmedAgainstTheDatoms|TestARetractionIsNotIndexed' -count=1 2>&1 | tee /tmp/adr021-t4a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr021-t4a.out \
  && go test ./internal/core/search/... ./internal/core/session/... ./internal/core/subscribe/... ./internal/core/tail/... -race -count=1 2>&1 | tee /tmp/adr021-t4b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr021-t4b.out
```

The second command re-runs the session, because its write path now shares the
indexing rule, and ADR-010's and ADR-017's suites, because the builder consumes
their types.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestRebuildFromTheLogMatchesIncremental` | `internal/core/search/builder_test.go` | An index fed datom-by-datom as the write path does, and one rebuilt by WALKING the log through a subscription, answer identically over six queries. ⚠ Compared by answers rather than internals: two indexes can hold the same postings and rank differently, and it is the answer a caller gets | — | S3, S4 |
| `TestRedeliveryDoesNotChangeTheAnswer` | `internal/core/search/builder_test.go` | Delivering the same log twice leaves the term count, the subjects AND the scores unchanged. ⚠ The scores are the sharp half — duplicated postings move the ranking while the same subjects come back in the same order, so every result still looks right | — | S5, S6 |
| `TestACandidateIsConfirmedAgainstTheDatoms` | `internal/core/search/builder_test.go` | Two subjects are indexed carrying a term, one is then changed so it no longer does, and `Confirm` drops exactly that one — a stale index cannot return a fact that is no longer there | — | S7 |
| `TestARetractionIsNotIndexed` | `internal/core/search/builder_test.go` | `TermsOf` yields nothing for a retraction and nothing for a reference, and both words for an ordinary assertion — the two exclusions live in one place, so the write path and a rebuild cannot disagree about them | — | S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests above. |
| 2 — something selects it | `TermsOf` is the only rule deciding what is indexed, and the session's write path calls it; `Builder` is the only thing that feeds an index from the log. |
| 3 — the caller can discover it | Nothing changes for the caller: `SEARCH` is already the surface. |
| 4 — it is used | `TermsOf` is on the live write path. ⚠ `Builder` and `Confirm` are NOT yet on the session's search path — see Risks. |

## Mutation Log

- 2026-09-04 · a1c458c* · mutant killed · exit 1 · `internal/core/search/builder.go` · indexes a REFERENCE as prose, so an entity name becomes a full-text term; because both the write path and the builder now share this rule, the drift shows in both at once rather than only in a rebuild · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · covers:one rule deciding which datoms are indexed
- 2026-09-04 · a1c458c* · mutant killed · exit 1 · `internal/core/search/builder.go` · removes the high-water guard, so an entry redelivered under ADR-010 at-least-once contract is indexed twice: the same subjects come back in the same order and the SCORES move, which is a wrong ranking that looks entirely right · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · covers:an index fed by an at-least-once stream being idempotent
- 2026-09-04 · a1c458c* · mutant killed · exit 1 · `internal/core/search/builder.go` · stops advancing the high-water mark, so the guard compares against a zero transaction and every entry after the first is skipped: the rebuilt index holds only what arrived first and answers differently from the write path · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · covers:an index rebuilt from the log answering as the write path's does
- 2026-09-04 · a1c458c* · mutant inconclusive · exit 1 · `internal/core/search/confirm.go` · keeps every candidate whatever the datoms say, which is the index being treated as the authority; the failure is invisible except on data that changed since the index last saw it · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · covers:a candidate confirmed against the datoms rather than trusted
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-04 · a1c458c* · mutant survived · exit 0 · `internal/core/search/confirm.go` · confirms against a second copy of the indexing rule instead of the shared one, so confirmation accepts terms the index would never have produced — a retracted fact confirms itself · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · covers:one rule deciding which datoms are indexed
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-04 · a1c458c* · mutant killed · exit 1 · `internal/core/search/confirm.go` · keeps every candidate whatever the datoms say, which is the index being treated as the authority; the failure is invisible except on data that changed since the index last saw it · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · covers:a candidate confirmed against the datoms rather than trusted
- 2026-09-04 · a1c458c* · mutant killed · exit 1 · `internal/core/search/confirm.go` · confirms against the raw visible datoms instead of what the entity now CARRIES, so the earlier assertion of a retracted attribute is still present and confirms a fact that was withdrawn · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · covers:a candidate confirmed against the datoms rather than trusted
- 2026-09-04 · a1c458c* · mutant survived · exit 0 · `internal/core/search/confirm.go` · confirms against a second copy of the indexing rule instead of the shared one, so confirmation accepts terms the index would never have produced · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · covers:one rule deciding which datoms are indexed
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-04 · a1c458c* · mutant killed · exit 1 · `internal/core/search/builder.go` · indexes a REFERENCE as prose, so an entity name becomes a full-text term; because both the write path and the builder now share this rule, the drift shows in both at once rather than only in a rebuild · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · covers:one rule deciding which datoms are indexed
- 2026-09-04 · a1c458c* · mutant killed · exit 1 · `internal/core/search/builder.go` · removes the high-water guard, so an entry redelivered under ADR-010 at-least-once contract is indexed twice: the same subjects come back in the same order and the SCORES move, which is a wrong ranking that looks entirely right · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · covers:an index fed by an at-least-once stream being idempotent
- 2026-09-04 · a1c458c* · mutant killed · exit 1 · `internal/core/search/builder.go` · stops advancing the high-water mark, so the guard compares against a zero transaction and every entry after the first is skipped: the rebuilt index holds only what arrived first and answers differently from the write path · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · covers:an index rebuilt from the log answering as the write path's does
- 2026-09-04 · a1c458c* · mutant inconclusive · exit 1 · `internal/core/search/confirm.go` · keeps every candidate whatever the datoms say, which is the index being treated as the authority; the failure is invisible except on data that changed since the index last saw it · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · covers:a candidate confirmed against the datoms rather than trusted
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-04 · a1c458c* · mutant killed · exit 1 · `internal/core/search/confirm.go` · confirms against a second copy of the indexing rule instead of the shared one, so confirmation accepts terms the index would never have produced — a retracted fact confirms itself · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · covers:one rule deciding which datoms are indexed
- 2026-09-04 · a1c458c* · mutant killed · exit 1 · `internal/core/search/confirm.go` · keeps every candidate whatever the datoms say, which is the index being treated as the authority · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · covers:a candidate confirmed against the datoms rather than trusted
- 2026-09-04 · a1c458c* · mutant killed · exit 1 · `internal/core/search/confirm.go` · confirms against the raw visible datoms instead of what the entity now CARRIES, so the earlier assertion of a retracted attribute confirms a fact that was withdrawn · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · covers:a candidate confirmed against the datoms rather than trusted
- 2026-09-04 · a1c458c* · mutant killed · exit 1 · `internal/core/search/confirm.go` · confirms against a second copy of the indexing rule, so a REFERENCE whose bytes happen to be the searched term confirms a subject the index would never have matched on · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · covers:one rule deciding which datoms are indexed

## Invariants

- One rule decides which datoms are indexed.
- A rebuilt index answers as the incrementally built one does.
- Redelivering an entry changes nothing, including the scores.
- An index shard never spans two tenants.

## Risks

- ⚠ **`Builder` and `Confirm` exist and the session's `SEARCH` does not call them yet.** The session indexes on the write path and answers from that index unconfirmed. This task makes both mechanisms real and provable; wiring the session's search through `Confirm` needs the search path to hold a reader and a snapshot, which is the same work as moving `SEARCH` onto the reader (`BACKLOG.md` §20). Recorded here rather than left for someone to discover — the mechanism being present is not the same as it being on the path.
- ⚠ **Confirming against the datoms is the rule that decays quietest**, because skipping it makes every search faster and the damage shows only on data that changed since the index saw it.
- ⚠ **A rebuild test that builds once and compares to itself proves nothing.** One index is fed datom-by-datom and the other by walking the log through a cursor and a sink — two drivers, one rule.
- ⚠ **The redelivery test nearly asserted the wrong thing.** A first version bounded the score by one; scoring is inverse-document-frequency based and legitimately exceeds it, so the assertion failed against correct code. The property that matters is that the scores are UNCHANGED, not that they are small.
- ⚠ **Search is the largest fan-out a single request can cause** — one query can touch every leaf holding a tenant's subtree.

## Stop Condition

Stop and ask before returning a search result that has not been confirmed against
the datoms, however much faster it is. That makes the index the authority, which
ADR-021 rule 3 forbids — and the failure is invisible except on data that changed
since the index last saw it.

## Out of Scope

- Wiring the session's `SEARCH` through `Confirm` (deferred: `docs/adr/BACKLOG.md` §20 — it needs the search path to carry a reader and a snapshot, which is the same work as moving `SEARCH` onto the reader)
- Counting search bytes against ADR-015's read budget (deferred: `docs/adr/BACKLOG.md` §22 — ⚠ NOTHING in this repository is admitted through that budget today, so gating search alone would make it the only caller of a gate no other read passes, which measures nothing and hides that the gate is unwired)
- Persisting the index itself to a disk (deferred: `docs/adr/BACKLOG.md` §15 — an index that must be published atomically is the same question as publishing an index over the tail, and answering it here would answer it twice)
- Ranking chosen against a real corpus (deferred: `docs/adr/BACKLOG.md` §27)
- What a posting contains (permanent: boundary: T1 fixed that, and this task feeds and confirms postings rather than redefining them)

## Verification Log
- 2026-09-04 · a1c458c* · exit 0 · `set -o pipefail …` · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · ms:4140
- 2026-09-04 · a1c458c* · exit 0 · `set -o pipefail …` · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · ms:3935
- 2026-09-04 · a1c458c* · exit 0 · `set -o pipefail …` · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · ms:3880
- 2026-09-04 · a1c458c* · exit 0 · `set -o pipefail …` · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · ms:3872
- 2026-09-04 · a1c458c* · exit 0 · `set -o pipefail …` · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · ms:3870
- 2026-09-04 · a1c458c* · exit 0 · `set -o pipefail …` · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · ms:3858
- 2026-09-04 · a1c458c* · exit 0 · `set -o pipefail …` · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · ms:3857
- 2026-09-04 · a1c458c* · exit 0 · `set -o pipefail …` · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · ms:3914
- 2026-09-04 · a1c458c* · exit 0 · `set -o pipefail …` · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · ms:3945
- 2026-09-04 · a1c458c* · exit 0 · `set -o pipefail …` · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · ms:3908
- 2026-09-04 · a1c458c* · exit 0 · `set -o pipefail …` · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · ms:3990
- 2026-09-04 · a1c458c* · exit 0 · `set -o pipefail …` · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · ms:4023
- 2026-09-04 · a1c458c* · exit 0 · `set -o pipefail …` · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · ms:3928
- 2026-09-04 · a1c458c* · exit 0 · `set -o pipefail …` · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · ms:3853
- 2026-09-04 · a1c458c* · exit 0 · `set -o pipefail …` · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · ms:3859
- 2026-09-04 · a1c458c* · exit 0 · `set -o pipefail …` · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · ms:3959
- 2026-09-04 · a1c458c* · exit 0 · `set -o pipefail …` · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · ms:3840
- 2026-09-04 · a1c458c* · exit 0 · `set -o pipefail …` · acceptance-sha256:d631b300b172cbe49d63d8d458c69cea16232bc06c2337721f9159d06ac4cd7e · ms:3852
