# Task ADR-001-T3: Deterministic placement, and client-local nearest-first ordering

**Depends-on:** T1, T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `placement.Resolve()`, `placement.Spread()`, `placement.Nearest()`
**Consumes:** `addr.LeafID`, `addr.Descend()` (T1); `topology.Map`, `topology.Load()` (T2)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `scoring that is identical in every process rather than merely within one`, `scoring that spreads across targets rather than favouring some`, `the depth guard`, `Spread preserving membership`, `the canonical set being independent of the caller`, `the exit code of the new unit run alone`, `the T1 and T2 regression suites still passing`

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

1. [S1] Write the failing tests first (TDD red): `TestResolveIsDeterministic`, `TestResolveTakesNoCallerIdentity`, `TestResolveIsStableUnderUnrelatedTopologyChange`, `TestResolveRefusesDepthMismatch`, `TestSpreadPrefersDistinctDomains`, `TestSpreadIsAPermutation`, `TestNearestPrefersSameRackThenSameDatacenter`, `TestNearestIsAPermutationOfResolve`, `TestNearestKeepsUnknownTargets`. Run the Acceptance fence and confirm it is red. [proof: acceptance]
2. [S2] Implement `Resolve` as a pure function over the map's declared hierarchy: descend the leaf's prefix bytes against the hierarchy, then select servers by the map's declared weights. Its result depends on the leaf and the map and on nothing else — in particular not on who called it.
3. [S3] Order `Resolve`'s result canonically, as a function of the leaf identifier alone, so two clients holding the same map agree byte for byte without coordinating.
4. [S4] Implement `Nearest` as a stable sort of a given set by topology distance from a named server — same rack first, then same datacenter, then the rest — leaving the set's membership untouched. This is the read-preference question, and it is deliberately a separate function so that locality can never change *who holds* a leaf.
5. [S5] Refuse a leaf identifier whose `Depth` does not match the map's `Depth` with a named sentinel, rather than resolving against a depth nobody asked for.
6. [S6] Write the package comment stating that `Resolve` performs no I/O, and that membership and preference are two questions with two functions. [proof: human: a reader confirms the comment explains the no-lookup property and the membership-versus-preference split rather than restating the signatures]
7. [S7] Implement `Spread` as a separate permutation that reorders a canonical set so consecutive entries fall in distinct failure domains at a caller-named level. It is deliberately not folded into `Resolve`: Resolve decides which targets are candidates, Spread decides the order a durability policy consumes them in, and only the second depends on which level counts as a failure domain.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/placement/... -run 'TestResolve|TestSpread|TestNearest|TestScoring' -count=1 2>&1 | tee /tmp/adr001-t3.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr001-t3.out \
  && go test ./internal/core/addr/... ./internal/core/topology/... -count=1
