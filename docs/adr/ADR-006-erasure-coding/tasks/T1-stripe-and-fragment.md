# Task ADR-006-T1: The stripe header, the fragment, and the checksum that makes an error an erasure

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `erasure.StripeHeader`, `erasure.StripeHeaderSize`, `erasure.Fragment`, `erasure.FragmentHeaderSize`, `erasure.MaxCodePositions`, `erasure.ErrSchemeTooWide`, `erasure.ErrInvalidScheme`, `erasure.ErrShortBuffer`
**Consumes:** `segment.Checksum` from ADR-005, `addr.LeafID` from ADR-001
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the scheme being recorded in the stripe rather than in configuration`, `the refusal of a scheme wider than the field allows`, `a fragment carrying its own checksum`

## Goal

Make a stripe describe itself — how many data and parity fragments produced it,
how large each is, and which block it belongs to — and give every fragment a
checksum, so a fault can be identified before decoding rather than after.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/erasure/doc.go` | add | Package comment: what a stripe is, why the scheme is in the stripe and not in configuration, and the erasure-versus-error distinction the whole record turns on. |
| `internal/core/erasure/stripe.go` | add | `StripeHeader`, its fixed-width encoding, the `k+m ≤ 255` refusal. |
| `internal/core/erasure/fragment.go` | add | `Fragment`, its index and checksum, and `Verify`. |
| `internal/core/erasure/erasure_test.go` | add | The tests below, including the falsifier named in ADR-006's `Enforced-by:`. |

★ Like `internal/core/segment`, this package works on byte slices and never opens
a file. Where fragments GO is ADR-004's policy and the placement service's
choice; this task decides only what one IS.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestStripeCarriesItsOwnScheme`, `TestSchemeWiderThanTheFieldIsRefused`, `TestStripeHeaderIsFixedWidth`, `TestFragmentCarriesItsOwnChecksum`, `TestStripeHeaderRoundTrips`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant — `-run` matches substrings, and `TestEncode` does not select `TestEncoding`. [proof: acceptance]
2. [S2] Define `StripeHeader` carrying the data-fragment count, the parity-fragment count, the fragment size, the leaf, and the block index within its segment.
3. [S3] Implement a fixed-width big-endian encoding for it, and a decode that refuses a short buffer by name.
4. [S4] Define `MaxCodePositions = 255` and refuse a header whose `k+m` exceeds it with `ErrSchemeTooWide`, and one with no data or no parity fragments with `ErrInvalidScheme`. ★The width limit is a property of `GF(2^8)` arithmetic, not a policy: there is no valid wider scheme to permit, so the refusal belongs at construction rather than in a configuration check somebody can forget. The two errors are separate because "too wide" is not a truthful name for a scheme with zero parity, and a scheme with zero parity tolerates nothing while still calling itself coded.
5. [S5] Define `Fragment` carrying its index within the stripe, its bytes, and a checksum over those bytes — reusing `segment.Checksum` rather than introducing a second checksum function.
6. [S6] Implement `Fragment.Verify`, returning `segment.ErrCorruptBlock` on mismatch. ★This is what converts an error into an erasure. Without it the code tolerates `⌊m/2⌋` faults instead of `m`, and a rotten fragment can produce a reconstruction that is wrong and reports success.
7. [S7] Write the package comment stating the erasure-versus-error distinction and why the scheme travels with the data. [proof: human: a reader confirms the comment explains why an error costs twice an erasure, not merely that fragments are checksummed]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/erasure/... -run 'TestStripe|TestSchemeWider|TestFragmentCarries' -count=1 2>&1 | tee /tmp/adr006-t1.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr006-t1.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestStripeCarriesItsOwnScheme` | `internal/core/erasure/erasure_test.go` | A stripe header round-trips `k`, `m` and the fragment size, so a stripe is decodable under the scheme it was written with and a configuration change cannot orphan it. **The falsifier ADR-006 names in `Enforced-by:`** | — | S2, S3 |
| `TestSchemeWiderThanTheFieldIsRefused` | `internal/core/erasure/erasure_test.go` | `k+m` above 255 yields `ErrSchemeTooWide` at construction, rather than a scheme that cannot be represented in `GF(2^8)` | — | S4 |
| `TestStripeHeaderIsFixedWidth` | `internal/core/erasure/erasure_test.go` | The encoded header is a pinned byte layout of fixed width, and headers written back to back are each decodable at their stride offset | — | S3 |
| `TestFragmentCarriesItsOwnChecksum` | `internal/core/erasure/erasure_test.go` | A flipped bit in a fragment makes `Verify` return `ErrCorruptBlock`, which is what lets a decoder treat the fragment as absent rather than as data | — | S5, S6 |
| `TestStripeHeaderRoundTrips` | `internal/core/erasure/erasure_test.go` | Property test over generated headers: encode then decode is the identity, so no field is silently dropped | — | S3 |

