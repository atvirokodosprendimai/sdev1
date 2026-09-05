# Task ADR-046-T3: The grant set decides, and it is read at the present

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `serve.ErrNoGrants`, `serve.ErrSystemTenant`, `serve.Options.Grants`
**Consumes:** `serve.PrincipalOf` (T1); `serve.Pool` (T2); `authz.Load`, `authz.Set.Allow`, `authz.Read`, `authz.SystemTenant`, `authz.GrantDatom`, `authz.RevokeDatom` from ADR-033; `addr.TenantOf` from ADR-016
**Data dependency:** hermetic — a real grant leaf and a real data leaf, both in `t.TempDir()`
**Proof map:** v1
**Rests-on:** `a retraction reaching a caller whose certificate and connection are both still live`, `an unconfigured grant source refusing rather than permitting`, `the reserved system tenant being unreadable over the wire`

## Goal

Close ADR-033's one deferral — the caller identity — and prove that authority
lives in the grant set rather than anywhere the connection remembers.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/serve/authn.go` | add | The per-request decision: tenant from the key, principal from the certificate, `Set.Allow`. |
| `internal/core/serve/authn_test.go` | add | The tests below, including this ADR's falsifier. |
| `internal/core/serve/server.go` | modify | `Options.Grants`; refuse construction without it; authorize before evaluating. |
| `internal/core/serve/doc.go` | modify | Why the decision takes no instant, and why an unconfigured node refuses. |
| `cmd/sdev1-serve/main.go` | modify | `--grants`, required. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestRevocationReachesALiveCertificate`, `TestAnUngrantedPrincipalIsRefused`, `TestANodeWithoutGrantsRefusesEveryRead`, `TestTheSystemTenantIsNotReadableOverTheWire`, `TestAPastQueryIsAuthorizedByThePresent`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Refuse `NewServer` without a `Grants` reader, with `ErrNoGrants`. ⚠Fail closed at construction. ADR-033 rule 5's dangerous reading is that an unconfigured grant store is a special case; it is exactly the case where a system fails open. [proof: mutation]
3. [S3] Refuse a request whose key names `authz.SystemTenant`, with `ErrSystemTenant`, BEFORE any store is touched. ⚠`Set.Allow` refusing that tenant does not cover this: reading the grant leaf is an ordinary read, so a node holding it would serve the grant table through the ordinary path. [proof: mutation]
4. [S4] Authorize: `addr.TenantOf(req.Key)` for the tenant, `PrincipalOf` for the principal, `authz.Load(ctx, grants, principal, at)` then `Set.Allow(tenant, authz.Read)`. ★`Allow` takes no instant, so the request's `Now` reaches the evaluator and never the decision. [proof: mutation]
5. [S5] Return a `wire.Refusal` naming the refusal. ⚠Not an empty answer — a caller reading zero rows would conclude it was permitted and the data was absent, which is a worse answer than "no". [proof: mutation]
6. [S6] Add `--grants` to `cmd/sdev1-serve`, required, and update the two-process binary test to pass it. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/serve/... -race -run 'TestRevocationReachesALiveCertificate|TestAnUngrantedPrincipalIsRefused|TestANodeWithoutGrantsRefusesEveryRead|TestTheSystemTenantIsNotReadableOverTheWire|TestAPastQueryIsAuthorizedByThePresent' -count=1 2>&1 | tee /tmp/adr046-t3a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr046-t3a.out \
  && go build -o /dev/null ./cmd/sdev1-serve \
  && go test ./internal/core/serve/... ./internal/core/authz/... ./internal/core/addr/... ./internal/core/wire/... -race -count=1 2>&1 | tee /tmp/adr046-t3b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr046-t3b.out
```

`authz` is in the second command because this task claims to consume ADR-033
unchanged; if that record's own tests broke, the claim would be false.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestRevocationReachesALiveCertificate` | `internal/core/serve/authn_test.go` | **This ADR's falsifier.** A granted principal reads; the grant is retracted; the SAME client reads again **on the pooled connection** and is refused — certificate unchanged, connection never closed. ★ The pooled reuse is the whole test: a fresh dial would pass even if authority were established at handshake time, which is the design being ruled out | — | S4 |
| `TestAnUngrantedPrincipalIsRefused` | `internal/core/serve/authn_test.go` | A principal with a valid certificate and no grant gets a `wire.Refusal`, not an answer and not an empty one. ⚠ Asserted on the response SHAPE — an empty answer would read as "permitted, nothing matched" | — | S4, S5 |
| `TestANodeWithoutGrantsRefusesEveryRead` | `internal/core/serve/authn_test.go` | `NewServer` without `Grants` is `ErrNoGrants`. ★ Refused at construction, so there is no running node in this state to test the request path of | — | S2 |
| `TestTheSystemTenantIsNotReadableOverTheWire` | `internal/core/serve/authn_test.go` | A request whose key is in tenant `0000` is refused even against a node that HOLDS that leaf and whose caller is granted everything else. ⚠ The fixture must actually place the grant leaf on the serving node, or the test passes for the wrong reason — because the leaf was absent rather than because it was refused | — | S3 |
| `TestAPastQueryIsAuthorizedByThePresent` | `internal/core/serve/authn_test.go` | A read `AS OF` an instant when the grant WAS live, made after revocation, is refused. ★ ADR-033 rule 3 over the wire: the tempting symmetry is to authorize the past against the past, and it makes revocation unable to reach backwards | — | S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests, over a real grant leaf and a real data leaf. |
| 2 — something selects it | Every served read passes through the decision; there is no path around it. |
| 3 — the caller can discover it | Two named sentinels, and `Options` still has no usable zero. |
| 4 — it is used | `cmd/sdev1-serve --grants` is required, and the two-process binary test passes it. |

## Mutation Log

## Invariants

- The decision takes no instant.
- No grant means refused; no grant SOURCE means refused.
- Tenant `0000` is never served.
- A refusal is a `wire.Refusal`, never an empty answer.

## Risks

- ⚠ **The falsifier must reuse the connection.** Revoke-then-redial passes against a design that caches authority per connection, which is the design being excluded. Read, revoke, read again — same client, same pool.
- ⚠ **`TestTheSystemTenantIsNotReadableOverTheWire` can pass for the wrong reason.** If the serving node does not hold the grant leaf, the read fails because there is nothing there. Place the grant leaf on that node so the refusal is the only explanation.
- ⚠ **Loading grants must not itself be authorized.** It is the node reading its own configuration, not a caller reading data, and `Set.Allow` refuses tenant `0000` unconditionally — so routing the grant read through the request path would deadlock the design against itself.
- ⚠ **Passing the request's `Now` to `Set.Allow` is impossible by signature and easy in spirit** — loading the set at the request's instant instead of the present has the same effect. `authz.Load`'s snapshot must be the present, not `req.Now`.
- A `Load` per request is a real cost with no number. §16.

## Stop Condition

Stop and ask before authorizing a WRITE. ADR-045 refuses a served write by name,
so there is no write path to authorize, and `authz.Write` existing is not a reason
to reach for it — wiring it would mean the refusal had been removed somewhere.

## Out of Scope

- Authorizing a write (permanent: boundary: ADR-045 refuses a served write by name, so there is no path to authorize)
- Replicating the grant leaf between nodes (deferred: `docs/adr/BACKLOG.md` §19)
- Who allocates a tenant identifier (deferred: `docs/adr/BACKLOG.md` §11)
- Authorizing the agent tool surface and the filesystem projection (deferred: `docs/adr/BACKLOG.md` §11)
- Measuring the per-request grant load (deferred: `docs/adr/BACKLOG.md` §16)

## Verification Log
