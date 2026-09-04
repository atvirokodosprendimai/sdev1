# Task ADR-016-T1: The tenant prefix in the key

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `addr.TenantID`, `addr.TenantBytes`, `addr.KeyOf(tenant, entity)`, `addr.TenantOf()`, `Key.TenantSubtree()`
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the tenant being written literally rather than hashed`, `the tenant prefix bounding every key of that tenant`

## Goal

Move the tenant into the leading bytes of the key so a tenant occupies one
contiguous subtree, while leaving the descent, the fan-out and the leaf
identifier untouched.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/addr/addr.go` | edit | `TenantID`, `TenantBytes`, the new `KeyOf`, `TenantOf`, `TenantSubtree`. |
| `internal/core/addr/doc.go` | edit | The package comment states the layout, so the format is readable from the code. |
| `internal/core/addr/addr_test.go` | edit | The tests below, plus updating existing calls to the new signature. |
| `internal/core/command/command.go` | edit | Its `New` takes a tenant and passes it through — the caller must say which tenant it means. |
| `internal/core/command/command_test.go` | edit | Updated for the new signature. |
| `cmd/sdev1-addr/main.go` | edit | A `--tenant` flag, since the command's whole job is showing where a key lands. |
| `cmd/sdev1-addr/main_test.go` | edit | Updated, plus a case showing two tenants land in different subtrees. |

★ Every caller changes, and that is the design rather than a cost of it: a
`KeyOf` with a default tenant is how multi-tenancy quietly becomes
single-tenancy with an extra field.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestTenantOwnsAContiguousPrefix`, `TestTenantIsNotHashed`, `TestTenantSubtreeContainsEveryKeyOfThatTenant`, `TestDifferentTenantsNeverShareASubtree`, `TestKeyIsStillThirtyTwoBytes`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `TenantBytes = 2` and `TenantID [TenantBytes]byte`.
3. [S3] Change `KeyOf(tenant TenantID, entity string) Key` to write the tenant literally into the leading bytes and the entity's digest into the remainder, keeping the total at 32.
4. [S4] Add `TenantOf(Key) TenantID` and `Key.TenantSubtree() LeafID`, both prefix operations rather than computations.
5. [S5] Update `command.New` to take a tenant, and `cmd/sdev1-addr` to take `--tenant`. [proof: acceptance]
6. [S6] Update the package comment to state the layout, so a reader learns the format from the code rather than from the record alone. [proof: human: a reader confirms the comment gives the byte layout and says the tenant is NOT hashed, which is the property every benefit rests on]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/addr/... ./internal/core/command/... -run 'TestTenant|TestKey|TestDifferent|TestFanOut|TestDescend|TestLeafID|TestCommandRequiresATenant' -count=1 2>&1 | tee /tmp/adr016-t1.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr016-t1.out \
  && go test ./... -count=1 \
  && go build -o /tmp/sdev1-addr ./cmd/sdev1-addr \
  && /tmp/sdev1-addr --topology testdata/topology/minimal.json --tenant 7 --entity demo-entity
```

The whole module is re-run rather than one package, because this task changes a
signature every caller uses; a green `addr` package would say nothing about
whether the callers were updated. The binary is then built and run, because a
flag that exists and is not wired is this pipeline's most common defect.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestTenantOwnsAContiguousPrefix` | `internal/core/addr/addr_test.go` | Every key of one tenant shares that tenant's leading bytes — the property every benefit in ADR-016 rests on. **The falsifier named in `Enforced-by:`** | — | S3 |
| `TestTenantIsNotHashed` | `internal/core/addr/addr_test.go` | Tenant `0x0007` appears literally as the first bytes, so a tenant's subtree is computable without a lookup | — | S3 |
| `TestTenantSubtreeContainsEveryKeyOfThatTenant` | `internal/core/addr/addr_test.go` | The subtree leaf identifier contains the leaf of every key belonging to that tenant, at any depth at or beyond `TenantBytes` | — | S4 |
| `TestDifferentTenantsNeverShareASubtree` | `internal/core/addr/addr_test.go` | Two tenants' subtrees are disjoint, so isolation is structural rather than conventional | — | S4 |
| `TestKeyIsStillThirtyTwoBytes` | `internal/core/addr/addr_test.go` | The tenant is carved OUT of the digest rather than added to it, so nothing downstream changes width | — | S2, S3 |
| `TestCommandRequiresATenant` | `internal/core/command/command_test.go` | A transaction names its tenant, so a caller cannot land in a default one by omission | — | S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The six tests above. |
| 2 — something selects it | `command.New` and `cmd/sdev1-addr` both call the new `KeyOf`; the fence builds and runs the binary, so an unwired flag fails. |
| 3 — the caller can discover it | The `--tenant` flag and its help text; exported doc comments on `TenantID`, `TenantOf` and `TenantSubtree`. |
| 4 — it is used | Nothing measures this yet. |

