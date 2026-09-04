# ADR-012 Tasks

Implementation tasks for ADR-012: Every component emits one event shape, and a
counter that nothing reads is a defect. See the parent ADR for the decision.

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
| T1 | A closed event vocabulary where every kind names its reader | done | — | `go test ./internal/core/observe/... -race -run 'TestEveryEmittedEvent\|TestUndeclaredKind\|TestDeclarationWithout\|TestEventCarriesTyped\|TestDeclaredKindsAreStable'` |
| T2 | A counter that names the question it answers, and a stream that cannot stall a request | done | — | `go test ./internal/core/observe/... -race -run 'TestCounterWithout\|TestEmissionNeverBlocks\|TestDroppedEvents\|TestDropCounterIsItself\|TestCountersAreStable'` then the whole package |

Status: `pending` | `partial` | `blocked` | `done`.

⚠ **This record covers what may be EMITTED, not a console.** There is no
transport, so the vocabulary and the checks on it are decidable now and rendering
is not.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `observe.Kind`, `observe.Event`, `observe.Declaration`, `observe.Register` | T2 | T1 before T2 |

## Notes

- ⚠ **A counter nobody reads is worse than no counter.** It costs emission on a
  hot path, makes a dashboard look thorough, and answers nothing — and it
  SURVIVES forever, because deleting it looks risky. That is why the rule is at
  declaration time rather than a periodic cleanup: every counter states the
  operator QUESTION it settles, and a counter whose question cannot be written is
  one nobody needs.
- **The question is not a description.** "How many blocks were read" is the
  name. "Is a degraded read costing us more than a healthy one" is the question,
  and only the second says why the number exists.
- ⚠ **A free-form message field turns a vocabulary back into a log.** It is what
  every caller wants during an incident, and once it exists the console is a grep
  again. The vocabulary is closed and an undeclared kind is refused at EMISSION,
  so drift fails where it happens rather than becoming a consumer's problem
  months later.
- ⚠ **The vocabulary check must look BOTH ways.** Every emitted kind needs a
  declaration — that catches drift. Every declaration needs a reader — that
  catches the long tidy list nobody looks at, which is the failure that actually
  accumulates.
- ⚠ **Emission never blocks and never errors into the caller.** Observability
  that can stall the thing it observes is worse than none, and a blocking emit is
  the failure that actually happens rather than a hypothetical one. It drops
  instead — and every drop is COUNTED, because a stream that loses events
  silently is lying exactly under the load an operator is investigating.
- When testing non-blocking emission, fill the buffer first and drain nothing.
  Emitting fewer events than the buffer holds proves nothing, because a blocking
  implementation would not block either.
- This component observes and does not act. Shedding is ADR-015's and refusing a
  write is ADR-004's; a component that did both would make every emission a
  control decision.
