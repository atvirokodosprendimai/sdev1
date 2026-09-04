# Task ADR-001-T1: The key type, the leaf identifier, and the byte-wise descent

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `addr.Key`, `addr.LeafID`, `addr.Descend()`, `addr.FanOut`
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the FanOut constant`, `the leaf identifier carrying its own depth`

## Goal

Provide the pure addressing core: hash an entity identifier to a 32-byte key, and
descend that key one byte per level to a leaf identifier that carries its own
depth.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `go.mod` | add | The module does not exist yet; this task creates it. |
| `internal/core/addr/addr.go` | add | `Key`, `LeafID`, `Descend`, and the `FanOut` constant. |
| `internal/core/addr/doc.go` | add | Package comment stating the addressing model in one paragraph, so the decision is readable from the code. |
| `internal/core/addr/addr_test.go` | add | The tests below, including the falsifiability fixture for `FanOut`. |

Nothing selects this package yet — it is the kernel every later package imports,
and T3 is its first caller. Rung 2 below records that honestly rather than
claiming a call site that does not exist.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestFanOutIsExactlyOneByte`,
   `TestDescendConsumesOneBytePerLevel`, `TestLeafIDIsStableAcrossDepthChange`,
   `TestDescendIsDeterministic`. Run the Acceptance fence and confirm it is red —
   at this point `internal/core/addr` does not exist, so the fence fails on
   "matched no packages", which is a legitimate red. [proof: acceptance]
2. [S2] Create `go.mod` for module `github.com/atvirokodosprendimai/sdev1` at Go 1.26. [proof: acceptance]
3. [S3] Define `const FanOut = 256` and `type Key [32]byte`, with `KeyOf(entity string) Key`
   returning the SHA-256 digest.
4. [S4] Define `type LeafID struct { Prefix [32]byte; Depth uint8 }` — the prefix
   bytes that produced the leaf plus the depth that produced it. Depth travels
   inside the identifier so a leaf stays interpretable after the cluster's live
   depth changes.
5. [S5] Implement `Descend(k Key, depth uint8) (LeafID, error)`, consuming `depth`
   bytes most-significant-first and refusing `depth == 0` or `depth > 32` with a
   named sentinel error.
6. [S6] Write the package comment stating the model, the fan-out invariant, and
   why depth is carried in the identifier.
   [proof: human: a reader confirms the comment states the invariant rather than restating the signature]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/addr/... -run 'TestFanOut|TestDescend|TestLeafID' -count=1 2>&1 | tee /tmp/adr001-t1.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr001-t1.out
```

Before the package exists this fails on `matched no packages`, so the fence is
red at the moment it is written. The `grep` guard is inside the same command as
the run, and `set -o pipefail` makes the pipeline carry the runner's status
rather than `tee`'s.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestFanOutIsExactlyOneByte` | `internal/core/addr/addr_test.go` | `FanOut == 1 << 8`; a fan-out that is not one byte makes the descent stop being a byte walk | — | S3 |
| `TestDescendConsumesOneBytePerLevel` | `internal/core/addr/addr_test.go` | A descent of depth *d* reads exactly the first *d* bytes and no others | — | S5 |
| `TestLeafIDIsStableAcrossDepthChange` | `internal/core/addr/addr_test.go` | Raising the live depth does not rename a leaf recorded at the old depth — the falsifier for the decision's central claim | — | S4, S5 |
| `TestDescendIsDeterministic` | `internal/core/addr/addr_test.go` | The same entity yields the same leaf across processes; no map iteration or clock enters the path | — | S3, S5 |
| `TestDescendRejectsOutOfRangeDepth` | `internal/core/addr/addr_test.go` | Depth 0 and depth 33 return the sentinel rather than a silently truncated leaf | — | S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five unit tests above. |
| 2 — something selects it | Nothing yet, and this is honest rather than a gap: `addr` is the kernel and T3 is its first caller. The mutation that proves reach belongs to T3, whose fence imports this package. |
| 3 — the caller can discover it | The package comment (S6) plus exported doc comments on `Key`, `LeafID` and `Descend`; `go doc ./internal/core/addr` is the check. |
| 4 — it is used | Nothing measures this yet. |

## Mutation Log

- 2026-09-04 · 2db614d* · mutant killed · exit 1 · `internal/core/addr/addr.go` · ADR-001 rule 4 says fan-out must be exactly one byte; a fan-out of 128 still compiles and reinterprets every stored key, so TestFanOutIsExactlyOneByte must go red · acceptance-sha256:b11945974e95af2dd553afa8ea8538d91bf7c2fae9208709b6582a6f77d62daf · covers:the FanOut constant
- 2026-09-04 · 2db614d* · mutant killed · exit 1 · `internal/core/addr/addr.go` · ADR-001 rule 5 makes a depth change a subdivision rather than a rename only because the identifier carries the depth that produced it; dropping that makes every leaf claim depth 1 and TestLeafIDIsStableAcrossDepthChange must go red · acceptance-sha256:b11945974e95af2dd553afa8ea8538d91bf7c2fae9208709b6582a6f77d62daf · covers:the leaf identifier carrying its own depth

## Invariants

- `FanOut` is a compile-time constant equal to 256 and is never read from configuration.
- `Descend` performs no I/O, allocates no map, and consults no clock — the same key and depth yield the same leaf in every process, forever.
- A `LeafID` carries the depth that produced it, so it is interpretable without knowing the cluster's current depth.

## Risks

- Someone later makes `FanOut` configurable for flexibility, silently changing what every stored key means. Mitigated by `TestFanOutIsExactlyOneByte` and by naming that test in the parent record's `Enforced-by:` line.
- `Key` as a `[32]byte` value type copies on every call. Measured cost is unknown and unmeasured at authoring; if it matters it is a later optimisation, not a reason to reach for a pointer now.

## Stop Condition

Stop and ask if the entity identifier turns out not to be a string — the hash
input is a contract with whatever creates entities, and this task assumes a
string because no record has decided otherwise.

## Out of Scope

- Choosing the live depth for any real cluster — that is T2's topology map.
- Anything about which servers hold a leaf — that is T3.

## Verification Log
- 2026-09-04 · 2db614d* · exit 0 · `set -o pipefail …` · acceptance-sha256:b11945974e95af2dd553afa8ea8538d91bf7c2fae9208709b6582a6f77d62daf · ms:605
- 2026-09-04 · 2db614d* · exit 0 · `set -o pipefail …` · acceptance-sha256:b11945974e95af2dd553afa8ea8538d91bf7c2fae9208709b6582a6f77d62daf · ms:697
- 2026-09-04 · 2db614d* · exit 0 · `set -o pipefail …` · acceptance-sha256:b11945974e95af2dd553afa8ea8538d91bf7c2fae9208709b6582a6f77d62daf · ms:535
