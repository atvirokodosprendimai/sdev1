# Task ADR-015-T1: Two budgets that share nothing, and a ceiling with two thresholds

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `admit.Ceiling`, `admit.NewCeiling`, `admit.Budget`, `admit.Kind`, `admit.KindRead`, `admit.KindWrite`, `admit.Controller`, `admit.NewController`, `admit.ErrNoCeiling`, `admit.ErrThresholdsInverted`
**Consumes:** none — this task is deliberately self-contained
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the read and write budgets sharing no state`, `a rejoin threshold strictly below the withdraw threshold`, `a ceiling being declared rather than discovered`

## Goal

Give a node a stated capacity, and make it impossible for read load to consume a
leaf's write capacity.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/admit/doc.go` | add | Package comment: why shedding is withdrawal rather than an error, why the budgets are separate, and why the two thresholds differ. |
| `internal/core/admit/admit.go` | add | `Ceiling`, `Budget`, `Kind`, `Controller`, and the two sentinels. |
| `internal/core/admit/admit_test.go` | add | The tests below, including the falsifier named in ADR-015's `Enforced-by:`. |

★ There are exactly two budget kinds and no way to make a third. A shared budget
is the thing this task exists to prevent, so it is prevented by there being
nowhere to put one.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestReadSheddingNeverStopsWrites`, `TestBudgetsShareNoState`, `TestInvertedThresholdsAreRefused`, `TestCeilingMustBeDeclared`, `TestUtilisationIsAFractionOfTheCeiling`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Ceiling` as a declared bandwidth plus a withdraw fraction and a rejoin fraction.
3. [S3] Refuse a ceiling of zero or less with `ErrNoCeiling`. ★A node that measures its own ceiling learns it by exceeding it, so the ceiling is stated by an operator and its absence is an error rather than a default.
4. [S4] Refuse a rejoin fraction that is not strictly below the withdraw fraction, with `ErrThresholdsInverted`. ★Equal thresholds oscillate by construction: the node rejoins at the level that made it leave, takes a burst, and leaves again — and the flapping costs more than the load did.
5. [S5] Define `Kind` with exactly two values, read and write, and no third.
6. [S6] Implement `Controller` holding one `Budget` per kind, with no shared field between them. [proof: mutation]
7. [S7] Implement `Utilisation` as a fraction of the declared ceiling, so a threshold is comparable across nodes with different links.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/admit/... -race -run 'TestReadSheddingNeverStops|TestBudgetsShareNoState|TestInvertedThresholds|TestCeilingMustBeDeclared|TestUtilisationIsAFraction' -count=1 2>&1 | tee /tmp/adr015-t1.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr015-t1.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestReadSheddingNeverStopsWrites` | `internal/core/admit/admit_test.go` | Driving read utilisation far past the ceiling leaves the write budget untouched and writes still admitting. **The falsifier ADR-015 names in `Enforced-by:`** | — | S5, S6 |
| `TestBudgetsShareNoState` | `internal/core/admit/admit_test.go` | A reflective check shows the two budgets are distinct values sharing no field, so a shared counter cannot be reintroduced by an "optimisation" | — | S6 |
| `TestInvertedThresholdsAreRefused` | `internal/core/admit/admit_test.go` | Equal or inverted thresholds are refused with `ErrThresholdsInverted`, so a node that would oscillate cannot be constructed | — | S4 |
| `TestCeilingMustBeDeclared` | `internal/core/admit/admit_test.go` | A ceiling of zero or less yields `ErrNoCeiling` rather than defaulting, since a node that discovers its ceiling discovers it by exceeding it | — | S2, S3 |
| `TestUtilisationIsAFractionOfTheCeiling` | `internal/core/admit/admit_test.go` | Utilisation is reported relative to the declared ceiling, so one threshold is meaningful on nodes with different links | — | S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | `NewController` is the only way a budget exists, and it takes a `Ceiling` that cannot be constructed with inverted thresholds; T2's decision reads exactly these budgets. |
| 3 — the caller can discover it | Two named kinds and two named sentinels; the absence of any third kind is itself the interface. |
| 4 — it is used | Nothing measures bandwidth yet; that needs a transport. |

## Mutation Log

- 2026-09-04 · aa9ce5e* · mutant killed · exit 1 · `internal/core/admit/admit.go` · points both kinds at one budget, which is a shared budget wearing two names — a read burst then consumes the write capacity and a load spike becomes a leaf refusing ingest · acceptance-sha256:20cfa4eb832871f2f8654dc0b1840d4a2e0bc77f780b2c512435f19b85848207 · covers:the read and write budgets sharing no state
- 2026-09-04 · aa9ce5e* · mutant killed · exit 1 · `internal/core/admit/admit.go` · admits EQUAL thresholds, which is what people write and which oscillates by construction: the node rejoins at exactly the load that made it leave, takes a burst, and leaves again · acceptance-sha256:20cfa4eb832871f2f8654dc0b1840d4a2e0bc77f780b2c512435f19b85848207 · covers:a rejoin threshold strictly below the withdraw threshold
- 2026-09-04 · aa9ce5e* · mutant killed · exit 1 · `internal/core/admit/admit.go` · accepts a ceiling of zero, so a node with no declared capacity computes utilisation against nothing and never sheds — the silent version of having no ceiling at all · acceptance-sha256:20cfa4eb832871f2f8654dc0b1840d4a2e0bc77f780b2c512435f19b85848207 · covers:a ceiling being declared rather than discovered

## Invariants

- The read and write budgets share no state.
- The rejoin threshold is strictly below the withdraw threshold.
- A ceiling must be declared and positive.
- There are exactly two budget kinds.
- This package measures nothing; it is told utilisation.

## Risks

- ⚠ A "budgets are separate" test that only checks two numbers differ would pass for two views over one counter. The test asserts that saturating one leaves the OTHER's utilisation and admission decision unchanged, which is the property that matters.
- ⚠ Thresholds are easy to check as "not inverted" while still allowing EQUAL. Equal is the oscillating case, so the test covers equal explicitly rather than only inverted.

## Stop Condition

Stop and ask before adding a third budget kind, or any budget shared between
kinds. Both are reasonable-sounding — a "total" budget especially — and both
reintroduce the coupling that lets a read storm stop a leaf's writes.

## Out of Scope

- The join/withdraw decision and its events — that is T2.
- Measuring bandwidth (deferred: `docs/adr/BACKLOG.md` §18)
- Refusing a write for durability reasons (permanent: boundary: ADR-004 owns the floor, which refuses because a write would not be SAFE rather than because a node is BUSY; conflating them would make a busy node look unsafe)

## Verification Log
- 2026-09-04 · aa9ce5e* · exit 0 · `set -o pipefail …` · acceptance-sha256:20cfa4eb832871f2f8654dc0b1840d4a2e0bc77f780b2c512435f19b85848207 · ms:1755
- 2026-09-04 · aa9ce5e* · exit 0 · `set -o pipefail …` · acceptance-sha256:20cfa4eb832871f2f8654dc0b1840d4a2e0bc77f780b2c512435f19b85848207 · ms:1682
- 2026-09-04 · aa9ce5e* · exit 0 · `set -o pipefail …` · acceptance-sha256:20cfa4eb832871f2f8654dc0b1840d4a2e0bc77f780b2c512435f19b85848207 · ms:1704
- 2026-09-04 · aa9ce5e* · exit 0 · `set -o pipefail …` · acceptance-sha256:20cfa4eb832871f2f8654dc0b1840d4a2e0bc77f780b2c512435f19b85848207 · ms:1737
- 2026-09-04 · aa9ce5e* · exit 0 · `set -o pipefail …` · acceptance-sha256:20cfa4eb832871f2f8654dc0b1840d4a2e0bc77f780b2c512435f19b85848207 · ms:1819
