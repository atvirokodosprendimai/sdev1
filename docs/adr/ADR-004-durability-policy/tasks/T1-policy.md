# Task ADR-004-T1: The policy type and its construction-time refusals

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `durability.Policy`, `durability.Tier`, `durability.Replicated()`, `durability.Coded()`, `durability.ErrInvalidPolicy`, `Policy.DomainsNeeded()`
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the refusal of a floor below two`, `the floor never exceeding the target`

## Goal

Make a durability policy a value that cannot be constructed in an unsafe shape,
so the guarantees below are properties of the type rather than of the operator's
attention.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/durability/durability.go` | add | `Tier`, `Policy`, the two constructors, `DomainsNeeded`. |
| `internal/core/durability/doc.go` | add | Package comment: the two knobs, why they are two, and the arithmetic that makes coding and survival trade against each other. |
| `internal/core/durability/durability_test.go` | add | The tests below, including the falsifier named in ADR-004's `Enforced-by:`. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestPolicyBelowMinSizeIsRefused`, `TestPolicyFloorCannotExceedTarget`, `TestReplicatedNeedsOneDomainPerCopy`, `TestCodedNeedsDataPlusParityDomains`, `TestTierIsExplicit`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Tier` with exactly two values, `Live` and `Sealed`, and no zero-value default — a policy must say which tier it is for, because the two have incompatible requirements.
3. [S3] Define `Policy` carrying the tier, `Size`, `MinSize`, the domain level label, and for a coded policy the data and parity shard counts.
4. [S4] Implement `Replicated(size, minSize int, domainLevel string)` and `Coded(data, parity, minSize int, domainLevel string)`, each returning `ErrInvalidPolicy` rather than a usable value when the shape is unsafe.
5. [S5] ★ Refuse `MinSize < 2` at construction. A configuration that permits one copy will eventually be set to one copy, and the moment it would be relaxed is the moment nobody is reading warnings — so it is a refusal rather than a default or an advisory.
6. [S6] Refuse `MinSize > Size`, which declares a floor the target can never reach and would make every write fail on a healthy cluster.
7. [S7] Implement `DomainsNeeded`: `Size` for a replicated policy, `data+parity` for a coded one. This is the number T2 checks a map against.
8. [S8] Write the package comment stating the two knobs and the domain arithmetic, including that surviving most-of-the-servers and low overhead cannot both hold over one pool. [proof: human: a reader confirms the comment states the CONFLICT and its resolution, not only the mechanism]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/durability/... -run 'TestPolicy|TestReplicated|TestCoded|TestTier' -count=1 2>&1 | tee /tmp/adr004-t1.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr004-t1.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestPolicyBelowMinSizeIsRefused` | `internal/core/durability/durability_test.go` | A floor of 0 or 1 is refused at construction, for both constructors. **The falsifier ADR-004 names in `Enforced-by:`** | — | S5 |
| `TestPolicyFloorCannotExceedTarget` | `internal/core/durability/durability_test.go` | A floor above the target is refused, since it would fail every write on a healthy cluster | — | S6 |
| `TestReplicatedNeedsOneDomainPerCopy` | `internal/core/durability/durability_test.go` | A replicated policy needs `Size` distinct domains | — | S7 |
| `TestCodedNeedsDataPlusParityDomains` | `internal/core/durability/durability_test.go` | A coded policy needs `data+parity` distinct domains — the arithmetic that makes a (8,2) code across three servers survive nothing at the server level | — | S7 |
| `TestTierIsExplicit` | `internal/core/durability/durability_test.go` | A policy names its tier, and the zero value is not a valid tier, so a policy cannot default into the wrong half of the design; and the policy struct carries both knobs plus the domain level | — | S2, S3 |
| `TestCodedRefusesZeroParity` | `internal/core/durability/durability_test.go` | A code with no parity shards tolerates no loss and is refused, since it is replication-with-extra-steps wearing a coding label | — | S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The six unit tests above. |
| 2 — something selects it | T2 validates a policy against a topology map and calls `DomainsNeeded`; its fence builds against this package. |
| 3 — the caller can discover it | Exported doc comments and named sentinels; `go doc ./internal/core/durability` is the check, and `ErrInvalidPolicy` is what a caller matches on. |
| 4 — it is used | Nothing measures this yet. |

## Mutation Log

- 2026-09-04 · 6036c3a* · mutant killed · exit 1 · `internal/core/durability/durability.go` · without the refusal a policy can permit data held once, and the moment an operator would relax it is the moment nobody is reading warnings; TestPolicyBelowMinSizeIsRefused must go red · acceptance-sha256:ca0b26ab7257dd11986af9ff3d17f978aac3e2cef7deb5cc948ae0d8fd571f34 · covers:the refusal of a floor below two
- 2026-09-04 · 6036c3a* · mutant killed · exit 1 · `internal/core/durability/durability.go` · a floor above the target refuses every write on a healthy cluster, which is a configuration error that must not be constructible; TestPolicyFloorCannotExceedTarget must go red · acceptance-sha256:ca0b26ab7257dd11986af9ff3d17f978aac3e2cef7deb5cc948ae0d8fd571f34 · covers:the floor never exceeding the target

## Invariants

- `MinSize` is at least 2. A policy permitting one copy cannot be constructed.
- `MinSize` never exceeds `Size`.
- A coded policy has at least one parity shard.
- A policy names its tier explicitly; the zero value is not a valid tier.
- This package performs no I/O and holds no cluster state. It answers questions about values it is given.

## Risks

- An operator under incident pressure will want to lower the floor to get writes flowing. The refusal is at construction rather than at use precisely so that the attempt fails early and visibly, but nothing here can stop someone editing the constant — that is a review and deployment concern rather than a type-system one, and saying so is more useful than implying the type protects against it.
- Two constructors rather than one general one means adding a third tier later touches this package. That is accepted: an explicit shape per tier is what keeps a coded policy from being constructed without parity.

## Stop Condition

Stop and ask if a third tier appears — a cold or archival one, for example. This
task assumes exactly two, and a third would need its own requirements rather than
inheriting either existing set.

## Out of Scope

- Checking a policy against a real cluster — that is T2.
- The erasure code itself (permanent: boundary: ADR-006 owns coding; this task counts shards and does not encode anything)

## Verification Log
- 2026-09-04 · 6036c3a* · exit 0 · `set -o pipefail …` · acceptance-sha256:ca0b26ab7257dd11986af9ff3d17f978aac3e2cef7deb5cc948ae0d8fd571f34 · ms:459
- 2026-09-04 · 6036c3a* · exit 0 · `set -o pipefail …` · acceptance-sha256:ca0b26ab7257dd11986af9ff3d17f978aac3e2cef7deb5cc948ae0d8fd571f34 · ms:451
- 2026-09-04 · 6036c3a* · exit 0 · `set -o pipefail …` · acceptance-sha256:ca0b26ab7257dd11986af9ff3d17f978aac3e2cef7deb5cc948ae0d8fd571f34 · ms:512
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:ca0b26ab7257dd11986af9ff3d17f978aac3e2cef7deb5cc948ae0d8fd571f34 · ms:478
