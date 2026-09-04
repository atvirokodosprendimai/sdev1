# Task ADR-002-T2: The transaction identifier and its total order

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `tx.TxID`, `tx.TxID.Compare()`, `tx.TxID` fixed-width encoding, `tx.Minter`, `Minter.Observe()`
**Consumes:** `hlc.Timestamp`, `hlc.Clock` (T1); `addr.LeafID` (ADR-001-T1)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the total order across leaves`, `the per-leaf monotonicity`, `the fixed-width byte-comparable encoding`

## Goal

Give every transaction an identifier that is totally ordered across the whole
cluster and strictly monotonic within its own leaf, in a fixed-width encoding a
segment index can order on without decoding.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/tx/tx.go` | add | `TxID{HLC hlc.Timestamp; Leaf addr.LeafID; Seq uint32}` and `Compare`. |
| `internal/core/tx/encoding.go` | add | The fixed-width, byte-comparable encoding. |
| `internal/core/tx/minter.go` | add | `Minter`, which issues `TxID`s for one leaf, holding the clock and the sequence. |
| `internal/core/tx/doc.go` | add | Package comment: why the tie-breakers exist and what "total" buys over "monotonic". |
| `internal/core/tx/tx_test.go` | add | The tests below. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestCompareIsATotalOrder`, `TestTwoLeavesNeverTie`, `TestMinterIsMonotonicPerLeaf`, `TestMinterRefusesAForeignLeaf`, `TestEncodingIsByteComparable`, `TestEncodingIsFixedWidth`, `TestEncodingRoundTrips`. Run the Acceptance fence and confirm it is red. [proof: acceptance]
2. [S2] Define `TxID{HLC, Leaf, Seq}`. `Compare` orders by `HLC`, then by `Leaf`, then by `Seq` — the tie-breakers exist so the order is *total* rather than merely partial, which is what makes a cross-leaf `AS OF` a well-defined question.
3. [S3] Implement the fixed-width encoding: the HLC's 12 bytes, then the leaf identifier, then the 4-byte sequence, all big-endian so that `bytes.Compare` on the encoding agrees with `Compare` on the value.
4. [S4] Implement `Minter` for one leaf: it holds an `hlc.Clock` and a sequence, and issues strictly increasing `TxID`s. One minter per leaf; the single-writer property ADR-003 will rely on is what makes the sequence safe.
5. [S5] Write the package comment explaining that a per-leaf counter would have been simpler and is insufficient, and why. [proof: human: a reader confirms the comment names the rejected simpler design, so the tie-breakers do not read as incidental]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/tx/... -run 'TestCompare|TestTwoLeaves|TestMint|TestObserve|TestEncoding' -count=1 -race 2>&1 | tee /tmp/adr002-t2.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr002-t2.out \
  && go test ./internal/core/hlc/... ./internal/core/addr/... -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestCompareIsATotalOrder` | `internal/core/tx/tx_test.go` | Property test: `Compare` is irreflexive, antisymmetric, transitive, and total over generated identifiers — no two distinct values compare equal | — | S2 |
