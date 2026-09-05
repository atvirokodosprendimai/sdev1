# Task ADR-047-T3: A denied serial stops a certificate mid-connection

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `certs.DenyDatom`, `certs.AllowDatom`, `certs.Denied`, `certs.SerialOf`, `serve.Identity`, `serve.ErrDeniedCertificate`
**Consumes:** `certs.Issue` (T1); `certs.Source` (T2); `serve.PrincipalOf` and the per-request decision from ADR-046; `authz.SystemTenant`, `ports.Carried` from ADR-033
**Data dependency:** hermetic — a real reserved-tenant leaf in `t.TempDir()`
**Proof map:** v1
**Rests-on:** `a denial reaching a request on a connection opened before it`, `a denial naming a serial rather than a principal`, `a denied serial being retained until the certificate expires`

## Goal

Make a stolen certificate stoppable without a CRL, and make the stop reach a
caller who is already connected.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/certs/deny.go` | add | The denial datom, and reading the current denial set. |
| `internal/core/certs/deny_test.go` | add | The datom-level tests. |
| `internal/core/serve/deny_test.go` | add | The falsifier, over a real pooled connection. |
| `internal/core/serve/tls.go` | modify | `PrincipalOf` becomes `IdentityOf`: principal AND serial off one connection. |
| `internal/core/serve/authn.go` | modify | The per-request denial check, beside the grant check. |
| `internal/core/serve/server.go` | modify | Carry the identity rather than the principal. |
| `cmd/sdev1-ca/main.go` | modify | `deny` and `allow`, writing the datoms. |
| `internal/core/certs/doc.go` | modify | Why a denial is a datom and why it is checked per request. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestDenyingASerialReachesALiveConnection`, `TestADenialNamesASerialNotAPrincipal`, `TestUndenyingIsARetraction`, `TestADenialOutlivesTheCertificateItDenies`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] `DenyDatom(serial, until, reason, id, from)` — entity is the serial, in reserved tenant `0000`, valid from `from` to the certificate's own expiry. ★An ordinary datom: bitemporal, retractable, and "who denied this and when" is answerable without a second mechanism. [proof: mutation]
3. [S3] `AllowDatom` is the RETRACTION, so un-denying is what ADR-033 already made revocation: a fact that stopped applying, not a fact that never was. [proof: mutation]
4. [S4] `IdentityOf(*tls.ConnectionState)` returns principal AND serial from the VERIFIED chain's leaf. ⚠From `VerifiedChains`, for the same reason ADR-046 reads the principal there — a serial taken from what the peer SENT is a serial the peer chose. [proof: mutation]
5. [S5] ⚠⚠Check the denial ON EVERY REQUEST, in `permits`, beside the grant check. A handshake check may exist as a cheap early refusal and MUST NOT be the only one: connections are pooled and long-lived (ADR-046 rule 8), so a handshake-only check leaves a denied certificate reading over a connection it opened moments before. [proof: mutation]
6. [S6] ⚠The denial's validity runs to the CERTIFICATE'S EXPIRY, not forever and not to a fixed horizon. Sweeping it earlier re-admits the certificate; running it forever fills the reserved tenant with facts about certificates that cannot be presented. [proof: mutation]
7. [S7] Add `deny` and `allow` to `cmd/sdev1-ca`. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/certs/... ./internal/core/serve/... -race -run 'TestDenyingASerialReachesALiveConnection|TestADenialNamesASerialNotAPrincipal|TestUndenyingIsARetraction|TestADenialOutlivesTheCertificateItDenies' -count=1 2>&1 | tee /tmp/adr047-t3a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr047-t3a.out \
  && go build -o /dev/null ./cmd/sdev1-ca \
  && go test ./internal/core/certs/... ./internal/core/serve/... ./internal/core/authz/... -race -count=1 2>&1 | tee /tmp/adr047-t3b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr047-t3b.out
