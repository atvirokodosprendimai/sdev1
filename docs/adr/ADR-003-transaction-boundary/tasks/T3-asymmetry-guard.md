# Task ADR-003-T3: The structural guard that keeps the asymmetry real

**Depends-on:** T1, T2
**Covers:** none — no spec
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** the read/write asymmetry guard
**Consumes:** `ports.Reader`, `ports.Store` (T1); `command.Transaction` (T2)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the scan covering every package`, `the exemption being narrow`

## Goal

Fail the build when a package that reads is handed something that can write, so
the asymmetry stays true as the codebase grows rather than only on the day it
was written.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/ports/asymmetry_test.go` | add | The scan and its exemption list. |

★ This task's product IS a check, which makes its own reachability unusual: the
thing that selects it is `go test`, and its value is realised on every future
change rather than on this one.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestNoReadPackageDependsOnWriter`, `TestExemptionListIsExhaustive`, `TestGuardScansEveryPackage`, `TestGuardFlagsAKnownOffender`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Implement the scan: walk every Go file under `internal/`, and flag any file that names `ports.Writer` or `ports.Store` unless its package is on the exemption list.
3. [S3] Keep the exemption list explicit and short — the write path only. An exemption is a decision, so it is written down with a reason beside it rather than inferred from a path pattern.
4. [S4] ★ Add `TestGuardScansEveryPackage`: assert the walk actually visited more than a trivial number of files. A guard whose scan silently matches nothing reads exactly like a guard that passed, and that is the failure this class of test has. [proof: mutation]
5. [S5] Assert the exemption list contains no entry for a package that does not exist, so a stale exemption cannot quietly widen the guard's blind spot.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/ports/... -run 'TestNoReadPackage|TestExemption|TestGuard' -count=1 2>&1 | tee /tmp/adr003-t3.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr003-t3.out \
  && go test ./internal/... -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestNoReadPackageDependsOnWriter` | `internal/core/ports/asymmetry_test.go` | No package outside the exemption list names a writable port, so a read model cannot acquire one by import | — | S2, S3 |
| `TestExemptionListIsExhaustive` | `internal/core/ports/asymmetry_test.go` | Every exemption names a package that exists, so a stale entry cannot silently widen the blind spot | — | S5 |
| `TestGuardScansEveryPackage` | `internal/core/ports/asymmetry_test.go` | The walk visited a non-trivial number of files — the assertion that separates "the guard found nothing" from "the guard looked at nothing" | — | S4 |
| `TestGuardFlagsAKnownOffender` | `internal/core/ports/asymmetry_test.go` | The guard's own logic, run against a file that SHOULD be flagged and two that should not — the positive control without which every other assertion here is unfalsifiable, because the tree contains no offender | — | S2, S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The three tests above. |
| 2 — something selects it | `go test ./internal/core/ports/...` selects it, and it runs in T1's and T2's fences as well as its own. |
| 3 — the caller can discover it | n/a: no declared interface — this is a guard, not a package surface. Its exemption list is the thing a reader consults. |
| 4 — it is used | It runs on every change under `internal/`, which is where its value is realised rather than here. |

## Mutation Log

- 2026-09-04 · cbd49ea* · mutant killed · exit 1 · `internal/core/ports/asymmetry_test.go` · narrowing the walk to the guard own package makes it report clean forever about nothing, which is a source-scanning guard characteristic failure; TestGuardScansEveryPackage must go red · acceptance-sha256:2384ed87baaf9178a29b509bce3ec46682267963d5fa47f8783f2d7c7cf4af9f · covers:the scan covering every package
- 2026-09-04 · cbd49ea* · mutant inconclusive · exit 1 · `internal/core/ports/asymmetry_test.go` · exempting every package makes the guard detect nothing; the clean tree cannot show this, so the positive control TestGuardFlagsAKnownOffender is what must go red · acceptance-sha256:2384ed87baaf9178a29b509bce3ec46682267963d5fa47f8783f2d7c7cf4af9f · covers:the exemption being narrow
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-04 · cbd49ea* · mutant killed · exit 1 · `internal/core/ports/asymmetry_test.go` · second attempt; the first was INCONCLUSIVE because it left pkg unused and did not compile. Exempting every package makes the guard detect nothing, and the clean tree cannot show that — the positive control TestGuardFlagsAKnownOffender is what must go red · acceptance-sha256:2384ed87baaf9178a29b509bce3ec46682267963d5fa47f8783f2d7c7cf4af9f · covers:the exemption being narrow

## Invariants

- The exemption list is explicit, short, and carries a reason per entry.
- The scan covers every Go file under `internal/`, and asserts that it did.
- A guard that finds nothing must be distinguishable from a guard that looked at nothing.

## Risks

- ★ A source-scanning guard's characteristic failure is a scan whose universe is empty or wrongly filtered: it reports clean, forever, about nothing. `TestGuardScansEveryPackage` is the answer and is why S4 carries `[proof: mutation]` — the mutant to run narrows the walk, and the suite must go red.
- ★ AND THE SECOND CHARACTERISTIC FAILURE, found while writing this task: A NEGATIVE ASSERTION OVER A CLEAN TREE IS UNFALSIFIABLE. With no offender present, `TestNoReadPackageDependsOnWriter` passes whether the guard works or not — widening the exemption check to exempt everything would not fail it. The guard's logic is therefore extracted and run against a KNOWN-BAD input as well as the real tree. A guard with no positive control cannot tell "nothing is wrong" from "nothing is being checked", and both read as green.
- The scan matches identifier names in source text, so it can be defeated by an alias or an indirection. It is a guard against carelessness rather than against determination, and saying so is more useful than implying otherwise.

## Stop Condition

Stop and ask if the write path grows past one or two packages. A long exemption
list means the asymmetry has stopped being structural and has become paperwork,
and the right response is to reconsider the boundary rather than to keep adding
entries.

## Out of Scope

- Enforcing the asymmetry across module boundaries — the scan covers this module.
- Any runtime check; this guard is a build-time property and a runtime one would be a different mechanism with different costs.

## Verification Log
- 2026-09-04 · cbd49ea* · exit 0 · `set -o pipefail …` · acceptance-sha256:2384ed87baaf9178a29b509bce3ec46682267963d5fa47f8783f2d7c7cf4af9f · ms:1287
- 2026-09-04 · cbd49ea* · exit 0 · `set -o pipefail …` · acceptance-sha256:2384ed87baaf9178a29b509bce3ec46682267963d5fa47f8783f2d7c7cf4af9f · ms:1301
- 2026-09-04 · cbd49ea* · exit 0 · `set -o pipefail …` · acceptance-sha256:2384ed87baaf9178a29b509bce3ec46682267963d5fa47f8783f2d7c7cf4af9f · ms:1301
- 2026-09-04 · cbd49ea* · exit 0 · `set -o pipefail …` · acceptance-sha256:2384ed87baaf9178a29b509bce3ec46682267963d5fa47f8783f2d7c7cf4af9f · ms:1169
