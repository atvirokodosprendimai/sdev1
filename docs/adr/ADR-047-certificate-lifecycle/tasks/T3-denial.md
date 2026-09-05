# Task ADR-047-T3: A denied serial stops a certificate mid-connection

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `certs.DenyDatom`, `certs.AllowDatom`, `certs.Denied`, `certs.SerialOf`, `certs.ParseSerial`, `certs.Inspect`, `certs.Entity`, `serve.Identity`, `serve.IdentityOf`, `serve.ErrDeniedCertificate`
**Consumes:** `certs.Issue` (T1); `certs.Source` (T2); `serve.PrincipalOf` and the per-request decision from ADR-046; `authz.SystemTenant`, `ports.Carried` from ADR-033
**Data dependency:** hermetic — a real reserved-tenant leaf in `t.TempDir()`
**Proof map:** v1
**Rests-on:** `a denial reaching a request on a connection opened before it`, `a denial naming a serial rather than a principal`, `a denied serial being retained until the certificate expires`, `a denial living in its own entity space`

⚠ **The fourth mechanism was added AFTER a mutant survived**, and the survival is
worth keeping in view. Dropping the `cert:` entity prefix round-trips perfectly —
writing and reading go through the same function — so every test here passed with
a denial filed under the bare serial, sharing a namespace with ordinary entities.
★ It is the class of property that only shows up when something OTHER than this
API writes, and no round-trip test can see it.

⚠ **And the sharpest form of the FIRST mechanism has no mutant.** Moving the
denial check to the handshake needs `server.go` edited, which does not import
`certs`, so it is a two-file change — and a mutant is one contiguous edit to one
file. The mutant that runs consults the denial set per request and ignores the
answer, which has the same observable consequence on the falsifier.

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
go test ./internal/core/certs/... ./internal/core/serve/... -race -run 'TestDenyingASerialReachesALiveConnection|TestADeniedCertificateIsRefusedEvenWithAGrant|TestTheSerialComesFromTheVerifiedChain|TestADenialNamesASerialNotAPrincipal|TestUndenyingIsARetraction|TestADenialOutlivesTheCertificateItDenies|TestASerialIsReadTheSameWayItIsWritten|TestADenialLivesInItsOwnEntitySpace' -count=1 2>&1 | tee /tmp/adr047-t3a.out \
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
| `TestADeniedCertificateIsRefusedEvenWithAGrant` | `internal/core/serve/deny_test.go` | A denied certificate is refused although its principal's grant is untouched, with `ErrDeniedCertificate` rather than `ErrNotPermitted`. ★ The positive control is the point of the whole design: the SAME principal's replacement certificate reads perfectly well, because a leaked key is one certificate | — | S2, S5 |
| `TestTheSerialComesFromTheVerifiedChain` | `internal/core/serve/deny_test.go` | `SerialOf` reads `VerifiedChains` and refuses a state carrying only `PeerCertificates`. ⚠ The second is what the peer SENT — a serial taken from it is a serial the peer chose, so denying it would deny nothing, and the hole would be invisible because every honest caller sends the same value. `IdentityOf` takes both fields off ONE chain so they cannot disagree | — | S4 |
| `TestASerialIsReadTheSameWayItIsWritten` | `internal/core/certs/deny_test.go` | `ParseSerial` normalises case, separators and leading zeroes to the one spelling `FormatSerial` writes. ⚠ A denial written `AB:CD` and looked up as `abcd` denies nothing, silently, because both sides look right — and the operator is then certain a key is revoked when it is not | — | S2 |
| `TestADenialNamesASerialNotAPrincipal` | `internal/core/certs/deny_test.go` | Denying one certificate does not deny a SECOND certificate issued to the same principal. ★ That is the whole reason for denying by serial: a leaked key is one certificate, and its holder must be able to carry on with a new one | — | S2, S4 |
| `TestUndenyingIsARetraction` | `internal/core/certs/deny_test.go` | `AllowDatom` produces a retraction, the serial leaves the current denial set, and the history still shows the denial happened. ⚠ Asserted on `Assert: false`, not on absence — a deletion would also make it leave the set, and would lose the fact that it was ever denied | — | S3 |
| `TestADenialOutlivesTheCertificateItDenies` | `internal/core/certs/deny_test.go` | A denial's validity ends at the certificate's own `NotAfter` and not before. ⚠ A denial swept early silently RE-ADMITS the certificate, and nothing about that looks like a change in access | — | S6 |
| `TestADenialLivesInItsOwnEntitySpace` | `internal/core/certs/deny_test.go` | A datom filed under the BARE serial does not deny the certificate — the denial entity carries a `cert:` prefix. ⚠ **Added after a mutant survived.** Dropping the prefix round-trips perfectly, because writing and reading share a function, so every other test passed with denials sharing a namespace with ordinary entities. The denial set would then be a function of whatever else is in the reserved tenant | — | S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests, over a real reserved-tenant leaf and a real pooled connection. |
| 2 — something selects it | Every served request passes `permits`, which now consults the denial set; there is no path around it. |
| 3 — the caller can discover it | `ErrDeniedCertificate`, and `sdev1-ca deny --help`. |
| 4 — it is used | `cmd/sdev1-ca deny` writes the datom a node reads, and the falsifier drives the whole path. |

