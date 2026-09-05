# Task ADR-045-T3: A client that is a routing.Cluster and nothing more

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `serve.Client`, `serve.NewClient`, `serve.Client.Serve`, `serve.Client.Read`
**Consumes:** `serve.Server` (T2); `wire.Request`, framing (T1); `routing.Cluster`, `routing.Cache`, `routing.Resolve` from ADR-008
**Data dependency:** hermetic — two real servers on `127.0.0.1:0`, each with its own temporary leaf
**Proof map:** v1
**Rests-on:** `a stale client being repaired by the node it wrongly asked`, `redirect following being routing.Resolve's rather than the client's`, `a client honouring the epoch rule it does not implement`

## Goal

Make the transport supply exactly one method — `routing.Cluster.Serve` — so that
ADR-008's redirect following, epoch rule and hop budget drive a real network with
no second implementation of any of them.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/serve/client.go` | add | `Client`, its `Serve` method, and a `Read` that drives `routing.Resolve`. |
| `internal/core/serve/client_test.go` | add | The tests below, over two real servers. |
| `internal/core/serve/doc.go` | modify | Why the client implements one method and borrows the rest. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestAStaleClientIsRedirectedAndRepaired`, `TestTheClientImplementsClusterAndNotRouting`, `TestAnOlderRouteIsNotInstalled`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Implement `Client.Serve(node, key) (routing.Redirect, bool)`: dial the node, send the framed request, and translate the response — an answer means served, a redirect means not-served plus the route. ★That signature is `routing.Cluster`, and it is the entire contract. [proof: mutation]
3. [S3] Implement `Read` as a thin driver: `routing.Resolve(cache, client, key, budget)` to find the node, then one exchange with it. ⚠No redirect loop of its own — the epoch rule and the hop budget belong to `Resolve` and a second copy is a second place to be wrong. [proof: mutation]
4. [S4] Cache the answer from the resolving exchange rather than re-asking, so `Read` costs the hops it took and not one more. [proof: mutation]
5. [S5] Confirm an older route is NOT installed: `routing.Cache.Install` already refuses one, and this task must not work around it. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/serve/... -race -run 'TestAStaleClientIsRedirectedAndRepaired|TestTheClientImplementsClusterAndNotRouting|TestAnOlderRouteIsNotInstalled' -count=1 2>&1 | tee /tmp/adr045-t3a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr045-t3a.out \
  && go test ./internal/core/serve/... ./internal/core/routing/... ./internal/core/wire/... -race -count=1 2>&1 | tee /tmp/adr045-t3b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr045-t3b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAStaleClientIsRedirectedAndRepaired` | `internal/core/serve/client_test.go` | **The falsifier ADR-045 names in `Enforced-by:`.** Two real servers; a client whose cache points at the WRONG one reads successfully, and afterwards its cache holds the right route — so the wrong node repaired it. ★ That repair is only possible because the request named a key the wrong node could descend | — | S2, S3 |
| `TestTheClientImplementsClusterAndNotRouting` | `internal/core/serve/client_test.go` | `*Client` satisfies `routing.Cluster` (a compile-time assertion), and `Read` reaches the right node through `routing.Resolve` — asserted by exhausting a hop budget of 1 against a stale cache and getting `ErrTooManyRedirects` from `routing`, not an error of the client's own | — | S3 |
| `TestAnOlderRouteIsNotInstalled` | `internal/core/serve/client_test.go` | A node advertising an OLDER epoch does not move the client's cache backwards, and the resolution stops rather than looping. ⚠ Driven through the real client so the property is checked where it will actually be used | — | S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The three tests, against two real listeners. |
| 2 — something selects it | `Read` is the caller-facing entry, and it goes through `routing.Resolve`. |
| 3 — the caller can discover it | `Client` satisfies `routing.Cluster`, which is the documented seam. |
| 4 — it is used | ⚠ Nothing in `cmd/` reads over the network yet — `sdev1-ql` still uses a local session. The client is exercised end to end by its tests against a real server; a networked CLI is a separate, small piece of work and is not smuggled in here. |

## Mutation Log

## Invariants

- The client implements `Serve` and borrows everything else.
- An older route is never installed.
- The hop budget is `routing.Resolve`'s.

## Risks

- ⚠ **Writing a redirect loop in the client is the natural thing to do** — it is right there in the response. It duplicates the epoch rule and the hop budget, and a duplicate that is wrong still redirects, so nothing fails visibly.
- ⚠ **The repair test must assert the CACHE afterwards**, not merely that the read succeeded. A client that succeeded by trying every node it knows has not been repaired and will pay the same cost next time.
- ⚠ **`ErrTooManyRedirects` must come from `routing`**, which is what shows the budget is that package's. A client-side error of the same shape would pass a weaker test.
- Two listeners on `127.0.0.1:0` in one test are two goroutines; close both, and let `t.Cleanup` do it so a failure does not leak a listener into the next test.

## Stop Condition

Stop and ask before adding retry, backoff or failover to `Serve`. Those are
policies about a cluster's behaviour, and `routing.Resolve` already owns the one
that exists; a second policy in the transport would be invisible to it.

## Out of Scope

- A networked `sdev1-ql` (deferred: `docs/adr/BACKLOG.md` §18 — small, separate, and not smuggled into a record about the protocol)
- Retry and backoff (deferred: `docs/adr/BACKLOG.md` §18)
- Pooling (deferred: `docs/adr/BACKLOG.md` §16)
- Authentication (deferred: `docs/adr/BACKLOG.md` §18)

## Verification Log
