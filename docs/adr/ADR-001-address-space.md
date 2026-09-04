# ADR-001: Address the key space as a 256-way trie of static fan-out and dynamic depth

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/addr/**`, `internal/core/topology/**`, `internal/core/placement/**`, `cmd/sdev1-addr/**`
**Enforced-by:** `internal/core/addr/addr_test.go::TestFanOutIsExactlyOneByte`
**Invalidates:** none — checked; this is the corpus's first record
**Served-path change:** A client resolves the server holding any key by descending that key's own bytes against a locally cached topology map, so the read and write paths reach their target without consulting a metadata service.

## Context

sdev1 is greenfield. At commit `2db614d` (branch `main`, clean, checked
2026-09-04) the repository held `README.md`, `LICENSE` and `.gitignore` and no Go
source, no schema and no prior decision record. Everything below is intent.

The standing requirement was given verbatim on 2026-09-04: *"it needs binary data
formad shared into 256 chunks. if you need to shard something always use 256
chunks. hardcoded … we are making planetary scale 'structural data engine'
imagine if datacenter is planet and dust it file in server in datacenter on disk.
need infinite scale system sharded with master election for chunk to
replicate/write to it."*

That sentence admits two readings, and they differ by an unbounded factor.

Under the first reading, 256 is the **number of shards**. The key space divides
into 256 parts, each part is owned by a server, and the cluster therefore holds at
most 256 servers. Every part of the system is simpler under this reading, and it
contradicts the requirement stated in the same breath — a 256-server ceiling is
not infinite scale, and no amount of hardware removes it.

Under the second reading, 256 is the **fan-out of one level**, and scale comes
from depth rather than from width. This reading is already present in the
requirement's own imagery: universe, planet, datacenter, rack, server, disk is a
statement about levels of containment, not about a count. A key hashed to 32 bytes descends
one byte per level; each byte selects one of 256 children; 32 levels of 256-way
fan-out address 2^256 leaves. The ceiling disappears without 256 ever changing.

The two readings produce identical code at one level of depth, which is why the
choice has to be recorded now rather than discovered. A cluster built under the
first reading and then grown hits the ceiling with data already written under a
key format that assumed it.

## Existing Primitives Audit

None — greenfield repository at `2db614d`, no Go source to reuse or reshape.

Prior art exists in the same workspace and was read rather than imported:
`temporaldbv1` is a single-node temporal event-sourced store and solves none of
the distribution problem, but its recorded defects inform ADR-002 and ADR-010.
No code is shared; the two projects have no dependency relationship.

## Decision

**Fan-out is exactly 256 at every level of the address trie, and it is a
compile-time constant. Depth is configuration. Scale comes from depth.**

Concretely:

1. A key is the 32-byte SHA-256 digest of the entity identifier. Nothing else is
   hashed at this level — the entity is the unit of locality, so everything about
   one entity resolves to one leaf.
2. Descending the trie consumes one byte per level, most significant first.
   Byte *i* selects one of 256 children at level *i*. A descent of depth *d*
   therefore names one of 256^*d* leaves and consumes *d* of the 32 available
   bytes.
3. The live depth *d* is a cluster-wide configured value, carried in the topology
   map rather than compiled in, with `d = 1` as the initial value. `d = 1` yields
   256 leaves and is byte-for-byte the first reading above — so the simple case is
   not paid for, it is simply the depth-1 case.
4. **The fan-out constant may not be read from configuration.** It is `const
   FanOut = 256` and the invariant that it equals `1 << 8` is asserted by a test,
   because a fan-out that is not a whole byte makes the descent stop being a byte
   walk and silently changes what every stored key means.
5. A leaf identifier is the prefix that produced it — the first *d* bytes plus
   *d* itself. It is written into segment headers, so a leaf identifier remains
   interpretable when the cluster's live depth later changes.

**What would falsify this decision.** The claim carrying weight is that depth
alone yields scale at constant fan-out. It fails if leaf identifiers are not
stable across a depth change: if raising *d* from 1 to 2 renames every existing
leaf, the design is a resharding scheme wearing a trie's clothes and the ceiling
returns as a migration cost. Rule 5 is what makes it hold, and `TestLeafIDIsStableAcrossDepthChange`
is what proves it. That test is falsifiable today, on a single-node checkout,
with no cluster and no data — which is the point of settling this before storage
exists.

**What this decision does not claim.** It does not claim the cluster can *use*
32 levels. Depth beyond the point where a leaf holds less than one segment is
waste, and the policy that decides when to deepen a subtree is deliberately not
decided here (Out of Scope, and `BACKLOG.md` §1).

## Alternatives Considered

- **256 as a fixed shard count, one server per shard.** Simplest possible model,
  and every routing structure collapses to a 256-entry array. Rejected because it
  caps the cluster at 256 servers, which directly contradicts the stated
  requirement; and because the cap is reached with data already on disk under a
  key format that assumed it.

- **Consistent hashing with a ring and virtual nodes.** The conventional answer,
  well understood, and it removes the ceiling without any trie. Rejected because
  it makes placement a *lookup* against ring state rather than a *computation*
  over a key, so every client needs the current ring; and because it gives up the
  hierarchy the requirement asks for — a ring has no notion of datacenter
  containing rack containing server, and the durability model (ADR-004) needs
  exactly that hierarchy to express a failure domain.

- **Range partitioning on the entity identifier, split on load.** What most
  distributed stores actually do, and it makes range scans over entities cheap.
  Rejected because it requires a metadata service on the read path to answer
  "which range holds this key", and because the split points become mutable
  cluster state that must itself be replicated and versioned. The trie gets
  splitting from the key's own bytes at the cost of range scans, and this engine
  does not promise ordered scans over entity identifiers.

- **Fan-out configurable per level (16, 64, 256, …).** Superficially more
  flexible. Rejected for the reason recorded against a sibling decision: a
  constant that is safe as *policy* is fatal as a *format assumption*. A descent
  is only a byte walk when the fan-out is 256; any other value makes the mapping
  from key bytes to levels depend on configuration that is not carried with the
  data, and a configuration change then silently reinterprets every key ever
  written.

## Component / Boundary Impact

Three new packages, none of which existed at `2db614d`, plus one command.

| Component | Owns | One reason to change? |
|-----------|------|-----------------------|
| `internal/core/addr` | The key type, the leaf identifier, the descent. No I/O, no cluster awareness. | Yes — changes only when the addressing model changes. |
| `internal/core/topology` | The declared level labels (`universe, planet, datacenter, rack, server, disk` by default, but data rather than types), the nested-set interval tree beneath them, its wire format, and the live depth. | Yes — changes only when the shape of a cluster's declaration changes. |
| `internal/core/placement` | Turning a leaf identifier plus a topology map into an ordered server set. Pure function; no network. | Yes — changes only when the placement rule changes. |
| `cmd/sdev1-addr` | Operator-facing inspection of a descent against a topology file. | Yes — presentation only. |

`addr` depends on nothing but the standard library. `topology` depends on
nothing but the standard library. `placement` depends on both and on neither
storage nor transport. This is deliberate: the whole addressing model is
computable in a unit test with no cluster, no disk and no clock.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `addr.Key` (32-byte digest type) | new | `internal/core/addr` (T1) | `placement` (T3), all future storage records |
| `addr.LeafID` (prefix + depth) | new | `internal/core/addr` (T1) | `placement` (T3), segment headers (ADR-005) |
| `addr.Descend(Key, depth) LeafID` | new | `internal/core/addr` (T1) | `placement` (T3), `cmd/sdev1-addr` (T4) |
| Topology map file format (JSON, versioned) | new | `internal/core/topology` (T2) | `placement` (T3), `cmd/sdev1-addr` (T4); every node reads it at startup |
| `placement.Resolve(LeafID, topology.Map) []topology.ServerID` | new | `internal/core/placement` (T3) | `cmd/sdev1-addr` (T4); the client router, when it exists |
| `sdev1-addr` CLI | new | `cmd/sdev1-addr` (T4) | operators |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `addr.Key`, `addr.LeafID`, `addr.Descend()` | T1 | T3, T4 | No — new surface, nothing depends on it yet |
| `topology.Map`, `topology.Load()` | T2 | T3, T4 | No — new surface |
| `placement.Resolve()` | T3 | T4 | No — new surface |

T1 and T2 are independent of each other and may be built in parallel. T3 requires
both. T4 requires all three.

## Implementation

Four tasks. See [`ADR-001-address-space/tasks/README.md`](ADR-001-address-space/tasks/README.md).

## Consequences

- **Positive:** the cluster has no built-in server ceiling, and removing it cost
  one configuration value rather than a different architecture. Everything about
  one entity lives in one leaf, so a per-entity transaction is single-leaf and
  needs no distributed commit — which is what makes ADR-003's boundary available
  as a choice at all.
- **Positive:** placement is *computed* from the key, so a client that holds the
  topology map needs no per-request metadata lookup. The topology map is small
  and changes rarely; per-object location is never stored anywhere.
- **Negative:** ordered scans over entity identifiers are gone. The trie orders
  by digest, which is deliberately unrelated to any meaningful order. Any query
  that wants "entities between A and B" needs a secondary index and will pay for
  it.
- **Negative:** a topology change reshuffles more data than the minimum a
  purpose-built rebalancer would move, because the descent is a pure function of
  the key and the depth rather than of current occupancy.
- **Neutral:** at `d = 1` this design and the rejected fixed-256 design are
  indistinguishable from outside. The difference only appears on the day the
  cluster outgrows 256 servers, which is exactly why it is recorded rather than
  discovered.

## Out of Scope

- Replication factor, erasure coding, and how many copies of a leaf exist (permanent: boundary: ADR-004 and ADR-006 own durability; this record decides only how a key names a leaf, and a leaf's copy count is orthogonal to its address)
- Leader election and which replica of a leaf accepts writes (permanent: boundary: ADR-009 owns consensus; a leaf is an address, not a process)
- The binary segment format that stores a leaf's data (permanent: boundary: ADR-005 owns the format; this record only fixes that a leaf identifier appears in its header)
- Using more than 32 levels of depth (permanent: fact: a SHA-256 digest supplies exactly 32 bytes of key material, which bounds the trie at 32 levels of 256-way fan-out; citation: version `crypto/sha256@go1.26.7`)
- Deciding when a subtree deepens, and migrating data during a split (deferred: `docs/adr/BACKLOG.md` §1)
- Mitigating a single hot entity whose write rate exceeds one leaf (deferred: `docs/adr/BACKLOG.md` §2)
- Bounding or prioritising repair traffic (deferred: `docs/adr/BACKLOG.md` §3)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Depth is raised in production and existing leaf identifiers are reinterpreted | Low | High — silent misrouting of every key | Rule 5 carries depth *inside* the leaf identifier, and `TestLeafIDIsStableAcrossDepthChange` fails if a raised depth renames an existing leaf |
| Someone makes fan-out configurable "for flexibility" | Medium | High — every stored key silently changes meaning | `const FanOut = 256` plus `TestFanOutIsExactlyOneByte`, named in `Enforced-by:` so the check is greppable from this record |
| Digest-ordered leaves make an unrelated feature want range scans | Medium | Medium — a later record proposes reverting to range partitioning | Recorded as a Negative consequence and as a rejected alternative, so the trade is visible rather than rediscovered |
| The topology map becomes large enough that shipping it to clients is itself a cost | Low | Medium | The map declares the hierarchy, never per-object location, so it grows with servers rather than with data; revisit at ADR-008 |

## Rollback

The decision governs persistent state — a leaf identifier is written into every
segment header — so rollback is not a code revert once data exists.

**Before any data is written** (the state at authoring): revert the branch. The
three packages have no callers and nothing persists.

**After data exists**, changing the addressing model is a migration, not a
rollback: stand up the new mapping alongside the old, dual-write, backfill by
reading every segment and re-placing it under the new rule, then cut reads over
and retire the old mapping. That is expensive by construction, and saying so here
is the point — it is the reason this record exists before the first byte is
written rather than after.

Rule 5 is what keeps a *depth* change from needing any of this. A depth change is
not a rollback of this decision; it is the decision working.

## Follow-ups

- [ ] Decide the topology map's versioning and distribution mechanism when ADR-008 is authored — this record fixes its content, not how it reaches a client.
