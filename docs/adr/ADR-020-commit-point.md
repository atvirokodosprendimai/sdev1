# ADR-020: A write commits when N memory replicas hold it, and the watermark is that commit point

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-004-durability-policy.md`, `docs/adr/ADR-005-segment-format.md`, `docs/adr/ADR-009-fenced-leases.md`, `docs/adr/ADR-017-lock-free-read-path.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/commit/**`
**Enforced-by:** `internal/core/commit/commit_test.go::TestReplicasInOneDomainDoNotCommit`
**Invalidates:** none — checked; ADR-004 decided how many copies exist and left what a copy must have DONE unstated, which is what this record fills
**Served-path change:** A write is acknowledged once N nodes hold it in memory across distinct power domains, rather than waiting for a disk — and a reader cannot see it before that.

## Context

ADR-004 decided how many copies a tier gets. It did not decide what a copy must
have DONE before a write is acknowledged, and that gap is where durability is
usually lost.

Replicating into memory on several nodes and flushing to disk afterwards is fast,
and it is genuine durability against the failures that dominate: a process
crashing, a panic, an out-of-memory kill, a binary being restarted. The other
node still has the data, and nothing was waiting on a disk.

⚠ **It is not durability against CORRELATED loss.** Two nodes sharing a power
feed, a rack PDU or a datacenter transfer switch lose everything unflushed at the
same instant, and nothing reports it — the write was acknowledged, the client
moved on, and the data is gone. N memory copies protect against INDEPENDENT
failures only, and whether the failures are independent is a placement question
rather than a count.

⚠ **And the flush unit is not the replication unit.** A block is a few megabytes
(ADR-005); a transaction is small. Replicating per block means holding a whole
block's worth of writes unacknowledged and taking a latency spike at every
boundary. Replicating per transaction and flushing per block is two granularities
doing two jobs, and conflating them is the ordinary mistake.

**What does NOT need inventing is atomicity.** ADR-017's watermark already makes
an unpublished entry unreachable rather than half-visible, and a reader loads it
once. So the commit point does not need a new mechanism — it needs the watermark
to advance at the right moment, which makes the watermark itself the commit
point rather than a second thing beside it.

## Existing Primitives Audit

- `internal/core/tail` (ADR-017): supplies the watermark and the rule that
  publication happens after the entry is complete. **Reused as the commit point
  itself.** Advancing it once N replicas hold the entry needs no new visibility
  mechanism, and a second one would be a second definition of "committed".
- `internal/core/durability` (ADR-004): supplies `Size`, `MinSize` and
  `DomainLevel`. **Reused whole** — this record adds no second count and no
  second floor; it says what each of those copies must have done.
- `internal/core/topology` (ADR-001): supplies the failure-domain hierarchy.
  **Reused** to decide whether two acknowledgements are independent, which is
  the whole of rule 3.
- `internal/core/lease` (ADR-009): supplies the epoch. **Relied on**: a commit is
  only meaningful under a current lease, and a fenced-out writer's replicas must
  not count.

## Decision

**A write commits when N replicas in DISTINCT failure domains hold it, and the
watermark advances at exactly that moment.**

1. **Acknowledgement means held in memory, not written to disk.** The flush
   happens afterwards and does not gate the acknowledgement. This is the whole
   performance argument, and it is honest about what it buys: protection from
   independent failures, not from correlated ones.

2. **The replicas must sit in DISTINCT failure domains at the declared level.**
   ★Counting acknowledgements without checking domains is the failure mode: three
   acknowledgements from three processes on one power feed is one failure domain
   wearing three names, and it reads as triple durability right up until the feed
   drops.

3. **The domain level for a memory commit is a POWER domain by default, not a
   rack.** Rack is the right unit for disk durability, where the failure being
   guarded against is a machine or a disk. For unflushed memory the failure is
   power, and the two boundaries are not the same — a rack can span feeds and a
   feed can span racks.

4. **The watermark advances only when the commit condition is met**, so it IS the
   commit point. A reader cannot see an entry that is not committed, because the
   watermark is the only thing that makes an entry reachable at all.

5. **Replication is per TRANSACTION; flushing is per BLOCK.** Two granularities,
   deliberately. Replicating per block holds a block's worth of writes
   unacknowledged and spikes latency at every boundary.

6. **A commit under a superseded epoch does not count.** ADR-009 fences a writer
   at the tail; a replica acknowledging to a fenced-out writer is acknowledging
   to nobody, and counting it would let a superseded writer believe it committed.

7. **Below the floor, the write is REFUSED rather than acknowledged at whatever
   was achieved.** ADR-004 already decided this; it is restated here only because
   the tempting alternative — acknowledge with a warning — is exactly how a
   cluster ends up holding data at a durability nobody chose.

**What would falsify this.** Acknowledgements from replicas in one failure domain
counting towards the commit. That is the falsifier named in `Enforced-by:`, it is
checkable today against a declared topology, and it is the case that looks
correct while being worthless.

## Alternatives Considered

- **Commit on disk write, on every replica.** The strongest guarantee and the
  simplest to reason about. Rejected for the live tier: it puts a disk in the
  acknowledgement path of every write, which is the latency this design exists to
  avoid, and ADR-004 already provides the stronger guarantee for the sealed tier.
- **Commit on ONE memory replica.** Fastest. Rejected: one copy is one failure
  from gone, and ADR-004's floor already forbids it — a single acknowledgement is
  not a durability policy.
- **Count acknowledgements without checking failure domains.** Much simpler, and
  what a naive implementation does. Rejected under rule 2: three processes on one
  power feed acknowledge three times and are one failure, and the count looks
  identical to real triple durability.
- **Use the rack as the domain level for memory commits.** Consistent with the
  disk tier and one fewer concept. Rejected under rule 3: the failure being
  guarded against for unflushed memory is power, and a rack is not a power
  boundary — the two overlap without coinciding.
- **Acknowledge with a warning when below the floor.** Keeps writes flowing
  during a degradation. Rejected under rule 7: it is precisely how a cluster ends
  up holding data at a durability nobody chose, and the warning is read by nobody
  at the moment it matters.
- **A separate commit-visibility mechanism beside the watermark.** Explicit, and
  separates two concerns. Rejected under rule 4: two definitions of "committed"
  drift, and the one a reader uses would not be the one the writer waited for.

## Component / Boundary Impact

One new component, `internal/core/commit`, owning the commit condition and the
domain check. It has one reason to change: what must be true before a write is
acknowledged.

⚠ The boundary: it decides WHEN a write is committed. It does not replicate
anything — there is no transport — it does not flush, and it does not decide how
many copies are wanted, which is ADR-004's. A component that also replicated
would make the condition and its satisfaction one thing, and nothing could then
test the condition alone.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `commit.Ack` | new — one replica's acknowledgement: node, domain, epoch | T1 | T2 |
| `commit.Condition` | new — the policy and the domain level a commit is judged against | T1 | T2 |
| `commit.Satisfied` | new — whether a set of acknowledgements commits, and why not | T1 | callers |
| `commit.ErrBelowFloor` / `ErrOneDomain` / `ErrStaleEpoch` | new sentinels naming the three ways it fails | T1 | callers |
| `commit.Gate` | new — holds acknowledgements and advances a tail's watermark when satisfied | T2 | writers |
| `internal/core/durability` | consumed unchanged | — | — |
| `internal/core/tail` | consumed — the watermark IS the commit point | T2 | readers |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `commit.Ack`, `commit.Condition`, `commit.Satisfied` | T1 | T2 | No — T2 is written against T1 |
| `commit.Gate` | T2 | none yet | No |

## Consequences

- **Positive:** A write is acknowledged at memory speed while still surviving the
  failures that actually dominate — a crash, a panic, an out-of-memory kill.
- **Positive:** Atomicity for readers costs nothing new. The watermark already
  makes an uncommitted entry unreachable, so making it the commit point removes a
  mechanism rather than adding one.
- **Positive:** Two acknowledgements from one power feed cannot masquerade as
  two, which is the failure that would otherwise be invisible until it was an
  incident.
- **Negative:** ⚠ Unflushed data is lost on correlated power loss. That is real,
  it is the price of not waiting for a disk, and it is bounded by how long a
  block takes to fill and flush — which nothing yet measures.
- **Negative:** Requiring distinct power domains constrains placement more than
  requiring distinct racks, and a cluster whose topology does not declare power
  domains cannot satisfy it. That is a topology-declaration burden, and it is
  better than a guarantee that is nominally met.
- **Neutral:** Nothing replicates yet. The condition is decidable and its
  satisfaction is not.

## Out of Scope

- Actually replicating an entry to another node (deferred: `docs/adr/BACKLOG.md` §18)
- Flushing anything to a disk (deferred: `docs/adr/BACKLOG.md` §12)
- How long unflushed data may remain unflushed, and what bounds the exposure window (deferred: `docs/adr/BACKLOG.md` §23)
- How many copies are wanted (permanent: boundary: ADR-004 owns `Size`, `MinSize` and the floor; this record says what each copy must have DONE, which is a different question)
- Who may write (permanent: boundary: ADR-009 owns the lease; this record only refuses to count an acknowledgement made to a superseded writer)
- Declaring which nodes share a power domain (permanent: fact: a failure domain is whatever the topology declares, and ADR-001's level labels already carry it; citation: file `internal/core/topology/topology.go:1`)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Acknowledgements from one failure domain are counted as several | High without rule 2 | Critical — apparent durability that is worthless, invisible until the feed drops | `Satisfied` counts DISTINCT domains and the falsifier drives the one-domain case explicitly |
| The domain level is set to rack for memory commits, out of consistency | Med | High — a rack is not a power boundary, so the guarantee is nominal | Rule 3 names power as the default and the record says why the two differ; the condition takes the level explicitly rather than defaulting silently |
| Unflushed data is lost on correlated power loss | Certain, by design | Med to High depending on the flush interval | Stated as a consequence rather than hidden; bounding the exposure window is `BACKLOG.md` §23 |
| A fenced-out writer believes it committed | Med | High — two writers each believing they committed different things | An acknowledgement carries the epoch it was made under, and one below the tail's highest is not counted |

## Rollback

No persistent state here, so a code revert is available — but the commit point is
a client-visible guarantee, and weakening it later is not a revert. Moving from
"N memory replicas in distinct power domains" to anything weaker means writes
already acknowledged were acknowledged under a promise the system no longer
makes, and nothing records which those were.

Strengthening is always available: waiting for a disk as well is a policy change,
not a format change, because nothing about a stored byte depends on when it was
acknowledged.

## Follow-ups

- [ ] When a transport exists (`BACKLOG.md` §18), confirm an acknowledgement means the replica HOLDS the entry rather than that it received the message — an acknowledgement of receipt would make the commit point a message in flight.
- [ ] When the segment writer lands (`BACKLOG.md` §12), bound the exposure window (`BACKLOG.md` §23) and measure it; the record states the risk and nothing yet says how large it is.
- [ ] When a real topology is declared, confirm power domains are actually expressible and populated — rule 3 is worthless if every node reports the same power domain because nobody filled the field in.
