# Task ADR-006-T2: Encode a block into a stripe, and reconstruct it from the survivors

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `erasure.Encode`, `erasure.Reconstruct`, `erasure.SchemeFromPolicy`, `erasure.Stripe`, `erasure.ErrInsufficientFragments`
**Consumes:** `erasure.StripeHeader`, `erasure.Fragment` (T1), `durability.Policy` from ADR-004, `segment.StageCoded` from ADR-005
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `a corrupt fragment being treated as absent rather than as data`, `the refusal to reconstruct from fewer than k verified fragments`, `the block's true length being recorded rather than inferred`

## Goal

Turn a block into `k+m` verifiable fragments and back, tolerating the loss of any
`m` of them, and refusing rather than inventing when too few survive.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/erasure/erasure.go` | add | `Encode`, `Reconstruct`, `SchemeFromPolicy`, and `ErrInsufficientFragments`. |
| `internal/core/erasure/coding_test.go` | add | The tests below. T1's format tests stay in `erasure_test.go`; the coding tests are a separate file because they exercise a different thing and share no fixtures with it. |
| `go.mod`, `go.sum` | edit | Add `github.com/klauspost/reedsolomon` — pure Go, so the project still ships without cgo. |

★ `SchemeFromPolicy` is the line that SELECTS a scheme, and it is why
`durability.Policy` appears in the Affected Files at all: it is the only place a
`k` and an `m` enter this package, and deleting it would leave `Encode` reachable
only from a test. Its test is `TestPolicySelectsTheScheme`.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestEncodeReconstructRoundTrips`, `TestAnyMFragmentsMayBeLost`, `TestCorruptFragmentIsTreatedAsAbsent`, `TestReconstructionRefusesBelowK`, `TestEncodingIsDeterministic`, `TestPolicySelectsTheScheme`, `TestCodedBlocksAreFlagged`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant — `-run` matches substrings, and `TestEncode` does NOT select `TestEncodingIsDeterministic`. [proof: acceptance]
2. [S2] Implement `SchemeFromPolicy(durability.Policy) (StripeHeader, error)`, taking `DataShards` and `ParityShards` from the policy and adding no arithmetic of its own. ★ADR-004 already decides how many failure domains a scheme needs and refuses a policy the topology cannot satisfy; a second definition here would be a cluster that codes data it cannot place.
3. [S3] Implement `Encode`: split the block into `k` data fragments, produce `m` parity fragments, checksum each, and return them with the stripe header. Pad the final data fragment deterministically and record the block's true length in the header so the padding is removable without guessing.
4. [S4] Set `segment.StageCoded` on the block header the coder was given, so a reader knows this block is coded. ★A segment may hold coded and uncoded blocks; nothing may assume that sealing implies coding.
5. [S5] Implement `Reconstruct`: verify every supplied fragment FIRST, discard those that fail, and treat them as absent. ★This is the step that makes the scheme's stated tolerance true — a fragment that fails its checksum must never reach the decoder as data.
6. [S6] Refuse with `ErrInsufficientFragments` when fewer than `k` fragments verify, naming how many were needed and how many survived. ★Below `k` the information is not present; a best-effort reconstruction would be returning invention.
7. [S7] After reassembly, check the block against ADR-005's block checksum before returning it. ★It should be redundant given S5, and it is the only check spanning the whole path — a coding-matrix bug passes every local check and fails this one.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/erasure/... -run 'TestEncod|TestReconstruct|TestAnyM|TestCorruptFragment|TestCodedBlocks|TestPolicySelects' -count=1 2>&1 | tee /tmp/adr006-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr006-t2a.out \
  && go test ./internal/core/erasure/... ./internal/core/segment/... ./internal/core/durability/... -count=1 2>&1 | tee /tmp/adr006-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr006-t2b.out
```

The first command is this task's own work and can carry the verdict alone. The
second is the regression half — the two packages this task consumes contracts
from — and cannot stand in for the first, because it does not name the new unit.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestEncodeReconstructRoundTrips` | `internal/core/erasure/coding_test.go` | Property test: encode then reconstruct returns the original block, across generated block sizes and several schemes, including sizes that do not divide by `k` | — | S3, S5 |
| `TestAnyMFragmentsMayBeLost` | `internal/core/erasure/coding_test.go` | Every combination of `m` lost fragments still reconstructs — not one sampled combination, which would leave the parity positions untested | — | S5 |
| `TestCorruptFragmentIsTreatedAsAbsent` | `internal/core/erasure/coding_test.go` | A fragment whose bytes are altered is excluded by its checksum and the block still reconstructs correctly; with `m` losses AND one corruption, reconstruction refuses rather than returning wrong bytes | — | S5 |
| `TestReconstructionRefusesBelowK` | `internal/core/erasure/coding_test.go` | Fewer than `k` verified fragments yields `ErrInsufficientFragments` naming both counts, rather than a best-effort answer | — | S6 |
| `TestEncodingIsDeterministic` | `internal/core/erasure/coding_test.go` | The same block and scheme produce byte-identical fragments across repeated encodes, so a rebuilt fragment is indistinguishable from the one it replaces | — | S3 |
| `TestPolicySelectsTheScheme` | `internal/core/erasure/coding_test.go` | `k` and `m` come from `durability.Policy` and this package computes no second opinion; a policy that is not coded is refused | — | S2 |
| `TestCodedBlocksAreFlagged` | `internal/core/erasure/coding_test.go` | Encoding sets `segment.StageCoded` on the block header, so a reader is never left assuming that a sealed block is coded | — | S4, S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The seven tests above. |
| 2 — something selects it | `SchemeFromPolicy` is the one line that turns an ADR-004 policy into a scheme, and `TestPolicySelectsTheScheme` fails if it is removed. No production caller exists yet, which is stated rather than implied. |
| 3 — the caller can discover it | Exported doc comments and a named sentinel; `Reconstruct` takes fragments and a header and no configuration, so its signature is the contract. |
| 4 — it is used | Nothing measures this yet. |

