# Task ADR-002-T3: Two-axis visibility and the qualifier defaults

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `temporal.Interval`, `temporal.Query`, `temporal.Visible()`, `temporal.ResolveQualifiers()`
**Consumes:** `tx.TxID` (T2)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the lone-instant binding rule`, `the independence of the two axes`, `the half-open interval`

## Goal

Provide the one predicate that decides whether a datom is visible to a query, and
the one function that turns a caller's time qualifiers into the two axes — so
there is exactly one place in the system where the axes are compared.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/temporal/temporal.go` | add | `Interval{From, To}`, `Query{AsOf *tx.TxID; ValidAt *int64}`, and `Visible`. |
| `internal/core/temporal/qualifiers.go` | add | `ResolveQualifiers` — ADR-002 rule 6's defaults table, implemented as a table rather than as branching prose. |
| `internal/core/temporal/doc.go` | add | Package comment: the two axes, why one instant binds valid time only, and the defect that rule exists to prevent. |
| `internal/core/temporal/temporal_test.go` | add | The tests below, including the falsifier named in ADR-002's `Enforced-by:`. |

★ `Visible` is deliberately the **only** site in the system where the two axes are
compared. ADR-002 records that the predecessor project's defect was a caller
passing one value into two parameters; concentrating the comparison here is what
makes that mistake reviewable in one place instead of at every call site.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestLoneInstantBindsValidTimeOnly`, `TestBackdatedWriteIsVisibleAtItsValidTime`, `TestTransactionTimeDefaultsToOpen`, `TestDefaultsTableIsExhaustive`, `TestVisibleRejectsOnEitherAxisIndependently`, `TestIntervalIsHalfOpen`, `TestVisibleIsTheOnlyComparisonSite`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter: a falsifier beside the command rather than inside it proves nothing, and this cost a spuriously surviving mutant. [proof: acceptance]
2. [S2] Define `Interval{From, To int64}` as a half-open business-time window — `[From, To)` — so that adjacent intervals neither overlap nor leave a gap. Closed intervals are the classic off-by-one here and are rejected deliberately.
3. [S3] Define `Query{AsOf *tx.TxID; ValidAt *int64}` with both qualifiers as pointers, so "not supplied" is representable and cannot be confused with a zero value.
4. [S4] Implement `ResolveQualifiers` as ADR-002 rule 6's table, written as a literal table in code so it can be read against the record rather than reconstructed from branches: nothing → (latest, now); `AS OF t` → (latest, t); `AS OF t TRANSACTION u` → (u, t); `TRANSACTION u` → (u, now).
5. [S5] Implement `Visible(validFrom, validTo int64, txID tx.TxID, q Query) bool`: the datom's business interval must contain `ValidAt`, **and** its `TxID` must not exceed `AsOf`. The two conditions are independent and neither may be derived from the other.
6. [S6] Write the package comment stating the rule, naming the defect it prevents, and stating that a lone instant binds valid time with transaction time left open. [proof: human: a reader confirms the comment states the binding rule explicitly, since the wrong behaviour is the one an implementer writes by default]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/temporal/... -run 'TestLoneInstant|TestBackdated|TestTransactionTime|TestDefaults|TestInterval|TestVisible' -count=1 2>&1 | tee /tmp/adr002-t3.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr002-t3.out \
  && go test ./internal/core/hlc/... ./internal/core/tx/... -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestLoneInstantBindsValidTimeOnly` | `internal/core/temporal/temporal_test.go` | A single supplied instant reaches `ValidAt` and leaves `AsOf` open. **This is the falsifier ADR-002 names in `Enforced-by:`** | — | S4 |
