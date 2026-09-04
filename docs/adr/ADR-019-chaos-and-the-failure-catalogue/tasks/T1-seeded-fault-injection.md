# Task ADR-019-T1: Seeded fault injection over the core, and the catalogue it fills

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `chaos.Fault`, `chaos.Disposition`, `chaos.Schedule`, `chaos.Registry`, `docs/adr/FAILURES.md`
**Consumes:** `erasure.Encode` and `erasure.Reconstruct` from ADR-006, `tail.Tail` from ADR-017, `durability.Policy` from ADR-004, `segment.Checksum` from ADR-005
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `every injected fault having a catalogue entry`, `a schedule being reproducible from its seed`, `an unrecoverable-by-design fault being distinguished from an open one`

## Goal

Break the packages that exist, from a seed, and write down what does not recover.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/chaos/doc.go` | add | Package comment: what a fault is here, why a seed matters more than realism, and why "unrecoverable by design" is an answer. |
| `internal/core/chaos/chaos.go` | add | `Fault`, `Disposition`, `Schedule`, `Registry`, and the seeded draw. |
| `internal/core/chaos/faults.go` | add | The faults themselves, each naming the record whose promise it tests. |
| `internal/core/chaos/chaos_test.go` | add | The tests below, including the catalogue check named in ADR-019's `Enforced-by:`. |
| `docs/adr/FAILURES.md` | add | The catalogue. It is the deliverable, not a by-product. |

★ `Registry` is what SELECTS a fault. Every fault must be registered to run, and
`TestEveryInjectedFaultIsCatalogued` reads the registry against the document — so
a fault that is written but never registered fails the suite rather than sitting
unreachable, which is this pipeline's most common shipped defect.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestEveryInjectedFaultIsCatalogued`, `TestScheduleIsReproducibleFromItsSeed`, `TestFragmentLossWithinToleranceRecovers`, `TestFragmentLossBeyondToleranceIsUnrecoverableByDesign`, `TestCorruptFragmentRecovers`, `TestWriterStoppedMidAppendLosesNothingPublished`, `TestCatalogueDistinguishesOpenFromByDesign`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Disposition` with exactly three values — recovers, unrecoverable by design, unrecoverable and open — and no fourth. ★A fourth value is how "we are looking into it" enters a catalogue and stops anything being countable.
3. [S3] Define `Fault`: a name, the record whose promise it tests, the injection, and the disposition expected.
4. [S4] Implement `Registry` and require registration for a fault to run.
5. [S5] Implement `Schedule`: a seeded, replayable draw over the registry, printing its seed on failure. ★An unreproducible failure is a report rather than a bug, and the cost of confirming it lands on somebody else.
6. [S6] Implement the fragment faults against ADR-006: lose `m` (recovers), lose `m+1` (unrecoverable by design), corrupt one (recovers, because the checksum makes it an erasure).
7. [S7] Implement the tail fault against ADR-017: stop the writer mid-append and assert every published entry is intact and the unpublished one is unreachable.
8. [S8] Write `docs/adr/FAILURES.md` with one entry per registered fault, and make the check in S9 read it. [proof: human: a reader confirms each entry says what an OPERATOR does, not only what the test asserts]
9. [S9] Implement `TestEveryInjectedFaultIsCatalogued`: fail on a registered fault with no entry AND on an entry naming no registered fault. ★One direction alone is half a gate — the first catches a new fault nobody wrote up, the second catches a fault that quietly stopped being injected while its entry still reads as current.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/chaos/... -race -run 'TestEveryInjectedFault|TestScheduleIsReproducible|TestFragmentLoss|TestCorruptFragment|TestWriterStopped|TestCatalogueDistinguishes' -count=1 2>&1 | tee /tmp/adr019-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr019-t1a.out \
  && go test ./internal/core/erasure/... ./internal/core/tail/... ./internal/core/durability/... -race -count=1 2>&1 | tee /tmp/adr019-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr019-t1b.out
```

