# ADR-009: Make leaf ownership a fenced lease enforced at the resource, and split consensus into one group per subtree

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-003-transaction-boundary.md`, `docs/adr/ADR-004-durability-policy.md`, `docs/adr/ADR-017-lock-free-read-path.md`, `docs/adr/ADR-019-chaos-and-the-failure-catalogue.md`, `docs/adr/FAILURES.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/lease/**`
**Enforced-by:** `internal/core/tail/fencing_test.go::TestFencedOutWriterCannotAppend`
**Invalidates:** ADR-017 — its `tail.WriterToken` is replaced by an epoch-carrying lease, because a token that cannot be superseded leaves a leaf read-only forever when its writer dies
**Served-path change:** A leaf whose writer dies is taken over by another node and accepts writes again, instead of being readable but permanently unwritable.

## Context

ADR-003 gave each leaf a single writer and ADR-017 built the tail on that
assumption, handing out one writer token and never taking it back. ADR-019's
chaos suite then found the consequence and catalogued it as the corpus's only
open failure: **a leaf whose writer process dies is read-only forever.** Reads
keep serving the published prefix correctly, so the failure is one-sided and
silent.

That entry also records why it was not fixed there. A bare release, or a token
any caller may claim after a timeout, is WORSE than the fault: it lets a second
writer take a leaf whose first writer is merely SLOW — paused by a garbage
collection, a stalled disk, a network hiccup — and two live writers appending to
one tail is not a degraded system, it is a corrupted one.

**Safe handover needs a fencing token, and the token has to be checked at the
resource.** This is the part that is easy to get subtly wrong. The natural
implementation is for a writer to ask "am I still the leader?", get yes, and then
write. Between the question and the write it can lose leadership — to a pause, a
partition, a slow disk — and the write lands anyway. The check and the write are
not atomic and no amount of checking makes them so.

So the epoch travels WITH the write, and the thing being written to refuses
anything below the highest epoch it has seen. A writer that was paused for a
minute comes back, appends with its old epoch, and is refused by the tail itself.
It cannot corrupt anything; it can only fail, which is what it should do.

The second half of this record is about scale. One consensus group per cluster
serializes every write in the system through one leader, which contradicts
ADR-003's whole point. So there is one group per leaf subtree — many small
groups, mostly quiet, whose heartbeats coalesce because the same few nodes
participate in many of them.

⚠ Consensus itself needs a transport, and there is none (`BACKLOG.md` §18). This
record therefore lands its fencing half now — which is what closes the open
failure — and states the consensus half as decided but unbuilt, rather than
writing a Raft against nothing.

## Existing Primitives Audit

- `internal/core/tail` (ADR-017): supplies the append path and `WriterToken`.
  **Reshaped, not replaced.** The token becomes epoch-carrying; the watermark,
  the chunking and the lock-free read path are untouched, because none of them
  depended on the token being permanent.
- `internal/core/durability` (ADR-004): supplies `Size`, `MinSize` and the
  witness shape its follow-up asked this record to confirm. **Reused whole.** A
  witness is a voting member holding no data, and it is expressible here as a
  lease participant that never receives an append.
- `internal/core/addr` (ADR-001): supplies `LeafID`, which is the unit a lease is
  held over. **Reused whole**: one group per subtree means the group's identity
  IS a prefix.
- `internal/core/chaos` (ADR-019): supplies the `writer-process-lost` fault.
  **Reused as the acceptance criterion** — the entry it catalogued is
  re-dispositioned by this record rather than being quietly dropped.
- A Raft library: **not chosen here.** Consensus is deferred with the transport,
  and picking a library before there is a transport to run it over would be
  choosing on no information.

## Decision

**Ownership of a leaf is a lease carrying a monotonically increasing epoch, and
the epoch is enforced by the thing being written to.**

1. **Every lease carries an epoch, strictly greater than every epoch previously
   granted for that leaf.** The epoch is the fencing token: it says not "who you
   are" but "how recent your claim is", which is the only question the resource
   can answer without knowing anything about liveness.

2. **The resource refuses, not the writer.** An append carries its epoch, and the
   tail rejects anything below the highest it has observed. ★This is the whole
   mechanism. A writer that checks its own leadership and then writes has a
   window between the two in which it can lose leadership, and no amount of
   checking closes it — the check and the write are not atomic and cannot be
   made so from the writer's side.

3. **Observing a higher epoch is irreversible.** Once the tail has seen epoch
   N it never again accepts N-1, even if the holder of N vanishes immediately.
   A leaf that has moved on cannot be dragged back by whoever was slowest.

4. **A new lease is granted without waiting for the old holder.** Waiting is what
   makes a dead writer a permanent outage; the epoch is what makes not waiting
   safe. The old holder is not asked to release, is not told, and cannot object —
   it simply discovers, on its next append, that it is not the writer any more.

5. **Consensus is one group per leaf subtree, not one per cluster.** A single
   group would serialize every write in the system through one leader, which is
   the thing ADR-003's per-entity boundary exists to avoid. Many small groups
   mostly sit idle, and their heartbeats coalesce because the same few nodes
   participate in many of them.

6. **A witness is a voting member that holds no data**, which closes ADR-004's
   follow-up. It exists because two is a durability floor and not a consensus
   floor: two voting members give a quorum of two, so a bare pair is less
   available than a single node. Two data replicas plus a witness is the smallest
   shape that is both durable and available.

7. **Fencing is separate from routing.** ADR-008 says where a request should go;
   a lease says who may write when it gets there. Reaching a node must never
   imply permission to change anything on it, or a stale route becomes a
   correctness bug instead of an extra hop.

**What would falsify this.** A write that lands on a leaf after a higher epoch
was granted. That is exactly what T2's test injects, and it is the criterion
ADR-019's `writer-process-lost` entry is re-dispositioned against — so the claim
is checked by the same suite that found the problem, rather than by a new test
written to agree with it.

## Alternatives Considered

- **Leave the writer token permanent, as ADR-017 shipped it.** Simple, and
  provably free of two-writer corruption. Rejected because ADR-019 catalogued
  what it costs: a leaf whose writer dies is read-only forever, which is a
  permanent outage caused by a transient fault.
- **A release call, or a timeout after which any caller may claim the token.**
  The obvious fix and the dangerous one. Rejected: it cannot distinguish a dead
  writer from a slow one, so a garbage-collection pause becomes two live writers
  appending to one tail. Trading a leaf that stops for a leaf that lies is a bad
  trade.
- **The writer checks its own leadership before each append.** Familiar, and what
  most systems do first. Rejected under rule 2: the check and the write are not
  atomic, so the writer can lose leadership in between and the write lands
  anyway. The window is small and it is the window that eventually corrupts.
- **Leases with wall-clock expiry and no epoch.** Widely used, and it removes the
  need for a token. Rejected: it makes correctness depend on bounded clock skew
  between nodes, which `BACKLOG.md` §4 records as unpoliced here, and a clock
  that jumps is a two-writer scenario with no mechanism able to detect it.
- **One consensus group for the whole cluster.** Far simpler to reason about and
  to operate. Rejected under rule 5: it puts every write in the system through
  one leader, which contradicts the per-entity boundary the corpus is built on.
- **One group per leaf rather than per subtree.** Maximum parallelism. Rejected
  for now as a heartbeat cost that scales with leaf count rather than with node
  count; the subtree is the coarser unit that keeps the group count bounded by
  the topology instead of by the data.

## Component / Boundary Impact

One new component, `internal/core/lease`, owning epochs and their granting. It
has one reason to change: how ownership of a leaf is established and superseded.

`internal/core/tail` is amended to enforce an epoch on append. That is a change
to a component ADR-017 governs, and it is why this record carries an
`Invalidates:` header rather than only a cross-reference.

⚠ The boundary: a lease says WHO MAY WRITE. It does not say where a request
should go — ADR-008 — and it does not say how many copies must exist, which is
ADR-004's floor. A lease holder writing to a leaf below its durability floor is
still refused, by a different mechanism, for a different reason.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `lease.Epoch` | new — a monotonically increasing fencing token | T1 | T2, `tail` |
| `lease.Lease` | new — a leaf, its holder, and its epoch | T1 | T2 |
| `lease.Registry` | new — grants strictly increasing epochs per leaf | T1 | T2 |
| `lease.ErrStaleEpoch` | new sentinel — the claim is not the newest | T1 | `tail`, callers |
| `tail.Tail.Append` | **changed** — takes a `lease.Epoch` and refuses a stale one | T2 | every writer |
| `tail.WriterToken` | **removed** — superseded by the lease | T2 | — |
| `chaos` fault `writer-process-lost` | **re-dispositioned** — was unrecoverable and open, becomes recovers | T2 | `docs/adr/FAILURES.md` |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `lease.Epoch`, `lease.Lease`, `lease.Registry`, `lease.ErrStaleEpoch` | T1 | T2 | No — T2 is written against T1 and does not exist before it |
| `tail.Tail.Append` taking an epoch | T2 | every caller of the tail | **Yes** — the signature changes and `WriterToken` is removed. Nothing outside the tests calls it yet, which is why the change is cheap NOW and would not be later |

## Implementation

Two tasks, sequential. See `docs/adr/ADR-009-fenced-leases/tasks/README.md`.

## Consequences

- **Positive:** The corpus's only open catalogued failure closes, with the same
  suite that found it re-run as the evidence.
- **Positive:** A slow writer is indistinguishable from a dead one to the granter,
  and that no longer matters. Correctness stops depending on liveness detection,
  which is the thing distributed systems get wrong most often.
- **Positive:** A fenced-out writer fails loudly at its next append rather than
  corrupting anything, so the fault is visible to it and to an operator.
- **Negative:** Every append carries an epoch and every tail keeps one more piece
  of state. Small, and paid on the hot write path.
- **Negative:** The consensus half is decided and unbuilt, so who GRANTS a lease
  is still a single in-process registry. The fencing is real; the election is
  not, and this record says which is which.
- **Neutral:** `Append`'s signature changes and `WriterToken` disappears. Nothing
  outside tests calls it yet — which is exactly why this is the moment.

## Out of Scope

- Raft itself: log replication, elections, membership changes (deferred: `docs/adr/BACKLOG.md` §19)
- Heartbeat coalescing across groups, and its wire representation (deferred: `docs/adr/BACKLOG.md` §19)
- The transport every part of consensus needs (deferred: `docs/adr/BACKLOG.md` §18)
- Where a lease registry lives, and how it survives its own restart (deferred: `docs/adr/BACKLOG.md` §19)
- Clock skew between nodes (permanent: boundary: this record deliberately avoids depending on clocks — the epoch is a counter, not a deadline — and `BACKLOG.md` §4 owns skew for the mechanisms that do care)
- Where a request should be sent (permanent: boundary: ADR-008 owns routing; reaching a node is not permission to write on it)
- How many copies a write needs (permanent: boundary: ADR-004 owns the floor; a lease holder writing below it is still refused, by a different mechanism for a different reason)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The epoch is checked by the writer rather than by the resource, reintroducing the non-atomic window | Med — it is the natural way to write it | Critical — two writers, silently | `Append` takes the epoch and refuses it itself; the mutant for that claim removes the check at the tail and is killed |
| A granter restarts and reissues an epoch it already granted | Med | Critical — fencing is void, since the token no longer orders anything | The tail refuses any epoch not strictly above what it has SEEN, so a reissued epoch is rejected at the resource even if the granter is confused. Granter durability is `BACKLOG.md` §19 |
| Someone adds a release or a timeout later, for convenience | Med | High — the alternative this record explicitly rejected | Stated in the Stop Condition and in the package comment, with the reason rather than the prohibition |
| The consensus half is assumed built because the fencing half is | Med | Med — a false sense of coverage | Task status and this record's Consequences both say it is unbuilt; the registry's name says it is in-process |

## Rollback

The epoch becomes part of the append path, so a revert has to consider data.
Nothing is persisted yet — the tail is in memory and the registry is in
memory — so today reverting is a code revert plus restoring `WriterToken`.

Once epochs are written into a durable log, reverting means a log whose entries
carry a field nothing reads, which is harmless, and a system that has stopped
refusing stale writers, which is not. At that point the rollback is forward: fix
the fencing rather than remove it.

## Follow-ups

- [ ] When a transport exists (`BACKLOG.md` §18), build the consensus half and confirm a granted epoch survives a granter restart — the tail's refusal covers a confused granter, but a granter that forgets everything cannot grant safely at all.
- [ ] When ADR-019's composed cluster runs, add the two faults this record makes testable: a fenced writer that does not know it, and two nodes each believing they hold the leaf.
- [ ] Re-check ADR-004's witness shape against the built consensus layer; this record confirms a witness is EXPRESSIBLE, not that a Raft implementation will accept one.
