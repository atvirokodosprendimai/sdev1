# ADR-015 Tasks

Implementation tasks for ADR-015: Shed reads by withdrawing from the queue, with
separate budgets and hysteresis. See the parent ADR for the decision.

**Source of truth:** the task files' headers. This README is a derived index —
when it disagrees with a task file, the task file wins.

## Execution Order

Two tasks, sequential.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Two budgets that share nothing, and a ceiling with two thresholds | done | — | `go test ./internal/core/admit/... -race -run 'TestReadSheddingNeverStops\|TestBudgetsShareNoState\|TestInvertedThresholds\|TestCeilingMustBeDeclared\|TestUtilisationIsAFraction'` |
| T2 | Withdraw from the read queue, rejoin well below, and say so | done | — | `go test ./internal/core/admit/... -race -run 'TestNodeBetweenThresholds\|TestWithdrawalStopsOnly\|TestStateChangesAreDeclared\|TestRisingAndFallingLoad\|TestWriteBudgetNeverWithdraws'` then the observe suite |

Status: `pending` | `partial` | `blocked` | `done`.

⚠ **Nothing measures bandwidth yet.** The budgets are told their utilisation;
populating them needs a transport (`BACKLOG.md` §18). The decision logic is
decidable now and the measurement is not.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `admit.Ceiling`, `admit.Budget`, `admit.Controller` | T2 | T1 before T2 |

## Notes

- ⚠ **Read and write budgets share NOTHING, and that is the record's central
  claim.** A leaf has one writer, so a shed write is an outage rather than a
  re-route, while a read can be served by any replica. Under one budget a read
  storm becomes a write outage — a load spike turning into data not being
  accepted, which is the failure that actually matters.
- **Shedding is WITHDRAWAL, not refusal.** A loaded node stops pulling work from
  the shared queue; the client is told nothing and retries nothing, and a peer
  serves it. Returning an error would invite a retry that arrives at the same
  node and makes it worse, exactly when it is least able to cope.
- ⚠ **Two thresholds, and the band between them is the mechanism.** Withdrawing
  and rejoining at one level oscillates by construction: the node rejoins at the
  level that made it leave, takes a burst, and leaves again — and the flapping
  costs more than the load did. A test that samples only above and only below
  proves nothing about the band, which is the only place the two differ in effect.
- **The ceiling is DECLARED, not measured.** A node that discovers its own
  ceiling discovers it by exceeding it, so the discovery IS the incident. An
  operator states it; a wrong ceiling is an operator's error and is the better of
  the two failures.
- **The ceiling is on bandwidth, not request count.** A degraded read pulls `k`
  fragments across `k` failure domains and a healthy one pulls one, so a count
  treats a cluster running on parity as if it were healthy — which is precisely
  when it is not.
- **Admission reads ADR-012's counters and keeps none of its own.** Two counts of
  one quantity diverge, and the one an operator sees would not be the one that
  shed.
- T2 adds two kinds to ADR-012's closed vocabulary rather than emitting ad hoc,
  and that record's both-ways check fails if either lacks a reader.