The first command is this task's own work and can carry the verdict alone; the
second is the regression half over the packages the faults are aimed at, and
cannot stand in for it because it names none of the new tests.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestEveryInjectedFaultIsCatalogued` | `internal/core/chaos/chaos_test.go` | Every registered fault has an entry in `FAILURES.md` and every entry names a registered fault — both directions, so neither a new fault nor a silently retired one can hide; and each fault names the record whose promise it tests. **The falsifier ADR-019 names in `Enforced-by:`** | — | S3, S4, S9 |
| `TestScheduleIsReproducibleFromItsSeed` | `internal/core/chaos/chaos_test.go` | Two schedules built from one seed draw the same faults in the same order, so a failure is replayable rather than merely reported | — | S5 |
| `TestFragmentLossWithinToleranceRecovers` | `internal/core/chaos/chaos_test.go` | Losing `m` fragments of a stripe still returns the original block, which is ADR-006's central promise under an actual fault | — | S6 |
| `TestFragmentLossBeyondToleranceIsUnrecoverableByDesign` | `internal/core/chaos/chaos_test.go` | Losing `m+1` refuses by name rather than returning something, and is catalogued as intended rather than as a bug | — | S2, S6 |
| `TestCorruptFragmentRecovers` | `internal/core/chaos/chaos_test.go` | A corrupted fragment is excluded by its checksum and the block still reconstructs — the difference between an erasure and an error, under a real injection | — | S6 |
| `TestWriterStoppedMidAppendLosesNothingPublished` | `internal/core/chaos/chaos_test.go` | Stopping a writer leaves every published entry intact and the unpublished one unreachable, which is ADR-017's claim under a fault | — | S7 |
| `TestCatalogueDistinguishesOpenFromByDesign` | `internal/core/chaos/chaos_test.go` | The catalogue's open entries are countable — an entry cannot be both, and there is no fourth disposition to hide in | — | S2, S8 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The seven tests above. |
| 2 — something selects it | `Registry` is the only way a fault runs, and `TestEveryInjectedFaultIsCatalogued` fails if a fault is registered and unwritten or written and unregistered. |
| 3 — the caller can discover it | `FAILURES.md` is the interface for a human; the registry is the interface for the suite. Both are checked against each other. |
| 4 — it is used | The catalogue's entry count is the measurement, and it is meaningful from the first run. |

## Mutation Log

- 2026-09-04 · 28a69a4* · mutant killed · exit 1 · `internal/core/chaos/faults.go` · renames a registered fault so it no longer matches its catalogue entry, which is what happens whenever a fault is added or renamed and the document is not updated with it: the fault runs and what it does is written down nowhere · acceptance-sha256:955ca8dff4ba153acc4d84eb524b18c81664240e8eca503454a1cdecfcb861b9 · covers:every injected fault having a catalogue entry
- 2026-09-04 · 28a69a4* · mutant killed · exit 1 · `internal/core/chaos/chaos.go` · leaves the registry in Go randomised map iteration order so a schedule drawn from one seed differs between runs, which makes every failure found by this suite a report nobody can reproduce · acceptance-sha256:955ca8dff4ba153acc4d84eb524b18c81664240e8eca503454a1cdecfcb861b9 · covers:a schedule being reproducible from its seed
- 2026-09-04 · 28a69a4* · mutant killed · exit 1 · `internal/core/chaos/chaos.go` · renders an open failure as an intended one so the two collapse into a single label, after which the count of things that are actually broken is unrecoverable from the catalogue · acceptance-sha256:955ca8dff4ba153acc4d84eb524b18c81664240e8eca503454a1cdecfcb861b9 · covers:an unrecoverable-by-design fault being distinguished from an open one

## Invariants

- Every registered fault has exactly one catalogue entry, and every entry names a registered fault.
- A schedule is a pure function of its seed.
- There are exactly three dispositions.
- The chaos package asserts what the OWNING record promised; it states no guarantee of its own.
- A fault that cannot be injected reproducibly is not registered.

## Risks

- ⚠ **A catalogue check that only looks one way is half a gate.** Checking that every fault has an entry catches a new fault nobody wrote up; it does not catch a fault that quietly stopped being injected while its entry still reads as current. Both directions are asserted, and the second is the one that rots.
- A fault model built from the same understanding that built the code will miss what that understanding missed. The composed suite (T2) is the only correction, and it is blocked — so entries marked "recovers" here carry an implicit "in simulation", and the record says so.
- A test that injects a fault and asserts recovery can pass because the fault did not actually land. Each fault asserts its own PRECONDITION — that the thing it damaged was really damaged — before asserting anything about recovery.

## Stop Condition

Stop and ask if a fault turns out to be unrecoverable AND open AND cheap to fix.
Fixing it is a change to the record that made the promise, not to this package,
and quietly patching it here would leave that record still claiming something
untrue.

## Out of Scope

- Anything needing more than one process — that is T2, and it is blocked.
- How slow a degraded read is (deferred: `docs/adr/BACKLOG.md` §16)
- Repairing what a fault damaged (deferred: `docs/adr/BACKLOG.md` §3)

## Verification Log
- 2026-09-04 · 28a69a4* · exit 0 · `set -o pipefail …` · acceptance-sha256:955ca8dff4ba153acc4d84eb524b18c81664240e8eca503454a1cdecfcb861b9 · ms:4289
- 2026-09-04 · 28a69a4* · exit 0 · `set -o pipefail …` · acceptance-sha256:955ca8dff4ba153acc4d84eb524b18c81664240e8eca503454a1cdecfcb861b9 · ms:4405
- 2026-09-04 · 28a69a4* · exit 0 · `set -o pipefail …` · acceptance-sha256:955ca8dff4ba153acc4d84eb524b18c81664240e8eca503454a1cdecfcb861b9 · ms:4144
- 2026-09-04 · 28a69a4* · exit 0 · `set -o pipefail …` · acceptance-sha256:955ca8dff4ba153acc4d84eb524b18c81664240e8eca503454a1cdecfcb861b9 · ms:4247
- 2026-09-04 · 28a69a4* · exit 0 · `set -o pipefail …` · acceptance-sha256:955ca8dff4ba153acc4d84eb524b18c81664240e8eca503454a1cdecfcb861b9 · ms:4178
