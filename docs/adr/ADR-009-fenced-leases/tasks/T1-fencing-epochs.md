# Task ADR-009-T1: The fencing epoch and the registry that only ever counts up

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `lease.Epoch`, `lease.Lease`, `lease.Registry`, `lease.NewRegistry`, `lease.Registry.Grant`, `lease.Registry.Current`, `lease.ErrStaleEpoch`, `lease.ErrNoLease`
**Consumes:** `addr.LeafID` from ADR-001
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `an epoch being strictly greater than every epoch granted before it for that leaf`, `a grant never waiting for the previous holder`, `epochs being ordered per leaf rather than globally`

## Goal

Give leaf ownership a token that orders claims by recency, so a handover is safe
without anyone having to decide whether the previous holder is dead.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/lease/doc.go` | add | Package comment: why the epoch is checked at the resource, why a release would be worse than the fault, and how it fails and recovers. |
| `internal/core/lease/lease.go` | add | `Epoch`, `Lease`, `Registry`, `Grant`, `Current`, and the two sentinels. |
| `internal/core/lease/lease_test.go` | add | The tests below. |

★ The registry is deliberately in-process and named for what it is. Who grants a
lease in a real cluster is a consensus question that needs a transport, and this
task builds the half that makes handover SAFE rather than the half that decides
when it happens.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestEpochOnlyEverIncreases`, `TestGrantDoesNotWaitForThePreviousHolder`, `TestEpochsAreOrderedPerLeaf`, `TestCurrentReportsTheLatestHolder`, `TestNoLeaseIsRefusedByName`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Epoch` as a monotonic counter and `Lease` as a leaf, a holder and an epoch.
3. [S3] Implement `Grant(leaf, holder)`, returning an epoch STRICTLY greater than every epoch previously granted for that leaf. ★Strictly: an equal epoch orders nothing, so two holders would be indistinguishable to the resource that has to choose between them.
4. [S4] Grant without consulting, notifying or waiting for the previous holder. ★Waiting is what makes a dead writer a permanent outage. The epoch is what makes not waiting safe — the old holder discovers it has been superseded when its next write is refused, and until then it can do no harm.
5. [S5] Keep epochs per LEAF, not global. ★A global counter would make every grant anywhere in the cluster a coordination point, which is the single-group design rule 5 of the record rejects.
6. [S6] Implement `Current(leaf)`, refusing with `ErrNoLease` when nothing has been granted rather than returning a zero lease. ★A zero lease is epoch zero held by nobody, and it would compare as older than everything — a valid-looking answer that silently means "no owner".
7. [S7] Write the package comment stating why a release call is not offered. [proof: human: a reader confirms the comment gives the REASON — that a release cannot distinguish a dead holder from a slow one — rather than only stating that there is none]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/lease/... -race -run 'TestEpochOnly|TestGrantDoesNot|TestEpochsAreOrdered|TestCurrentReports|TestNoLeaseIs' -count=1 2>&1 | tee /tmp/adr009-t1.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr009-t1.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestEpochOnlyEverIncreases` | `internal/core/lease/lease_test.go` | Repeated grants for one leaf yield strictly increasing epochs, including under concurrent granting, so no two holders can ever compare equal | — | S2, S3 |
| `TestGrantDoesNotWaitForThePreviousHolder` | `internal/core/lease/lease_test.go` | A grant succeeds while the previous holder still exists and has done nothing to release, so a dead writer is never a permanent outage | — | S4 |
| `TestEpochsAreOrderedPerLeaf` | `internal/core/lease/lease_test.go` | Granting on one leaf does not advance another's epoch, so a busy leaf cannot make every other leaf's grants a coordination point | — | S5 |
| `TestCurrentReportsTheLatestHolder` | `internal/core/lease/lease_test.go` | `Current` names the most recent grant, so an operator asking who owns a leaf gets the answer the resource will act on | — | S3, S6 |
| `TestNoLeaseIsRefusedByName` | `internal/core/lease/lease_test.go` | A leaf nobody has been granted yields `ErrNoLease` rather than a zero lease, which would compare as older than everything and read as "owned by nobody" | — | S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | `Grant` is the only source of an epoch, and T2's tail refuses every append that does not carry one from it. |
| 3 — the caller can discover it | Exported doc comments and two named sentinels; the absence of a `Release` method is itself the interface, and the package comment says why. |
| 4 — it is used | Nothing measures this yet; the granter is in-process and consensus is unbuilt. |