| `TestBackdatedWriteIsVisibleAtItsValidTime` | `internal/core/temporal/temporal_test.go` | A datom whose `ValidFrom` is in the past but whose `TxID` was minted now IS returned by a query at that past instant — the exact case the predecessor project returned nothing for | — | S4, S5 |
| `TestTransactionTimeDefaultsToOpen` | `internal/core/temporal/temporal_test.go` | With no transaction qualifier, no datom is excluded on the transaction axis, whatever its `TxID` | — | S4, S5 |
| `TestDefaultsTableIsExhaustive` | `internal/core/temporal/temporal_test.go` | All four combinations of supplied/omitted qualifiers resolve, and each matches ADR-002 rule 6's row | — | S3, S4 |
| `TestVisibleRejectsOnEitherAxisIndependently` | `internal/core/temporal/temporal_test.go` | A datom failing only the business axis and one failing only the transaction axis are both excluded — proving neither condition is derived from the other | — | S5 |
| `TestIntervalIsHalfOpen` | `internal/core/temporal/temporal_test.go` | A datom whose interval ends exactly at `ValidAt` is excluded, and one whose interval starts exactly there is included — adjacent intervals neither overlap nor gap | — | S2 |
| `TestVisibleIsTheOnlyComparisonSite` | `internal/core/temporal/temporal_test.go` | A source scan asserts no other file in `internal/` compares both a validity bound and a `TxID` — the structural guard that keeps the comparison concentrated. ⚠Amended 2026-09-04 for ADR-011: it now carries a `sanctionedSites` allowlist, and each entry is CONDITIONAL on the substitute check that replaces it still existing | — | S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The seven unit tests above. |
| 2 — something selects it | T4's property suite drives `Visible` and `ResolveQualifiers` across generated divergent-axis cases; ADR-011's evaluator will be the production caller. |
| 3 — the caller can discover it | Exported doc comments plus the defaults table written literally in `qualifiers.go`; `go doc ./internal/core/temporal` is the check. |
| 4 — it is used | Nothing measures this yet. |

## Mutation Log

- 2026-09-04 · a3ba183* · mutant killed · exit 1 · `internal/core/temporal/qualifiers.go` · ignoring a supplied instant and using the current one silently answers a different question than the caller asked; TestLoneInstantBindsValidTimeOnly must go red · acceptance-sha256:5847e3378b3da24dbb41f13dfd733770dd0f0a0bffd345b726d0009ec0fa3ce3 · covers:the lone-instant binding rule
- 2026-09-04 · a3ba183* · mutant killed · exit 1 · `internal/core/temporal/temporal.go` · dropping the transaction condition makes visibility depend on business time alone, so a datom recorded after the cutoff is wrongly included; TestVisibleRejectsOnEitherAxisIndependently must go red · acceptance-sha256:5847e3378b3da24dbb41f13dfd733770dd0f0a0bffd345b726d0009ec0fa3ce3 · covers:the independence of the two axes
- 2026-09-04 · a3ba183* · mutant survived · exit 0 · `internal/core/temporal/temporal.go` · a closed interval makes two adjacent versions both visible at the boundary instant, which is the classic off-by-one in a versioned store; TestIntervalIsHalfOpen must go red · acceptance-sha256:5847e3378b3da24dbb41f13dfd733770dd0f0a0bffd345b726d0009ec0fa3ce3 · covers:the half-open interval
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-04 · a3ba183* · mutant killed · exit 1 · `internal/core/temporal/qualifiers.go` · re-bound to the widened fence: ignoring a supplied instant silently answers a different question than the caller asked · acceptance-sha256:c07bd81e4762dd053804cedc309030524e44ab7e4d7b99dfb002e57449cc4e9d · covers:the lone-instant binding rule
- 2026-09-04 · a3ba183* · mutant killed · exit 1 · `internal/core/temporal/temporal.go` · re-bound to the widened fence: dropping the transaction condition makes visibility depend on business time alone · acceptance-sha256:c07bd81e4762dd053804cedc309030524e44ab7e4d7b99dfb002e57449cc4e9d · covers:the independence of the two axes
- 2026-09-04 · a3ba183* · mutant killed · exit 1 · `internal/core/temporal/temporal.go` · second attempt. The first SURVIVED only because TestIntervalIsHalfOpen sat outside the fence filter, so the falsifier was never run. A closed interval makes two adjacent versions both visible at the boundary instant · acceptance-sha256:c07bd81e4762dd053804cedc309030524e44ab7e4d7b99dfb002e57449cc4e9d · covers:the half-open interval

## Invariants

- `Visible` is the only place in the system where a business-time bound and a transaction-time bound are both compared. `TestVisibleIsTheOnlyComparisonSite` is what keeps that true as the codebase grows.
- The two axes are independent. Neither condition is derived from the other, and a lone caller-supplied instant never reaches both.
- `Interval` is half-open `[From, To)`.
- An omitted qualifier is a nil pointer, never a zero value.

