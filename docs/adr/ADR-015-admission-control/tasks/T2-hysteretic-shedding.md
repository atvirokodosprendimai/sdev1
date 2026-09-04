# Task ADR-015-T2: Withdraw from the read queue, rejoin well below, and say so

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `admit.State`, `admit.StateJoined`, `admit.StateWithdrawn`, `admit.Controller.Decide`, `admit.Controller.State`, `observe.KindQueueWithdrawn`, `observe.KindQueueRejoined`
**Consumes:** `admit.Ceiling`, `admit.Budget`, `admit.Controller` (T1), `observe.Kind` and `observe.Register` from ADR-012
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `a node between the two thresholds keeping its current state`, `shedding removing only the pull of new read work`, `every state change being an observable event`

## Goal

Make saturation a routing outcome rather than an error, without letting the node
flap at the threshold.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/admit/shed.go` | add | `State`, `Decide`, and the hysteresis. |
| `internal/core/observe/kinds.go` | edit | Two declared kinds for the state changes, each naming its reader. |
| `internal/core/admit/shed_test.go` | add | The tests below. |

⚠ This task EDITS a file ADR-012 governs. That is deliberate and is what ADR-012
asked for: its vocabulary is closed, so a new event kind is a declaration there
rather than an ad-hoc emission here, and its both-ways check will fail if either
kind lacks a reader.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestNodeBetweenThresholdsKeepsItsState`, `TestWithdrawalStopsOnlyNewReadWork`, `TestStateChangesAreDeclaredEvents`, `TestRisingAndFallingLoadDoNotFlap`, `TestWriteBudgetNeverWithdraws`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `State` with exactly two values, joined and withdrawn.
3. [S3] Implement `Decide`: withdraw above the withdraw threshold, rejoin below the rejoin threshold, and OTHERWISE KEEP THE CURRENT STATE. ★The band between the two is where hysteresis lives — a decision that consulted only one threshold would oscillate, and the flapping costs more than the load did.
4. [S4] Make withdrawal apply to reads only; the write budget never withdraws. ★A leaf has one writer, so a shed write has nowhere to go — it would be an outage rather than a re-route.
5. [S5] Declare `KindQueueWithdrawn` and `KindQueueRejoined` in ADR-012's vocabulary, each naming its reader. [proof: mutation]
6. [S6] Emit a state-change event on every transition, and none when the state is unchanged. ★An event per evaluation would bury the transitions an operator is looking for in a stream of non-events.
7. [S7] Leave work already accepted alone. Withdrawal removes the pull of NEW work, and a withdrawal that abandoned in-flight reads would turn shedding back into the failure it replaces. [proof: human: a reader confirms `Decide` returns a state and touches no in-flight accounting, since nothing here can see in-flight work]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/admit/... -race -run 'TestNodeBetweenThresholds|TestWithdrawalStopsOnly|TestStateChangesAreDeclared|TestRisingAndFallingLoad|TestWriteBudgetNeverWithdraws' -count=1 2>&1 | tee /tmp/adr015-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr015-t2a.out \
  && go test ./internal/core/admit/... ./internal/core/observe/... -race -count=1 2>&1 | tee /tmp/adr015-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr015-t2b.out
```

The second command is load-bearing: ADR-012's both-ways vocabulary check fails if
either new kind is declared without a reader, or named in code without a
declaration.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestNodeBetweenThresholdsKeepsItsState` | `internal/core/admit/shed_test.go` | There are exactly two states, and in the band between rejoin and withdraw a joined node stays joined while a withdrawn node stays withdrawn — the band IS the hysteresis | — | S2, S3 |
| `TestWithdrawalStopsOnlyNewReadWork` | `internal/core/admit/shed_test.go` | A withdrawn node's write budget still admits and its read state is the only thing that changed | — | S4 |
| `TestStateChangesAreDeclaredEvents` | `internal/core/admit/shed_test.go` | Both event kinds are declared in ADR-012's vocabulary with readers, and a transition produces one while a non-transition produces none | — | S5, S6 |
| `TestRisingAndFallingLoadDoNotFlap` | `internal/core/admit/shed_test.go` | A load profile that crosses the withdraw threshold and falls back into the band produces exactly ONE transition, not one per sample | — | S3, S6 |
| `TestWriteBudgetNeverWithdraws` | `internal/core/admit/shed_test.go` | Whatever the write utilisation, the write budget never enters a withdrawn state — a shed write has nowhere to go | — | S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | `Decide` is the only way a state changes, and the two kinds are registered in ADR-012's `kinds.go`, whose both-ways check fails if either is unread. |
| 3 — the caller can discover it | Two named states and two declared events; a caller sees the transition rather than having to poll a number. |
| 4 — it is used | Nothing drives real utilisation yet; that needs a transport. |

