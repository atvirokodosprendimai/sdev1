# Task ADR-002-T2: The transaction identifier and its total order

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `tx.TxID`, `tx.TxID.Compare()`, `tx.TxID` fixed-width encoding, `tx.Minter`
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

1. [S1] Write the failing tests first (TDD red): `TestCompareIsATotalOrder`, `TestTwoLeavesNeverTie`, `TestMinterIsMonotonicPerLeaf`, `TestEncodingIsByteComparable`, `TestEncodingIsFixedWidth`. Run the Acceptance fence and confirm it is red. [proof: acceptance]
2. [S2] Define `TxID{HLC, Leaf, Seq}`. `Compare` orders by `HLC`, then by `Leaf`, then by `Seq` — the tie-breakers exist so the order is *total* rather than merely partial, which is what makes a cross-leaf `AS OF` a well-defined question.
3. [S3] Implement the fixed-width encoding: the HLC's 12 bytes, then the leaf identifier, then the 4-byte sequence, all big-endian so that `bytes.Compare` on the encoding agrees with `Compare` on the value.
4. [S4] Implement `Minter` for one leaf: it holds an `hlc.Clock` and a sequence, and issues strictly increasing `TxID`s. One minter per leaf; the single-writer property ADR-003 will rely on is what makes the sequence safe.
5. [S5] Write the package comment explaining that a per-leaf counter would have been simpler and is insufficient, and why. [proof: human: a reader confirms the comment names the rejected simpler design, so the tie-breakers do not read as incidental]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/tx/... -run 'TestCompare|TestTwoLeaves|TestMinter|TestEncoding' -count=1 -race 2>&1 | tee /tmp/adr002-t2.out \
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

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five unit tests above. |
| 2 — something selects it | T3's `Visible` takes a `TxID` and T4's property suite mints them; both fences build against this package. |
| 3 — the caller can discover it | Exported doc comments; `go doc ./internal/core/tx` is the check. |
| 4 — it is used | Nothing measures this yet. |

## Mutation Log

## Invariants

- `Compare` is a total order: no two distinct `TxID`s compare equal.
- A `Minter` for one leaf issues strictly increasing identifiers, safely under concurrency.
- The encoding is fixed-width and `bytes.Compare` on it agrees with `Compare` on the value — this is what lets a segment index sort without decoding.

## Risks

- The identifier is 16 bytes in every datom, and ADR-002 names this as the largest fixed overhead the design imposes. Nothing here reduces it; ADR-005 owns earning it back by interning the other fields.
- `Seq` is per-leaf and safe only under single-writer. If ADR-003 admits concurrent writers to one leaf, this task's monotonicity argument does not hold and the minter needs revisiting.

## Stop Condition

Stop and ask if ADR-003 will permit more than one writer per leaf. `Minter`'s
sequence assumes it will not, and that assumption is load-bearing rather than
convenient.

## Out of Scope

- Persisting a minter's state across a restart — recovery is ADR-009's, and a restarted leaf gets its position from the log rather than from local state.
- Compressing the identifier within a segment (permanent: boundary: a transaction identifier must be comparable without decoding neighbours, so any delta scheme is ADR-005's segment-format concern rather than an identity one)

## Verification Log
