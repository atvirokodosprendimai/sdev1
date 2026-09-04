# Task ADR-021-T4: Persist the index, and confirm what it returns

**Depends-on:** T3
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `search.Builder`, `search.Confirm`
**Consumes:** `search.Index` (T3); `subscribe.Subscription` from ADR-010; a storage engine (`docs/adr/BACKLOG.md` §12); a query evaluator (§20)
**Data dependency:** needs a running store — an index is a projection of the datom log, and there is no log on a disk to project
**Proof map:** v1
**Rests-on:** `an index rebuilt from the log reproducing the one built incrementally`, `a candidate confirmed against the datoms rather than trusted`

## Status

⚠ **`pending`, and blocked on two things that do not exist.**

- **A storage engine** (`BACKLOG.md` §12). An index is a read model over the
  datom log, and there is no log on a disk to project from.
- **A query evaluator** (`BACKLOG.md` §20). Rule 3 says a result is a set of
  CANDIDATES, confirmed against the datoms before it is returned. Without an
  evaluator there is nothing to confirm against, and a search that skipped the
  confirmation would be exactly the authoritative index the record refuses.

★ **What is NOT blocked, and is done:** the posting model (T1), the grammar (T2)
and a working in-memory index with deterministic ranking (T3). An earlier version
of this task claimed the grammar and the index were blocked on the storage engine
too. They were not — parsing is parsing and an index is a data structure — and
over-deferring them left search looking further away than it was.

## Goal

Make the index durable and its answers trustworthy.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/search/builder.go` | add | `Builder`: feed an index from ADR-010's subscription. |
| `internal/core/search/confirm.go` | add | `Confirm`: drop candidates whose datoms no longer match. |
| `internal/core/search/builder_test.go` | add | The tests below. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestRebuildFromTheLogMatchesIncremental`, `TestACandidateIsConfirmedAgainstTheDatoms`. Run the Acceptance fence and confirm it is red. [proof: acceptance]
2. [S2] Implement `Builder` as a consumer of ADR-010's subscription, so the index is fed by the same primitive as streaming backup and the console rather than a third way to follow the log. [proof: mutation]
3. [S3] Make a full rebuild from the log produce the same answers as an index built incrementally, and test it by building both and comparing. ⚠"The index is rebuildable" is the sentence that makes losing it a performance event rather than a data-loss event, and it is worth nothing unproven. [proof: mutation]
4. [S4] Implement `Confirm` so a candidate whose datoms no longer match is dropped before the result is returned. [proof: human: a reader confirms the search path reads the datoms for each candidate rather than trusting the index, since an index fed by subscription is always behind and nothing else can detect it]
5. [S5] Count search bytes against ADR-015's READ budget. [proof: human: a reader confirms search traffic is admitted through the same budget as other reads, since the link does not care which bytes are background and search is the largest amplifier a single request has]
6. [S6] Keep an index shard inside one tenant subtree. [proof: human: a reader confirms an index shard is addressed by a tenant subtree so a search structurally cannot cross tenants, rather than being filtered afterwards]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/search/... -race -run 'TestRebuildFromTheLogMatchesIncremental|TestACandidateIsConfirmedAgainstTheDatoms' -count=1 2>&1 | tee /tmp/adr021-t4.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr021-t4.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestRebuildFromTheLogMatchesIncremental` | `internal/core/search/builder_test.go` | An index rebuilt wholesale from the log answers identically to one built incrementally, so it is genuinely a projection and losing it costs only time | — | S2, S3 |
| `TestACandidateIsConfirmedAgainstTheDatoms` | `internal/core/search/builder_test.go` | A candidate whose datoms no longer match is dropped before the result is returned, so a stale index cannot return a wrong answer | — | S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The two tests above. |
| 2 — something selects it | `Builder` is the only thing that writes an index from the log, and `Confirm` the only gate between a candidate and a returned result. |
| 3 — the caller can discover it | Nothing changes for the caller: `SEARCH` is already the surface, and confirmation happens behind it. |
| 4 — it is used | `pending` — blocked on the storage engine and the evaluator. |

## Mutation Log

## Invariants

- A rebuilt index answers the same as an incrementally built one.
- No result is returned without being confirmed against the datoms.
- An index shard never spans two tenants.

## Risks

- ⚠ **Confirming against the datoms is the rule that decays quietest**, because skipping it makes every search faster and the damage shows only on data that changed since the index saw it. A stale-index test is the only thing that notices.
- ⚠ **A rebuild test that builds once and compares to itself proves nothing.** The test builds an index incrementally AND rebuilds one wholesale, then compares the two answers.
- ⚠ **Search is the largest fan-out a single request can cause** — one query can touch every leaf holding a tenant's subtree. Excluding it from the read budget would let one caller saturate the cluster while user queries are shed.

## Stop Condition

Stop and ask before returning a search result that has not been confirmed against
the datoms, however much faster it is. That makes the index the authority, which
rule 3 forbids — and the failure is invisible except on data that changed since
the index last saw it.

## Out of Scope

- The storage engine and the evaluator (deferred: `docs/adr/BACKLOG.md` §12 and §20)
- Ranking chosen against a real corpus (deferred: `docs/adr/BACKLOG.md` §27)

## Verification Log