## Mutation Log

- 2026-09-05 · 93c1920* · mutant inconclusive · exit 1 · `internal/core/serve/server.go` · Move the denial check to the HANDSHAKE — check it once per connection and stop carrying the serial per request. ★★ This is the implementation anyone would write: it is cheaper, it refuses before anything else runs, and it is correct for every caller who connects AFTER a denial. It passes the datom tests, the serial-spelling test, the verified-chain test and the grant tests untouched. What it destroys is the one case that matters — ADR-046 rule 8 made connections pooled and long-lived, so a stolen certificate keeps reading over the connection it opened moments before the denial, for as long as the pool holds it. The revocation reports success and stops nothing. · acceptance-sha256:dfe1e4d8654501455ada9350c153eeda90f947e701d2217d2cfe7e9842a38efc · covers:a denial reaching a request on a connection opened before it
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-05 · 93c1920* · mutant inconclusive · exit 1 · `internal/core/serve/authn.go` · Remove the PER-REQUEST denial check. ⚠ This is the achievable one-file form of the defect this record exists to rule out — the real one moves the check to the handshake, which needs a second file edited (server.go does not import certs) and is therefore not expressible as a mutant. Both have the same observable consequence on the falsifier: a stolen certificate keeps reading over a connection opened before the denial, for as long as ADR-046 rule 8 keeps that connection pooled. Everything else stays green — the datom tests, the serial spelling, the verified chain and every grant test — because the denial is still written, still stored, still readable, and still correct for anyone who connects afterwards. · acceptance-sha256:dfe1e4d8654501455ada9350c153eeda90f947e701d2217d2cfe7e9842a38efc · covers:a denial reaching a request on a connection opened before it
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-05 · 93c1920* · mutant killed · exit 1 · `internal/core/serve/authn.go` · Consult the denial set on every request and do not act on the answer — the shape a gate takes when it is refactored into a log line, or when someone silences an error return in a hurry. ⚠ It is also the achievable one-file form of the defect this record exists to rule out: the real one moves the check to the HANDSHAKE, which needs a second file edited (server.go does not import certs) and so is not expressible as a mutant. Both have the same consequence on the falsifier — a stolen certificate keeps reading over a connection opened before the denial, for as long as ADR-046 rule 8 keeps it pooled. Everything else stays green: the denial is still written, stored, readable, and correct for anyone who connects afterwards. · acceptance-sha256:dfe1e4d8654501455ada9350c153eeda90f947e701d2217d2cfe7e9842a38efc · covers:a denial reaching a request on a connection opened before it
- 2026-09-05 · 93c1920* · mutant survived · exit 0 · `internal/core/certs/deny.go` · Drop the entity prefix, so a denial is filed under the bare serial. ★ Nothing breaks: writing and reading use the same function, so every denial test round-trips perfectly and the falsifier still passes. What is lost is the separation between entity spaces — a denial now shares a namespace with ordinary entities, so any datom somebody stores under a forty-hex-character name lands on the denial attribute, and the denial set becomes a function of what else happens to be in the reserved tenant. It is a property no test that uses only this API can see, which is why the prefix is a constant rather than an assumption. · acceptance-sha256:dfe1e4d8654501455ada9350c153eeda90f947e701d2217d2cfe7e9842a38efc · covers:a denial naming a serial rather than a principal
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-05 · 93c1920* · mutant killed · exit 1 · `internal/core/serve/authn.go` · Consult the denial set on every request and do not act on the answer — the shape a gate takes when it is refactored into a log line, or when someone silences an error return in a hurry. ⚠ It is also the achievable one-file form of the defect this record exists to rule out: the real one moves the check to the HANDSHAKE, which needs server.go edited and that file does not import certs. Both have the same consequence on the falsifier — a stolen certificate keeps reading over a connection opened before the denial, for as long as ADR-046 rule 8 keeps it pooled. Everything else stays green: the denial is still written, stored, readable, and correct for anyone who connects afterwards. · acceptance-sha256:9d3010831719704983cdcc64fcad8e674910ccd5acbf42225d61482a34aa549f · covers:a denial reaching a request on a connection opened before it
- 2026-09-05 · 93c1920* · mutant killed · exit 1 · `internal/core/certs/deny.go` · Drop the entity prefix, so a denial is filed under the bare serial. ★ Nothing round-trips wrong: writing and reading go through the same function, so every denial test passed and the falsifier passed — this mutant SURVIVED on its first run. What is lost is the separation between entity spaces: a denial then shares a namespace with ordinary entities, so any datom stored under a forty-hex-character name lands on the denial attribute, and the denial set becomes a function of whatever else is in the reserved tenant. It is killed only by a test that writes something the denial API did not, which is the class of property no round-trip can see. · acceptance-sha256:9d3010831719704983cdcc64fcad8e674910ccd5acbf42225d61482a34aa549f · covers:a denial living in its own entity space
- 2026-09-05 · 93c1920* · mutant killed · exit 1 · `internal/core/serve/authn.go` · Look the denial up by PRINCIPAL instead of by serial — the change someone makes when "revoke this identity" is read as "revoke this person". ⚠ It reads as a simplification: one name instead of two, and it lines up with how grants are keyed. What it produces is a revocation that cannot express the case it exists for. A leaked key is ONE certificate; denying the name blocks the legitimate holder from carrying on with a replacement, and "this person may no longer read" is a grant retraction that already says it better and with a proper audit trail. Every denial written by serial then matches nothing, so the compromised certificate keeps working. · acceptance-sha256:9d3010831719704983cdcc64fcad8e674910ccd5acbf42225d61482a34aa549f · covers:a denial naming a serial rather than a principal
- 2026-09-05 · 93c1920* · mutant killed · exit 1 · `internal/core/certs/deny.go` · Give a denial a fixed twenty-four-hour life "so the reserved tenant does not fill up with dead facts". ★ Every immediate test still passes — the certificate is denied now, the retraction still works, the entity is still right — and the concern is even real, because a denial that ran forever WOULD accumulate. What it does is silently RE-ADMIT the certificate tomorrow. Nothing logs it, nothing alarms, and access returning is indistinguishable from access never having been removed. The certificate carries its own expiry, so the retention rule is derivable; a horizon anyone picks by hand is a horizon that is wrong for every certificate but one. · acceptance-sha256:9d3010831719704983cdcc64fcad8e674910ccd5acbf42225d61482a34aa549f · covers:a denied serial being retained until the certificate expires

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
- 2026-09-05 · 93c1920* · exit 0 · `set -o pipefail …` · acceptance-sha256:dfe1e4d8654501455ada9350c153eeda90f947e701d2217d2cfe7e9842a38efc · ms:7781
- 2026-09-05 · 93c1920* · exit 0 · `set -o pipefail …` · acceptance-sha256:dfe1e4d8654501455ada9350c153eeda90f947e701d2217d2cfe7e9842a38efc · ms:7642
- 2026-09-05 · 93c1920* · exit 0 · `set -o pipefail …` · acceptance-sha256:dfe1e4d8654501455ada9350c153eeda90f947e701d2217d2cfe7e9842a38efc · ms:7642
- 2026-09-05 · 93c1920* · exit 0 · `set -o pipefail …` · acceptance-sha256:dfe1e4d8654501455ada9350c153eeda90f947e701d2217d2cfe7e9842a38efc · ms:7623
- 2026-09-05 · 93c1920* · exit 0 · `set -o pipefail …` · acceptance-sha256:dfe1e4d8654501455ada9350c153eeda90f947e701d2217d2cfe7e9842a38efc · ms:7501
- 2026-09-05 · 93c1920* · exit 0 · `set -o pipefail …` · acceptance-sha256:9d3010831719704983cdcc64fcad8e674910ccd5acbf42225d61482a34aa549f · ms:7936
- 2026-09-05 · 93c1920* · exit 0 · `set -o pipefail …` · acceptance-sha256:9d3010831719704983cdcc64fcad8e674910ccd5acbf42225d61482a34aa549f · ms:7380
- 2026-09-05 · 93c1920* · exit 0 · `set -o pipefail …` · acceptance-sha256:9d3010831719704983cdcc64fcad8e674910ccd5acbf42225d61482a34aa549f · ms:7549
- 2026-09-05 · 93c1920* · exit 0 · `set -o pipefail …` · acceptance-sha256:9d3010831719704983cdcc64fcad8e674910ccd5acbf42225d61482a34aa549f · ms:7679
- 2026-09-05 · 93c1920* · exit 0 · `set -o pipefail …` · acceptance-sha256:9d3010831719704983cdcc64fcad8e674910ccd5acbf42225d61482a34aa549f · ms:7764