## Mutation Log

- 2026-09-04 · 58df263* · mutant killed · exit 1 · `internal/core/addr/addr.go` · hashing the tenant into the prefix spreads each tenant across the trie, so no tenant owns a contiguous subtree and every property ADR-016 claims disappears; TestTenantIsNotHashed and TestTenantOwnsAContiguousPrefix must go red · acceptance-sha256:315b40396a013078cf05c234a37090125ab68602b1c1386e10d3fc37e3fae95b · covers:the tenant being written literally rather than hashed
- 2026-09-04 · 58df263* · mutant killed · exit 1 · `internal/core/addr/addr.go` · a subtree that does not carry the tenant own bytes contains keys of every tenant and none of its own; TestTenantSubtreeContainsEveryKeyOfThatTenant and TestDifferentTenantsNeverShareASubtree must go red · acceptance-sha256:315b40396a013078cf05c234a37090125ab68602b1c1386e10d3fc37e3fae95b · covers:the tenant prefix bounding every key of that tenant
- 2026-09-04 · 58df263* · mutant killed · exit 1 · `internal/core/addr/addr.go` · re-bound to the widened fence: hashing the tenant into the prefix spreads each tenant across the trie, so no tenant owns a contiguous subtree · acceptance-sha256:5cad37e5dc2fc1f43a575119772b7e51c1c027dd298ffa4f88018e52d72f64c6 · covers:the tenant being written literally rather than hashed
- 2026-09-04 · 58df263* · mutant killed · exit 1 · `internal/core/addr/addr.go` · re-bound to the widened fence: a subtree not carrying the tenant own bytes contains keys of every tenant and none of its own · acceptance-sha256:5cad37e5dc2fc1f43a575119772b7e51c1c027dd298ffa4f88018e52d72f64c6 · covers:the tenant prefix bounding every key of that tenant

## Invariants

- The tenant is written LITERALLY into the leading bytes. It is never hashed, because hashing it scatters the tenant and destroys contiguity.
- A key is 32 bytes. The tenant is carved out of the digest, not appended to it.
- Two distinct tenants have disjoint subtrees.
- The descent, the fan-out constant and the leaf identifier are unchanged by this task.

## Risks

- `KeyOf` gains a parameter, so every caller breaks at compile time. That is intended and is why the fence runs the whole module rather than one package — a caller silently defaulting its tenant would be worse than a build failure.
- 65,536 tenants is a ceiling inherited from `TenantBytes = 2`. Widening it is the same format decision taken again, at re-ingest cost; ADR-016 records that rather than hedging with a variable width.

## Stop Condition

Stop and ask if any caller genuinely has no tenant to name. A system tenant for
internal data is a legitimate answer and should be an explicit reserved value;
"whatever the zero value is" is not, and would reintroduce the default this task
exists to remove.

## Out of Scope

- Allocating or reusing tenant identifiers (deferred: `docs/adr/BACKLOG.md` §11)
- Authorization, which is what a tenant boundary is usually wanted for (deferred: `docs/adr/BACKLOG.md` §11)

## Verification Log
- 2026-09-04 · 58df263* · exit 0 · `set -o pipefail …` · acceptance-sha256:315b40396a013078cf05c234a37090125ab68602b1c1386e10d3fc37e3fae95b · ms:1759
- 2026-09-04 · 58df263* · exit 0 · `set -o pipefail …` · acceptance-sha256:315b40396a013078cf05c234a37090125ab68602b1c1386e10d3fc37e3fae95b · ms:1635
- 2026-09-04 · 58df263* · exit 0 · `set -o pipefail …` · acceptance-sha256:315b40396a013078cf05c234a37090125ab68602b1c1386e10d3fc37e3fae95b · ms:1808
- 2026-09-04 · 58df263* · exit 0 · `set -o pipefail …` · acceptance-sha256:5cad37e5dc2fc1f43a575119772b7e51c1c027dd298ffa4f88018e52d72f64c6 · ms:1628
- 2026-09-04 · 58df263* · exit 0 · `set -o pipefail …` · acceptance-sha256:5cad37e5dc2fc1f43a575119772b7e51c1c027dd298ffa4f88018e52d72f64c6 · ms:1613
- 2026-09-04 · 58df263* · exit 0 · `set -o pipefail …` · acceptance-sha256:5cad37e5dc2fc1f43a575119772b7e51c1c027dd298ffa4f88018e52d72f64c6 · ms:1703
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:5cad37e5dc2fc1f43a575119772b7e51c1c027dd298ffa4f88018e52d72f64c6 · ms:2233
