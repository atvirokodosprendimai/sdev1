# Task ADR-002-T1: The hybrid logical clock

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `hlc.Timestamp`, `hlc.Clock`, `Clock.Now()`, `Clock.Merge()`
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the monotonicity invariant`, `the injected wall-clock function`

## Goal

Provide a clock that never moves backwards, stays close to wall time, and orders
causally related events correctly across nodes without special hardware.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/hlc/hlc.go` | add | `Timestamp{Wall int64; Logical uint32}`, `Clock`, `Now`, `Merge`. |
| `internal/core/hlc/doc.go` | add | Package comment: what an HLC guarantees, what it does not, and why the wall clock is an input rather than the ordering. |
| `internal/core/hlc/hlc_test.go` | add | The tests below, including the backwards-clock fixture. |

The wall-clock reading is an injected `func() int64` rather than a direct
`time.Now()` call, because every interesting property of this package is about
what happens when the clock misbehaves, and a test cannot make the real clock
jump backwards.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestNowIsStrictlyMonotonic`, `TestNowSurvivesBackwardsWallClock`, `TestLogicalIncrementsWhenWallDoesNotAdvance`, `TestMergeAdvancesPastRemote`, `TestTimestampOrdersLexicographically`. Run the Acceptance fence and confirm it is red. [proof: acceptance]
2. [S2] Define `Timestamp{Wall int64; Logical uint32}` with a `Compare` that orders by `Wall` then `Logical`, and a fixed-width 12-byte encoding.
3. [S3] Define `Clock` holding the last issued `Timestamp`, a mutex, and an injected `now func() int64`.
4. [S4] Implement `Now()`: read the wall clock, take `max(read, last.Wall)`; if that equals `last.Wall`, increment `Logical`, otherwise reset `Logical` to zero. Store and return.
5. [S5] Implement `Merge(remote Timestamp)`: advance local state past both the local reading and the remote timestamp, incrementing `Logical` on a tie. This is what makes a message carrying a timestamp establish causality.
6. [S6] Write the package comment stating the monotonicity guarantee, the drift consequence (an HLC follows the fastest clock it hears from and never returns), and that this package is not a skew detector. [proof: human: a reader confirms the comment states the drift consequence, which is the property most likely to surprise an operator]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/hlc/... -run 'TestNow|TestLogical|TestMerge|TestTimestamp' -count=1 -race 2>&1 | tee /tmp/adr002-t1.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr002-t1.out
```

`-race` is in the fence rather than optional: `Clock` is shared by every commit on
a node, so a data race here is a correctness defect and not a performance note.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestNowIsStrictlyMonotonic` | `internal/core/hlc/hlc_test.go` | Successive `Now()` calls are strictly increasing under `Compare`, including under concurrent callers | — | S4 |
| `TestNowSurvivesBackwardsWallClock` | `internal/core/hlc/hlc_test.go` | With an injected clock that jumps backwards, `Now()` still increases — the property NTP alone cannot give | — | S3, S4 |
| `TestLogicalIncrementsWhenWallDoesNotAdvance` | `internal/core/hlc/hlc_test.go` | A frozen wall clock yields distinct increasing timestamps via the logical counter | — | S4 |
| `TestMergeAdvancesPastRemote` | `internal/core/hlc/hlc_test.go` | After merging a remote timestamp, the next local `Now()` is strictly greater than that remote — the causality guarantee | — | S5 |
| `TestTimestampOrdersLexicographically` | `internal/core/hlc/hlc_test.go` | The 12-byte encoding sorts as bytes in the same order `Compare` gives, so an index can order on it without decoding | — | S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five unit tests above. |
| 2 — something selects it | T2's `TxID` embeds an `hlc.Timestamp` and T2's fence builds against this package; deleting the embed breaks that build. |
| 3 — the caller can discover it | Exported doc comments; `go doc ./internal/core/hlc` is the check. |
| 4 — it is used | Nothing measures this yet. |

## Mutation Log

## Invariants

- `Now()` never returns a timestamp less than or equal to one it has already returned, whatever the wall clock does.
- The wall clock is an input to the algorithm, never the ordering itself.
- The 12-byte encoding is fixed-width and byte-comparable in the same order as `Compare`.
- `Clock` is safe for concurrent use; every commit on a node goes through one.

## Risks

- An HLC follows the fastest clock it hears from and cannot come back, so one badly-skewed node degrades every timestamp it touches, permanently. This package deliberately does not police that — bounding skew is `BACKLOG.md` §4 — and the package comment must say so rather than leaving an operator to discover it.
- A 32-bit logical counter could in principle wrap if the wall clock froze for a very long time under sustained load. Untested and unmeasured at authoring; recorded rather than guessed at.

## Stop Condition

Stop and ask if any consumer needs the clock to expose its uncertainty bound —
that is a TrueTime-shaped requirement, it was explicitly rejected in ADR-002's
Alternatives, and reintroducing it changes what this package is.

## Out of Scope

- Detecting or bounding clock skew between nodes (deferred: `docs/adr/BACKLOG.md` §4)
- Transporting a timestamp between nodes — this package produces and merges values; who sends them is ADR-009's.

## Verification Log
