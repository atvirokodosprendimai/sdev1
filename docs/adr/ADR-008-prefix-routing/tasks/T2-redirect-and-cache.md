# Task ADR-008-T2: The redirect, the epoch that orders it, and a client cache that never holds the map

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `routing.Redirect`, `routing.Cache`, `routing.NewCache`, `routing.Cache.Install`, `routing.Cache.Lookup`, `routing.Resolve`, `routing.ErrTooManyRedirects`
**Consumes:** `routing.Route`, `routing.Table`, `routing.ErrNoRoute` (T1), `addr.Key` from ADR-001
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `a stale route producing a redirect rather than an error or an answer`, `an older epoch never replacing a newer route`, `the hop budget bounding a redirect chain`

## Goal

Let a client start from one frontdoor and learn its way, so a stale route costs a
hop instead of being either an outage or a wrong answer.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/routing/redirect.go` | add | `Redirect`, `Cache`, `Resolve`, and `ErrTooManyRedirects`. |
| `internal/core/routing/redirect_test.go` | add | The tests below, including the falsifier named in ADR-008's `Enforced-by:`. |

★ `Cache` is what makes rule 3 true rather than aspirational: it is a `Table`
that starts nearly empty and is only ever filled by redirects. Nothing here can
load a full map, because nothing here has a way to ask for one.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestStaleRouteRedirectsRatherThanFailing`, `TestOlderEpochNeverReplacesNewer`, `TestRedirectChainIsBounded`, `TestClientLearnsFromOneFrontdoor`, `TestRedirectIsNotMistakableForAnAnswer`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Redirect`: the route the receiving node believes is correct, and nothing that could be read as data.
3. [S3] Implement `Cache`: a client's partial table, filled only by `Install`. It has no bulk load and no way to obtain one.
4. [S4] Implement `Install`, refusing a route whose epoch is not NEWER than the one already held for that prefix. ★Without this a client can install a stale route over a fresh one and stay wrong until something unrelated corrects it — and two nodes with opposing views redirect a client between them forever, each redirect looking exactly as authoritative as the last.
5. [S5] Implement `Resolve`: look up, follow redirects, install what is learned, and return the node to talk to. ★A stale route yields a redirect, never an error and never data. Refusing would make every topology change a fleet-wide outage; answering anyway would be silently wrong.
6. [S6] Give `Resolve` a hop budget and refuse with `ErrTooManyRedirects` naming the CHAIN. ★Epochs make a loop impossible in a correct cluster; the budget makes it bounded in an incorrect one, and the chain is what an operator needs to find the node that is lying.
7. [S7] Make a redirect structurally distinct from an answer, so no caller can treat one as the other. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/routing/... -race -run 'TestStaleRoute|TestOlderEpoch|TestRedirectChain|TestClientLearns|TestRedirectIsNot' -count=1 2>&1 | tee /tmp/adr008-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr008-t2a.out \
  && go test ./internal/core/routing/... ./internal/core/addr/... ./internal/core/placement/... -race -count=1 2>&1 | tee /tmp/adr008-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr008-t2b.out
```