```

The new unit runs alone first and must carry the verdict by itself; the second
command re-runs T1's and T2's suites so this task cannot land by breaking them.
Both must pass.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestResolveIsDeterministic` | `internal/core/placement/placement_test.go` | The same leaf and map yield the same ordered set on repeated calls **within one process**. ⚠ This row claimed "across processes" until 2026-09-04 and the test never checked it — see the two rows below and the Risks note | — | S2, S3 |
| `TestResolveOrderIsPinnedAcrossProcesses` | `internal/core/placement/placement_test.go` | The actual resolved order for three pinned leaves matches recorded golden values, which is the only form of check that survives a process boundary — a per-process seed changes these and nothing else in the suite notices | — | S2, S3 |
| `TestScoringSpreadsAcrossTargets` | `internal/core/placement/placement_test.go` | Over all 256 leaves the fixture can address, every target wins first place within 0.6–1.6× its fair share. Determinism and DISTRIBUTION are separate requirements and nothing checked the second — a deterministic but badly distributed hash passes every other test in this file | — | S2, S3 |
| `TestScoringUsesNoPerProcessSeed` | `internal/core/placement/placement_test.go` | The package imports nothing that can yield a per-process or per-run value — `hash/maphash`, either `math/rand`, `crypto/rand`, `time`. Inspects the IMPORT LIST rather than the source text, because the first version grepped for a symbol and flagged the comment explaining why not to use it | — | S2, S3 |
| `TestResolveTakesNoCallerIdentity` | `internal/core/placement/placement_test.go` | `Resolve`'s signature admits no caller identity, so its answer structurally cannot vary by who asks — the invariant locality is most likely to break. Asserts the signature rather than a behaviour, because a function that cannot see the caller cannot depend on them | — | S2, S3 |
| `TestResolveIsStableUnderUnrelatedTopologyChange` | `internal/core/placement/placement_test.go` | Adding a target in a different rack leaves the RELATIVE order of the existing targets untouched — rendezvous hashing's defining property, and what stops a topology change reshuffling the cluster | — | S2, S3 |
| `TestResolveRefusesDepthMismatch` | `internal/core/placement/placement_test.go` | A leaf recorded at one depth is refused against a map declaring another, rather than silently re-placed | — | S5 |
| `TestSpreadPrefersDistinctDomains` | `internal/core/placement/placement_test.go` | Consuming a prefix of a spread order yields distinct failure domains wherever the map offers them — what a durability rule depends on | — | S7 |
| `TestSpreadIsAPermutation` | `internal/core/placement/placement_test.go` | `Spread` reorders and never changes membership: domain diversity may change the order a policy consumes targets in, never which targets exist | — | S7 |
| `TestNearestPrefersSameRackThenSameDatacenter` | `internal/core/placement/placement_test.go` | From a given node the order runs same-rack, then same-datacenter, then remote | — | S4 |
| `TestNearestIsAPermutationOfResolve` | `internal/core/placement/placement_test.go` | `Nearest` reorders and never adds or drops a target — the assertion that keeps read preference from silently changing replica membership | — | S4 |
| `TestNearestKeepsUnknownTargets` | `internal/core/placement/placement_test.go` | An unresolvable target sorts last rather than disappearing, since dropping it would let read preference change membership | — | S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The nine unit tests above. |
| 2 — something selects it | `cmd/sdev1-addr` (T4) calls both `Resolve` and `Nearest`; T4's fence builds and runs the binary, so deleting either call makes it red. |
| 3 — the caller can discover it | Exported doc comments on both functions; `go doc ./internal/core/placement` is the check. |
| 4 — it is used | Nothing measures this yet. |

## Mutation Log

- 2026-09-04 · 4cb6e9f* · mutant killed · exit 1 · `internal/core/placement/placement.go` · a per-call seed makes two clients compute different orders for the same leaf, which is the one thing placement may not do; TestResolveIsDeterministic must go red · acceptance-sha256:a89ae116e849b4c7cc6164a574dbb765f2982717ca0111fd1770e26e7b0f004c · covers:the fixed scoring seed
- 2026-09-04 · 4cb6e9f* · mutant killed · exit 1 · `internal/core/placement/placement.go` · a leaf recorded at one depth resolved against a map declaring another routes it somewhere nobody asked for; TestResolveRefusesDepthMismatch must go red · acceptance-sha256:a89ae116e849b4c7cc6164a574dbb765f2982717ca0111fd1770e26e7b0f004c · covers:the depth guard
- 2026-09-04 · 4cb6e9f* · mutant killed · exit 1 · `internal/core/placement/placement.go` · dropping the targets whose domain is already used makes Spread lose members instead of reordering them, so a durability policy would silently see fewer replicas; TestSpreadIsAPermutation must go red · acceptance-sha256:a89ae116e849b4c7cc6164a574dbb765f2982717ca0111fd1770e26e7b0f004c · covers:Spread preserving membership
- 2026-09-04 · c07f4df* · mutant survived · exit 0 · `internal/core/placement/placement.go` · changes the scoring function while keeping it perfectly deterministic, which is what a reseed or a hash swap looks like from inside one process — every in-process determinism assertion still passes, and only the pinned golden orders notice. This is the exact shape that hid a per-process random seed in this file from its first implementation until 2026-09-04 · acceptance-sha256:00cb57f096fe7a1c094c872b2164a45444daa96e1efa41704b6a5d9acaa826af · covers:scoring that is identical in every process rather than merely within one
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-04 · c07f4df* · mutant survived · exit 0 · `internal/core/placement/placement.go` · perturbs the hash input so every target scores differently and the canonical order changes, while scoring stays perfectly deterministic within a process. That is precisely what a reseed or a hash swap looks like from inside one test binary: every in-process determinism assertion still passes and only the pinned golden orders notice. It is the shape that hid a per-process random seed here from the first implementation until 2026-09-04 · acceptance-sha256:00cb57f096fe7a1c094c872b2164a45444daa96e1efa41704b6a5d9acaa826af · covers:scoring that is identical in every process rather than merely within one
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-04 · c07f4df* · mutant killed · exit 1 · `internal/core/placement/placement.go` · perturbs the scoring input so the canonical order changes while scoring stays perfectly deterministic inside one process. That is exactly what a reseed or a hash swap looks like from within a single test binary — every in-process determinism assertion still passes, and only the pinned golden orders notice. It is the shape that hid a per-process random seed in this file from the first implementation until 2026-09-04 · acceptance-sha256:00cb57f096fe7a1c094c872b2164a45444daa96e1efa41704b6a5d9acaa826af · covers:scoring that is identical in every process rather than merely within one
- 2026-09-04 · c07f4df* · mutant killed · exit 1 · `internal/core/placement/placement.go` · drops the targets whose failure domain is already used instead of deferring them to a later pass, so Spread loses members rather than reordering them — a durability policy would then silently see fewer replicas than the cluster actually holds, and count itself satisfied at a lower copy count than it asked for · acceptance-sha256:00cb57f096fe7a1c094c872b2164a45444daa96e1efa41704b6a5d9acaa826af · covers:Spread preserving membership
- 2026-09-04 · c07f4df* · mutant killed · exit 1 · `internal/core/placement/placement.go` · accepts a leaf recorded at one depth against a map declaring another, so instead of a named refusal the leaf is silently resolved against a depth nobody asked for and routed to servers that do not hold it · acceptance-sha256:00cb57f096fe7a1c094c872b2164a45444daa96e1efa41704b6a5d9acaa826af · covers:the depth guard
- 2026-09-04 · c07f4df* · mutant killed · exit 1 · `internal/core/placement/placement.go` · drops the leaf from the scoring input so every leaf scores its targets identically and one server wins first place for all of them. Scoring stays perfectly deterministic and every determinism assertion still passes — which is the point: a stable ranking can still be a badly distributed one, and FNV-1a failed exactly this way here on 2026-09-04 with a 2.9x spread between the most and least favoured target · acceptance-sha256:00cb57f096fe7a1c094c872b2164a45444daa96e1efa41704b6a5d9acaa826af · covers:scoring that spreads across targets rather than favouring some