```

⚠ The `-run` filter spans TWO packages, so both are named in the first command;
`go test` applies the filter to each, and the `no tests to run` grep is what
catches a name that matched in neither.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestDenyingASerialReachesALiveConnection` | `internal/core/serve/deny_test.go` | **This record's falsifier.** A client reads successfully, its certificate's serial is denied, and the SAME client reading over the SAME pooled connection is refused — with `Server.Accepted` unmoved, so no new handshake re-checked anything. ⚠ A handshake-only deny list passes every other test in this task and fails only this one | — | S5 |
| `TestADenialNamesASerialNotAPrincipal` | `internal/core/certs/deny_test.go` | Denying one certificate does not deny a SECOND certificate issued to the same principal. ★ That is the whole reason for denying by serial: a leaked key is one certificate, and its holder must be able to carry on with a new one | — | S2, S4 |
| `TestUndenyingIsARetraction` | `internal/core/certs/deny_test.go` | `AllowDatom` produces a retraction, the serial leaves the current denial set, and the history still shows the denial happened. ⚠ Asserted on `Assert: false`, not on absence — a deletion would also make it leave the set, and would lose the fact that it was ever denied | — | S3 |
| `TestADenialOutlivesTheCertificateItDenies` | `internal/core/certs/deny_test.go` | A denial's validity ends at the certificate's own `NotAfter` and not before. ⚠ A denial swept early silently RE-ADMITS the certificate, and nothing about that looks like a change in access | — | S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests, over a real reserved-tenant leaf and a real pooled connection. |
| 2 — something selects it | Every served request passes `permits`, which now consults the denial set; there is no path around it. |
| 3 — the caller can discover it | `ErrDeniedCertificate`, and `sdev1-ca deny --help`. |
| 4 — it is used | `cmd/sdev1-ca deny` writes the datom a node reads, and the falsifier drives the whole path. |

## Mutation Log

## Invariants

- A denial is checked on every request, not only at the handshake.
- A denial names a serial.
- Un-denying is a retraction.
- A denial's validity ends at the certificate's expiry.

## Risks

- ⚠ **Checking only at the handshake is the natural implementation and it is the defect.** It is cheaper, it is once per connection, and every test except the falsifier passes. Pooled connections are what turn it from an optimisation into a hole.
- ⚠ **A serial read from `PeerCertificates` is a serial the peer chose.** Use `VerifiedChains`, exactly as ADR-046 does for the principal, and for the same reason.
- ⚠ **Serial formatting must be canonical.** `big.Int` renders differently depending on how it is asked; a denial written as one spelling and read as another denies nothing, and the failure is silent. Fix one representation and use it in both directions.
- ⚠ **The denial set is read per request, like the grant set.** That is a second `Load` per read. Real, unmeasured, and §16's — but say it rather than discover it.
- ⚠ **A denial is only as fast as whatever replicates the reserved tenant**, which is nothing yet (§19). A denial that reaches one node in five is a partial revocation, and partial is worse than it sounds because it looks complete from the node that has it.

## Stop Condition

Stop and ask before denying by principal, or before removing the per-request
check in favour of a handshake one. The first conflates a compromised key with a
withdrawn permission, which grant retraction already handles better; the second is
this record's falsifier.

## Out of Scope

- Replicating denials between nodes (deferred: `docs/adr/BACKLOG.md` §19)
- Sweeping expired denials (deferred: `docs/adr/BACKLOG.md` §10 — rule 10 is the constraint retention must honour)
- A CRL or OCSP responder (permanent: boundary: ADR-047 rule 7)
- Short-lived certificates that need no revocation (deferred: `docs/adr/BACKLOG.md` §19)
- Denying an authority wholesale, rather than a leaf (deferred: `docs/adr/BACKLOG.md` §18 — retiring a CA is T2's pool rotation, and a denial per issued leaf is not how anyone would want to do it)

## Verification Log
