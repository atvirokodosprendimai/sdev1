# ADR-016: Put the tenant in the leading bytes of the key

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-001-address-space.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/addr/**`
**Enforced-by:** `internal/core/addr/addr_test.go::TestTenantOwnsAContiguousPrefix`
**Invalidates:** ADR-001 — the clause of its Decision rule 1 reading "A key is the 32-byte SHA-256 digest of the entity identifier"
**Served-path change:** Every key names its tenant in its first bytes, so a tenant occupies one contiguous subtree and an operation scoped to a tenant becomes a prefix range rather than a scan.

## Context

ADR-001 rule 1 hashes the entity identifier alone. That is correct for a
single-tenant engine and wrong for this one, and the difference is a format
decision: the key layout is written into every datom, every segment header and
every routing decision, so changing it costs one edit today and a full re-ingest
once data exists.

The observation that makes the change cheap is that everything in this system is
already bytes with a byte-comparable encoding. A key is 32 bytes; a transaction
identifier encodes fixed-width so that comparing encodings as bytes matches
comparing values; the trie descends one byte per level. "A tenant is the leading
bytes", "the trie descends a byte at a time" and "prune a range by comparing
bytes" are therefore the same operation, not three.

## Existing Primitives Audit

- `internal/core/addr` (ADR-001, T1): supplies the key and the descent, and is
  the package this record changes. **Reshaped**, not replaced: the descent, the
  leaf identifier and the fan-out constant are untouched; only what is hashed
  into the first bytes changes.
- `internal/core/placement` (ADR-001, T3): unaffected. It consumes a leaf
  identifier and never inspects a key's structure.
- `internal/core/topology` (ADR-001, T2): unaffected.

## Decision

**A key is `TenantBytes` bytes of tenant identifier followed by the leading bytes
of the entity's digest, to a total of 32.**

1. `TenantBytes` is **2**, giving 65,536 tenants, and the remaining 30 bytes
   carry the entity digest — 240 bits, far beyond any collision concern.
2. **A tenant is the subtree rooted at depth `TenantBytes`.** At a live depth of
   2 or more, every tenant occupies whole leaves that no other tenant shares. At
   depth 1 tenants share leaves, which is legal and appropriate for a small
   deployment.
3. **The tenant is not hashed.** It is written literally, so tenant `0x0007` is
   always the subtree `0007…`. Hashing it would spread a tenant across the trie
   and destroy every property below.
4. `TenantOf(key)` returns the tenant, and a tenant's subtree is the leaf
   identifier of depth `TenantBytes` formed from its bytes. Both are prefix
   operations on bytes.

**What this buys, and it is the reason to accept a format change:**

- **Tenant deletion is a subtree drop**, which is already the sweep's unit.
- **Data residency is a placement rule** pinning a subtree to a region, rather
  than a per-object attribute nothing can enforce.
- **Per-tenant durability, retention, compression and shred-key namespaces** all
  become subtree policy, so ADR-004's policies and ADR-010's retention need no
  tenant concept of their own.
- **Noisy-neighbour isolation** lands on a subtree boundary.
- **A hot tenant is absorbed by the depth mechanism** ADR-001 already has: its
  subtree simply grows deeper. The tenant hot-spot problem and the scaling
  mechanism are the same problem.

**What would falsify this decision.** The claim is that a tenant occupies a
contiguous, computable prefix range. It fails if two tenants can share a prefix
or if one tenant's keys can fall outside its own subtree — both checkable today,
on one node, with no cluster, which is what
`TestTenantOwnsAContiguousPrefix` does.

⚠**The ceiling is real and is the thing to revisit.** 65,536 tenants is ample for
an engine embedded in a product and low for a public multi-tenant service.
Widening `TenantBytes` is a format change with the same re-ingest cost as this
one, so it is a decision to take now rather than later. It is recorded as a risk
rather than hedged with a variable width, because a variable-width prefix would
make the tenant boundary depend on configuration that is not carried with the
data — the failure this corpus has already rejected three times.

## Alternatives Considered

- **Hash the tenant together with the entity** (`hash(tenant || entity)`).
  Spreads every tenant evenly across the trie, which is excellent for load
  balance. Rejected because it destroys contiguity, and contiguity is the entire
  point: tenant deletion becomes a full scan, residency becomes unenforceable,
  and per-tenant policy has nowhere to attach.

- **A separate tenant field beside the key, not inside it.** Keeps ADR-001
  untouched. Rejected because routing reads the key and nothing else — a field
  beside the key cannot influence placement, so tenants would still be scattered
  and every benefit above would still be absent.

- **A separate trie per tenant.** Perfect isolation. Rejected because it
  multiplies every piece of cluster state by the tenant count: routing tables,
  placement maps and consensus groups all become per-tenant, and a small tenant
  costs as much fixed overhead as a large one.

- **Variable-width tenant prefix, configured per cluster.** Removes the ceiling.
  Rejected for the reason this corpus has now rejected three times: a constant
  that is safe as policy is fatal as a format assumption. A variable boundary
  makes the meaning of every stored key depend on configuration the data does
  not carry.

## Component / Boundary Impact

| Component | Owns | One reason to change? |
|-----------|------|-----------------------|
| `internal/core/addr` | The key layout, now including the tenant prefix, plus `TenantOf` and the tenant's subtree. | Yes — it already owns the addressing model, and this is that model. |

No new package. This record narrows an existing one rather than adding a
neighbour, which is why it declares `Invalidates` rather than standing alongside.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `addr.TenantID`, `addr.TenantBytes` | new | `internal/core/addr` (T1) | every caller that constructs a key |
| `addr.KeyOf(tenant, entity)` | **breaking** — was `KeyOf(entity)` | `internal/core/addr` (T1) | `command`, `cmd/sdev1-addr`, every future writer |
| `addr.TenantOf(Key) TenantID` | new | `internal/core/addr` (T1) | placement policy, retention, authorization |
| `addr.Key.TenantSubtree() LeafID` | new | `internal/core/addr` (T1) | tenant-scoped operations |

The signature change is breaking, and deliberately so: every existing caller must
say which tenant it means rather than silently defaulting to one.

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `addr.TenantID`, `addr.KeyOf`, `addr.TenantOf` | T1 | none in this record | **Yes** — `KeyOf` gains a parameter, and every caller is updated in the same change |

## Implementation

One task. See [`ADR-016-tenant-prefix/tasks/README.md`](ADR-016-tenant-prefix/tasks/README.md).

## Consequences

- **Positive:** four capabilities that would each have needed their own mechanism
  — tenant deletion, residency, per-tenant policy, isolation — become prefix
  operations on a structure that already exists.
- **Positive:** the change is small because everything was already bytes. Nothing
  about the descent, the fan-out or the leaf identifier moves.
- **Negative:** 65,536 tenants is a ceiling, and raising it later costs a
  re-ingest.
- **Negative:** `KeyOf` gains a parameter, so every caller changes. That is the
  cost of not having a default tenant, and a default tenant is how multi-tenancy
  quietly becomes single-tenancy with extra fields.
- **Neutral:** at depth 1 tenants share leaves. Isolation begins at depth 2, and
  a deployment wanting it must run at least that deep — 65,536 leaves, which is
  a statement about cluster size an operator should make knowingly.

## Out of Scope

- Roles, grants and authorization (deferred: `docs/adr/BACKLOG.md` §11)
- Allocating tenant identifiers, and what happens when one is reused (deferred: `docs/adr/BACKLOG.md` §11)
- Pinning a tenant's subtree to a region (permanent: boundary: ADR-008 owns placement; this record makes the pin expressible by giving a tenant a subtree, and does not decide the rule)
- Widening `TenantBytes` (permanent: boundary: a wider prefix is the same format decision taken again, and taking it twice is what this record's ceiling risk is for)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| 65,536 tenants proves too few | Medium | **High** — widening is a re-ingest | Stated plainly as the decision to revisit, with the variable-width alternative explicitly rejected and the reason given, so a later reader sees it was considered rather than missed |
| A caller passes a zero tenant because the parameter is now required | Medium | Medium — everything lands in one subtree | The parameter is required rather than defaulted, so passing zero is visible at the call site rather than implied by absence |
| Someone re-hashes the tenant "for better balance" | Low | High — every property here is destroyed silently | Rule 3 states it, and `TestTenantOwnsAContiguousPrefix` fails if a tenant's keys stop sharing a prefix |

## Rollback

The key layout is persistent, so this is a format decision and rollback after
data exists is a re-ingest. That is exactly why it is being taken now, while the
repository holds no data: today it is one edit and a test update.

Before data exists: revert the branch.

## Follow-ups

- [ ] Decide tenant identifier allocation and reuse before the first real deployment; a reused identifier inherits the previous tenant's subtree, including anything not yet swept.
