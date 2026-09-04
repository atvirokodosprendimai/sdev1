# Task ADR-002-T4: A property suite that forces the two time axes apart

**Depends-on:** T2, T3
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the divergence property suite and its generator
**Consumes:** `tx.TxID` (T2); `temporal.Visible()`, `temporal.ResolveQualifiers()` (T3)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the generator actually producing divergent cases`, `the oracle being independent of the implementation`

## Goal

Generate datoms whose business time and transaction time deliberately disagree,
and assert visibility against an independently-derived oracle — so that a green
suite means the axes are independently correct rather than that they happened to
agree.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/temporal/divergence_test.go` | add | The property suite and its generator. |
| `internal/core/temporal/oracle_test.go` | add | A deliberately naive, slow reference implementation of visibility, written from ADR-002's rules rather than from `Visible`'s code. |

★ This task exists because of a measured failure, not a hypothetical one. ADR-002
records that the predecessor project's ~140 tests, including `-race`, stayed green
across the two-axis defect **because every one of them happened to write with
`valid_from == tx_time`** — the two parameters were never actually different in
any test, so the bug had structurally no test that could see it. A hand-written
fixture encodes what its author expected and therefore cannot falsify the
expectation. Only generated divergence can.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestGeneratorProducesDivergentCases`, `TestVisibleAgreesWithOracle`, `TestNoGeneratedCaseHasAgreeingAxes`. Run the Acceptance fence and confirm it is red. [proof: acceptance]
2. [S2] Write the generator: it produces a datom with a business interval and a `TxID` drawn **independently**, so `ValidFrom` is routinely far from the wall time the `TxID` was minted at — backdated, future-dated, and overlapping cases all appear.
3. [S3] Write the oracle in `oracle_test.go` as a direct, slow, obviously-correct transcription of ADR-002's rules: the interval contains `ValidAt`, and the `TxID` does not exceed `AsOf`. It must be written from the record, never by calling or copying `Visible`, or it proves only that the code equals itself.
4. [S4] Assert `Visible` agrees with the oracle across generated cases and all four qualifier combinations from rule 6's table.
5. [S5] ★ Add the meta-assertion `TestNoGeneratedCaseHasAgreeingAxes`: fail the suite if the generator produces a corpus in which business time and transaction time always agree. This is the guard against the exact failure mode above — a property suite whose generator is too tame is indistinguishable from the hand-written fixtures it replaced. [proof: mutation]
6. [S6] Seed the generator deterministically and print the seed on failure, so a discovered counterexample is reproducible rather than folklore.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/temporal/... -run 'TestGenerator|TestVisibleAgreesWithOracle|TestNoGeneratedCase' -count=1 2>&1 | tee /tmp/adr002-t4.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr002-t4.out \
  && go test ./internal/core/temporal/... ./internal/core/tx/... ./internal/core/hlc/... -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestGeneratorProducesDivergentCases` | `internal/core/temporal/divergence_test.go` | The generated corpus contains backdated, future-dated and agreeing cases, in non-trivial proportion | — | S2 |
| `TestVisibleAgreesWithOracle` | `internal/core/temporal/divergence_test.go` | `Visible` matches the independently-written oracle over every generated case and all four qualifier combinations | — | S3, S4 |
| `TestNoGeneratedCaseHasAgreeingAxes` | `internal/core/temporal/divergence_test.go` | The suite fails if the generator degenerates to agreeing axes — the guard on the guard | — | S5 |
| `TestCounterexampleIsReproducible` | `internal/core/temporal/divergence_test.go` | A failure prints a seed which, replayed, reproduces the same case | — | S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests above. |
| 2 — something selects it | This task's product *is* a check; `go test ./internal/core/temporal/...` selects it, and it runs in T3's fence as well as its own. |
| 3 — the caller can discover it | n/a: no declared interface — this is a test suite, not a package surface. |
| 4 — it is used | It runs in CI on every change to `internal/core/temporal`, which is where its value is realised. |

## Mutation Log

## Invariants

- The oracle is written from ADR-002's record, never from `Visible`'s implementation. A test that calls the code it is testing proves the code equals itself.
- The generator draws business time and transaction time independently. If they are ever coupled "for realism", this suite stops being able to see the defect it exists for.
- Failures are reproducible: the seed is printed.

## Risks

- A property suite can be green because the generator is weak, which looks exactly like a property suite that is green because the code is right. `TestNoGeneratedCaseHasAgreeingAxes` is the answer, and it is why that assertion carries `[proof: mutation]` — the mutant to run is one that tames the generator, and the suite must go red.
- The oracle could be written by copying `Visible`, which would make the whole task decorative. This is not mechanically checkable; it is a review obligation, stated here so a reviewer knows to look.

## Stop Condition

Stop if the generator cannot be made to produce divergent cases without knowing
something about the storage layer — that would mean visibility is not in fact a
pure function of a datom and a query, which contradicts T3's design and is a
finding about ADR-002 rather than about this task.

## Out of Scope

- Performance of the property suite. It is allowed to be slow; the oracle is deliberately naive.
- Fuzzing the encoding — that belongs with T2, which owns the encoding.

## Verification Log
