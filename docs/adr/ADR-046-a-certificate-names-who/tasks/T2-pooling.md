# Task ADR-046-T2: A connection is kept only while its stream position is known

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `serve.Pool`, `serve.PoolBounds`, `serve.ErrNoPoolBounds`, `serve.Client.Close`, `serve.Server.Accepted`
**Consumes:** `serve.TLSConfig` and the TLS dial (T1); `serve.Client`, `wire.ReadFrame`/`WriteFrame` from ADR-045
**Data dependency:** hermetic — a real server on `127.0.0.1:0` reporting its own accept count
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
| `internal/core/serve/server.go` | modify | ⚠ **Not in the original plan.** `handle` serves exchanges in a LOOP. A server that closed after one would make client-side pooling useless — see below. |
| `internal/core/serve/doc.go` | modify | Why a connection is discarded on any error, and why there is still one exchange in flight. |

⚠ **The server had to change too, and missing that would have shipped a pool
that made things worse.** ADR-045 rule 7 closed the connection after one
exchange. Pooling only the client against that server means the client keeps a
connection its peer has already hung up: every second read fails on the write,
falls back to S5's redial, and costs MORE than not pooling at all — while the
pool's own tests, which count accepts, would still show reuse. ★ The reason this
is worth writing down is that it fails in the direction that looks like success.

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
| `TestAConnectionIsReusedAcrossExchanges` | `internal/core/serve/pool_test.go` | Three reads against one node cost ONE accepted connection, read from `Server.Accepted`. ★ Counting ACCEPTS is the only honest measure — asserting the pool's own length would pass for a pool that stores a connection and hands it out to nobody, where the numbers look right and every read still dials | — | S3, S4 |
| `TestAFailedExchangeDiscardsItsConnection` | `internal/core/serve/pool_test.go` | A pooled connection the far end dropped while it was idle is discarded and redialled, and the next read SUCCEEDS. ⚠ **A refusal is NOT a failed exchange** — the first draft of this test used one and was wrong: a refused write is a complete, well-formed response, the stream is still at a frame boundary, and keeping that connection is correct. The failure that matters is the frame not arriving, reproduced with a short server read deadline because that is what a firewall does in production and says nothing about | — | S4, S5 |
| `TestPoolBoundsAreRequired` | `internal/core/serve/pool_test.go` | A zero or negative `MaxIdlePerNode` or `IdleTimeout` is `ErrNoPoolBounds` — at `NewPool` and at `NewClient` | — | S2 |
| `TestAnIdleConnectionIsClosedAfterItsLifetime` | `internal/core/serve/pool_test.go` | A connection idle past `IdleTimeout` is closed rather than handed out, and closing is asserted by writing to it. ⚠ Driven by an INJECTED CLOCK, never by sleeping — a test that sleeps for its own timeout is slow always and flaky on a loaded machine | — | S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests, against a real TLS server that reports its own accept count. |
| 2 — something selects it | `roundTrip` is the only path to a node, and it goes through the pool. |
| 3 — the caller can discover it | `PoolBounds` has no usable zero and `ErrNoPoolBounds` names it. |
| 4 — it is used | Every `Client.Read` uses it, including ADR-045's own client tests and the two-process binary test. |

## Mutation Log

