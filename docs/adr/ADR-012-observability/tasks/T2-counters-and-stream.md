# Task ADR-012-T2: A counter that names the question it answers, and a stream that cannot stall a request

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `observe.Counter`, `observe.RegisterCounter`, `observe.Counters`, `observe.Stream`, `observe.NewStream`, `observe.Stream.Dropped`, `observe.ErrNoQuestion`
**Consumes:** `observe.Kind`, `observe.Event`, `observe.Declaration` (T1)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `a counter without a stated question being refused`, `emission never blocking the caller`, `dropped events being counted rather than lost silently`

## Goal

Stop the dashboard filling with numbers nobody reads, and stop the observability
path from being able to stall the thing it observes.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/observe/counter.go` | add | `Counter`, `RegisterCounter`, `Counters`, and `ErrNoQuestion`. |
| `internal/core/observe/stream.go` | add | `Stream`, non-blocking emission, and the dropped count. |
| `internal/core/observe/counter_test.go` | add | The tests below. |

★ A counter declares the OPERATOR QUESTION it settles, not a description of what
it counts — the name already says that. A counter whose question cannot be
written is a counter nobody needs, and writing the question is where that becomes
obvious.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestCounterWithoutAQuestionIsRefused`, `TestEmissionNeverBlocksTheCaller`, `TestDroppedEventsAreCounted`, `TestDropCounterIsItselfDeclared`, `TestCountersAreStableAndOrdered`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Counter` with a name, the operator question it answers, and a value.
3. [S3] Refuse a registration whose question is empty with `ErrNoQuestion`. ★A description of what it counts is not a question. "How many blocks were read" is the name; "is a degraded read costing us more than a healthy one" is the question, and only the second says why the number exists.
4. [S4] Implement `Stream` with a bounded buffer, emitting without blocking. [proof: mutation]
5. [S5] Drop rather than block when the buffer is full. ★Observability that can stall the served path is worse than none, and a blocking emit is the failure that actually happens rather than a hypothetical one.
6. [S6] Count every drop, and declare the drop counter with its own reader and question. ★A stream that loses events silently is lying exactly under the load an operator is investigating.
7. [S7] Return counters and declarations in a stable order, so a consumer and a test see the same set each run.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/observe/... -race -run 'TestCounterWithout|TestEmissionNeverBlocks|TestDroppedEvents|TestDropCounterIsItself|TestCountersAreStable' -count=1 2>&1 | tee /tmp/adr012-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr012-t2a.out \
  && go test ./internal/core/observe/... -race -count=1 2>&1 | tee /tmp/adr012-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr012-t2b.out
```

