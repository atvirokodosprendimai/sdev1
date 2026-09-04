# Task ADR-008-T1: The prefix table, longest-match lookup, and the aggregation that bounds it

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `routing.Route`, `routing.Table`, `routing.NewTable`, `routing.Table.Insert`, `routing.Table.Lookup`, `routing.Table.Aggregate`, `routing.Table.Routes`, `routing.ErrNoRoute`
**Consumes:** `addr.LeafID` and `addr.Key` from ADR-001, `addr.FanOut` from ADR-001
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the longest matching prefix winning`, `aggregation collapsing children that share their next hops`, `a table with no default refusing rather than guessing`

## Goal

Make a trie prefix a routable prefix, so a node advertises a subtree as one route
and a lookup finds the most specific answer without knowing what is below it.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/routing/doc.go` | add | Package comment: why a trie prefix is a routable prefix, what bounds the table, and how routing and placement differ. |
| `internal/core/routing/table.go` | add | `Route`, `Table`, insert, longest-prefix lookup, `Aggregate`, `ErrNoRoute`. |
| `internal/core/routing/routing_test.go` | add | The tests below. |

★ This package works on addresses and never opens a socket. What a route MEANS
has to be right before anything carries one, and keeping it free of transport is
what makes every property below testable with no cluster.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestLongestPrefixWins`, `TestAggregationCollapsesIdenticalChildren`, `TestAggregationKeepsAnOddChildOut`, `TestTableWithoutADefaultRefuses`, `TestTableSizeIsBoundedByVariety`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Route`: a prefix, its next hops, and an epoch.
3. [S3] Implement `Insert` and `Lookup`, matching the DEEPEST prefix that contains the key. ★This is what lets a subtree be carved out of a larger route by advertising a deeper one, without withdrawing the parent — the property that makes a repair a local change.
4. [S4] Refuse a lookup that matches nothing with `ErrNoRoute` rather than returning a zero route. ★A zero route is a next hop of nowhere, and a client that received one would send a request into the dark rather than reporting that it does not know where to go.
5. [S5] Implement `Aggregate`: where every child of a node resolves to the same next hops, replace the children with the parent. ★This is what bounds the table by placement VARIETY rather than by leaf count, and it is the claim rule 2 of the record rests on.
6. [S6] Leave a parent's children in place when even one differs, so aggregation never changes what a lookup answers. [proof: mutation]
7. [S7] Write the package comment stating why routing and placement are allowed to disagree. [proof: human: a reader confirms the comment says the disagreement is what a repair looks like from outside, not that one of them is wrong]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/routing/... -race -run 'TestLongestPrefix|TestAggregation|TestTableWithoutADefault|TestTableSizeIsBounded' -count=1 2>&1 | tee /tmp/adr008-t1.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr008-t1.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestLongestPrefixWins` | `internal/core/routing/routing_test.go` | A deeper prefix beats a shallower one containing the same key, so a subtree can be carved out without withdrawing its parent | — | S2, S3 |
| `TestAggregationCollapsesIdenticalChildren` | `internal/core/routing/routing_test.go` | Children that all resolve to the same next hops are replaced by their parent, and every key still routes to the same place afterwards | — | S5 |
| `TestAggregationKeepsAnOddChildOut` | `internal/core/routing/routing_test.go` | One differing child prevents the collapse, so aggregation never changes an answer — the property that makes it safe to run at any time | — | S6 |
| `TestTableWithoutADefaultRefuses` | `internal/core/routing/routing_test.go` | A lookup matching nothing yields `ErrNoRoute` rather than a zero route, so a client reports that it does not know rather than sending a request nowhere | — | S4 |
| `TestTableSizeIsBoundedByVariety` | `internal/core/routing/routing_test.go` | A table over many leaves sharing next hops aggregates to few routes, while one over varied placement does not — the bound is on variety, not on leaf count | — | S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | `Lookup` is the only way a key becomes a next hop, and T2's cache and redirect are its first consumer. |
| 3 — the caller can discover it | Exported doc comments and a named sentinel; `Lookup` returning `(Route, error)` states that not knowing is a possible answer. |
| 4 — it is used | Nothing measures this yet; no transport exists. |

## Mutation Log

- 2026-09-04 · bbb6744* · mutant killed · exit 1 · `internal/core/routing/table.go` · collapses a set of children whose next hops DIFFER, so aggregation silently changes where keys route and a repaired subtree is aggregated back into the route it was carved out of · acceptance-sha256:9cdda6ff5883a08745927ee456af25fead7eb6db5a38351ac7bc64938bdf391a · covers:aggregation collapsing children that share their next hops
- 2026-09-04 · bbb6744* · mutant killed · exit 1 · `internal/core/routing/table.go` · returns a zero route instead of refusing, so a client that does not know where to go is handed a next hop of nowhere and sends the request into the dark rather than reporting it · acceptance-sha256:9cdda6ff5883a08745927ee456af25fead7eb6db5a38351ac7bc64938bdf391a · covers:a table with no default refusing rather than guessing
- 2026-09-04 · bbb6744* · mutant killed · exit 1 · `internal/core/routing/table.go` · walks the trie shallowest-first so the least specific route wins, which means a subtree carved out by a repair is never reached and every request for it keeps going to the node it just left · acceptance-sha256:9cdda6ff5883a08745927ee456af25fead7eb6db5a38351ac7bc64938bdf391a · covers:the longest matching prefix winning

## Invariants

- A lookup returns the deepest prefix containing the key, or `ErrNoRoute`.
- Aggregation never changes what a lookup answers.
- A route with no next hops is not a route; inserting one is refused.
- This package performs no network or file I/O.

## Risks

- ⚠ An aggregation test that only checks the table SHRANK would pass for an aggregation that dropped routes. Every aggregation test re-checks that a sample of keys still routes to the same next hops afterwards, so shrinking is only ever measured alongside preservation.
- A bound stated as "small" is unfalsifiable. `TestTableSizeIsBoundedByVariety` asserts a specific count for a uniform placement AND asserts a varied placement does NOT collapse, so the claim is about variety rather than about size.

## Stop Condition

Stop and ask if a route needs to carry anything beyond a prefix, next hops and an
epoch — a weight, a health signal, a capacity hint. Each is plausible and each
makes a route a second placement policy, which ADR-004 already owns.

## Out of Scope

- The redirect, the client cache and epoch ordering — that is T2.
- The transport, and how routes reach a node at all (deferred: `docs/adr/BACKLOG.md` §18)
- Ordering next hops by nearness (permanent: boundary: `placement.Nearest` and `topology.Distance` already do this, and a second ordering here would drift from the first)

## Verification Log
- 2026-09-04 · bbb6744* · exit 0 · `set -o pipefail …` · acceptance-sha256:9cdda6ff5883a08745927ee456af25fead7eb6db5a38351ac7bc64938bdf391a · ms:1819
- 2026-09-04 · bbb6744* · exit 0 · `set -o pipefail …` · acceptance-sha256:9cdda6ff5883a08745927ee456af25fead7eb6db5a38351ac7bc64938bdf391a · ms:1778
- 2026-09-04 · bbb6744* · exit 0 · `set -o pipefail …` · acceptance-sha256:9cdda6ff5883a08745927ee456af25fead7eb6db5a38351ac7bc64938bdf391a · ms:1697
- 2026-09-04 · bbb6744* · exit 0 · `set -o pipefail …` · acceptance-sha256:9cdda6ff5883a08745927ee456af25fead7eb6db5a38351ac7bc64938bdf391a · ms:1802
- 2026-09-04 · bbb6744* · exit 0 · `set -o pipefail …` · acceptance-sha256:9cdda6ff5883a08745927ee456af25fead7eb6db5a38351ac7bc64938bdf391a · ms:1746
