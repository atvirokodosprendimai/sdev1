# Task ADR-003-T2: The single-entity transaction, and its refusal

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `command.Transaction`, `command.New()`, `command.ErrCrossEntity`, `Transaction.Assert()`, `Transaction.Retract()`
**Consumes:** `ports.Datom`, `ports.Store` (T1); `addr.KeyOf`, `addr.Descend` from ADR-001; `tx.TxID` from ADR-002
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the cross-entity refusal`, `the refusal happening before anything is written`

## Goal

Make a transaction structurally unable to touch a second entity, so that the
property removing distributed commit from the whole system is enforced rather
than trusted.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/command/command.go` | add | `Transaction`, `New`, `Assert`, `Retract`, and the refusal. |
| `internal/core/command/doc.go` | add | Package comment: what the boundary buys, what it costs the caller, and why it is a refusal rather than a convention. |
| `internal/core/command/command_test.go` | add | The tests below. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestNewBindsOneEntity`, `TestAssertRefusesASecondEntity`, `TestRetractRefusesASecondEntity`, `TestRefusalHappensBeforeAnythingIsRecorded`, `TestRetractionIsADatomNotAnAbsence`, `TestTransactionResolvesToOneLeaf`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Transaction` bound to one entity at construction: `New(entity string)` fixes the entity and the leaf it resolves to, and there is no method that changes either.
3. [S3] Implement `Assert` and `Retract`, each taking an attribute and a validity interval. Both refuse a datom naming a different entity with `ErrCrossEntity`, naming both entities in the message so the caller can see what they attempted.
4. [S4] Make the refusal happen BEFORE the datom is recorded, so a rejected transaction carries no partial state and can be discarded without cleanup.
5. [S5] Record a retraction as a datom with the assert flag cleared, never by omitting one — "no longer true" and "never recorded" are different facts and only the first is expressible by an absence in a mutable store.
6. [S6] Write the package comment stating what the caller loses: an operation spanning entities becomes several transactions plus a compensating one, and its intermediate states are visible. [proof: human: a reader confirms the comment states the COST to the caller, not only the benefit to the system]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/command/... -run 'TestNew|TestAssert|TestRetract|TestRefusal|TestTransaction|TestDatoms' -count=1 2>&1 | tee /tmp/adr003-t2.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr003-t2.out \
  && go test ./internal/core/ports/... ./internal/core/addr/... ./internal/core/tx/... -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestNewBindsOneEntity` | `internal/core/command/command_test.go` | A transaction's entity is fixed at construction and no method changes it | — | S2 |
| `TestAssertRefusesASecondEntity` | `internal/core/command/command_test.go` | Asserting about a different entity returns `ErrCrossEntity` — the refusal that removes distributed commit from the system | — | S3 |
| `TestRetractRefusesASecondEntity` | `internal/core/command/command_test.go` | Retraction is refused on the same grounds, so the boundary has no back door | — | S3 |
| `TestRefusalHappensBeforeAnythingIsRecorded` | `internal/core/command/command_test.go` | A refused operation leaves the transaction's datom count unchanged, so a rejected transaction carries no partial state | — | S4 |
| `TestRetractionIsADatomNotAnAbsence` | `internal/core/command/command_test.go` | A retraction appears as a datom with the flag cleared, so "no longer true" is distinguishable from "never recorded" | — | S5 |
| `TestTransactionResolvesToOneLeaf` | `internal/core/command/command_test.go` | Every datom in a transaction resolves to the same leaf, which is what makes the commit single-leaf and therefore local | — | S2 |
| `TestDatomsIsACopy` | `internal/core/command/command_test.go` | A caller mutating the returned slice cannot change the transaction's own datoms, so the boundary cannot be defeated after the fact | — | S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The six unit tests above. |
| 2 — something selects it | T3's structural guard scans for callers constructing transactions; the write path will be the production caller once ADR-005 and ADR-009 exist. |
| 3 — the caller can discover it | Exported doc comments and a named sentinel error; `go doc ./internal/core/command` is the check, and `ErrCrossEntity` is what a caller matches on. |
| 4 — it is used | Nothing measures this yet. |

## Mutation Log

- 2026-09-04 · cbd49ea* · mutant killed · exit 1 · `internal/core/command/command.go` · without the refusal a transaction silently spans entities and therefore leaves, and every commit becomes a distributed one; TestAssertRefusesASecondEntity and TestRetractRefusesASecondEntity must go red · acceptance-sha256:c3c75cb7e4805cdafc711fc0bc43d0f16a738245c024fddc2d688db125f3a127 · covers:the cross-entity refusal
- 2026-09-04 · cbd49ea* · mutant killed · exit 1 · `internal/core/command/command.go` · recording on the refusal path leaves a rejected transaction carrying partial state, so discarding it is no longer safe; TestRefusalHappensBeforeAnythingIsRecorded must go red · acceptance-sha256:c3c75cb7e4805cdafc711fc0bc43d0f16a738245c024fddc2d688db125f3a127 · covers:the refusal happening before anything is written

## Invariants

- A transaction's entity is fixed at construction and cannot be changed.
- Every datom a transaction carries names that entity and resolves to that leaf.
- A refused operation records nothing; the transaction is unchanged and discardable.
- A retraction is an explicit datom, never an omission.

## Risks

- The boundary is the decision most likely to be reopened, because it is the one a domain can find intolerable. The refusal is a named error precisely so that the first genuine case surfaces loudly rather than being worked around silently. `BACKLOG.md` §8 tracks that no real domain has tested it.
- A caller can defeat the boundary by opening several transactions and committing them together at a higher layer. Nothing here can prevent that; ADR-009 owns whether such a group is offered any atomicity, and the answer is expected to be no.

## Stop Condition

Stop and ask if a required domain operation cannot be expressed as one entity per
transaction. That is ADR-003's stated falsifier rather than an inconvenience, and
the answer decides whether the record stands — it is not a case to work around in
this task.

## Out of Scope

- Committing a transaction — persistence is ADR-005's and replication is ADR-009's. This task builds the value a commit will take.
- Validating an assertion against current state — the writer's own index is ADR-005's.

## Verification Log
- 2026-09-04 · cbd49ea* · exit 0 · `set -o pipefail …` · acceptance-sha256:c3c75cb7e4805cdafc711fc0bc43d0f16a738245c024fddc2d688db125f3a127 · ms:1210
- 2026-09-04 · cbd49ea* · exit 0 · `set -o pipefail …` · acceptance-sha256:c3c75cb7e4805cdafc711fc0bc43d0f16a738245c024fddc2d688db125f3a127 · ms:1231
- 2026-09-04 · cbd49ea* · exit 0 · `set -o pipefail …` · acceptance-sha256:c3c75cb7e4805cdafc711fc0bc43d0f16a738245c024fddc2d688db125f3a127 · ms:1232
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:c3c75cb7e4805cdafc711fc0bc43d0f16a738245c024fddc2d688db125f3a127 · ms:1364