⚠ `TestStripeHeaderIsFixedWidth` asserts the EXACT bytes of one known header, not
only that a round trip succeeds. A round trip uses the same offsets on both
sides and therefore cannot see a symmetric layout bug: two fields written and
read at each other's offsets round-trip perfectly and are wrong for every other
reader of the format. Asserting `len(encoded) == StripeHeaderSize` would be worse
still — with a fixed-size array return that is a tautology, and it passes at any
value of the constant.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | T2's `Encode` and `Reconstruct` are the only producers and consumers of these types; its fence builds against this file. |
| 3 — the caller can discover it | Exported doc comments and two named sentinels; `go doc ./internal/core/erasure` is the check, and for a format package the doc IS the specification. |
| 4 — it is used | Nothing measures this yet; no storage engine exists. |

## Mutation Log

- 2026-09-04 · b3162b1* · mutant killed · exit 1 · `internal/core/erasure/stripe.go` · writes a fixed data-shard count instead of the stripe own, which is what holding the scheme in configuration would do: a stripe written under RS(10,4) then decodes as something else · acceptance-sha256:a8ad43fca75a9a04677c67fcb87a51d69780ee1ea61a439ca816b008f05a33d6 · covers:the scheme being recorded in the stripe rather than in configuration
- 2026-09-04 · b3162b1* · mutant killed · exit 1 · `internal/core/erasure/stripe.go` · removes the field-width gate, so a scheme with more code positions than GF(2 to the 8) can represent is accepted and misbehaves later instead of being refused at construction · acceptance-sha256:a8ad43fca75a9a04677c67fcb87a51d69780ee1ea61a439ca816b008f05a33d6 · covers:the refusal of a scheme wider than the field allows
- 2026-09-04 · b3162b1* · mutant killed · exit 1 · `internal/core/erasure/fragment.go` · makes fragment verification always succeed, so a rotten fragment reaches the decoder as data, the tolerance drops from m erasures to half that many errors, and reconstruction can return wrong bytes reporting success · acceptance-sha256:a8ad43fca75a9a04677c67fcb87a51d69780ee1ea61a439ca816b008f05a33d6 · covers:a fragment carrying its own checksum

## Invariants

- A stripe is decodable from its own header. `k`, `m` and the fragment size are never read from configuration at decode time.
- The fragment checksum uses `segment.Checksum` — one polynomial, one implementation, one place to change it.
- `k + m ≤ 255`, refused at construction.
- This package performs no file I/O.

## Risks

- Two checksum implementations would eventually disagree, and the disagreement would look like corruption. Reusing ADR-005's function makes that impossible rather than unlikely.
- A stripe header is paid once per block, so its width bounds how small a block can usefully be — the same trade ADR-005 recorded for its block header, and it compounds with it.

## Stop Condition

Stop and ask if a fragment needs authentication rather than a detection code.
This task checksums against accidental corruption, the same threat model ADR-005
chose. An adversary who can write to a disk can recompute a CRC, and defending
against that needs the key material ADR-007 owns.

## Out of Scope

- The coding arithmetic itself, and reconstruction — that is T2.
- Where fragments are placed — ADR-004 and the placement service own it.
- Anything that opens a file (deferred: `docs/adr/BACKLOG.md` §12)

## Verification Log
- 2026-09-04 · b3162b1* · exit 0 · `set -o pipefail …` · acceptance-sha256:a8ad43fca75a9a04677c67fcb87a51d69780ee1ea61a439ca816b008f05a33d6 · ms:613
- 2026-09-04 · b3162b1* · exit 0 · `set -o pipefail …` · acceptance-sha256:a8ad43fca75a9a04677c67fcb87a51d69780ee1ea61a439ca816b008f05a33d6 · ms:599
- 2026-09-04 · b3162b1* · exit 0 · `set -o pipefail …` · acceptance-sha256:a8ad43fca75a9a04677c67fcb87a51d69780ee1ea61a439ca816b008f05a33d6 · ms:671
- 2026-09-04 · b3162b1* · exit 0 · `set -o pipefail …` · acceptance-sha256:a8ad43fca75a9a04677c67fcb87a51d69780ee1ea61a439ca816b008f05a33d6 · ms:560
- 2026-09-04 · b3162b1* · exit 0 · `set -o pipefail …` · acceptance-sha256:a8ad43fca75a9a04677c67fcb87a51d69780ee1ea61a439ca816b008f05a33d6 · ms:602
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:a8ad43fca75a9a04677c67fcb87a51d69780ee1ea61a439ca816b008f05a33d6 · ms:707
