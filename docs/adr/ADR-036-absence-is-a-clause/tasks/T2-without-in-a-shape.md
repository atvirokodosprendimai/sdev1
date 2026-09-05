# Task ADR-036-T2: WITHOUT in a shape — a third leg kind that filters and never scores

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** S
**Owner:** unassigned
**Produces:** `ql.LegExcluded`
**Consumes:** the `WITHOUT` keyword (T1); `ql.Leg`, `ql.LegKind`, `ql.BuildRow`, `ql.TimeClause` from ADR-011
**Data dependency:** hermetic — `BuildRow` is a pure function and parsing is a pure function
**Proof map:** v1
**Rests-on:** `an excluded leg that MATCHED dropping the row`, `an excluded leg contributing no binding rather than an unbound one`, `an excluded leg carrying its own time clause`

## Goal

Give the shape query its missing third leg kind, and decide now — before any
metric exists — that an excluded leg FILTERS rather than scores.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/ql/shape.go` | modify | `LegExcluded`, its `String`, the `WITHOUT` clause in `parseShape`, and `BuildRow`'s handling. |
| `internal/core/ql/excluded_test.go` | add | The tests below, in their own file rather than appended to `shape_test.go` — they share one fixture carrying all three leg kinds, and that fixture is the point. |
| `docs/QUERY-LANGUAGE.md`, `README.md` | modify | The third leg kind, and the narrowed binding invariant. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestAnExcludedLegDropsASubjectThatHasIt`, `TestAnExcludedLegBindsNothing`, `TestAnExcludedLegCarriesItsOwnTimeClause`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Add `LegExcluded` and parse `WITHOUT legs` after `OPTIONAL`, through the SAME `parseLegs` the other two use — so an excluded leg carries its own time clause without that being a special case. ⚠ADR-011's central property is that time is a clause and therefore attaches per leg; a leg kind that could not carry one would be the first exception to the thing the record exists to hold. [proof: mutation]
3. [S3] In `BuildRow`, an excluded leg that MATCHED drops the row. [proof: mutation]
4. [S4] ⚠An excluded leg contributes NO BINDING. ★`Unbound` already means "an OPTIONAL leg matched nothing"; using it here would make "had no value to give" and "was required to have none" render identically, and the collision is silent. [proof: mutation]
5. [S5] Document the third leg kind and the narrowed invariant: one binding per leg THAT PROJECTS, rather than one per leg. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/ql/... -race -run 'TestAnExcludedLegDropsASubjectThatHasIt|TestAnExcludedLegBindsNothing|TestAnExcludedLegCarriesItsOwnTimeClause|TestQueryLanguageDocIsComplete|TestPublishedExamplesParse' -count=1 2>&1 | tee /tmp/adr036-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr036-t2a.out \
  && go test ./... -race -count=1 2>&1 | tee /tmp/adr036-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr036-t2b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAnExcludedLegDropsASubjectThatHasIt` | `internal/core/ql/excluded_test.go` | Over one shape with a required, an optional and an excluded leg: a subject carrying the excluded attribute is dropped even though it matches BOTH other legs, and one lacking it is kept. ⚠ Both halves, plus a subject missing the required leg — "always drop" and "never drop" each satisfy one assertion alone | — | S3 |
| `TestAnExcludedLegBindsNothing` | `internal/core/ql/excluded_test.go` | A kept row has one binding per REQUIRED and OPTIONAL leg and none for the excluded one, and `Get` on the excluded attribute reports it is not there. ★ Asserted against the optional leg IN THE SAME ROW, which matched nothing too and DOES bind `Unbound` — with either leg alone the two rules render identically, so only a row carrying both can tell them apart | — | S4 |
| `TestAnExcludedLegCarriesItsOwnTimeClause` | `internal/core/ql/excluded_test.go` | `WITHOUT retired AS OF … TRANSACTION …` parses, the clause lands on that LEG rather than on the query, and a second excluded leg without a clause does not inherit the first's — the per-leg property ADR-011 exists to hold | — | S2 |
| `TestQueryLanguageDocIsComplete` | `internal/core/ql/doccoverage_test.go` | Existing gate. `LegExcluded` is a new export | — | S5 |
| `TestPublishedExamplesParse` | `internal/core/ql/docexamples_test.go` | Existing gate | — | S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The three tests, over the parser and over `BuildRow`. |
| 2 — something selects it | `parseShape` reads the clause; `BuildRow` is the only place a row is assembled. |
| 3 — the caller can discover it | Both pages document the third leg kind. |
| 4 — it is used | ⚠ **Nothing evaluates a shape query yet.** `MATCH SHAPE` parses and is refused by name, because a similarity metric needs real data (`BACKLOG.md` §20). `BuildRow` decides the ROW rules and is tested directly; the metric is not this record's. Recorded rather than implied. |

## Mutation Log

- 2026-09-05 · 9a503d2* · mutant killed · exit 1 · `internal/core/ql/shape.go` · keeps a subject that CARRIES the excluded attribute, so WITHOUT filters nothing and a shape query returns exactly the subjects the caller asked to exclude · acceptance-sha256:2ff4d6d16b3ee26322d0f08009fcc07a785e025706e4cce64b42d6c18bd7ed00 · covers:an excluded leg that MATCHED dropping the row
- 2026-09-05 · 9a503d2* · mutant killed · exit 1 · `internal/core/ql/shape.go` · binds an excluded leg as Unbound, which already means "an OPTIONAL leg matched nothing" — so "had no value to give" and "was required to have none" render identically and a consumer cannot tell which rule produced the binding · acceptance-sha256:2ff4d6d16b3ee26322d0f08009fcc07a785e025706e4cce64b42d6c18bd7ed00 · covers:an excluded leg contributing no binding rather than an unbound one
- 2026-09-05 · 9a503d2* · mutant killed · exit 1 · `internal/core/ql/shape.go` · reads excluded legs with a bespoke loop that cannot take a per-leg time clause, so `WITHOUT retired AS OF 1600000000` stops parsing — the first leg kind that cannot carry its own qualifier, which is the exception ADR-011 exists to prevent · acceptance-sha256:2ff4d6d16b3ee26322d0f08009fcc07a785e025706e4cce64b42d6c18bd7ed00 · covers:an excluded leg carrying its own time clause

## Invariants

- An excluded leg that matched drops the row.
- An excluded leg contributes no binding.
- Every leg kind carries its own time clause.

## Risks

- ⚠ **`BuildRow`'s existing `default` arm binds `Unbound`.** An excluded leg falling through to it is the likely bug, and it renders identically to an optional leg that matched nothing — so the test must count bindings in a row that has BOTH kinds, not merely check that the row survived.
- ⚠ **Nothing evaluates a shape query**, so this decides rules that only `BuildRow` currently enforces. That is why S4's rule is worth fixing now: once a metric exists, the tempting version is to treat a carried excluded attribute as "less similar" rather than as not a candidate, and by then there would be scores to preserve.
- Rule 6 narrows a documented invariant. Nothing indexes `Row.Bindings` positionally today, which is exactly why this is the cheapest moment.

## Stop Condition

Stop and ask before letting an excluded attribute affect a similarity SCORE. It is
a filter: a subject carrying it is not a weaker match, it is not a match.

## Out of Scope

- Evaluating a shape query, or choosing a metric (deferred: `docs/adr/BACKLOG.md` §20)
- `WITHOUT` on a read (deferred: T1 — done there)
- Indexing absence (deferred: `docs/adr/BACKLOG.md` §27)

## Verification Log
- 2026-09-05 · 9a503d2* · exit 0 · `set -o pipefail …` · acceptance-sha256:2ff4d6d16b3ee26322d0f08009fcc07a785e025706e4cce64b42d6c18bd7ed00 · ms:4663
- 2026-09-05 · 9a503d2* · exit 0 · `set -o pipefail …` · acceptance-sha256:2ff4d6d16b3ee26322d0f08009fcc07a785e025706e4cce64b42d6c18bd7ed00 · ms:4554
- 2026-09-05 · 9a503d2* · exit 0 · `set -o pipefail …` · acceptance-sha256:2ff4d6d16b3ee26322d0f08009fcc07a785e025706e4cce64b42d6c18bd7ed00 · ms:4706
- 2026-09-05 · 9a503d2* · exit 0 · `set -o pipefail …` · acceptance-sha256:2ff4d6d16b3ee26322d0f08009fcc07a785e025706e4cce64b42d6c18bd7ed00 · ms:4698
