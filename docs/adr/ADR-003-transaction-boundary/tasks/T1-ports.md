# Task ADR-003-T1: The read/write port asymmetry

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `ports.Reader`, `ports.Writer`, `ports.Store`, `ports.Publisher`, `ports.Snapshot`
**Consumes:** `addr.LeafID` from ADR-001; `tx.TxID` from ADR-002
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `Store being exactly both halves`, `the notification carrying an identifier rather than state`, `retraction being an explicit flag`

## Goal

Put the read/write split in the type system, so that a read model cannot write
because it was never handed anything that can.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/ports/ports.go` | add | `Reader`, `Writer`, `Store`, `Publisher`, `Snapshot`, and the datom the ports carry. |
| `internal/core/ports/doc.go` | add | Package comment: why the asymmetry is the point, and what a read model is deliberately not given. |
| `internal/core/ports/ports_test.go` | add | The tests below, including the falsifier named in ADR-003's `Enforced-by:`. |

★ This package contains interfaces and nothing else. It is where a rule that
would otherwise live in prose becomes a compile error, so keeping it free of
implementation is what keeps the rule cheap to enforce.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestReadModelCannotWrite`, `TestReaderHasNoWriteMethod`, `TestStoreSatisfiesBothHalves`, `TestSnapshotCarriesATransactionIdentifier`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define the datom the ports carry: entity, attribute, value, the validity interval, the transaction identifier, and the assert/retract flag. A retraction is a datom with the flag cleared, never an absence.
3. [S3] Define `Reader` with load operations only, and `Snapshot` as a transaction identifier plus the business instant a read is evaluated at — a snapshot is those two values and the existing visibility predicate, not a new mechanism.
4. [S4] Define `Writer` with append only, `Store` as both halves, and `Publisher` as a notification carrying an identifier rather than state.
5. [S5] Write `TestReadModelCannotWrite` as a COMPILE-TIME assertion: a function taking a `Reader` must not be able to reach a write, and the test states that in a form a reviewer can check by reading it. [proof: human: a reviewer confirms the assertion rests on the interface's method set rather than on a runtime check that could be removed]
6. [S6] Write the package comment stating that the asymmetry is the rule and prose is not. [proof: human: a reader confirms the comment explains why a read model is handed the narrow half rather than restating the interfaces]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/ports/... -run 'TestReadModel|TestReader|TestStore|TestSnapshot|TestWriter|TestPublisher|TestDatom' -count=1 2>&1 | tee /tmp/adr003-t1.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr003-t1.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestReadModelCannotWrite` | `internal/core/ports/ports_test.go` | A value satisfying `Reader` does not satisfy `Writer`, checked by reflection over the interface method sets. **The falsifier ADR-003 names in `Enforced-by:`** | — | S3, S5 |
| `TestReaderHasNoWriteMethod` | `internal/core/ports/ports_test.go` | `Reader`'s method set contains no method that mutates — enumerated by name, so adding one to `Reader` fails rather than silently widening what every read model may do | — | S3 |
| `TestStoreSatisfiesBothHalves` | `internal/core/ports/ports_test.go` | `Store` is exactly `Reader` plus `Writer`, so the write path gets both and nothing else has to be assembled by hand | — | S4 |
| `TestSnapshotCarriesATransactionIdentifier` | `internal/core/ports/ports_test.go` | A snapshot is a transaction identifier and a business instant — the two values ADR-002's visibility predicate needs, and nothing more | — | S3 |
| `TestPublisherCarriesAnIdentifierNotState` | `internal/core/ports/ports_test.go` | The publisher's method takes an identifier, so a notification cannot carry rendered state that a slow consumer would apply out of order | — | S4 |
| `TestDatomCarriesRetractionExplicitly` | `internal/core/ports/ports_test.go` | A retraction is a datom with a cleared flag rather than an absence, so "no longer true" is distinguishable from "never recorded" | — | S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The six unit tests above. |
| 2 — something selects it | T2's `command.Transaction` is constructed against these types and T3's guard scans for their use; both fences build against this package. |
| 3 — the caller can discover it | Exported doc comments; `go doc ./internal/core/ports` is the check, and for an interfaces-only package the doc IS the contract. |
| 4 — it is used | Nothing measures this yet. |

## Mutation Log

- 2026-09-04 · cbd49ea* · mutant inconclusive · exit 1 · `internal/core/ports/ports.go` · embedding Writer in Reader means everything handed a Reader can write, and the whole read/write split becomes decorative; TestReadModelCannotWrite must go red · acceptance-sha256:657e64ec3addc941b81e6ed9c220f3c4dbbeaa69792b89ac1453c50ecfbab563 · covers:the absence of a write method on Reader
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-04 · cbd49ea* · mutant killed · exit 1 · `internal/core/ports/ports.go` · a notification carrying rendered state lets a slow consumer apply an older render over a newer one; TestPublisherCarriesAnIdentifierNotState must go red · acceptance-sha256:657e64ec3addc941b81e6ed9c220f3c4dbbeaa69792b89ac1453c50ecfbab563 · covers:the notification carrying an identifier rather than state
- 2026-09-04 · cbd49ea* · mutant killed · exit 1 · `internal/core/ports/ports.go` · without the flag a retraction could only be expressed as an absent datom, which is indistinguishable from a fact that was never recorded; TestDatomCarriesRetractionExplicitly must go red · acceptance-sha256:657e64ec3addc941b81e6ed9c220f3c4dbbeaa69792b89ac1453c50ecfbab563 · covers:retraction being an explicit flag
- 2026-09-04 · cbd49ea* · mutant killed · exit 1 · `internal/core/ports/ports.go` · a Store missing its Writer half leaves the write path unable to append, and the failure would surface at a distant call site rather than here; TestStoreSatisfiesBothHalves must go red · acceptance-sha256:657e64ec3addc941b81e6ed9c220f3c4dbbeaa69792b89ac1453c50ecfbab563 · covers:Store being exactly both halves
- 2026-09-04 · cbd49ea* · mutant killed · exit 1 · `internal/core/ports/ports.go` · re-bound: a notification carrying rendered state lets a slow consumer apply an older render over a newer one · acceptance-sha256:657e64ec3addc941b81e6ed9c220f3c4dbbeaa69792b89ac1453c50ecfbab563 · covers:the notification carrying an identifier rather than state
- 2026-09-04 · cbd49ea* · mutant killed · exit 1 · `internal/core/ports/ports.go` · re-bound: without the flag a retraction could only be an absent datom, indistinguishable from a fact never recorded · acceptance-sha256:657e64ec3addc941b81e6ed9c220f3c4dbbeaa69792b89ac1453c50ecfbab563 · covers:retraction being an explicit flag

## Invariants

- `Reader` has no method that mutates. This is checked by enumerating its method set, so widening it is a test failure rather than a review miss.
- A read model is handed `Reader`. The write path is handed `Store`. Nothing hands a read model a `Store` for convenience.
- A publisher notification carries an identifier, never state.
- A retraction is an explicit flag on a datom, never an absent datom.
- This package holds interfaces and types only. No I/O, no implementation.

## Risks

- A reflection-based method-set assertion is coarser than a compile failure, and it can be weakened by editing the test rather than the interface. That is a review obligation.
- ★ `TestReadModelCannotWrite` — this record's own `Enforced-by` check — CANNOT BE BOUND BY A MUTATION, and the reason is structural rather than an oversight. Any mutation making `Reader` writable also stops the test file's stub from satisfying `Reader`, so the mutant does not compile and `adr-verify` grades it INCONCLUSIVE: a skipped mutant wearing a kill's exit code. Binding it would need the interface and its implementations mutated together, which is more than one file. Measured 2026-09-04. The assertion is still the right one and still runs; what is missing is proof that it can fail, and `Rests-on` therefore does not claim it. ⚠Do not "fix" this by mutating the test itself — that measures the test against itself and proves nothing.
- Interfaces defined before any implementation exists routinely turn out slightly wrong. They are deliberately minimal for that reason — widening later is additive, and narrowing is not.

## Stop Condition

Stop and ask if a read model turns out to need a write for its own bookkeeping —
a checkpoint, a cursor, a materialised position. That is a real need and it must
NOT be met by handing it a `Store`; it needs a separate narrow port owned by
whichever record introduces it, or the asymmetry is decorative.

## Out of Scope

- Any implementation of these ports — storage is ADR-005's.
- The subscription that drives a read model — that is ADR-010's.

## Verification Log
- 2026-09-04 · cbd49ea* · exit 0 · `set -o pipefail …` · acceptance-sha256:657e64ec3addc941b81e6ed9c220f3c4dbbeaa69792b89ac1453c50ecfbab563 · ms:571
- 2026-09-04 · cbd49ea* · exit 0 · `set -o pipefail …` · acceptance-sha256:657e64ec3addc941b81e6ed9c220f3c4dbbeaa69792b89ac1453c50ecfbab563 · ms:564
- 2026-09-04 · cbd49ea* · exit 0 · `set -o pipefail …` · acceptance-sha256:657e64ec3addc941b81e6ed9c220f3c4dbbeaa69792b89ac1453c50ecfbab563 · ms:623
- 2026-09-04 · cbd49ea* · exit 0 · `set -o pipefail …` · acceptance-sha256:657e64ec3addc941b81e6ed9c220f3c4dbbeaa69792b89ac1453c50ecfbab563 · ms:564
- 2026-09-04 · cbd49ea* · exit 0 · `set -o pipefail …` · acceptance-sha256:657e64ec3addc941b81e6ed9c220f3c4dbbeaa69792b89ac1453c50ecfbab563 · ms:530
- 2026-09-04 · cbd49ea* · exit 0 · `set -o pipefail …` · acceptance-sha256:657e64ec3addc941b81e6ed9c220f3c4dbbeaa69792b89ac1453c50ecfbab563 · ms:599
- 2026-09-04 · cbd49ea* · exit 0 · `set -o pipefail …` · acceptance-sha256:657e64ec3addc941b81e6ed9c220f3c4dbbeaa69792b89ac1453c50ecfbab563 · ms:577
- 2026-09-04 · cbd49ea* · exit 0 · `set -o pipefail …` · acceptance-sha256:657e64ec3addc941b81e6ed9c220f3c4dbbeaa69792b89ac1453c50ecfbab563 · ms:655
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:657e64ec3addc941b81e6ed9c220f3c4dbbeaa69792b89ac1453c50ecfbab563 · ms:597
