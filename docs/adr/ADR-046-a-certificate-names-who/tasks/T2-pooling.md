# Task ADR-046-T2: A connection is kept only while its stream position is known

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `serve.Pool`, `serve.PoolBounds`, `serve.ErrNoPoolBounds`
**Consumes:** `serve.TLSConfig` and the TLS dial (T1); `serve.Client`, `wire.ReadFrame`/`WriteFrame` from ADR-045
**Data dependency:** hermetic — a real server on `127.0.0.1:0` and a counting listener
**Proof map:** v1
**Rests-on:** `a connection being reused across two exchanges rather than redialled`, `a connection being discarded rather than returned after a failed exchange`, `pool bounds being declared rather than defaulted`

## Goal

Pay the TLS handshake once per connection instead of once per read, without
reintroducing the half-consumed stream ADR-045 rule 7 removed.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/serve/pool.go` | add | `Pool`, `PoolBounds`, get/put/discard and the idle sweep. |
| `internal/core/serve/pool_test.go` | add | The tests below. |
| `internal/core/serve/client.go` | modify | `ClientOptions.Pool`; `roundTrip` takes a pooled connection and returns it only on success. |
| `internal/core/serve/doc.go` | modify | Why a connection is discarded on any error, and why there is still one exchange in flight. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestAConnectionIsReusedAcrossExchanges`, `TestAFailedExchangeDiscardsItsConnection`, `TestPoolBoundsAreRequired`, `TestAnIdleConnectionIsClosedAfterItsLifetime`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `PoolBounds` — `MaxIdlePerNode`, `IdleTimeout` — and refuse either non-positive with `ErrNoPoolBounds`. ⚠An unbounded pool and an unconfigured one are indistinguishable from outside, and the unbounded one is a descriptor leak that only appears under load. [proof: mutation]
3. [S3] Implement `Pool.Get(node)` returning an idle connection or nil, and `Pool.Put(node, conn)` which closes rather than stores when the node is at its bound. [proof: mutation]
4. [S4] ★Rewrite `roundTrip` so the connection is returned to the pool ONLY after a complete, successfully decoded response. ⚠Every other path — write error, read error, frame error, decode error, deadline — closes it. A connection whose stream position is unknown cannot be resynchronised, because the next thing read would be a length prefix taken from the middle of somebody's payload. [proof: mutation]
5. [S5] Retry ONCE on a pooled connection that fails at the first write, with a fresh dial. ⚠This is the one retry this record allows and it is not a cluster policy: it is the pool admitting its own cached connection went stale while idle. It must not extend to a connection that failed after the request was sent, because that request may have been served. [proof: mutation]
6. [S6] Close idle connections past `IdleTimeout`, and close every connection on `Client.Close`. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/serve/... -race -run 'TestAConnectionIsReusedAcrossExchanges|TestAFailedExchangeDiscardsItsConnection|TestPoolBoundsAreRequired|TestAnIdleConnectionIsClosedAfterItsLifetime' -count=1 2>&1 | tee /tmp/adr046-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr046-t2a.out \
  && go test ./internal/core/serve/... ./internal/core/wire/... ./internal/core/routing/... -race -count=1 2>&1 | tee /tmp/adr046-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr046-t2b.out
```

`-race` matters more here than anywhere else in this record: a pool is the first
shared mutable state on the client side.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAConnectionIsReusedAcrossExchanges` | `internal/core/serve/pool_test.go` | Two reads against one node produce ONE accepted connection, counted by a listener that wraps `Accept`. ★ Counting accepts is the only honest measure — asserting the pool's own length would let a pool that stores a connection and never hands it out pass | — | S3, S4 |
| `TestAFailedExchangeDiscardsItsConnection` | `internal/core/serve/pool_test.go` | After a server-side failure mid-exchange, the connection is CLOSED and not stored; the next read dials afresh and succeeds. ⚠ The assertion is on the next read succeeding — a stored broken connection would make it fail, which is exactly the silent corruption this rule prevents | — | S4 |
| `TestPoolBoundsAreRequired` | `internal/core/serve/pool_test.go` | A zero or negative `MaxIdlePerNode` or `IdleTimeout` is `ErrNoPoolBounds` at construction | — | S2 |
| `TestAnIdleConnectionIsClosedAfterItsLifetime` | `internal/core/serve/pool_test.go` | A connection idle past `IdleTimeout` is closed rather than handed out, and the next read dials. ⚠ Driven by an injected clock, never by sleeping — a test that sleeps for a timeout is a test that is flaky on a loaded machine | — | S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests, against a real TLS server and a counting listener. |
| 2 — something selects it | `roundTrip` is the only path to a node, and it goes through the pool. |
| 3 — the caller can discover it | `PoolBounds` has no usable zero and `ErrNoPoolBounds` names it. |
| 4 — it is used | Every `Client.Read` uses it, including ADR-045's own client tests and the two-process binary test. |

## Mutation Log

## Invariants

- A connection is stored only after a complete decoded exchange.
- One exchange in flight per connection; no multiplexing.
- The bounds are declared.
- A retry happens only before the request was sent.

## Risks

- ⚠ **Storing a connection after a decode error is the natural bug**, because the transport-level read succeeded and it feels like a caller problem. It is not: a decode failure means the bytes on that stream are not what was expected, so the position of the NEXT frame is unknown.
- ⚠ **Retrying after the request was sent is a correctness bug, not a robustness feature.** The node may have served it. Retry only on a failure at the first write, which is the only point where nothing can have happened yet. ★ Reads are idempotent today, so this looks harmless — it stops being harmless the moment anything else uses this path, and by then the reason will have been forgotten.
- ⚠ **Counting `Pool.Len()` instead of accepts** would pass for a pool that stores connections and never reuses them. Wrap the listener.
- ⚠ **A sleeping test for `IdleTimeout`** is flaky under load and slow always. Inject the clock.
- A pool is the client's first shared mutable state — every test here runs under `-race`, and `Client` must document whether it is safe for concurrent use.

## Stop Condition

Stop and ask before multiplexing, or before adding retry beyond the single
pre-send case in S5. Both look like the obvious next step; the first reintroduces
what ADR-045 rule 7 removed, and the second is a cluster policy `routing.Resolve`
already owns.

## Out of Scope

- Multiplexing (permanent: boundary: ADR-046 rule 8 — correlation identifiers reintroduce the half-consumed stream)
- Retry and backoff beyond S5's pre-send case (permanent: boundary: ADR-045's Stop Condition)
- Measuring what the pool saves (deferred: `docs/adr/BACKLOG.md` §16)
- Authorization (deferred: T3)

## Verification Log