## Mutation Log

- 2026-09-04 · aa9ce5e* · mutant killed · exit 1 · `internal/core/admit/shed.go` · collapses the two thresholds into one, so a withdrawn node rejoins at exactly the load that made it leave — it takes a burst, leaves again, and the flapping costs more than the load ever did · acceptance-sha256:9e3be1b5c18def61f6e95b907c6cbb0b790289e1b97b0bdc1c75307635d820fe · covers:a node between the two thresholds keeping its current state
- 2026-09-04 · aa9ce5e* · mutant killed · exit 1 · `internal/core/admit/admit.go` · lets a withdrawn node stop admitting writes, so read saturation becomes a leaf refusing ingest — a shed write has nowhere to go, making it an outage rather than a re-route · acceptance-sha256:9e3be1b5c18def61f6e95b907c6cbb0b790289e1b97b0bdc1c75307635d820fe · covers:shedding removing only the pull of new read work
- 2026-09-04 · aa9ce5e* · mutant killed · exit 1 · `internal/core/admit/shed.go` · emits on every evaluation rather than on every transition, burying the handful of state changes an operator is looking for in a stream of non-events · acceptance-sha256:9e3be1b5c18def61f6e95b907c6cbb0b790289e1b97b0bdc1c75307635d820fe · covers:every state change being an observable event

## Invariants

- Between the thresholds, the current state is kept.
- The write budget never withdraws.
- An event is emitted on transition and never on a non-transition.
- Both event kinds are declared with readers in ADR-012's vocabulary.
- Withdrawal removes the pull of new work and touches nothing in flight.

## Risks

- ⚠ **A hysteresis test that samples only above and only below proves nothing about the band, which is the whole mechanism.** `TestNodeBetweenThresholdsKeepsItsState` samples INSIDE the band from both starting states, because that is the only place the two thresholds differ in effect.
- A flap test that counts transitions over a rising profile can pass for a decision that never withdraws at all. The profile rises past the threshold and falls back, and the test asserts exactly one transition — not zero and not one per sample.
- Emitting on every evaluation rather than every transition would bury the signal an operator wants. The test asserts a non-transition produces NO event, which is the direction that is easy to get wrong.

## Stop Condition

Stop and ask before letting a write be shed, under any condition. A leaf has one
writer, so a shed write is an outage rather than a re-route — and the request
that will ask for it is "we are overloaded, shed everything".

## Out of Scope

- Measuring actual bandwidth (deferred: `docs/adr/BACKLOG.md` §18)
- What happens when every replica sheds at once (deferred: `docs/adr/BACKLOG.md` §22)
- Prioritising between classes of read (deferred: `docs/adr/BACKLOG.md` §22)
- Actually removing a node from a queue, which needs ADR-010's queue to be real (deferred: `docs/adr/BACKLOG.md` §18)

## Verification Log
- 2026-09-04 · aa9ce5e* · exit 0 · `set -o pipefail …` · acceptance-sha256:9e3be1b5c18def61f6e95b907c6cbb0b790289e1b97b0bdc1c75307635d820fe · ms:3566
- 2026-09-04 · aa9ce5e* · exit 0 · `set -o pipefail …` · acceptance-sha256:9e3be1b5c18def61f6e95b907c6cbb0b790289e1b97b0bdc1c75307635d820fe · ms:3500
- 2026-09-04 · aa9ce5e* · exit 0 · `set -o pipefail …` · acceptance-sha256:9e3be1b5c18def61f6e95b907c6cbb0b790289e1b97b0bdc1c75307635d820fe · ms:3317
- 2026-09-04 · aa9ce5e* · exit 0 · `set -o pipefail …` · acceptance-sha256:9e3be1b5c18def61f6e95b907c6cbb0b790289e1b97b0bdc1c75307635d820fe · ms:3375