## Invariants

- `Resolve` is a pure function of the leaf and the map. No I/O, no clock, no randomness, no caller identity, and no map-iteration order escaping into the result.
- `Resolve`'s result is byte-identical for every caller holding the same map. Two clients agree without talking to each other, and locality is not permitted to weaken this.
- `Nearest` is a permutation. It changes preference order only; membership is `Resolve`'s answer and `Nearest` may not add or drop a server.
- A depth mismatch is refused, never coerced.

## Risks

- Splitting membership from preference is one function more than the obvious design, and a later caller may be tempted to fold them back together for convenience. `TestResolveIsIndependentOfCaller` and `TestNearestIsAPermutationOfResolve` are what make that fold fail rather than merely be discouraged.
- The weight semantics inherited from T2 are declared but unproven against any real cluster; `TestResolveSpreadsAcrossRacks` proves the spread property on the fixture map only and says nothing about a heterogeneous one. Recorded rather than hidden.
- Determinism makes a topology change reshuffle more data than an occupancy-aware rebalancer would move. That is the parent record's stated Negative consequence, not a defect of this task.
- ⚠ **THE SCORING SEED WAS NOT FIXED, AND A KILLED MUTANT SAID IT WAS.** From this task's first implementation until 2026-09-04, `weightedScore` used `maphash.MakeSeed()`, which returns a NEW RANDOM seed per process — so two clients placed the same leaf on different servers, and the data one wrote is where the other would never look. The comment directly above the line asserted the opposite: *"seed is fixed rather than random so that scoring is identical in every process."*
- ⚠ **Why nothing caught it, which is the transferable part.** A Go test binary is ONE process, so the seed was constant for the whole suite and every determinism assertion passed. The mutant bound to the old `the fixed scoring seed` claim introduced a per-CALL seed, which `TestResolveIsDeterministic` does catch. So the mutant was genuinely killed and genuinely proved something — just not the named property. **The mutant and the test shared the test's blind spot**, and a claim can read as proved while the mechanism it names was never exercised. Found by running `cmd/sdev1-addr` twice by hand and getting two different orders.
- ⚠ **A cross-process invariant can only be held by a check on VALUES**, which is why the replacement pins golden orders rather than asserting a property. The earlier row stays in the Mutation Log rather than being deleted, because removing it would hide that a killed mutant once stood in for a false claim.
- ⚠ **AND THE FIRST FIX INTRODUCED A SECOND DEFECT, WHICH IS THE MORE USEFUL HALF OF THIS STORY.** Replacing the seeded hash with FNV-1a made scoring deterministic and made it BADLY DISTRIBUTED: its avalanche is weak enough that the ranking tracked the target's NAME. Measured 2026-09-04 over all 256 leaves the fixture can address, one target won 107 times and another 37, against a fair share of 64 — a 2.9× spread, meaning placement systematically favours some servers and starves others. Every determinism test passed throughout, including the new golden pin, because a biased ranking is still a perfectly stable one.
- ⚠ **Determinism and DISTRIBUTION are separate requirements and this task only ever declared the first.** Scoring now uses SHA-256 truncated to 64 bits (61 / 60 / 65 / 70 on the same measurement), and `TestScoringSpreadsAcrossTargets` holds the band. It was written after checking that FNV-1a fails it at both ends — a guard whose ability to fail has not been demonstrated is not a guard.
- The golden orders are recorded against `testdata/topology/minimal.json`. They are expected to move only when the scoring function is deliberately changed — and such a change re-places every leaf in every existing cluster, which is why it should be hard to do by accident.

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
- 2026-09-04 · 4cb6e9f* · exit 0 · `set -o pipefail …` · acceptance-sha256:a89ae116e849b4c7cc6164a574dbb765f2982717ca0111fd1770e26e7b0f004c · ms:1129
- 2026-09-04 · 4cb6e9f* · exit 0 · `set -o pipefail …` · acceptance-sha256:a89ae116e849b4c7cc6164a574dbb765f2982717ca0111fd1770e26e7b0f004c · ms:1126
- 2026-09-04 · 4cb6e9f* · exit 0 · `set -o pipefail …` · acceptance-sha256:a89ae116e849b4c7cc6164a574dbb765f2982717ca0111fd1770e26e7b0f004c · ms:1141
- 2026-09-04 · 4cb6e9f* · exit 0 · `set -o pipefail …` · acceptance-sha256:a89ae116e849b4c7cc6164a574dbb765f2982717ca0111fd1770e26e7b0f004c · ms:1072
- 2026-09-04 · c07f4df* · exit 0 · `set -o pipefail …` · acceptance-sha256:00cb57f096fe7a1c094c872b2164a45444daa96e1efa41704b6a5d9acaa826af · ms:1706
- 2026-09-04 · c07f4df* · exit 0 · `set -o pipefail …` · acceptance-sha256:00cb57f096fe7a1c094c872b2164a45444daa96e1efa41704b6a5d9acaa826af · ms:1134
- 2026-09-04 · c07f4df* · exit 0 · `set -o pipefail …` · acceptance-sha256:00cb57f096fe7a1c094c872b2164a45444daa96e1efa41704b6a5d9acaa826af · ms:1093
- 2026-09-04 · c07f4df* · exit 0 · `set -o pipefail …` · acceptance-sha256:00cb57f096fe7a1c094c872b2164a45444daa96e1efa41704b6a5d9acaa826af · ms:1314
- 2026-09-04 · c07f4df* · exit 0 · `set -o pipefail …` · acceptance-sha256:00cb57f096fe7a1c094c872b2164a45444daa96e1efa41704b6a5d9acaa826af · ms:1143
- 2026-09-04 · c07f4df* · exit 0 · `set -o pipefail …` · acceptance-sha256:00cb57f096fe7a1c094c872b2164a45444daa96e1efa41704b6a5d9acaa826af · ms:1149
- 2026-09-04 · c07f4df* · exit 0 · `set -o pipefail …` · acceptance-sha256:00cb57f096fe7a1c094c872b2164a45444daa96e1efa41704b6a5d9acaa826af · ms:1167
- 2026-09-04 · c07f4df* · exit 0 · `set -o pipefail …` · acceptance-sha256:00cb57f096fe7a1c094c872b2164a45444daa96e1efa41704b6a5d9acaa826af · ms:1169
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:00cb57f096fe7a1c094c872b2164a45444daa96e1efa41704b6a5d9acaa826af · ms:1205
