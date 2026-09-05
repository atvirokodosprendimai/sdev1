# ADR-045 Tasks

Implementation tasks for ADR-045: a request names a KEY, not a leaf — which is
what makes a redirect possible. See the parent ADR for the decision.

**Source of truth:** the task files' headers. This README is a derived index —
when it disagrees with a task file, the task file wins.

## Execution Order

Three tasks, strictly in order. T2 needs T1's frames; T3's tests need T2's
listener.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T2 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | A request names a key, and a frame declares its bound | done | — | four codec/framing tests, then the wire, addr and routing suites |
| T2 | A server that serves or redirects, and refuses a write by name | done | — | four server tests over real sockets, a `cmd/sdev1-serve` build, then four suites |
| T3 | A client that is a routing.Cluster and nothing more | pending | — | three client tests against two real servers, then three suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `wire.Request`, `wire.ReadFrame`/`WriteFrame` | T2, T3 | neither can speak before there is something to say |
| T2 | `serve.Server` | T3 | T3's tests need a real listener to be redirected by |

## Notes

- ★★ **THE DECISION IS ONE SENTENCE: a request names the KEY, never the leaf.**
  Naming the leaf is the obvious design — the client resolved a route, so it has
  one to hand — and it destroys ADR-008. A node that does not serve that leaf
  **cannot compute a redirect from a leaf name**: it holds a name it does not
  recognise and no way to work out which key produced it. Naming the key makes the
  redirect computable *at the receiver*, and the redirect is the whole of ADR-008
  rule 4.
- ★ **ADR-008 already cut the seam.** `routing.Cluster` is one method, declared
  where it is consumed and documented as *"implemented by whatever actually talks
  to nodes"*. `routing.Resolve` already follows redirects, enforces the epoch rule
  and spends a hop budget. **The transport supplies one method and none of that
  changes** — which is why T3 is small.
- ⚠ **A frame length is a number a stranger chose.** Read-then-allocate is how one
  packet exhausts a node, so the bound is declared and an oversized frame is
  refused BEFORE anything is allocated.
- ⚠ **READS ONLY, refused by name.** ADR-009 fences a writer by epoch and ADR-020
  commits on N replicas; neither has a leader, because §19's consensus is unbuilt.
  Serving a write would be unfenced and committed at a durability nobody has.
  ★ And the refusal must be a `wire.Refusal`, never an empty ANSWER — a client
  reading "no rows" would conclude the write landed.
- ⚠ **One request per connection**, closed after. No pooling, no multiplexing: a
  connection carrying exactly one exchange has a failure model with nothing in it.
  A real cost, taken deliberately, and the first thing to revisit once §16 can
  measure it.
- ⚠ **NOTHING AUTHENTICATES.** ADR-033 decided the grant rule and left the
  enforcement point to whatever gains a caller identity; this does not provide one.
  Anyone who can reach the socket can read any leaf the node holds. That is a
  deferral, stated — and it must be closed before this faces a network the operator
  does not own.
- ⚠ **HTTP was rejected for THIS surface only**, and the house Go guidance names
  chi for routers. The reason is specific: ADR-043 fixed a binary envelope whose
  properties are a closed tag set, no optional fields, and refused trailing bytes,
  and HTTP gives every response a headers-and-body shape with an optional body —
  which is that record's rule 2 undone. A control or admin surface has no such
  constraint and should use the house stack.
