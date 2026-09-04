# Task ADR-009-T2: Enforce the epoch at the tail, and close the open failure

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `tail.Tail.Append` taking a `lease.Epoch`, `tail.Tail.Epoch`, `tail.ErrFencedOut`
**Consumes:** `lease.Epoch`, `lease.Registry` (T1), `tail.Tail` from ADR-017, the `writer-process-lost` fault from ADR-019
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the tail itself refusing an epoch below the highest it has seen`, `a superseded writer being unable to append`, `a leaf accepting writes again after its writer is lost`

## Goal

Make the resource do the refusing, so a writer that lost its lease while paused
fails at its next append instead of corrupting the tail — and a leaf whose writer
died can be taken over.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/tail/tail.go` | edit | `Append` takes an epoch and refuses a stale one; `WriterToken` is removed. |
| `internal/core/tail/fencing_test.go` | add | The tests below, including the falsifier named in ADR-009's `Enforced-by:`. |
| `internal/core/tail/tail_test.go` | edit | Existing tests move from the token to an epoch. |
| `internal/core/chaos/faults.go` | edit | `writer-process-lost` is re-run and re-dispositioned. |
| `docs/adr/FAILURES.md` | edit | The catalogue's only open entry closes, naming this record. |

⚠ This task EDITS a file ADR-017 governs. That is why ADR-009 carries an
`Invalidates:` header: the writer token ADR-017 shipped cannot be superseded, and
that is the defect being fixed rather than an incidental refactor.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestFencedOutWriterCannotAppend`, `TestTailRefusesAnEpochItHasSeenPast`, `TestLeafAcceptsWritesAfterHandover`, `TestPublishedEntriesSurviveHandover`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Change `Append` to take a `lease.Epoch` and remove `WriterToken`.
3. [S3] Track the highest epoch the tail has OBSERVED, and refuse anything below it with `ErrFencedOut`. ★The refusal happens here, at the resource, not at the writer. A writer that asks "am I still the leader?" and then writes has a window between the two in which it can lose leadership, and no amount of checking closes it.
4. [S4] Make observing a higher epoch irreversible: once seen, a lower one is never accepted again, even if the higher holder vanishes immediately. ★A leaf that has moved on must not be draggable back by whichever writer was slowest.
5. [S5] Accept the FIRST epoch seen, whatever its value, so a tail does not need to be told where the counter started.
6. [S6] Re-run ADR-019's `writer-process-lost` fault and re-disposition it from unrecoverable-and-open to recovers, updating `FAILURES.md` in the same commit. ★The fault that found the defect is the evidence the defect is fixed; writing a fresh test that agrees with the fix would prove less.
7. [S7] Update the catalogue's open-entry count and the entry's prose. [proof: human: a reader confirms the entry says what CHANGED and names this record, rather than being deleted as though the failure never happened]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/tail/... -race -run 'TestFencedOut|TestTailRefusesAnEpoch|TestLeafAcceptsWrites|TestPublishedEntriesSurvive' -count=1 2>&1 | tee /tmp/adr009-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr009-t2a.out \
  && go test ./internal/core/tail/... ./internal/core/lease/... ./internal/core/chaos/... -race -count=1 2>&1 | tee /tmp/adr009-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr009-t2b.out
```

