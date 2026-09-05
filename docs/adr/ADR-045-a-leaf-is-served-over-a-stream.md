# ADR-045: A request names a KEY, not a leaf — which is what makes a redirect possible

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-001-address-space.md`, `docs/adr/ADR-008-prefix-routing.md`, `docs/adr/ADR-009-fenced-leases.md`, `docs/adr/ADR-015-admission-control.md`, `docs/adr/ADR-025-datom-encoding.md`, `docs/adr/ADR-027-evaluator.md`, `docs/adr/ADR-043-response-envelope.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/wire/**`, `internal/core/serve/**`
**Enforced-by:** `internal/core/serve/client_test.go::TestAStaleClientIsRedirectedAndRepaired`
**Invalidates:** none — ADR-008 declared `routing.Cluster` "implemented by whatever actually talks to nodes" and left the wire open; `BACKLOG.md` §18 has carried it since
**Served-path change:** A leaf is reachable over a socket. `sdev1-serve` listens, `serve.Client` connects, a stale client is redirected and repairs itself, and everything before this was in-process only.

## Context

Everything in this system runs in one process. `BACKLOG.md` §18 calls the
transport *"the single largest unbuilt piece"* and names what waits behind it:
ADR-018's read-ahead, ADR-019's composed chaos, and anything that measures a
degraded read (§16). ADR-043 fixed the response envelope; the rest is open.

★ **ADR-008 already cut the seam this fits into.** `routing.Cluster` is one
method — *"does this node hold the leaf, and if not, where does it say the leaf
went"* — declared where it is consumed and, in its own comment, *"implemented by
whatever actually talks to nodes"*. `routing.Resolve` already follows redirects,
enforces the epoch rule and spends a hop budget. **A transport supplies one
method and none of that changes.**

⚠ **And there is one question §18 asks that is not obvious: "how a request
identifies the leaf it is for".** The obvious answer is that it names the leaf.
★ That answer breaks ADR-008 completely, and it is the substance of this record.

## Existing Primitives Audit

- `internal/core/routing` (ADR-008): supplies `Route`, `Redirect`,
  `Destination`, `Cache`, `Resolve` and the `Cluster` seam. **Consumed, not
  changed** — the client implements `Cluster` and `Resolve` drives it unmodified.
- `internal/core/wire` (ADR-043): supplies the response envelope and its three
  refusals. **Extended with a request and framing**, reusing the same rules
  rather than inventing a second discipline.
- `internal/core/addr` (ADR-001): supplies `Key`, `KeyOf` and `Descend`.
  **Reused as the thing a request names** — see rule 1.
- `internal/core/leafstore` (ADR-026) and `internal/core/eval` (ADR-027):
  supply the read. **Consumed** — the server holds a store and answers from it.
- `internal/core/admit` (ADR-015): **not wired.** Shedding needs a queue this
  does not have; recorded rather than half-connected.
- An HTTP framework, or a schema language: **rejected below.**

## Decision

**A request names the KEY it wants; the server resolves that key against its own
map and either serves or redirects; the client implements `routing.Cluster`.**

1. ★★ **A request carries an `addr.Key`, NEVER a leaf identifier.** ⚠ Naming the
   leaf is the obvious design and it destroys ADR-008: a client with a stale map
   would address a leaf that has moved, and the receiving node **could not
   redirect, because it would not know what was wanted** — it would hold a leaf
   name it does not serve and no way to work out which key produced it. Naming
   the key is what makes a redirect *computable at the receiver*, and a redirect
   is the whole of ADR-008 rule 4.

2. **The server resolves the key against ITS OWN map, and the client's map is
   never consulted.** ★ ADR-008 rule 3: a client starts with one frontdoor and
   learns the rest. The authority is the node's, the cache is the client's, and
   the redirect is how the second catches up with the first.

3. **The client implements `routing.Cluster` and nothing else.** ⚠ Redirect
   following, the epoch rule and the hop budget are `routing.Resolve`'s, already
   written and tested. A transport that reimplemented them would be a second
   implementation of the property ADR-008 exists to hold.

4. **Frames are length-prefixed with a DECLARED maximum, and an oversized frame
   is refused rather than allocated.** ⚠ A length field is a number a stranger
   controls; reading it and allocating is how one packet exhausts a node's
   memory. The maximum is declared by the caller, not defaulted — the same
   refusal ADR-040, ADR-041 and ADR-042 make for their bounds.

5. **Timeouts are DECLARED, and a server or client without them is refused.**
   ⚠ A read with no deadline is a goroutine a stranger can pin forever, and
   "no timeout" is indistinguishable from "not configured yet".

6. ⚠ **READS ONLY. A write is refused by name.** ADR-009 fences a writer by
   epoch and ADR-020 commits on N replicas; neither has a leader, because
   `BACKLOG.md` §19's consensus is unbuilt. ★ Serving a write here would accept
   data at a durability nobody has and with no fencing — exactly what ADR-004
   refuses at the floor and ADR-009 refuses for a superseded writer. Reads are
   also the elastic half (ADR-015), so this is the half that a redirect can
   repair.

7. **One request per connection, and the connection closes after the response.**
   ⚠ No pooling and no multiplexing. Both are optimisations with their own
   decisions, and a connection carrying exactly one exchange has a failure model
   with nothing in it: no half-consumed stream, no correlation identifiers, no
   in-flight state to reconcile after a drop. Stated as a cost, not hidden.

**What would falsify this.** A client holding a stale route that cannot be
repaired by talking to the wrong node — because the node could not tell what it
was being asked for. That is the falsifier in `Enforced-by:`, and it is what
naming the leaf produces.

## Alternatives Considered

- **Name the LEAF in the request.** It is the obvious design: the client already
  resolved a route, so it knows the leaf. Rejected under rule 1, and it is the
  substance of this record — a node that does not serve that leaf cannot compute
  a redirect from a leaf name, so ADR-008 rule 4 becomes unimplementable and a
  stale client gets an error instead of a repair.
- **Put the client's map version in the request so the server can diff it.** It
  would let the server send only what changed. Rejected under rule 2: it makes
  the client's cache an input to the server's decision, and ADR-008's whole
  design is that the client holds no authority. It also cannot help — the server
  must answer the key regardless.
- **Reimplement redirect-following in the client.** It would avoid a dependency
  on `routing`. Rejected under rule 3: the epoch rule and the hop budget are the
  loop protection, and a second implementation is a second place for them to be
  wrong — silently, since a wrong one still redirects.
- **HTTP, with chi or the standard mux.** Mature, debuggable, and the house Go
  guidance names chi for routers. ⚠ Rejected for THIS surface and not in
  general: ADR-043 already fixed a binary response envelope whose properties are
  a closed tag set, no optional fields and refused trailing bytes. HTTP gives
  every response a headers-and-body shape with an optional body, which is rule 2
  of that record undone — and a status code plus a body is exactly the
  "flattening" it rejects. A future control or admin surface has no such
  constraint and should use the house stack.
- **A schema language — protobuf, Cap'n Proto.** Nobody hand-writes codecs.
  Rejected on ADR-043's argument, unchanged: optional fields and ignorable
  unknowns are their defaults, and they are what the response envelope refuses.
- **Serve writes too.** It would make the transport complete. Rejected under
  rule 6: there is no leader, so a write would be unfenced and committed at a
  durability nobody has.
- **Persistent, multiplexed connections.** Fewer handshakes, lower latency.
  Rejected under rule 7 as premature: it needs correlation identifiers, a
  reconciliation story after a drop, and a reason. None exists yet, and the
  measurement that would justify it needs §16.

## Component / Boundary Impact

One new component, `internal/core/serve`, owning one thing: moving a request and
a response between two processes.

⚠ The boundary: it decides nothing about what an answer MEANS. `eval` evaluates,
`leafstore` stores, `routing` decides where. The server holds them and speaks;
it does not reimplement any of them.

⚠ And it decides nothing about WHO is asking. ADR-033 has grants and `Allow`,
and nothing here calls it — a caller identity needs authentication this record
does not provide, and inventing one would decide an interface nobody has asked
for.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `wire.Request` | new — names an `addr.Key` and a statement | T1 | server, client |
| `wire.EncodeRequest` / `wire.DecodeRequest` | new | T1 | server, client |
| `wire.MaxFrame` / `wire.WriteFrame` / `wire.ReadFrame` | new — length-prefixed framing with a declared bound | T1 | server, client |
| `wire.ErrFrameTooLarge` / `wire.ErrNoFrameBound` | new sentinels | T1 | callers |
| `serve.Server` / `serve.NewServer` / `Serve` / `Addr` / `Close` | new — the listener | T2 | operators |
| `serve.Options` | new — declared timeouts and frame bound | T2 | callers |
| `serve.ErrWriteNotServed` / `serve.ErrNoTimeout` | new sentinels | T2 | callers |
| `serve.Client` / `serve.NewClient` | new — implements `routing.Cluster` | T3 | callers |
| `cmd/sdev1-serve` | new — a real listening process | T2 | operators |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `wire.Request`, framing (T1) | T1 | T2, T3 | No |
| `serve.Server` (T2) | T2 | T3 | No — T3's tests need a real listener |

## Consequences

- **Positive:** The system is reachable over a network, and §18's keystone is
  no longer blocking §3, §5, §7, §16, §19, §25 and §26 on "there is no
  transport".
- **Positive:** Redirect following, the epoch rule and the hop budget arrive for
  free and unchanged, because ADR-008 cut the seam for exactly this.
- **Positive:** Rule 1 means a stale client is *repaired* by the wrong node
  rather than erroring — which is ADR-008 rule 4 finally reachable from outside
  the process.
- **Negative:** ⚠ **Reads only.** A write over the wire is refused by name until
  there is a leader (§19). The transport is half a transport and says so.
- **Negative:** ⚠ One request per connection is a handshake per read. That is a
  real cost, taken deliberately for a failure model with nothing in it, and it is
  the first thing to revisit once §16 can measure what it costs.
- **Negative:** ⚠ **Nothing authenticates.** ADR-033's `Allow` is not called,
  because there is no caller identity. Anyone who can reach the socket can read
  any leaf it holds. Stated plainly — this is a decision to defer, not an
  oversight, and it must be closed before anything faces a network it does not
  own.
- **Neutral:** No shedding. `admit` needs a queue that does not exist.

## Out of Scope

- Serving WRITES (deferred: `docs/adr/BACKLOG.md` §19 — a write needs a fenced leader, and there is no consensus to elect one)
- Authenticating a caller, and calling `authz.Allow` (deferred: `docs/adr/BACKLOG.md` §18 — a caller identity needs authentication this record does not provide; ADR-033 decided the RULE and left the enforcement point here)
- Connection pooling and multiplexing (deferred: `docs/adr/BACKLOG.md` §16 — rule 7's cost, and justifying the complexity needs the measurement §16 owns)
- TLS, and any confidentiality on the wire (deferred: `docs/adr/BACKLOG.md` §18 — a separate decision with its own key-management story)
- How a route REACHES a node — gossip, control plane, or derived from the map (deferred: `docs/adr/BACKLOG.md` §18 — and §18 is right that it is a performance decision, since ADR-008 is correct under all of them)
- When a node may forget a route it no longer serves (deferred: `docs/adr/BACKLOG.md` §18)
- Wiring `admit` shedding into the accept loop (deferred: `docs/adr/BACKLOG.md` §22 — shedding is a queue-level outcome and there is no queue)
- Using HTTP or a schema language for OTHER surfaces (permanent: boundary: the rejection above is about the one message whose properties ADR-043 fixed, and says nothing about a control or admin surface)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A request names the leaf instead of the key | **High — the client already resolved a route, so it has the leaf to hand** | **Critical** — a node that does not serve that leaf cannot compute a redirect, so ADR-008 rule 4 becomes unimplementable and a stale client errors instead of being repaired | Rule 1, and it is the record's falsifier |
| The client reimplements redirect following | Med — it avoids importing `routing` | High — the epoch rule and hop budget get a second implementation, wrong silently, because a wrong one still redirects | Rule 3: the client implements `Cluster` and nothing more |
| A frame length is trusted and allocated | High — it is the obvious read | Critical — one packet exhausts a node's memory, from a number a stranger chose | Rule 4, with a declared bound and a refusal |
| A connection has no deadline | High — it is what `net.Conn` gives you | High — a goroutine a stranger can pin forever | Rule 5, refused at construction |
| A write is served because it looks symmetric | Med | Critical — unfenced, and committed at a durability nobody has | Rule 6, refused by name |
| The open socket is mistaken for a secured one | **Med, and this is the one to watch** | **Critical** — anyone who can reach it reads any leaf the node holds | Stated in Consequences and Out of Scope; ADR-033 has the rule and this has no identity to apply it to |

## Rollback

Removing the transport returns the system to in-process, which is where it was.
⚠ Nothing depends on it yet, so rollback is cheap today and will not be once a
client exists — which is the usual asymmetry and the reason rule 1 is settled now
rather than after there are requests in flight.

## Follow-ups

- [ ] Close the authentication gap before this faces any network the operator does not own. ADR-033 decided the rule and `Allow` is waiting; what is missing is a caller identity, and the socket is readable by anyone who can reach it until then.
- [ ] When consensus exists (`BACKLOG.md` §19), serve writes — and re-check rule 6's refusal is removed deliberately rather than by a caller discovering it can be.
- [ ] When a degraded read can be measured (`BACKLOG.md` §16), revisit rule 7: one connection per request is the simplest failure model and the most handshakes, and only a measurement can say whether that trade is right.
