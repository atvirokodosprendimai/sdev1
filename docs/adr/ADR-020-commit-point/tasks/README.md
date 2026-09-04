# ADR-020 Tasks

Implementation tasks for ADR-020: A write commits when N memory replicas hold it,
and the watermark is that commit point. See the parent ADR for the decision.

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
| T1 | The commit condition, counted over distinct failure domains | done | — | `go test ./internal/core/commit/... -race -run 'TestReplicasInOneDomain\|TestDistinctDomainsCommit\|TestStaleEpochAcknowledgements\|TestShortfallIsRefused\|TestConditionNamesWhy'` then the durability and lease suites |
| T2 | Make the watermark advance at the commit point, and nowhere else | done | — | `go test ./internal/core/commit/... -race -run 'TestUncommittedEntryIsUnreachable\|TestWatermarkAdvancesOnlyOnCommit\|TestOneDefinitionOfCommitted\|TestPendingEntriesAreCountable\|TestLateAcknowledgement'` then the tail suite |

Status: `pending` | `partial` | `blocked` | `done`.

⚠ **Nothing replicates yet.** The condition is decidable now; satisfying it needs
a transport (`BACKLOG.md` §18), and flushing needs a segment writer (§12).

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `commit.Ack`, `commit.Condition`, `commit.Satisfied` | T2 | T1 before T2 |

## Notes

- **Memory replication is genuine durability against the failures that
  dominate** — a crash, a panic, an out-of-memory kill, a restart. The other node
  still has it and nothing waited on a disk.
- ⚠ **It is NOT durability against correlated loss.** Two nodes on one power
  feed lose everything unflushed at the same instant, and nothing reports it: the
  write was acknowledged and the client moved on. N copies protect against
  INDEPENDENT failures, and whether failures are independent is a placement
  question rather than a count.
- ⚠ **So distinct FAILURE DOMAINS are counted, never acknowledgements.** Three
  replies from three processes on one feed is one failure domain wearing three
  names, and it reads as triple durability right up until the feed drops.
- ⚠ **The domain level for a memory commit is POWER, not rack.** Rack is right
  for disk durability, where the failure is a machine or a disk. For unflushed
  memory the failure is power, and a rack can span feeds while a feed can span
  racks — the two overlap without coinciding.
- **The flush unit and the replication unit are different granularities.**
  Replicate per TRANSACTION, flush per BLOCK. Replicating per block holds a whole
  block's worth of writes unacknowledged and spikes latency at every boundary.
- **Atomicity needs no new mechanism.** ADR-017's watermark already makes an
  unpublished entry unreachable rather than half-visible, so advancing it at the
  commit point makes it the commit point — rather than a second definition
  beside it. ⚠Two definitions drift, and the one a reader uses would not be the
  one the writer waited for.
- **A shortfall is REFUSED, never acknowledged with a warning.** The warning is
  read by nobody at the moment it matters, and that is how a cluster ends up
  holding data at a durability nobody chose.
