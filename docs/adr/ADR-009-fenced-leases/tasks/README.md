# ADR-009 Tasks

Implementation tasks for ADR-009: Make leaf ownership a fenced lease enforced at
the resource, and split consensus into one group per subtree. See the parent ADR
for the decision.

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
| T1 | The fencing epoch and the registry that only ever counts up | done | — | `go test ./internal/core/lease/... -race -run 'TestEpochOnly\|TestGrantDoesNot\|TestEpochsAreOrdered\|TestCurrentReports\|TestNoLeaseIs'` |
| T2 | Enforce the epoch at the tail, and close the open failure | done | — | `go test ./internal/core/tail/... -race -run 'TestFencedOut\|TestTailRefusesAnEpoch\|TestLeafAcceptsWrites\|TestPublishedEntriesSurvive'` then the tail, lease and chaos suites |

Status: `pending` | `partial` | `blocked` | `done`.

⚠ **This record covers the FENCING half only.** Consensus — Raft, elections,
membership, heartbeat coalescing — needs a transport that does not exist, and is
`BACKLOG.md` §19. The registry here is in-process and named for what it is. The
fencing is real; the election is not, and saying which is which is part of the
work.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `lease.Epoch`, `lease.Lease`, `lease.Registry`, `lease.ErrStaleEpoch` | T2 | T1 before T2 |

## Notes

- ⚠ **The resource refuses, not the writer.** This is the whole mechanism and the
  easiest thing to get subtly wrong. A writer that asks "am I still the leader?",
  gets yes, and then writes has a window between the two in which it can lose
  leadership — to a garbage collection, a stalled disk, a partition — and the
  write lands anyway. The check and the write are not atomic and no amount of
  checking makes them so. So the epoch travels WITH the write and the tail
  refuses anything below the highest it has seen.
- ⚠ **There is no release and no expiry, deliberately.** ADR-019 catalogued the
  fault this record fixes and also recorded why the obvious fix is worse: a
  release, or a timeout after which anyone may claim the leaf, cannot distinguish
  a dead holder from a slow one. Two live writers appending to one tail is not a
  degraded system, it is a corrupted one. Trading a leaf that stops for a leaf
  that lies is a bad trade.
- **A grant never waits for the previous holder.** Waiting is what makes a dead
  writer a permanent outage; the epoch is what makes not waiting safe. The old
  holder is not asked, not told, and cannot object — it discovers on its next
  append that it is no longer the writer.
- ⚠ **Nothing on this path may consult liveness.** No heartbeat, no timeout, no
  health check. The point of an epoch is that the resource refuses correctly
  while knowing nothing about whether anyone is alive, which is what stops
  correctness depending on failure detection.
- **Epochs are per leaf.** A global counter would make every grant anywhere a
  coordination point, which is the single-group design this record rejects.
- ⚠ **T2 re-dispositions a catalogue entry, which is the moment a catalogue
  becomes untrustworthy.** The entry is kept and its prose says what changed; the
  fault stays registered and running, and the chaos suite's check fails if the
  code and the document disagree. An inconvenient finding must never be closed by
  deletion.