The second command runs T1's vocabulary checks too, which matters here: T2 adds a
declared counter, and the both-ways vocabulary check fails if it has no reader.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestCounterWithoutAQuestionIsRefused` | `internal/core/observe/counter_test.go` | A counter registered with no operator question yields `ErrNoQuestion`, so the usual drift towards unread numbers is closed where it starts | — | S2, S3 |
| `TestEmissionNeverBlocksTheCaller` | `internal/core/observe/counter_test.go` | With the buffer full and no consumer, many concurrent emissions all return promptly — observability cannot stall the served path | — | S4, S5 |
| `TestDroppedEventsAreCounted` | `internal/core/observe/counter_test.go` | Events dropped for a full buffer are counted exactly, so a stream under load reports what it lost rather than lying | — | S5, S6 |
| `TestDropCounterIsItselfDeclared` | `internal/core/observe/counter_test.go` | The drop counter is registered with its own reader and question, so the count that reveals lost events is not itself an unread number | — | S6 |
| `TestCountersAreStableAndOrdered` | `internal/core/observe/counter_test.go` | Counters and declarations come back in a stable order rather than Go's randomised map order | — | S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | `RegisterCounter` is the only way a counter exists, and `Stream.Emit` the only way an event reaches a sink; deleting the question check breaks the first test. |
| 3 — the caller can discover it | `Counters()` returns each counter WITH its question, so a reader of the list learns why each number is there. |
| 4 — it is used | The drop count is a real measurement from the first run. |

## Mutation Log

- 2026-09-04 · 15ace10* · mutant killed · exit 1 · `internal/core/observe/counter.go` · accepts a counter that answers no stated question, after which the dashboard fills with numbers nobody can justify — and each survives forever, because deleting a metric looks risky and nobody can prove it is unused · acceptance-sha256:24bb04b00f9015de24a3b03fc5aee5dac671b5c3b269264c85f162ef4589e2a3 · covers:a counter without a stated question being refused
- 2026-09-04 · 15ace10* · mutant killed · exit 1 · `internal/core/observe/stream.go` · makes emission block on a full buffer, so the observability path can stall the served path — the failure that actually happens, and precisely under the load an operator is investigating · acceptance-sha256:24bb04b00f9015de24a3b03fc5aee5dac671b5c3b269264c85f162ef4589e2a3 · covers:emission never blocking the caller
- 2026-09-04 · 15ace10* · mutant killed · exit 1 · `internal/core/observe/stream.go` · drops events without counting them, so a stream under load loses data and reports nothing — lying exactly when an operator most needs to trust it · acceptance-sha256:24bb04b00f9015de24a3b03fc5aee5dac671b5c3b269264c85f162ef4589e2a3 · covers:dropped events being counted rather than lost silently

## Invariants

- A counter without a stated operator question is refused.
- Emission never blocks and never errors into the caller.
- Every dropped event is counted.
- The drop counter is itself declared, with a reader and a question.
- Counters and declarations are returned in a stable order.

## Risks

- ⚠ **A non-blocking test that emits fewer events than the buffer holds proves nothing.** `TestEmissionNeverBlocksTheCaller` fills the buffer first and emits with NO consumer draining, which is the only shape where a blocking implementation would actually block.
- ⚠ **A test for "this never blocks" must not block while finding out, and the first version did.** Written the obvious way — emitting synchronously into a full stream — the blocking mutant did not FAIL the suite, it HUNG it: the run burned Go's full ten-minute timeout, buried the reason in a goroutine dump, and would have come back `inconclusive` rather than `killed`. Every emission into a possibly-full buffer now goes through a `promptly` helper that bounds its own wait, so a blocking implementation fails in seconds. Found by running the mutant, which is the only way this shows up.
- A drop-count test that drops one event can pass for an off-by-one. The test drops a known larger number and asserts the count exactly, including that nothing was dropped before the buffer filled.
- ⚠ A "counter has a question" check is easy to satisfy with the counter's own name repeated. Nothing mechanical can judge that, and the record says so: the rule buys the moment of writing it, not a proof that it was written well.

## Stop Condition

Stop and ask before making emission blocking, even under a flag. A flag that
makes observability able to stall the served path will be turned on during an
incident, which is exactly when it must not be.

## Out of Scope

- Rendering a console (deferred: `docs/adr/BACKLOG.md` §18)
- Export to an external metrics system, sampling and retention (deferred: `docs/adr/BACKLOG.md` §21)
- Deciding what to do about what is counted (permanent: boundary: ADR-015 owns shedding and ADR-004 owns refusing a write; observing and acting in one component would make every emission a control decision)

## Verification Log
- 2026-09-04 · 15ace10* · exit 0 · `set -o pipefail …` · acceptance-sha256:24bb04b00f9015de24a3b03fc5aee5dac671b5c3b269264c85f162ef4589e2a3 · ms:3466
- 2026-09-04 · 15ace10* · exit 0 · `set -o pipefail …` · acceptance-sha256:24bb04b00f9015de24a3b03fc5aee5dac671b5c3b269264c85f162ef4589e2a3 · ms:3363
- 2026-09-04 · 15ace10* · exit 0 · `set -o pipefail …` · acceptance-sha256:24bb04b00f9015de24a3b03fc5aee5dac671b5c3b269264c85f162ef4589e2a3 · ms:3610
- 2026-09-04 · 15ace10* · exit 0 · `set -o pipefail …` · acceptance-sha256:24bb04b00f9015de24a3b03fc5aee5dac671b5c3b269264c85f162ef4589e2a3 · ms:3378
- 2026-09-04 · 15ace10* · exit 0 · `set -o pipefail …` · acceptance-sha256:24bb04b00f9015de24a3b03fc5aee5dac671b5c3b269264c85f162ef4589e2a3 · ms:3360
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:24bb04b00f9015de24a3b03fc5aee5dac671b5c3b269264c85f162ef4589e2a3 · ms:3513
