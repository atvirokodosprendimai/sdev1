# ADR-008 Tasks

Implementation tasks for ADR-008: Route on aggregated trie prefixes, and make a
stale route a redirect rather than a wrong answer. See the parent ADR for the
decision.

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
| T1 | The prefix table, longest-match lookup, and the aggregation that bounds it | done | — | `go test ./internal/core/routing/... -race -run 'TestLongestPrefix\|TestAggregation\|TestTableWithoutADefault\|TestTableSizeIsBounded'` |
| T2 | The redirect, the epoch that orders it, and a client cache that never holds the map | done | — | `go test ./internal/core/routing/... -race -run 'TestStaleRoute\|TestOlderEpoch\|TestRedirectChain\|TestClientLearns\|TestRedirectIsNot'` then the addr and placement suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `routing.Route`, `routing.Table`, `routing.ErrNoRoute` | T2 | T1 before T2 |

## Notes

- **A trie prefix IS a routable prefix**, and that observation is the whole
  record. A node holding everything under one prefix advertises it once instead
  of the millions of leaves beneath it, so the table is bounded by how much
  placement VARIES rather than by how much data exists.
- ⚠ **A stale route must produce a REDIRECT.** An error would turn every
  topology change into a fleet-wide outage; an answer would be silently wrong,
  served by a node that no longer holds the leaf. The node receiving a misrouted
  request already knows where the leaf went, so saying so costs one hop and
  repairs the client permanently.
- ⚠ **A redirect without an epoch is a loop.** Two nodes with opposing stale
  views will bounce a client between them forever, and each redirect looks
  exactly as authoritative as the last. Monotonic epochs make that impossible in
  a correct cluster; the hop budget bounds it in an incorrect one, and the error
  names the chain so the lying node is findable. Both are needed and neither
  substitutes for the other.
- ⚠ **A redirect must never carry data.** The moment it can, a stale route can
  serve an answer, which is the exact failure the redirect exists to prevent.
  `Resolve` returns a destination or an error and never a `Redirect`, so the type
  system carries the rule.
- **Routing is not placement, and they are allowed to disagree.** Placement is
  canonical and computed; routing is observed and cached. They differ exactly
  while a repair is in flight, and the redirect is how a client is told. A
  diagnostic showing both is more useful than one that picks a side.
- ⚠ When testing aggregation, never assert only that the table SHRANK — that
  passes for an aggregation that dropped routes. Re-check that a sample of keys
  still routes to the same next hops afterwards.