The first command is this task's own work and can carry the verdict alone. The
second is the regression half, and it is load-bearing here in a way it usually is
not: the chaos suite's catalogue check fails if the fault's disposition and the
document disagree, so the two cannot drift apart.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestFencedOutWriterCannotAppend` | `internal/core/tail/fencing_test.go` | A writer that held the leaf, was superseded while it did nothing, and then appends is refused with `ErrFencedOut` — the refusal happening at the tail rather than at the writer. **The falsifier ADR-009 names in `Enforced-by:`** | — | S2, S3 |
| `TestTailRefusesAnEpochItHasSeenPast` | `internal/core/tail/fencing_test.go` | Once a higher epoch has been observed, every lower one is refused for good, even after the higher holder stops appending | — | S4, S5 |
| `TestLeafAcceptsWritesAfterHandover` | `internal/core/tail/fencing_test.go` | A leaf whose writer is lost accepts writes again under a new epoch, which is the failure `FAILURES.md` catalogued as open | — | S2, S6 |
| `TestPublishedEntriesSurviveHandover` | `internal/core/tail/fencing_test.go` | Every entry published before a handover stays readable and whole afterwards, so fencing costs nothing that was already committed | — | S3, S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests above. |
| 2 — something selects it | `Append` is the only way an entry enters the tail and it now requires an epoch, so every writer in the repository passes through the check; the chaos fault exercises it end to end. |
| 3 — the caller can discover it | `Append`'s signature carries the epoch, so a caller cannot fail to supply one, and a named sentinel says what refusal means. |
| 4 — it is used | `FAILURES.md`'s open-entry count is the measurement, and it moves from one to zero. |

## Mutation Log

- 2026-09-04 · 5cb6794* · mutant killed · exit 1 · `internal/core/tail/tail.go` · removes the refusal at the resource, so a writer superseded while paused appends anyway and two writers land entries in one tail — the corruption that a release-based handover would also have caused · acceptance-sha256:478af73fb59c71eaf1148a006b76408796b63433f0a2cff6c95082666ce759c4 · covers:the tail itself refusing an epoch below the highest it has seen
- 2026-09-04 · 5cb6794* · mutant killed · exit 1 · `internal/core/tail/tail.go` · stops the tail remembering the epochs it has seen, so nothing is ever below the highest and a superseded writer is never fenced out however many handovers have happened since · acceptance-sha256:478af73fb59c71eaf1148a006b76408796b63433f0a2cff6c95082666ce759c4 · covers:a superseded writer being unable to append
- 2026-09-04 · 5cb6794* · mutant killed · exit 1 · `internal/core/tail/tail.go` · refuses any epoch that differs from the one first seen, so a replacement writer can never take the leaf and it stays read-only forever — which is exactly the failure the catalogue recorded before this record · acceptance-sha256:478af73fb59c71eaf1148a006b76408796b63433f0a2cff6c95082666ce759c4 · covers:a leaf accepting writes again after its writer is lost

## Invariants

- The tail refuses any epoch below the highest it has observed.
- Observing a higher epoch is irreversible.
- The first epoch a tail sees is accepted whatever its value.
- Entries published before a handover remain readable and whole.
- No release, no expiry, and no liveness check anywhere on this path.

## Risks

- ⚠ **A test that supersedes and then appends immediately proves less than it looks.** The dangerous case is a writer that was paused across the handover and does not know — so `TestFencedOutWriterCannotAppend` has the old writer append AFTER the new one has already published, which is the ordering that actually happens in production.
- Moving the existing tail tests from a token to an epoch could quietly weaken them. The regression half of the fence runs the whole tail suite, so the lock-free and watermark properties are re-proved rather than assumed to have survived.
- ⚠ Re-dispositioning a catalogue entry is exactly the moment a catalogue becomes untrustworthy — it is where an inconvenient finding could be quietly dropped. The entry is kept, its prose says what changed, and the fault stays registered and running.

## Stop Condition

Stop and ask if closing this requires the tail to consult anything about
liveness — a heartbeat, a timeout, a health check. It must not: the whole point
of an epoch is that the resource can refuse correctly while knowing nothing about
whether anyone is alive.

## Out of Scope

- Who decides that a handover should happen (deferred: `docs/adr/BACKLOG.md` §19)
- Raft, elections and membership (deferred: `docs/adr/BACKLOG.md` §19)
- The two multi-node faults this makes testable — a fenced writer that does not know it, and two nodes each believing they hold the leaf (deferred: ADR-019's T2, which needs a composed cluster)

## Verification Log
- 2026-09-04 · 5cb6794* · exit 0 · `set -o pipefail …` · acceptance-sha256:478af73fb59c71eaf1148a006b76408796b63433f0a2cff6c95082666ce759c4 · ms:3753
- 2026-09-04 · 5cb6794* · exit 0 · `set -o pipefail …` · acceptance-sha256:478af73fb59c71eaf1148a006b76408796b63433f0a2cff6c95082666ce759c4 · ms:3869
- 2026-09-04 · 5cb6794* · exit 0 · `set -o pipefail …` · acceptance-sha256:478af73fb59c71eaf1148a006b76408796b63433f0a2cff6c95082666ce759c4 · ms:3783
- 2026-09-04 · 5cb6794* · exit 0 · `set -o pipefail …` · acceptance-sha256:478af73fb59c71eaf1148a006b76408796b63433f0a2cff6c95082666ce759c4 · ms:3776