## Mutation Log

- 2026-09-04 · 5cb6794* · mutant killed · exit 1 · `internal/core/lease/lease.go` · grants the same epoch again instead of a higher one, so two holders compare equal and the resource that has to choose between them cannot — which is exactly the case fencing exists to make impossible · acceptance-sha256:43d3aec3bcfc1c30ed53fa3048bec768e369bb1753e163cef0bf83ce820fa0bd · covers:an epoch being strictly greater than every epoch granted before it for that leaf
- 2026-09-04 · 5cb6794* · mutant killed · exit 1 · `internal/core/lease/lease.go` · refuses to grant while a previous holder still exists, which is what any design that waits for a release does: a writer whose process died then holds the leaf forever and the leaf is permanently unwritable · acceptance-sha256:43d3aec3bcfc1c30ed53fa3048bec768e369bb1753e163cef0bf83ce820fa0bd · covers:a grant never waiting for the previous holder
- 2026-09-04 · 5cb6794* · mutant killed · exit 1 · `internal/core/lease/lease.go` · makes the counter global across every leaf, so a grant anywhere in the cluster advances every other leaf and every grant becomes a coordination point — the single-group design this record rejects · acceptance-sha256:43d3aec3bcfc1c30ed53fa3048bec768e369bb1753e163cef0bf83ce820fa0bd · covers:epochs being ordered per leaf rather than globally

## Invariants

- An epoch granted for a leaf is strictly greater than every epoch granted for that leaf before it.
- A grant never blocks on, notifies, or requires anything from the previous holder.
- Epochs are per leaf; granting on one leaf does not advance another's.
- There is no release, and no expiry.

## Risks

- ⚠ A monotonicity test that grants sequentially in one goroutine would pass for a counter with a data race. `TestEpochOnlyEverIncreases` grants concurrently and asserts every epoch is distinct AND ordered, and the fence runs under the race detector.
- "Strictly increasing" is the kind of check that silently accepts equality. The test asserts strict inequality between consecutive grants rather than non-decrease, because an equal epoch orders nothing and is exactly the case fencing must not permit.

## Stop Condition

Stop and ask before adding a release call, a timeout, or an expiry. Each is a
reasonable-sounding convenience and each reintroduces the alternative this record
explicitly rejected: none of them can distinguish a dead holder from a slow one,
so all of them permit two live writers.

## Out of Scope

- Enforcing the epoch on the write path — that is T2.
- Raft, elections and membership (deferred: `docs/adr/BACKLOG.md` §19)
- Where a registry lives and how it survives a restart (deferred: `docs/adr/BACKLOG.md` §19)

## Verification Log
- 2026-09-04 · 5cb6794* · exit 0 · `set -o pipefail …` · acceptance-sha256:43d3aec3bcfc1c30ed53fa3048bec768e369bb1753e163cef0bf83ce820fa0bd · ms:1720
- 2026-09-04 · 5cb6794* · exit 0 · `set -o pipefail …` · acceptance-sha256:43d3aec3bcfc1c30ed53fa3048bec768e369bb1753e163cef0bf83ce820fa0bd · ms:1645
- 2026-09-04 · 5cb6794* · exit 0 · `set -o pipefail …` · acceptance-sha256:43d3aec3bcfc1c30ed53fa3048bec768e369bb1753e163cef0bf83ce820fa0bd · ms:1704
- 2026-09-04 · 5cb6794* · exit 0 · `set -o pipefail …` · acceptance-sha256:43d3aec3bcfc1c30ed53fa3048bec768e369bb1753e163cef0bf83ce820fa0bd · ms:1708
- 2026-09-04 · 5cb6794* · exit 0 · `set -o pipefail …` · acceptance-sha256:43d3aec3bcfc1c30ed53fa3048bec768e369bb1753e163cef0bf83ce820fa0bd · ms:1715
