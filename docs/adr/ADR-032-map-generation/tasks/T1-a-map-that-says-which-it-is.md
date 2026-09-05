# Task ADR-032-T1: Give a map an identity, and refuse to place without one

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `topology.Map.Generation`, `topology.Map.FormatVersion`, `topology.Map.Placeable`, `topology.ErrBadGeneration`, `placement.ErrNoGeneration`
**Consumes:** `topology.Map`, `topology.Load` from ADR-001; `tx.TxID`, `tx.Encode`, `tx.Decode` from ADR-002; `placement.Resolve` from ADR-001
**Data dependency:** hermetic — authored maps the tests write
**Proof map:** v1
**Rests-on:** `a placement refused against a map that cannot say which it is`, `a generation that is not the file format version`, `a generation authored rather than assigned at load`

## Goal

Make a placement reproducible against the map it was made under, or refused.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/topology/generation.go` | add | The generation's authored form, and its decoding. |
| `internal/core/topology/topology.go` | modify | `Map` gains `Generation`; `Version` becomes `FormatVersion`. |
| `internal/core/topology/load.go` | modify | Decode the optional `generation` field. |
| `internal/core/topology/topology_test.go` | modify | The renamed field, and the generation cases. |
| `internal/core/placement/placement.go` | modify | `Resolve` refuses a map with no generation. |
| `internal/core/placement/generation_test.go` | add | The falsifier. |
| `internal/core/placement/placement_test.go` | modify | Its fixtures carry a generation, since they place. |
| `testdata/topology/minimal.json` | modify | The fixture the binary runs against gains one. |

⚠ `internal/core/topology/**` and `internal/core/placement/**` are governed by
ADR-001, not by this record. The Acceptance re-runs its suites for that reason.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestPlacementRefusesAMapThatCannotSayWhichItIs`, `TestGenerationIsNotTheFormatVersion`, `TestAGenerationIsAuthoredNotAssigned`, `TestAMalformedGenerationIsRefused`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Rename `Map.Version` to `Map.FormatVersion`. ⚠Not cosmetic: a field called `Version` on a map, meaning the FILE FORMAT, is the single most likely way to implement this decision wrongly — and the wrong implementation looks correct and never fails. [proof: mutation]
3. [S3] Add `Map.Generation` as a `tx.TxID`, and an optional authored `generation` field carrying the hex of its fixed-width encoding. ★One representation, because `tx` already has a canonical one. [proof: mutation]
4. [S4] Decode the generation at load, refusing a malformed one with `ErrBadGeneration`, and never GENERATING one. ⚠A generation minted at load gives the same file a different identity in every process, which is the original failure wearing a new hat. [proof: mutation]
5. [S5] Add `Map.Placeable`, reporting whether the map carries a generation, so the question has one spelling. [proof: mutation]
6. [S6] Make `placement.Resolve` refuse a map that is not placeable, with `ErrNoGeneration`. ⚠A zero generation must never read as "generation zero": that is an answer, and it means every map is the same map. [proof: mutation]
7. [S7] Give every fixture that PLACES a generation, and leave the ones that only inspect a map without one — the refusal belongs where the consequence is. ★The fence re-runs every package that loads a map, so a fixture that places without a generation fails there rather than needing a test of its own. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/placement/... -race -run 'TestPlacementRefusesAMapThatCannotSayWhichItIs|TestGenerationIsNotTheFormatVersion|TestAGenerationIsAuthoredNotAssigned' -count=1 2>&1 | tee /tmp/adr032-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr032-t1a.out \
  && go test ./internal/core/topology/... -race -run 'TestAMalformedGenerationIsRefused' -count=1 2>&1 | tee /tmp/adr032-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr032-t1b.out \
  && go test ./internal/core/topology/... ./internal/core/placement/... ./internal/core/durability/... ./internal/core/prefetch/... -race -count=1 2>&1 | tee /tmp/adr032-t1c.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr032-t1c.out \
  && go build -o /dev/null ./cmd/sdev1-addr
```

The third command re-runs every package that loads a map, and the build proves the
binary that reads the fixture still compiles against the renamed field.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestPlacementRefusesAMapThatCannotSayWhichItIs` | `internal/core/placement/generation_test.go` | `Resolve` against a map loaded without a generation is `ErrNoGeneration` and returns no targets — **the falsifier ADR-032 names in `Enforced-by:`**. ⚠ The same map WITH a generation resolves normally, so the refusal is about the identity rather than about the map being broken | — | S6 |
| `TestGenerationIsNotTheFormatVersion` | `internal/core/placement/generation_test.go` | Two maps with the same `FormatVersion` and different generations are distinguishable, and a map's generation is not derived from its format version — the trap this record exists to remove | — | S2, S3 |
| `TestAGenerationIsAuthoredNotAssigned` | `internal/core/placement/generation_test.go` | The same bytes loaded twice give the same generation, so two processes reading one file agree about which map it is | — | S4 |
| `TestAMalformedGenerationIsRefused` | `internal/core/topology/generation_test.go` | A `generation` field that is not the hex of an encoded identifier is `ErrBadGeneration` at load, rather than a map that silently has none | — | S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests above. |
| 2 — something selects it | `Resolve` is the only placement path and it consults `Placeable`; `Load` is the only way a map is built from a file. |
| 3 — the caller can discover it | Two named sentinels, and `Placeable` gives the question one spelling. |
| 4 — it is used | `cmd/sdev1-addr` places against the fixture, which now carries a generation. ⚠ No SEGMENT records one, because nothing places a segment — see the parent record. |

## Mutation Log

- 2026-09-05 · c86f335* · mutant killed · exit 1 · `internal/core/placement/placement.go` · places against a map with no identity, so the location it returns cannot be arrived at again — and the answer looks exactly like a good one, which is why nothing else notices · acceptance-sha256:bf456c4e4ec3e75bd8ae252baae6e0a33f00ba3fd0cf8b323810f6315bb7434b · covers:a placement refused against a map that cannot say which it is
- 2026-09-05 · c86f335* · mutant killed · exit 1 · `internal/core/topology/generation.go` · answers the identity question from the FORMAT VERSION, which is a constant — so every map reports itself placeable and every map claims the same generation forever, which is the exact trap this record removes · acceptance-sha256:bf456c4e4ec3e75bd8ae252baae6e0a33f00ba3fd0cf8b323810f6315bb7434b · covers:a generation that is not the file format version
- 2026-09-05 · c86f335* · mutant inconclusive · exit 1 · `internal/core/topology/generation.go` · mints a generation when the file carries none, so the same bytes become a different map in every process that reads them — the original failure wearing a new hat · acceptance-sha256:bf456c4e4ec3e75bd8ae252baae6e0a33f00ba3fd0cf8b323810f6315bb7434b · covers:a generation authored rather than assigned at load
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-05 · c86f335* · mutant killed · exit 1 · `internal/core/topology/generation.go` · ignores the authored value and substitutes one of its own, so every map that carries a generation reports the SAME one; a clock-derived assignment is the other shape of this defect and cannot be mutated in without an import the mutation may not add · acceptance-sha256:bf456c4e4ec3e75bd8ae252baae6e0a33f00ba3fd0cf8b323810f6315bb7434b · covers:a generation authored rather than assigned at load

## Invariants

- A placement against a map with no generation is refused.
- A generation is a transaction identifier, never the format version.
- The same file yields the same generation in every process.

## Risks

- ⚠ **The rename is the substance of S2, not tidying.** Leaving a field called `Version` beside `Generation` keeps the trap alive: the next person to reach for a map's identity finds a plausible field that is a constant.
- ⚠ **A refusal test must also show the map RESOLVING with a generation**, or it proves only that the map was broken. Both halves use the same authored map, differing in one field.
- ⚠ **"Authored, not assigned" is invisible in a single-process test unless the same bytes are loaded twice.** The test loads one source twice and compares, which is the only shape that catches a generation minted at load.
- Nothing records the generation a segment was placed under, because nothing places a segment. Stated on the parent record as the follow-up rather than half-built here.

## Stop Condition

Stop and ask before defaulting a missing generation to the zero identifier. It is
what Go's zero value does for free, it makes every unidentified map the same map,
and nothing fails.

## Out of Scope

- Recording a generation in a segment header (deferred: `docs/adr/BACKLOG.md` §6)
- Publishing or distributing a map (deferred: `docs/adr/BACKLOG.md` §18)
- Retiring a generation (deferred: `docs/adr/BACKLOG.md` §6)
- Who mints a generation, and under what authority (deferred: `docs/adr/BACKLOG.md` §19)
- Ordering maps by anything but their transaction (permanent: boundary: ADR-002's identifier is the system's only total order)

## Verification Log
- 2026-09-05 · c86f335* · exit 0 · `set -o pipefail …` · acceptance-sha256:bf456c4e4ec3e75bd8ae252baae6e0a33f00ba3fd0cf8b323810f6315bb7434b · ms:5677
- 2026-09-05 · c86f335* · exit 0 · `set -o pipefail …` · acceptance-sha256:bf456c4e4ec3e75bd8ae252baae6e0a33f00ba3fd0cf8b323810f6315bb7434b · ms:5976
- 2026-09-05 · c86f335* · exit 0 · `set -o pipefail …` · acceptance-sha256:bf456c4e4ec3e75bd8ae252baae6e0a33f00ba3fd0cf8b323810f6315bb7434b · ms:5878
- 2026-09-05 · c86f335* · exit 0 · `set -o pipefail …` · acceptance-sha256:bf456c4e4ec3e75bd8ae252baae6e0a33f00ba3fd0cf8b323810f6315bb7434b · ms:5843
- 2026-09-05 · c86f335* · exit 0 · `set -o pipefail …` · acceptance-sha256:bf456c4e4ec3e75bd8ae252baae6e0a33f00ba3fd0cf8b323810f6315bb7434b · ms:5765
- 2026-09-05 · c86f335* · exit 0 · `set -o pipefail …` · acceptance-sha256:bf456c4e4ec3e75bd8ae252baae6e0a33f00ba3fd0cf8b323810f6315bb7434b · ms:5890