## Risks

- `TestVisibleIsTheOnlyComparisonSite` is a source-scanning test, which is coarse and can produce a false positive on an unrelated file that happens to mention both concepts. It is kept anyway: the defect it guards shipped in a sibling project past ~140 green tests, and a coarse guard that fires is worth more than an elegant one that does not exist.
- ⚠ **Amended 2026-09-04, when ADR-011 tripped it.** The query language necessarily NAMES both axes — `AS OF` and `TRANSACTION` *are* the two axes — so it is not the "unrelated file" the coarseness note anticipated. It is the highest-RISK site, because a language surface is exactly where a caller would pass one instant into both. The guard now carries a `sanctionedSites` allowlist pairing each exempt package with the check that holds the guarantee there instead (`ql.TestPackageComputesNoDefaultsOfItsOwn`, which asserts `Resolve` forwards and branches on nothing). ★The exemption is CONDITIONAL: the guard reads the substitute's source and fails if it has been deleted or renamed, so an exemption can never outlive the check that paid for it. An unconditional allowlist would have been the hole this guard exists to prevent.
- The defaults table is duplicated between ADR-002 rule 6 and `qualifiers.go`. `TestDefaultsTableIsExhaustive` asserts the code's shape but cannot read the record, so the two can still drift. Reviewing them together is a Follow-up on the parent record rather than something a gate can close.

## Stop Condition

Stop and ask if ADR-011 introduces a third temporal notion — decision time, or a
user-visible ingestion time. ADR-002's defaults table is stated as valid for
exactly two qualifiers, and a third makes the table incomplete rather than wrong.

## Out of Scope

- The syntax a user writes for the qualifiers — ADR-011 owns that, and is bound by this table.
- Deciding what "latest" resolves to on a replica that is behind (deferred: `docs/adr/BACKLOG.md` §5)

## Verification Log
- 2026-09-04 · a3ba183* · exit 0 · `set -o pipefail …` · acceptance-sha256:5847e3378b3da24dbb41f13dfd733770dd0f0a0bffd345b726d0009ec0fa3ce3 · ms:1154
- 2026-09-04 · a3ba183* · exit 0 · `set -o pipefail …` · acceptance-sha256:5847e3378b3da24dbb41f13dfd733770dd0f0a0bffd345b726d0009ec0fa3ce3 · ms:1353
- 2026-09-04 · a3ba183* · exit 0 · `set -o pipefail …` · acceptance-sha256:5847e3378b3da24dbb41f13dfd733770dd0f0a0bffd345b726d0009ec0fa3ce3 · ms:1226
- 2026-09-04 · a3ba183* · exit 0 · `set -o pipefail …` · acceptance-sha256:5847e3378b3da24dbb41f13dfd733770dd0f0a0bffd345b726d0009ec0fa3ce3 · ms:1291
- 2026-09-04 · a3ba183* · exit 0 · `set -o pipefail …` · acceptance-sha256:c07bd81e4762dd053804cedc309030524e44ab7e4d7b99dfb002e57449cc4e9d · ms:1202
- 2026-09-04 · a3ba183* · exit 0 · `set -o pipefail …` · acceptance-sha256:c07bd81e4762dd053804cedc309030524e44ab7e4d7b99dfb002e57449cc4e9d · ms:1210
- 2026-09-04 · a3ba183* · exit 0 · `set -o pipefail …` · acceptance-sha256:c07bd81e4762dd053804cedc309030524e44ab7e4d7b99dfb002e57449cc4e9d · ms:1197
- 2026-09-04 · a3ba183* · exit 0 · `set -o pipefail …` · acceptance-sha256:c07bd81e4762dd053804cedc309030524e44ab7e4d7b99dfb002e57449cc4e9d · ms:1188
- 2026-09-04 · 4016ba8* · exit 0 · `set -o pipefail …` · acceptance-sha256:c07bd81e4762dd053804cedc309030524e44ab7e4d7b99dfb002e57449cc4e9d · ms:1427
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:c07bd81e4762dd053804cedc309030524e44ab7e4d7b99dfb002e57449cc4e9d · ms:1086