| `TestTwoLeavesNeverTie` | `internal/core/tx/tx_test.go` | Two leaves minting at the identical HLC reading still order deterministically — the case a per-leaf counter cannot answer | — | S2 |
| `TestMinterIsMonotonicPerLeaf` | `internal/core/tx/tx_test.go` | A minter's output is strictly increasing under concurrent callers | — | S4 |
| `TestEncodingIsByteComparable` | `internal/core/tx/tx_test.go` | Property test: for generated pairs, `bytes.Compare(enc(a), enc(b))` has the same sign as `a.Compare(b)` | — | S3 |
| `TestEncodingIsFixedWidth` | `internal/core/tx/tx_test.go` | Every encoding is the same length, so a segment index can stride over them | — | S3 |
| `TestEncodingRoundTrips` | `internal/core/tx/tx_test.go` | An identifier survives the form an index orders it by, so the byte-comparable encoding is not a one-way projection | — | S3 |
| `TestMinterRefusesAForeignLeaf` | `internal/core/tx/tx_test.go` | A minter issues only for its own leaf, so the per-leaf sequence cannot be advanced by another leaf's writer | — | S4 |
| `TestMintedIdentifiersCarryAdvancingClock` | `internal/core/tx/tx_test.go` | The CLOCK READING advances between mints, not merely that identifiers differ. Added after a surviving mutant showed uniqueness is over-determined, so no uniqueness assertion can bind the clock | — | S4 |
| `TestObserveOrdersAfterARemoteLeaf` | `internal/core/tx/tx_test.go` | A minter that has observed a remote transaction cannot then issue one that appears to precede it — causality crossing a leaf boundary | — | S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The nine unit tests above, two of them property tests over generated identifiers and one exercising eight concurrent callers under `-race`. |
| 2 — something selects it | T3's `Visible` takes a `TxID` and T4's property suite mints them; both fences build against this package. |
| 3 — the caller can discover it | Exported doc comments; `go doc ./internal/core/tx` is the check. |
| 4 — it is used | Nothing measures this yet. |

## Mutation Log

- 2026-09-04 · 47dd203* · mutant killed · exit 1 · `internal/core/tx/tx.go` · without the leaf tie-break two leaves minting at the identical reading and sequence compare equal, and a query spanning both has no defined answer; TestCompareIsATotalOrder and TestTwoLeavesNeverTie must go red · acceptance-sha256:a42b0b741e03b6338efc506ab0879abaab24d20c7cc816dd6e235de3c65a07ec · covers:the total order across leaves
- 2026-09-04 · 47dd203* · mutant survived · exit 0 · `internal/core/tx/minter.go` · probing whether the clock reading is load-bearing for uniqueness or whether the sequence alone carries it · acceptance-sha256:a42b0b741e03b6338efc506ab0879abaab24d20c7cc816dd6e235de3c65a07ec · covers:the per-leaf monotonicity
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-04 · 47dd203* · mutant survived · exit 0 · `internal/core/tx/minter.go` · second attempt: the first mutant SURVIVED because uniqueness is over-determined — the clock reading and the sequence are each independently sufficient, so no uniqueness test can kill either alone. TestMintedIdentifiersCarryAdvancingClock asserts the reading advances, which is what cross-leaf ordering actually rests on · acceptance-sha256:a42b0b741e03b6338efc506ab0879abaab24d20c7cc816dd6e235de3c65a07ec · covers:the per-leaf monotonicity
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-04 · 47dd203* · mutant killed · exit 1 · `internal/core/tx/encoding.go` · a little-endian field breaks byte-order agreement with Compare, so a segment index sorting on the bytes would order transactions wrongly; TestEncodingIsByteComparable must go red · acceptance-sha256:a42b0b741e03b6338efc506ab0879abaab24d20c7cc816dd6e235de3c65a07ec · covers:the fixed-width byte-comparable encoding
- 2026-09-04 · 47dd203* · mutant killed · exit 1 · `internal/core/tx/tx.go` · re-bound to the widened fence: without the leaf tie-break two leaves at the identical reading and sequence compare equal · acceptance-sha256:984ebcedb35091be69b16f24751452e03fa2a3223c0af97ca57f9f164b045537 · covers:the total order across leaves
- 2026-09-04 · 47dd203* · mutant killed · exit 1 · `internal/core/tx/minter.go` · third attempt. It survived twice: first because uniqueness is over-determined (clock and sequence are each sufficient), then because the falsifier sat OUTSIDE the fence filter. Both fixed · acceptance-sha256:984ebcedb35091be69b16f24751452e03fa2a3223c0af97ca57f9f164b045537 · covers:the per-leaf monotonicity
- 2026-09-04 · 47dd203* · mutant killed · exit 1 · `internal/core/tx/encoding.go` · re-bound to the widened fence: a little-endian field breaks byte-order agreement with Compare · acceptance-sha256:984ebcedb35091be69b16f24751452e03fa2a3223c0af97ca57f9f164b045537 · covers:the fixed-width byte-comparable encoding

