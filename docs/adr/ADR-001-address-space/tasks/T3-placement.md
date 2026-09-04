# Task ADR-001-T3: Deterministic placement, and client-local nearest-first ordering

**Depends-on:** T1, T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `placement.Resolve()`, `placement.Nearest()`
**Consumes:** `addr.LeafID`, `addr.Descend()` (T1); `topology.Map`, `topology.Load()` (T2)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the absence of I/O in Resolve`, `the canonical set being independent of the caller`, `the exit code of the new unit run alone`, `the T1 and T2 regression suites still passing`

## Goal

Turn a leaf identifier plus a topology map into the canonical set of servers that
hold it — identically for every caller — and separately let a caller sort that set
by its own distance, so reads prefer near servers without placement depending on
who is asking.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/placement/placement.go` | add | `Resolve(addr.LeafID, topology.Map) ([]topology.ServerID, error)` — the canonical set. |
| `internal/core/placement/nearest.go` | add | `Nearest([]topology.ServerID, topology.ServerID, topology.Map) []topology.ServerID` — caller-local reordering by topology distance. |
| `internal/core/placement/doc.go` | add | Package comment: why placement is computed rather than looked up, and why the canonical set and the preference order are two different questions. |
| `internal/core/placement/placement_test.go` | add | The tests below, including the determinism, no-I/O and caller-independence fixtures. |

This is the first caller of both `addr` and `topology`, so it is where their
reachability is actually demonstrated (T1 and T2 rung 2 point here).

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestResolveIsDeterministic`, `TestResolveIsIndependentOfCaller`, `TestResolveSpreadsAcrossRacks`, `TestResolveIsStableUnderUnrelatedTopologyChange`, `TestResolveRefusesDepthMismatch`, `TestNearestPrefersSameRackThenSameDatacenter`, `TestNearestIsAPermutationOfResolve`. Run the Acceptance fence and confirm it is red. [proof: acceptance]
2. [S2] Implement `Resolve` as a pure function over the map's declared hierarchy: descend the leaf's prefix bytes against the hierarchy, then select servers by the map's declared weights. Its result depends on the leaf and the map and on nothing else — in particular not on who called it.
3. [S3] Order `Resolve`'s result canonically, as a function of the leaf identifier alone, so two clients holding the same map agree byte for byte without coordinating.
4. [S4] Implement `Nearest` as a stable sort of a given set by topology distance from a named server — same rack first, then same datacenter, then the rest — leaving the set's membership untouched. This is the read-preference question, and it is deliberately a separate function so that locality can never change *who holds* a leaf.
5. [S5] Refuse a leaf identifier whose `Depth` does not match the map's `Depth` with a named sentinel, rather than resolving against a depth nobody asked for.
6. [S6] Write the package comment stating that `Resolve` performs no I/O, and that membership and preference are two questions with two functions. [proof: human: a reader confirms the comment explains the no-lookup property and the membership-versus-preference split rather than restating the signatures]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/placement/... -run 'TestResolve|TestNearest' -count=1 2>&1 | tee /tmp/adr001-t3.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr001-t3.out \
  && go test ./internal/core/addr/... ./internal/core/topology/... -count=1
```

The new unit runs alone first and must carry the verdict by itself; the second
command re-runs T1's and T2's suites so this task cannot land by breaking them.
Both must pass.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestResolveIsDeterministic` | `internal/core/placement/placement_test.go` | The same leaf and map yield the same ordered set across processes | — | S2, S3 |
| `TestResolveIsIndependentOfCaller` | `internal/core/placement/placement_test.go` | `Resolve` takes no caller identity and its result cannot vary by who asks — the invariant that locality must not be allowed to break | — | S2, S3 |
| `TestResolveSpreadsAcrossRacks` | `internal/core/placement/placement_test.go` | Candidates for one leaf occupy distinct racks where the map offers them, which is what makes a rack-level failure domain expressible | — | S2 |
| `TestResolveIsStableUnderUnrelatedTopologyChange` | `internal/core/placement/placement_test.go` | Adding a server in a different rack does not reshuffle this leaf's candidates | — | S2, S3 |
| `TestResolveRefusesDepthMismatch` | `internal/core/placement/placement_test.go` | A leaf recorded at depth 1 is refused against a depth-2 map rather than silently re-placed | — | S5 |
| `TestResolvePerformsNoIO` | `internal/core/placement/placement_test.go` | `Resolve` opens no file and dials no address | — | S2 |
| `TestNearestPrefersSameRackThenSameDatacenter` | `internal/core/placement/placement_test.go` | From a given server, the returned order is same-rack, then same-datacenter, then remote | — | S4 |
| `TestNearestIsAPermutationOfResolve` | `internal/core/placement/placement_test.go` | `Nearest` reorders and never adds or drops a server — the assertion that keeps read preference from silently changing replica membership | — | S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The eight unit tests above. |
| 2 — something selects it | `cmd/sdev1-addr` (T4) calls both `Resolve` and `Nearest`; T4's fence builds and runs the binary, so deleting either call makes it red. |
| 3 — the caller can discover it | Exported doc comments on both functions; `go doc ./internal/core/placement` is the check. |
| 4 — it is used | Nothing measures this yet. |

## Mutation Log

## Invariants

- `Resolve` is a pure function of the leaf and the map. No I/O, no clock, no randomness, no caller identity, and no map-iteration order escaping into the result.
- `Resolve`'s result is byte-identical for every caller holding the same map. Two clients agree without talking to each other, and locality is not permitted to weaken this.
- `Nearest` is a permutation. It changes preference order only; membership is `Resolve`'s answer and `Nearest` may not add or drop a server.
- A depth mismatch is refused, never coerced.

## Risks

- Splitting membership from preference is one function more than the obvious design, and a later caller may be tempted to fold them back together for convenience. `TestResolveIsIndependentOfCaller` and `TestNearestIsAPermutationOfResolve` are what make that fold fail rather than merely be discouraged.
- The weight semantics inherited from T2 are declared but unproven against any real cluster; `TestResolveSpreadsAcrossRacks` proves the spread property on the fixture map only and says nothing about a heterogeneous one. Recorded rather than hidden.
- Determinism makes a topology change reshuffle more data than an occupancy-aware rebalancer would move. That is the parent record's stated Negative consequence, not a defect of this task.

## Stop Condition

Stop and ask if ADR-004 turns out to need `Resolve` to return the set already
grouped by failure domain rather than flat — a durability rule that spreads across
racks needs to know which returned servers share one. This task returns the flat
set deliberately, and grouping is an additive change only while nothing consumes
the flat shape.

Stop also if placement must become a function of a *versioned* topology map — that
is, if a segment written under an older map must resolve against that older map
rather than the current one. That is a real requirement raised on 2026-09-04 and
it is `BACKLOG.md` §6; it changes `Resolve`'s signature, so it must be settled
before callers exist rather than after.

## Out of Scope

- How many of the returned servers actually hold a copy — that is ADR-004's replication factor, not an addressing question.
- Which of them accepts writes — that is ADR-009's leader election.
- Spare servers and what happens when one of the returned servers is dead — that is ADR-004 and `BACKLOG.md` §7; `Resolve` answers where a leaf belongs, not who is currently up.

## Verification Log