- 2026-09-05 · dd09716* · mutant killed · exit 1 · `internal/core/serve/client.go` · Never take a connection from the pool — always dial. ★ Every read still succeeds, every answer is still correct, and every other test in this package passes untouched: the only thing that changes is a TLS handshake per read, which is invisible to any assertion about CONTENT. It is also what the code silently degrades to if Get is ever made to return nil on an edge case nobody tests. Only counting the accepts a server saw can tell a pool that is working from one that is merely present. · acceptance-sha256:7b57b383d9358add8a3de72bf40d08a6efee2fe2631890eaa2ee8410592a2e82 · covers:a connection being reused across two exchanges rather than redialled
- 2026-09-05 · dd09716* · mutant inconclusive · exit 1 · `internal/core/serve/client.go` · Return the connection to the pool on EVERY exit rather than only after a complete decoded exchange — the single-line "simplification" that removes a flag and reads like tidying. Nothing fails immediately: a broken connection sits in the pool looking identical to a good one, and the damage arrives on whichever later read draws it and starts reading a length prefix from the middle of somebody else"s payload. This is the mutant the whole type exists to make impossible. · acceptance-sha256:7b57b383d9358add8a3de72bf40d08a6efee2fe2631890eaa2ee8410592a2e82 · covers:a connection being discarded rather than returned after a failed exchange
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-05 · dd09716* · mutant killed · exit 1 · `internal/core/serve/client.go` · Return the connection to the pool on a FAILED exit instead of closing it. ⚠ Nothing fails immediately and nothing looks wrong: the read still succeeds via S5"s retry, the answer is still correct, and the server still shows two accepts. The pool simply holds a corpse — a connection at an unknown stream position, indistinguishable from a good one, until some later read draws it and takes a length prefix from the middle of somebody"s payload. ★ The test PASSED against this mutant until an assertion on the idle count was added; succeeding is not the property, discarding is. · acceptance-sha256:7b57b383d9358add8a3de72bf40d08a6efee2fe2631890eaa2ee8410592a2e82 · covers:a connection being discarded rather than returned after a failed exchange
- 2026-09-05 · dd09716* · mutant inconclusive · exit 1 · `internal/core/serve/pool.go` · Fill in sensible pool defaults instead of refusing — the same "helpful" change that was made and rejected for the frame bound and the timeouts, arriving in a third place. It reviews well because every connection is still bounded and 8/30s are reasonable numbers. What it destroys is the difference between an operator who chose them and one who never configured the client at all, and the resulting pool is identical from outside. · acceptance-sha256:7b57b383d9358add8a3de72bf40d08a6efee2fe2631890eaa2ee8410592a2e82 · covers:pool bounds being declared rather than defaulted
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-05 · dd09716* · mutant killed · exit 1 · `internal/core/serve/pool.go` · Refuse only when BOTH bounds are missing — the classic and/or slip, and the one that survives a casual reading because the sentinel, the message and the refusal all still exist. A caller who declared an idle count and forgot the lifetime is then accepted with a zero timeout, which means every pooled connection is expired the instant it is stored: the pool silently does nothing while reporting itself configured. Only asserting each field INDEPENDENTLY catches it, which is why the test enumerates one-missing cases rather than testing the empty struct. · acceptance-sha256:7b57b383d9358add8a3de72bf40d08a6efee2fe2631890eaa2ee8410592a2e82 · covers:pool bounds being declared rather than defaulted

## Invariants

- A connection is stored only after a complete decoded exchange.
- One exchange in flight per connection; no multiplexing.
- The bounds are declared.
- A retry happens only before the request was sent.

## Risks

- ⚠ **Storing a connection after a decode error is the natural bug**, because the transport-level read succeeded and it feels like a caller problem. It is not: a decode failure means the bytes on that stream are not what was expected, so the position of the NEXT frame is unknown.
- ⚠ **Retrying after the request was sent is a correctness bug, not a robustness feature.** The node may have served it. Retry only on a failure at the first write, which is the only point where nothing can have happened yet. ★ Reads are idempotent today, so this looks harmless — it stops being harmless the moment anything else uses this path, and by then the reason will have been forgotten.
- ⚠ **Counting `Pool.Len()` instead of accepts** would pass for a pool that stores connections and never reuses them. Use the server's own accept count — one accept is one handshake, which is the cost being paid down. ★ `Server.Accepted` was added for this and is ordinary observability rather than a test hook: it is the number a pool exists to hold down.
- ⚠ **A refusal is not a failed exchange.** The first draft of `TestAFailedExchangeDiscardsItsConnection` refused a write and expected a redial; the connection was correctly kept, because a refusal is a complete response and the stream is still at a frame boundary. Reproduce the real failure — a peer that dropped an idle connection — instead.
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
- 2026-09-05 · dd09716* · exit 0 · `set -o pipefail …` · acceptance-sha256:7b57b383d9358add8a3de72bf40d08a6efee2fe2631890eaa2ee8410592a2e82 · ms:6926
- 2026-09-05 · dd09716* · exit 0 · `set -o pipefail …` · acceptance-sha256:7b57b383d9358add8a3de72bf40d08a6efee2fe2631890eaa2ee8410592a2e82 · ms:6815
- 2026-09-05 · dd09716* · exit 0 · `set -o pipefail …` · acceptance-sha256:7b57b383d9358add8a3de72bf40d08a6efee2fe2631890eaa2ee8410592a2e82 · ms:6849
- 2026-09-05 · dd09716* · exit 0 · `set -o pipefail …` · acceptance-sha256:7b57b383d9358add8a3de72bf40d08a6efee2fe2631890eaa2ee8410592a2e82 · ms:7304
- 2026-09-05 · dd09716* · exit 0 · `set -o pipefail …` · acceptance-sha256:7b57b383d9358add8a3de72bf40d08a6efee2fe2631890eaa2ee8410592a2e82 · ms:7479
- 2026-09-05 · dd09716* · exit 0 · `set -o pipefail …` · acceptance-sha256:7b57b383d9358add8a3de72bf40d08a6efee2fe2631890eaa2ee8410592a2e82 · ms:7667