The first command is this task's own work and can carry the verdict alone; the
second is the regression half over T1's table and the address and placement
packages this record builds on.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestStaleRouteRedirectsRatherThanFailing` | `internal/core/routing/redirect_test.go` | A client holding an out-of-date route reaches the right node, learns the new route, and neither errors nor is served by the wrong node. **The falsifier ADR-008 names in `Enforced-by:`** | — | S2, S5 |
| `TestOlderEpochNeverReplacesNewer` | `internal/core/routing/redirect_test.go` | Installing a route whose epoch is not newer leaves the held route in place, so a client cannot be dragged backwards by a stale redirect | — | S4 |
| `TestRedirectChainIsBounded` | `internal/core/routing/redirect_test.go` | A cluster whose nodes redirect in a cycle yields `ErrTooManyRedirects` naming the chain, rather than looping forever | — | S6 |
| `TestClientLearnsFromOneFrontdoor` | `internal/core/routing/redirect_test.go` | A cache starting with a single route reaches keys across the space and grows only by what it used, never loading a map | — | S3, S5 |
| `TestRedirectIsNotMistakableForAnAnswer` | `internal/core/routing/redirect_test.go` | A redirect and a resolved destination are distinct types, so a caller cannot treat "go elsewhere" as "here is your node" | — | S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | `Resolve` is the only path from a key to a node for a client, and `Install` the only way a cache grows; deleting the epoch check breaks `TestOlderEpochNeverReplacesNewer`. |
| 3 — the caller can discover it | `Resolve` returns a destination or an error and never a redirect, so its signature says a redirect is internal to resolution rather than something a caller handles. |
| 4 — it is used | Nothing measures this yet; no transport exists. |

## Mutation Log

- 2026-09-04 · bbb6744* · mutant inconclusive · exit 1 · `internal/core/routing/redirect.go` · serves the request from whichever node the client first tried, so a node that no longer holds the leaf answers anyway — silently wrong data rather than a redirect, which is the worst of the three things a stale route could do · acceptance-sha256:3e2cc9e11218117c7bd8e42eeae03ae2a4219947d0fa2c112405dbb850ab3428 · covers:a stale route producing a redirect rather than an error or an answer
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-04 · bbb6744* · mutant killed · exit 1 · `internal/core/routing/redirect.go` · accepts a route at the SAME epoch as the one held, so two nodes can flap a client between two routes forever with neither being newer and nothing able to break the tie · acceptance-sha256:3e2cc9e11218117c7bd8e42eeae03ae2a4219947d0fa2c112405dbb850ab3428 · covers:an older epoch never replacing a newer route
- 2026-09-04 · bbb6744* · mutant killed · exit 1 · `internal/core/routing/redirect.go` · returns a destination when the budget runs out instead of refusing, so a client in a redirect cycle silently settles on whichever node it happened to stop at rather than reporting that routing is broken · acceptance-sha256:3e2cc9e11218117c7bd8e42eeae03ae2a4219947d0fa2c112405dbb850ab3428 · covers:the hop budget bounding a redirect chain
- 2026-09-04 · bbb6744* · mutant killed · exit 1 · `internal/core/routing/redirect.go` · serves the request from whichever node the client first tried, so a node that no longer holds the leaf answers anyway — silently wrong data rather than a redirect, which is the worst of the three things a stale route could do · acceptance-sha256:3e2cc9e11218117c7bd8e42eeae03ae2a4219947d0fa2c112405dbb850ab3428 · covers:a stale route producing a redirect rather than an error or an answer

## Invariants

- A stale route yields a redirect, never an error and never data.
- A route is installed only if its epoch is strictly newer than the one held.
- A redirect chain is bounded, and exhausting the budget names the chain.
- A cache has no bulk load; it grows only through redirects it followed.
- `Resolve` never returns a `Redirect` to a caller.

## Risks

- ⚠ **A loop test that uses two nodes with the SAME epoch would pass under a broken epoch check.** `TestRedirectChainIsBounded` builds a cycle whose epochs increase, which is the case the budget exists for — a cycle that the epoch rule alone cannot stop.
- A "client never holds the map" claim is hard to falsify by behaviour, because a cache that happened to be full looks the same as one that loaded a map. The test asserts the cache's SIZE tracks what it used, which is the observable form of the claim; the structural form is that no bulk-load method exists.
- Epoch comparison is the kind of check that silently accepts equality. The test covers strictly-older, equal and strictly-newer, because accepting an equal epoch would let two nodes flap a client between two routes forever without either being newer.

## Stop Condition

Stop and ask if a redirect ever needs to carry data alongside the new route — a
partial result, a hint, a cached answer. It is a reasonable-sounding
optimisation and it destroys the property in rule 4: the moment a redirect can
carry an answer, a stale route can serve one.

## Out of Scope

- The transport, and how a redirect is carried on the wire (deferred: `docs/adr/BACKLOG.md` §18)
- How routes are distributed between nodes (deferred: `docs/adr/BACKLOG.md` §18)
- When a node may forget a route for a leaf it no longer serves (deferred: `docs/adr/BACKLOG.md` §18)
- Authenticating a redirect against a node that lies (permanent: boundary: a hostile node inside the cluster is a threat model this corpus has not taken on anywhere, and taking it on here alone would be incoherent)

## Verification Log
- 2026-09-04 · bbb6744* · exit 0 · `set -o pipefail …` · acceptance-sha256:3e2cc9e11218117c7bd8e42eeae03ae2a4219947d0fa2c112405dbb850ab3428 · ms:3510
- 2026-09-04 · bbb6744* · exit 0 · `set -o pipefail …` · acceptance-sha256:3e2cc9e11218117c7bd8e42eeae03ae2a4219947d0fa2c112405dbb850ab3428 · ms:3452
- 2026-09-04 · bbb6744* · exit 0 · `set -o pipefail …` · acceptance-sha256:3e2cc9e11218117c7bd8e42eeae03ae2a4219947d0fa2c112405dbb850ab3428 · ms:3435
- 2026-09-04 · bbb6744* · exit 0 · `set -o pipefail …` · acceptance-sha256:3e2cc9e11218117c7bd8e42eeae03ae2a4219947d0fa2c112405dbb850ab3428 · ms:3601
- 2026-09-04 · bbb6744* · exit 0 · `set -o pipefail …` · acceptance-sha256:3e2cc9e11218117c7bd8e42eeae03ae2a4219947d0fa2c112405dbb850ab3428 · ms:3473