## Invariants

- `Compare` is a total order: no two distinct `TxID`s compare equal.
- A `Minter` for one leaf issues strictly increasing identifiers, safely under concurrency.
- The encoding is fixed-width and `bytes.Compare` on it agrees with `Compare` on the value — this is what lets a segment index sort without decoding.

## Risks

- The identifier is 16 bytes in every datom, and ADR-002 names this as the largest fixed overhead the design imposes. Nothing here reduces it; ADR-005 owns earning it back by interning the other fields.
- `Seq` is per-leaf and safe only under single-writer. If ADR-003 admits concurrent writers to one leaf, this task's monotonicity argument does not hold and the minter needs revisiting.
- ★ UNIQUENESS IS OVER-DETERMINED, and it cost two surviving mutants to notice. The clock reading and the sequence are each independently sufficient to make identifiers distinct, so replacing the clock reading with a repeated one leaves every uniqueness assertion green. What actually breaks is CROSS-LEAF ORDERING: identifiers from one leaf would all carry the same reading, and ordering between leaves would collapse onto tie-breakers that say nothing about when anything happened. The test that binds the clock therefore asserts the reading ADVANCES rather than that identifiers differ. ⚠The second survival had a different cause worth recording separately: the new test sat OUTSIDE the fence's `-run` filter, because `TestMinter` does not match `TestMinted`. A falsifier beside the command rather than inside it proves nothing.

## Stop Condition

Stop and ask if ADR-003 will permit more than one writer per leaf. `Minter`'s
sequence assumes it will not, and that assumption is load-bearing rather than
convenient.

## Out of Scope

- Persisting a minter's state across a restart — recovery is ADR-009's, and a restarted leaf gets its position from the log rather than from local state.
- Compressing the identifier within a segment (permanent: boundary: a transaction identifier must be comparable without decoding neighbours, so any delta scheme is ADR-005's segment-format concern rather than an identity one)

## Verification Log
- 2026-09-04 · 47dd203* · exit 0 · `set -o pipefail …` · acceptance-sha256:a42b0b741e03b6338efc506ab0879abaab24d20c7cc816dd6e235de3c65a07ec · ms:2691
- 2026-09-04 · 47dd203* · exit 0 · `set -o pipefail …` · acceptance-sha256:a42b0b741e03b6338efc506ab0879abaab24d20c7cc816dd6e235de3c65a07ec · ms:2699
- 2026-09-04 · 47dd203* · exit 0 · `set -o pipefail …` · acceptance-sha256:a42b0b741e03b6338efc506ab0879abaab24d20c7cc816dd6e235de3c65a07ec · ms:2652
- 2026-09-04 · 47dd203* · exit 0 · `set -o pipefail …` · acceptance-sha256:a42b0b741e03b6338efc506ab0879abaab24d20c7cc816dd6e235de3c65a07ec · ms:3085
- 2026-09-04 · 47dd203* · exit 0 · `set -o pipefail …` · acceptance-sha256:a42b0b741e03b6338efc506ab0879abaab24d20c7cc816dd6e235de3c65a07ec · ms:2842
- 2026-09-04 · 47dd203* · exit 0 · `set -o pipefail …` · acceptance-sha256:984ebcedb35091be69b16f24751452e03fa2a3223c0af97ca57f9f164b045537 · ms:2561
- 2026-09-04 · 47dd203* · exit 0 · `set -o pipefail …` · acceptance-sha256:984ebcedb35091be69b16f24751452e03fa2a3223c0af97ca57f9f164b045537 · ms:2764
- 2026-09-04 · 47dd203* · exit 0 · `set -o pipefail …` · acceptance-sha256:984ebcedb35091be69b16f24751452e03fa2a3223c0af97ca57f9f164b045537 · ms:2739
- 2026-09-04 · 47dd203* · exit 0 · `set -o pipefail …` · acceptance-sha256:984ebcedb35091be69b16f24751452e03fa2a3223c0af97ca57f9f164b045537 · ms:2958