## Mutation Log

- 2026-09-04 · b3162b1* · mutant killed · exit 1 · `internal/core/erasure/erasure.go` · lets a fragment that fails its checksum through to the decoder as data, which drops the tolerance from m erasures to half that many errors and produces a reconstructed block that is wrong while reporting success · acceptance-sha256:6e7cc5f69a3d1ef77424fb858b936df5ead9cb80c261453abd3d48ae112a9e6b · covers:a corrupt fragment being treated as absent rather than as data
- 2026-09-04 · b3162b1* · mutant killed · exit 1 · `internal/core/erasure/erasure.go` · removes the floor, so reconstruction is attempted with less information than the block contains and returns whatever the decoder produces instead of refusing · acceptance-sha256:6e7cc5f69a3d1ef77424fb858b936df5ead9cb80c261453abd3d48ae112a9e6b · covers:the refusal to reconstruct from fewer than k verified fragments
- 2026-09-04 · b3162b1* · mutant killed · exit 1 · `internal/core/erasure/erasure.go` · infers the block length from the padded fragment geometry instead of recording it, so every block whose length does not divide by k comes back with its padding attached · acceptance-sha256:6e7cc5f69a3d1ef77424fb858b936df5ead9cb80c261453abd3d48ae112a9e6b · covers:the block's true length being recorded rather than inferred

## Invariants

- A fragment that fails its checksum is never given to the decoder as data.
- Reconstruction from fewer than `k` verified fragments is refused, never attempted.
- `k` and `m` come from `durability.Policy`; this package adds no arithmetic about failure domains.
- Encoding is deterministic: the same block and scheme give byte-identical fragments.
- `Reconstruct` takes no configuration argument, so it cannot consult one.
- This package performs no file I/O.

## Risks

- A property test over random loss patterns can miss the parity positions entirely. `TestAnyMFragmentsMayBeLost` enumerates every combination of `m` losses rather than sampling, which is cheap at the schemes under test and is the only form that covers them.
- A test that corrupts a fragment without also removing others proves less than it appears to: the code may simply have had enough survivors. The corruption test therefore ALSO runs at the tolerance boundary, where excluding the corrupt fragment is the difference between a correct answer and a refusal.
- The library's own padding or shard-sizing behaviour is an assumption. The block's true length is recorded in the stripe header rather than inferred, so a change in the library cannot silently alter what a decoded block contains.
- ⚠ **`TestEncodingIsDeterministic` proves less than its name suggests, and `Rests-on:` deliberately does not claim it.** It encodes repeatedly in ONE process against ONE build, so it proves the function has no internal source of entropy. It cannot prove determinism across library versions or platforms, which is the risk that actually matters for repair: a coding matrix that changed between releases would make every node's rebuilt fragment agree with itself and disagree with its neighbours. Nor is the claim bindable by mutation — a text substitution cannot introduce entropy, so no mutant can make this test fail while the code still compiles. What guards the real risk is the pinned dependency version and the format version in ADR-005, not this test. Recorded rather than papered over.

## Stop Condition

Stop and ask before adding a coding library that requires cgo. The house
preference is a pure-Go build, and a dependency that breaks it changes how the
whole project ships rather than just how a block is stored.

## Out of Scope

- When a damaged stripe is repaired, and how much bandwidth repair may consume (deferred: `docs/adr/BACKLOG.md` §3)
- Re-coding existing stripes after the configured scheme changes (deferred: `docs/adr/BACKLOG.md` §14)
- Anything that opens a file, including writing fragments out (deferred: `docs/adr/BACKLOG.md` §12)

## Verification Log
- 2026-09-04 · b3162b1* · exit 0 · `set -o pipefail …` · acceptance-sha256:6e7cc5f69a3d1ef77424fb858b936df5ead9cb80c261453abd3d48ae112a9e6b · ms:1387
- 2026-09-04 · b3162b1* · exit 0 · `set -o pipefail …` · acceptance-sha256:6e7cc5f69a3d1ef77424fb858b936df5ead9cb80c261453abd3d48ae112a9e6b · ms:1431
- 2026-09-04 · b3162b1* · exit 0 · `set -o pipefail …` · acceptance-sha256:6e7cc5f69a3d1ef77424fb858b936df5ead9cb80c261453abd3d48ae112a9e6b · ms:1386
- 2026-09-04 · b3162b1* · exit 0 · `set -o pipefail …` · acceptance-sha256:6e7cc5f69a3d1ef77424fb858b936df5ead9cb80c261453abd3d48ae112a9e6b · ms:1414
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:6e7cc5f69a3d1ef77424fb858b936df5ead9cb80c261453abd3d48ae112a9e6b · ms:1376
