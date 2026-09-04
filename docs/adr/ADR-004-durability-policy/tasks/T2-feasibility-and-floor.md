# Task ADR-004-T2: Feasibility against a map, and the runtime floor

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `Policy.Validate()`, `Policy.Satisfied()`, `durability.ErrInsufficientDomains`, `durability.ErrBelowFloor`
**Consumes:** `durability.Policy`, `Policy.DomainsNeeded()` (T1); `topology.Map`, `topology.AncestorAtLevel` from ADR-001
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `counting DISTINCT domains rather than replicas`, `the two checks being independent`

## Goal

Refuse a policy the cluster could never satisfy when it is loaded, and refuse a
write the cluster cannot currently satisfy when it is attempted — two different
failures that must not be collapsed into one check.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/durability/feasibility.go` | add | `Validate` against a topology map. |
| `internal/core/durability/floor.go` | add | `Satisfied` against a set of domains currently holding copies. |
| `internal/core/durability/durability_test.go` | edit | The tests below. |

★ The two checks answer different questions and neither substitutes for the
other. `Validate` catches a policy that could NEVER work — a (8,2) code declared
over a three-rack map. `Satisfied` catches a cluster that has STOPPED working —
enough racks declared, not enough currently holding copies. A design with only
the first accepts writes into a degraded cluster; one with only the second
discovers a misconfiguration during an incident.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestValidateRefusesTooFewDomains`, `TestValidateAcceptsASufficientMap`, `TestSatisfiedRefusesBelowFloor`, `TestSatisfiedCountsDistinctDomains`, `TestValidateAndSatisfiedAreIndependent`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Implement `Validate(topology.Map)`: resolve the policy's domain level label, count the DISTINCT nodes at that level, and refuse with `ErrInsufficientDomains` when there are fewer than `DomainsNeeded()`. The error names both numbers, because "not enough domains" without them tells an operator nothing actionable.
3. [S3] Refuse a domain level the map does not declare, rather than treating an unknown label as "no constraint" — a typo must not silently disable the spread requirement.
4. [S4] Implement `Satisfied(domains []string)`: count DISTINCT domains and refuse with `ErrBelowFloor` when fewer than `MinSize`. ★Counting distinct domains rather than replicas is the whole point — three copies in one rack is one domain, and a policy spreading across racks is not satisfied by them.
5. [S5] Keep the two entirely separate, so that neither can be made to stand in for the other. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/durability/... -run 'TestValidate|TestSatisfied|TestPolicy|TestReplicated|TestCoded|TestTier' -count=1 2>&1 | tee /tmp/adr004-t2.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr004-t2.out \
  && go test ./internal/core/topology/... -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestValidateRefusesTooFewDomains` | `internal/core/durability/durability_test.go` | A (8,2) policy over a map declaring three racks is refused, with both numbers named — the arithmetic that makes coding and survival trade against each other | — | S2 |
| `TestValidateAcceptsASufficientMap` | `internal/core/durability/durability_test.go` | The same policy over a map with enough racks is accepted, so the check is not merely always-refusing | — | S2 |
| `TestValidateRefusesAnUndeclaredLevel` | `internal/core/durability/durability_test.go` | A domain level the map does not declare is refused rather than treated as no constraint, so a typo cannot silently disable the spread | — | S3 |
| `TestSatisfiedRefusesBelowFloor` | `internal/core/durability/durability_test.go` | Fewer distinct domains than `MinSize` yields `ErrBelowFloor`, which is what makes a degraded cluster stop accepting writes | — | S4 |
| `TestSatisfiedCountsDistinctDomains` | `internal/core/durability/durability_test.go` | Three copies in one rack count as ONE domain, not three — the assertion that separates spread from replica count | — | S4 |
| `TestValidateAndSatisfiedAreIndependent` | `internal/core/durability/durability_test.go` | A policy can be feasible and currently unsatisfied, and neither check answers the other's question | — | S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The six unit tests above. |
| 2 — something selects it | Node startup will call `Validate` and the write path will call `Satisfied` once ADR-005 and ADR-009 exist. Neither exists yet, so this is honestly a rung-1 component today. |
| 3 — the caller can discover it | Exported doc comments and two named sentinels; `go doc ./internal/core/durability` is the check. |
| 4 — it is used | Nothing measures this yet. |

## Mutation Log

- 2026-09-04 · 6036c3a* · mutant killed · exit 1 · `internal/core/durability/floor.go` · counting replicas instead of domains reports a healthy cluster with every copy in one rack, which a single rack failure would take entirely; TestSatisfiedCountsDistinctDomains must go red · acceptance-sha256:7a1f1e55c433b92352e543469a7714a971b12409b2f4fce36b0a67c9af0abe5c · covers:counting DISTINCT domains rather than replicas
- 2026-09-04 · 6036c3a* · mutant killed · exit 1 · `internal/core/durability/feasibility.go` · checking feasibility against the runtime floor instead of the requirement collapses the two checks into one, so an infeasible policy loads cleanly and fails later during a repair; TestValidateRefusesTooFewDomains must go red · acceptance-sha256:7a1f1e55c433b92352e543469a7714a971b12409b2f4fce36b0a67c9af0abe5c · covers:the two checks being independent

## Invariants

- `Validate` counts DISTINCT nodes at the declared level. Two replicas in one rack are one domain.
- An undeclared domain level is refused, never treated as an absent constraint.
- `Satisfied` and `Validate` are separate and neither is derived from the other.
- Both errors name the numbers involved, because an operator acting on them needs the shortfall rather than the verdict.

## Risks

- `Validate` checks a DECLARED map. A map claiming ten racks that share one power feed declares ten domains and has one, and no property of the map can tell the difference. ADR-004 records this as an Out of Scope fact rather than pretending to mitigate it.
- A cluster can pass `Validate` at startup and fail `Satisfied` seconds later, which is correct and will still look like a contradiction in an incident. The two errors are deliberately different sentinels so a log makes the distinction obvious.

## Stop Condition

Stop and ask if the caller needs to know WHICH domains are missing rather than
how many. That is a different and larger answer — it needs the current placement,
not just a count — and belongs with whatever record owns repair.

## Out of Scope

- Deciding what to do about a leaf below the floor: alarm, re-replicate, or evict (deferred: `docs/adr/BACKLOG.md` §10)
- Choosing which domains a leaf's copies should occupy — that is placement's, and it already spreads.

## Verification Log
- 2026-09-04 · 6036c3a* · exit 0 · `set -o pipefail …` · acceptance-sha256:7a1f1e55c433b92352e543469a7714a971b12409b2f4fce36b0a67c9af0abe5c · ms:996
- 2026-09-04 · 6036c3a* · exit 0 · `set -o pipefail …` · acceptance-sha256:7a1f1e55c433b92352e543469a7714a971b12409b2f4fce36b0a67c9af0abe5c · ms:966
- 2026-09-04 · 6036c3a* · exit 0 · `set -o pipefail …` · acceptance-sha256:7a1f1e55c433b92352e543469a7714a971b12409b2f4fce36b0a67c9af0abe5c · ms:1001
