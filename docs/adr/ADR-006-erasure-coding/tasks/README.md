# ADR-006 Tasks

Implementation tasks for ADR-006: Erasure-code a block into a stripe that records
its own scheme, and checksum every fragment. See the parent ADR for the decision.

**Source of truth:** the task files' headers. This README is a derived index —
when it disagrees with a task file, the task file wins.

## Execution Order

Two tasks, sequential.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | The stripe header, the fragment, and the checksum that makes an error an erasure | done | — | `go test ./internal/core/erasure/... -run 'TestStripe\|TestSchemeWider\|TestFragmentCarries'` |
| T2 | Encode a block into a stripe, and reconstruct it from the survivors | done | — | `go test ./internal/core/erasure/... -run 'TestEncod\|TestReconstruct\|TestAnyM\|TestCorruptFragment\|TestCodedBlocks\|TestPolicySelects'` then the segment and durability suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `erasure.StripeHeader`, `erasure.Fragment`, the fragment checksum | T2 | T1 before T2 |

## Notes

- **This package never opens a file, and that is deliberate**, exactly as in
  `internal/core/segment`. It decides what a fragment IS. Where fragments go is
  ADR-004's policy and the placement service's choice, and a coder that also
  placed would be a second authority over one question.
- ⚠ **An erasure and an error are not the same fault, and the difference is the
  whole record.** A code with `m` parity fragments corrects `m` fragments known
  to be MISSING, or only `⌊m/2⌋` fragments that are present and WRONG — locating
  a fault costs as much redundancy as repairing it. So `RS(8,2)` tolerates two
  losses and ZERO silent corruptions. The per-fragment checksum is what converts
  every error into an erasure before decoding starts; without it, one rotten
  fragment yields a block that is wrong with no error raised anywhere.
- ⚠ **ADR-005's block checksum does not cover this**, because it is only
  available after reconstruction. It says the answer is wrong without saying
  which fragment to exclude.
- ⚠ **Two follow-ups from other records are closed here**, and both should be
  checked when reviewing: ADR-004 asked that `k+m` be validated against the
  domain count that record already computes rather than a second definition
  (T2-S2), and ADR-005 asked that the coder write its stage into the block header
  rather than anything assuming a sealed block is coded (T2-S4).
- ⚠ When adding a test during implementation, check its name is SELECTED by the
  fence's `-run` filter before running any mutant. `-run` matches substrings:
  `TestEncode` does not select `TestEncodingIsDeterministic`.
