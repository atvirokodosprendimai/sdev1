# ADR-008: Route on aggregated trie prefixes, and make a stale route a redirect rather than a wrong answer

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-001-address-space.md`, `docs/adr/ADR-016-tenant-prefix.md`, `docs/adr/ADR-004-durability-policy.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/routing/**`
**Enforced-by:** `internal/core/routing/redirect_test.go::TestStaleRouteRedirectsRatherThanFailing`
**Invalidates:** none — checked; ADR-001 fixed what a leaf address IS and explicitly left how a client reaches one to this record
**Served-path change:** A client reaches any key from a single known frontdoor, learning routes as it goes, instead of holding the cluster's map or asking a metadata service on every request.

## Context

ADR-001 gave every key an address in a 256-way trie and said a client resolves a
leaf to its holders. It did not say how a client comes to know that, and the two
obvious answers are both wrong at planetary scale.

**Every client holds the whole map.** It is simple and it is what the first
draft of ADR-001 implied. It fails on two counts: the map is proportional to the
cluster, so every client pays for every machine that exists; and every
topology change has to reach every client before anything is correct, which
turns a routine repair into a fleet-wide push.

**A metadata service answers every request.** Also simple, and it puts a
synchronous hop and a single point of failure in front of every read in the
system.

The way out is that **a trie prefix is a routable prefix**. A node that holds
everything under `0x2A/1` can advertise that one prefix instead of the sixteen
million leaves beneath it, and a client matching longest-prefix-first reaches the
right place without knowing what is below. The table is then bounded by fan-out
and live depth rather than by how much data exists — which is the property that
makes the whole thing hold as the cluster grows.

**The remaining problem is staleness, and it is the interesting one.** A cached
route goes out of date the moment a leaf moves, and a client cannot know when
that happened. If a stale route produces an error, every topology change breaks
every client that had not yet heard; if it produces a WRONG ANSWER — a read
served by a node that no longer holds the leaf — the system is silently
incorrect.

So a stale route must produce a REDIRECT. The node that receives a misrouted
request knows where the leaf went, says so, and the client pays one extra hop and
learns something. Staleness becomes a performance cost that repairs itself,
rather than a correctness problem that needs preventing.

⚠ And a redirect that is not ordered is a loop. Two nodes can each believe the
other holds a leaf, and a client will bounce between them forever. That is the
failure this record has to close, not merely mention.

## Existing Primitives Audit

- `internal/core/addr` (ADR-001, ADR-016): supplies `LeafID`, its depth, and
  `Contains`. **Reused whole.** Longest-prefix matching is `Contains` plus a
  depth comparison; the trie already IS the routing structure, which is the
  observation this record rests on.
- `internal/core/topology` (ADR-001): supplies the level hierarchy and
  `Distance`. **Reused** for ordering next hops by nearness, not for routing
  itself.
- `internal/core/placement` (ADR-001): supplies `Resolve` and `Nearest`.
  **Reused rather than replaced**: placement says which nodes SHOULD hold a leaf;
  routing says where a client should send a request now. They disagree exactly
  when a repair is in flight, and the redirect is how that disagreement resolves.
- A routing library: **none.** The table is a prefix trie with longest-match
  lookup over an address space this repository already defines, and a general
  library would bring a second address model.

## Decision

**A route is a trie prefix and a set of next hops. Clients learn routes by being
redirected, and never hold the map.**

1. **Longest prefix wins.** A lookup walks from the most specific matching
   prefix to the least, so a subtree can be carved out of a larger route by
   advertising a deeper one, without withdrawing the parent.

2. **Aggregation is what bounds the table.** When every child of a node resolves
   to the same next hops, the children are replaced by the parent. A table's size
   is therefore governed by how much the cluster's placement VARIES, not by how
   many leaves exist — a uniform cluster of any size aggregates to very few
   routes.

3. **A client starts with one frontdoor and learns the rest.** No bootstrap map,
   no discovery protocol, no metadata service on the request path. The first
   request goes to a known address and every redirect teaches the client
   something it keeps.

4. **A stale route is answered with a redirect, never with an error and never
   with data.** ★This is the whole record. A node that no longer holds a leaf
   knows where it went; saying so costs one hop and repairs the client. Serving
   the request anyway would be silently wrong, and refusing would make every
   topology change a fleet-wide outage.

5. **Every route carries a monotonically increasing epoch, and a client never
   installs an older route over a newer one.** ⚠ This is what makes a redirect
   ORDERED rather than merely a hint, and it is the difference between
   self-healing and a loop: without it, two nodes with opposing stale views
   redirect a client to each other forever, and each redirect looks exactly as
   authoritative as the last.

6. **A resolution carries a hop budget and refuses when it is exhausted.** The
   epoch rule makes a loop impossible in a correct cluster; the budget makes it
   BOUNDED in an incorrect one. A bug in one node must not turn a client into an
   infinite loop, and `ErrTooManyRedirects` naming the chain is what an operator
   needs to find the node that is lying.

7. **Routing is not placement, and the two are allowed to disagree.** Placement
   is canonical and computed; routing is observed and cached. During a repair the
   right answer differs from the computed one, and the redirect is precisely how
   a client is told.

**What would falsify this.** If aggregation does not actually bound the table —
if real placement varies so much that few routes ever collapse — then the table
grows with the cluster and rule 2 fails, taking rule 3 with it. That measurement
needs a real deployment and does not exist, so it is recorded as the thing to
measure rather than as a settled property. What CAN be shown now is the
arithmetic: the bound is a function of fan-out and live depth, and a test asserts
it holds for a table built over generated placements.

## Alternatives Considered

- **Every client holds the full topology map.** Simplest, one lookup, no
  redirects. Rejected: it is proportional to the cluster, and every topology
  change must reach every client before anything is correct. ADR-001 implied this
  and the implication is corrected here.
- **A metadata service consulted on every request.** Always correct, trivially
  understood. Rejected: a synchronous hop and a single point of failure in front
  of every read, and the thing every large storage system eventually has to
  remove.
- **Consistent hashing with no routing layer at all.** A client computes the
  holder directly, which is what `placement.Resolve` already does. Rejected as
  the whole answer: it gives the CANONICAL holder, and during a repair,
  rebalance or failure the canonical holder is not where the data is. It is kept
  as the starting point, with routing correcting it.
- **Gossip the full map to clients.** Bounded staleness, no redirects on the hot
  path. Rejected: it is the full map again, with a delivery mechanism attached,
  and it makes every client a participant in cluster membership.
- **Fail a misrouted request and let the client re-resolve.** Simple, and
  correct. Rejected: it converts every topology change into an error visible to
  callers, when the node answering already knows the right answer and could just
  say it.
- **Redirects without epochs, relying on a hop budget alone.** Fewer moving
  parts. Rejected under rule 5: the budget bounds a loop but does not prevent
  one, so every route flap would cost every client its full budget, and a client
  could install a stale route over a fresh one and stay wrong until something
  else corrected it.

## Component / Boundary Impact

One new component, `internal/core/routing`, owning the route table, its
aggregation, and the redirect. It has one reason to change: how a client finds
the node to talk to.

⚠ The boundary: routing does not decide WHERE data should live — that is
placement and ADR-004's policy — and it does not decide who may write, which is
ADR-009's. It answers "where should this request go now", which is a different
question from all three, and the disagreement between it and placement is
information rather than a bug.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `routing.Route` | new — a prefix, its next hops, and an epoch | T1 | T2, clients |
| `routing.Table` | new — insert, longest-prefix lookup, aggregate | T1 | T2 |
| `routing.Table.Aggregate` | new — collapse children sharing next hops | T1 | operators, T2 |
| `routing.Redirect` | new — the answer a misrouted node returns | T2 | clients |
| `routing.Cache` | new — a client's partial table, which never holds the map | T2 | clients |
| `routing.ErrTooManyRedirects` | new sentinel — the chain, for an operator | T2 | clients |
| `routing.ErrNoRoute` | new sentinel — nothing matches, not even a default | T1 | clients |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `routing.Route`, `routing.Table`, `routing.ErrNoRoute` | T1 | T2 | No — T2 is written against T1 and does not exist before it |
| `routing.Redirect`, `routing.Cache`, `routing.ErrTooManyRedirects` | T2 | none yet | No |

## Implementation

Two tasks, sequential. See `docs/adr/ADR-008-prefix-routing/tasks/README.md`.

## Consequences

- **Positive:** A client's memory is bounded by the routes it has actually used,
  not by the size of the cluster. A new machine costs existing clients nothing
  until something moves onto it.
- **Positive:** A topology change needs to reach nobody. The first client to hit
  a stale route is corrected by the node that received it, and pays one hop.
- **Positive:** The table is bounded by fan-out and live depth rather than data
  volume, so it is the same size on a cluster of ten machines and ten thousand
  with the same placement variety.
- **Negative:** The first request to any new region of the key space costs extra
  hops while the cache fills. Cold clients are slower, and there is no
  pre-warming here.
- **Negative:** A node must know where a leaf WENT in order to redirect, which
  means holding some routing state about leaves it no longer serves. That state
  has to be aged out, and nothing here says when.
- **Neutral:** Routing and placement can disagree, and a diagnostic that shows
  both is more useful than one showing either. ADR-012's console should show the
  pair rather than picking one.

## Out of Scope

- The transport, its framing, and how a redirect is actually carried on the wire (deferred: `docs/adr/BACKLOG.md` §18)
- How routes are distributed between nodes, and whether that is gossip, a control plane or something else (deferred: `docs/adr/BACKLOG.md` §18)
- When a node may forget a route for a leaf it no longer serves (deferred: `docs/adr/BACKLOG.md` §18)
- Who is allowed to write to a leaf once a client has reached it (permanent: boundary: ADR-009 owns leader election and fencing; a route says where to go, never what you may do there)
- Where data SHOULD live (permanent: boundary: ADR-004 owns the durability policy and `internal/core/placement` computes the canonical holders; routing observes where it actually is)
- Authenticating a redirect against a node that lies about routes (permanent: boundary: a hostile node inside the cluster is a threat model this corpus has not taken on anywhere, and taking it on here alone would be incoherent)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Two nodes redirect a client to each other forever | Med without epochs, low with them | High — a client hangs and nothing reports why | Monotonic epochs make a loop impossible in a correct cluster; a hop budget bounds it in an incorrect one, and the error names the chain so the lying node is findable |
| A client installs a stale route over a fresh one | Med | Med — the client is wrong until something else corrects it | The cache refuses a route whose epoch is not newer, which is asserted rather than assumed |
| Aggregation does not bound the table in practice, because real placement varies too much | Unknown — no deployment exists | High — rule 3 fails with it | Stated as the falsifier with the measurement named; the arithmetic bound is tested now, the empirical one when a cluster exists |
| Routing state for departed leaves grows without limit | Med | Med | Named as a consequence and deferred to `BACKLOG.md` §18 rather than left implicit |

## Rollback

No persistent state — a route table is derived and a client cache is
disposable — so rollback is a code revert. Nothing this record decides is written
to a disk.

The client-visible contract is the part with a cost: once callers expect a
redirect rather than an error, removing redirects makes every topology change
visible again. That is a compatibility question rather than a data one, and it is
why the redirect is in the record from the start rather than added later.

## Follow-ups

- [ ] When a transport exists (`BACKLOG.md` §18), confirm a redirect is carried in a way a client cannot mistake for a successful answer — a redirect that looks like data is the failure this record is built to avoid.
- [ ] When ADR-009 lands, confirm a route and a write lease are separate: reaching a node must not imply permission to write there, or a stale route becomes a correctness bug rather than a hop.
- [ ] When a cluster exists, measure whether aggregation actually bounds the table under real placement variety. That is this record's stated falsifier and it cannot be checked before then.
- [ ] When ADR-012's console lands, show routing and placement side by side; their disagreement is what a repair looks like from outside.
