# ADR-047: Issuance is offline, rotation is per connection, and revocation is a datom

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-002-transaction-identity.md`, `docs/adr/ADR-016-tenant-prefix.md`, `docs/adr/ADR-033-grants-and-tenant-allocation.md`, `docs/adr/ADR-045-a-leaf-is-served-over-a-stream.md`, `docs/adr/ADR-046-a-certificate-names-who.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/certs/**`, `cmd/sdev1-ca/**`
**Enforced-by:** `internal/core/serve/deny_test.go::TestDenyingASerialReachesALiveConnection`
**Invalidates:** none — ADR-046 named issuance, rotation and revocation as its own Out of Scope and Consequences; this closes all three rather than contradicting any of them
**Served-path change:** A node picks up a replaced certificate without restarting, and a stolen certificate can be denied while its holder is mid-connection. Before this, rotating meant a restart and revoking an identity meant reissuing the CA.

## Context

ADR-046 made a certificate the caller's identity and then said, plainly, what it
had not built: *"An operator must run a CA. Certificates must be issued,
distributed and rotated, and this record provides none of that — it consumes
certificates and does not make them."* The README's own status table has carried
the row `Issuing, rotating or revoking a CERTIFICATE ❌` ever since, and `BACKLOG`
§18 names it as the thing that now gates exposure.

Three problems hide in that one row, and they have three different shapes.

### Issuance cannot be an endpoint, and that is a proof rather than a preference

★ **Online issuance over this transport is circular.** Every request to this
system is authenticated by a client certificate (ADR-046 rule 1). An endpoint that
issued certificates would have to authenticate the requester — with a certificate
the requester does not yet have. There is no bootstrap that does not either
weaken the transport for one path or invent a second credential, and the second
credential is the "token on top of mTLS" ADR-046 rejected for being a second
identity that can disagree with the first.

⚠ This is not a limitation to apologise for; it is how cluster PKI actually
bootstraps everywhere. The first credential arrives out of band, by definition.

### Rotation is the one with a live defect

`tls.LoadX509KeyPair` runs ONCE, inside `NewServer`. A replaced certificate on
disk is not noticed, so rotating means restarting every node — and an expiry
nobody watched takes the cluster down at a moment nothing chose.

### Revocation already half exists, and the missing half is smaller than a CRL

★ **ADR-046 made AUTHORITY revocable, and that works today**: a grant is a datom,
revocation is a retraction, and it reaches a caller mid-connection. A principal
whose grants are retracted can still connect and can read nothing.

What is left is the residual: a **stolen key**. Its holder still completes
handshakes, still consumes a node's resources, and — worse — is re-admitted the
moment anyone grants that principal name again for a legitimate reason.

⚠ **The obvious fix is an X.509 CRL or an OCSP responder, and both are the wrong
shape here.** They are a second distribution system with their own freshness
problem, in a system that already has a bitemporal, retractable, auditable fact
store with a reserved tenant for exactly this kind of statement. OCSP also puts a
network dependency inside every handshake, which is a new failure mode on the
read path bought to solve a problem the read path already knows how to express.

### The trap, and it is why this record has a falsifier at all

⚠ **The obvious place to check a revocation is the handshake.** It is cheap, it
is once per connection, and it refuses the caller before anything else runs.

It also silently undoes ADR-046. That record's central property is that a
revocation reaches a caller whose certificate and connection are both still
live — and ADR-046 amended ADR-045 so that connections are now POOLED and long-
lived. A deny list consulted only at the handshake would let a stolen certificate
keep reading over a connection it opened a second before the denial, for as long
as the pool holds it. The check that makes the property true has to be the
per-request one.

## Existing Primitives Audit

- `internal/core/serve` (ADR-045, ADR-046): supplies `TLSConfig`, `PrincipalOf`,
  the per-request decision point in `permits`. **Extended** — the certificate
  material becomes a live source rather than a snapshot, and the identity read off
  a connection grows a serial.
- `internal/core/authz` (ADR-033): **read, not modified.** A denial is not a
  grant, so it is not `authz`'s: grants are about what a principal may do, and
  this is about which certificate is still believable. ★ It uses the same reserved
  tenant, the same datom shape and the same "current only" reading, which is the
  part worth reusing.
- `internal/core/leafstore` (ADR-026): the reserved tenant's leaf. **Reused
  unchanged** — a denial is stored and read exactly as a grant is.
- `crypto/x509`, `crypto/ed25519` (stdlib): **used directly.** Ed25519 because it
  needs no curve or size decision, has no parameter an operator can get wrong, and
  produces small keys.
- An ACME client, `cert-manager`, Vault, or a SPIFFE workload API: **none.** Each
  assumes an online issuance path, which is the thing that cannot exist here yet.
  ⚠ Named because they are the right answer once a control plane exists (§19), and
  this record should not be read as arguing against them.
- A CRL or OCSP responder: **none.** See the Context and the Alternatives.

## Decision

**A certificate is issued offline by a command, re-read per connection, and
revoked by denying its SERIAL as a datom that is checked on every request.**

1. **Issuance is a command, never an endpoint.** `sdev1-ca` mints a CA and issues
   leaf certificates. ⚠ It runs wherever the CA key is kept, which is not a node:
   the CA private key never reaches the machines that serve.

2. ⚠ **`sdev1-ca` refuses to overwrite an existing CA key.** Silently replacing it
   invalidates every certificate ever issued from it, and the failure appears as
   an unrelated fleet-wide handshake outage some time later.

3. **Every issued certificate's serial is recorded** where the operator can read
   it. ★ A serial nobody wrote down cannot be denied, so rule 6 would be
   unreachable in practice without this.

4. **Certificate material is re-read PER CONNECTION**, through
   `tls.Config.GetConfigForClient` on a server and `GetClientCertificate` on a
   client. Rotation is therefore replacing a file; no restart, no signal.

5. ⚠ **A failed reload KEEPS THE LAST GOOD MATERIAL** and reports the failure. A
   half-written file or a typo must not take a node down — failing closed here
   turns a routine rotation into an outage, and the node that has been serving
   happily for a month has working material in hand.

6. ⚠ **A certificate that is ALREADY EXPIRED is refused at load**, not installed.
   That is a configuration error rather than a transient, and installing it means
   every handshake fails with an error about the peer.

7. **Revocation denies a SERIAL, as a datom in reserved tenant `0000`.** Not a
   CRL, not OCSP. The entity is the serial, the fact is that it is denied, and
   un-denying is a retraction — so the whole of ADR-033's machinery applies
   without deciding any of it again.

8. ⚠ **By SERIAL, never by principal.** A stolen key is ONE certificate. Denying
   the principal punishes the legitimate holder, blocks their reissuance, and
   conflates "this key is compromised" with "this person may no longer read" —
   which is what retracting a grant already says, better.

9. ⚠⚠ **THE DENIAL IS CHECKED ON EVERY REQUEST, not only at the handshake.** A
   handshake check may also exist and is a cheap early refusal; it must never be
   the only one. Connections are pooled and long-lived (ADR-046 rule 8), so a
   handshake-only check leaves a denied certificate reading over a connection it
   opened moments earlier.

10. ⚠ **A denial must be RETAINED until the certificate would have expired
    anyway.** Sweeping it earlier silently re-admits the certificate, and nothing
    about that looks like a change in access. The expiry is in the certificate, so
    the retention rule is derivable rather than guessed.

**What would falsify this.** A denied certificate continuing to read over a
connection that was open before the denial. That is the falsifier in
`Enforced-by:`, it is ADR-046's own falsifier pointed at identity rather than
authority, and it is exactly what a handshake-only deny list produces.

## Alternatives Considered

- **An X.509 CRL, distributed to nodes.** The standard answer, and it works.
  Rejected under rule 7: it is a second distribution mechanism with its own
  freshness question, in a system whose reserved tenant already carries
  bitemporal, retractable, auditable statements — and a CRL cannot answer "who
  denied this, and when" without a third thing.
- **An OCSP responder.** Fresher than a CRL. Rejected for the same reason plus a
  worse one: it puts a synchronous network dependency inside every handshake, so
  a responder outage becomes a cluster outage, on the read path, to solve a
  problem the read path can already express locally.
- **Short-lived certificates instead of revocation** (the SPIFFE answer: issue for
  an hour, never revoke). ★ Genuinely better, and it is where this should end up.
  Rejected NOW because it requires online issuance to renew, which rule 1 shows is
  circular until there is a control plane — see the Follow-ups.
- **Deny by principal rather than by serial.** Simpler, and it reuses the grant
  entity space. Rejected under rule 8: it cannot express "this key leaked, issue
  the same person another", and "this person may no longer read" is already a
  grant retraction that works.
- **Check the deny list only at the handshake.** Cheap, once per connection, and
  it refuses before anything else runs. ⚠ Rejected under rule 9 and it is the
  record's falsifier: pooled connections make it a hole rather than an
  optimisation.
- **Reload certificates on SIGHUP.** Conventional. Rejected under rule 4: a signal
  is a second mechanism to get right, it does not help when the file changed and
  nobody sent one, and per-connection reading is simpler than both.
- **Fail closed when a reload fails.** Defensible on security grounds. Rejected
  under rule 5: the material already in hand is valid, the new file is the thing
  in doubt, and a typo during a routine rotation would take down every node that
  read it.
- **An online issuance endpoint, authenticated by a bootstrap token.** Rejected
  under rule 1: the bootstrap token is a second identity that can disagree with
  the certificate, which is the thing ADR-046 rejected on the way in.

## Component / Boundary Impact

One new package, `internal/core/certs`, holding the parts that are about
certificates rather than about serving: minting, loading, and the denial datom.
One new command, `cmd/sdev1-ca`.

⚠ **`internal/core/authz` is NOT modified.** A denial is not a grant, and folding
it in would widen ADR-033 to mean "everything in the reserved tenant" rather than
"what a principal may do".

`internal/core/serve` gains a dependency on `certs` and keeps its existing
boundary: it still decides, and it still does not mint anything.

## Wiring & Contract Changes

| What | Change | Consumer |
|------|--------|----------|
| `certs.Authority`, `certs.Mint`, `certs.Issue` | new — offline issuance | `cmd/sdev1-ca` |
| `certs.Source` | new — reloadable certificate material | `serve` |
| `certs.DenyDatom`, `certs.AllowDatom`, `certs.Denied` | new — the denial fact and its reading | `serve` |
| `serve.TLSConfig` | now builds configs with per-connection callbacks | both ends |
| `serve.Identity` | new — principal AND serial off one connection | `serve` |
| `serve.ErrDeniedCertificate` | new sentinel | callers |
| `cmd/sdev1-ca` | new binary: `mint`, `issue`, `deny` | operators |
| Wire format | **unchanged.** No serial, no certificate and no denial travels in a frame; all of this is underneath ADR-043's envelope. |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `certs.Authority`, `certs.Issue` (T1) | T1 | T2, T3 | No — the test fixtures move onto it |
| `certs.Source` (T2) | T2 | T3 | No |

## Implementation

Three tasks, strictly in order. See
`docs/adr/ADR-047-certificate-lifecycle/tasks/README.md`.

## Consequences

- **Positive:** Rotation is replacing a file. The README's row stops saying ❌ and
  §18's exposure gate closes.
- **Positive:** Revocation reuses ADR-033 entirely — bitemporal, retractable, and
  "who denied this and when" is answerable, which a CRL cannot do alone.
- **Positive:** ⚠ The CA private key never reaches a node, so compromising a node
  does not let an attacker mint peers.
- **Negative:** ⚠ **The deny list is LOCAL to each node**, so a denial propagates
  as fast as whatever replicates the reserved tenant — which is nothing, until
  §19. This is the same limitation grants already have and it matters more here,
  because a denial is a response to a compromise.
- **Negative:** ⚠ **A denial must be retained until the certificate's own expiry**
  (rule 10). Nothing sweeps the reserved tenant today, so this is currently a rule
  nobody can break; it becomes a real obligation the moment §10's retention
  arrives.
- **Negative:** No automatic renewal. An operator runs `sdev1-ca` and copies
  files, and nothing here watches an expiry date for them.
- **Negative:** ⚠ **`sdev1-ca` writes a CA private key to disk unencrypted.**
  Protecting it is the operator's, and this record does not pretend otherwise.
- **Neutral:** Certificates are Ed25519 with a one-year default life for a CA and
  ninety days for a leaf. Both are conventions, not decisions this record defends.

## Out of Scope

- Automatic renewal, and watching an expiry date (deferred: `docs/adr/BACKLOG.md` §18)
- Short-lived certificates that need no revocation (deferred: `docs/adr/BACKLOG.md` §19 — it needs online issuance, which needs a control plane)
- Replicating the reserved tenant, so a denial reaches every node (deferred: `docs/adr/BACKLOG.md` §19)
- Sweeping expired denials (deferred: `docs/adr/BACKLOG.md` §10 — retention owns it, and rule 10 is the constraint it must honour)
- Encrypting the CA private key at rest (deferred: `docs/adr/BACKLOG.md` §18)
- An online issuance endpoint (permanent: boundary: rule 1 — authenticating a certificate request requires a certificate, so it cannot bootstrap itself without a second credential ADR-046 rejected)
- A CRL or OCSP responder (permanent: boundary: rule 7 — a second distribution system for statements the reserved tenant already carries better)
- Intermediate CAs and a chain longer than two (permanent: boundary: one CA and one leaf is what a cluster of this shape needs; depth is an operator's decision to bring, not this record's to invent)
- Certificate transparency, name constraints, key usage beyond client and server auth (deferred: `docs/adr/BACKLOG.md` §18)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The deny list is checked only at the handshake | **High — it is the obvious place, it is cheaper, and it looks complete** | **Critical** — a stolen certificate keeps reading over a pooled connection for as long as the pool holds it, and ADR-046's central property is silently undone | Rule 9, and it is this record's falsifier |
| Revocation is by principal because it is easier to write | Med | High — a leaked key then cannot be replaced without denying the person, and "this key is compromised" gets conflated with "this person may no longer read" | Rule 8; the deny entity is a serial |
| A failed reload takes the node down | **Med — failing closed feels like the secure choice** | High — a typo during a routine rotation becomes a fleet outage, when valid material was already in hand | Rule 5, with a test that corrupts the file and asserts the node keeps serving |
| A reload silently installs an expired certificate | Med | High — every subsequent handshake fails with an error about the PEER, so the diagnosis points at the wrong machine | Rule 6, refused at load |
| `sdev1-ca` overwrites an existing CA key | Med — it is a second run of the same command | **Critical** — every certificate ever issued is invalidated, and the outage appears later and elsewhere | Rule 2, refused |
| A denial is swept before its certificate expires | Low today, certain once retention exists | **Critical** — the certificate is re-admitted, and nothing about it looks like a change in access | Rule 10, stated as an obligation on §10 |
| The CA key is kept on a node "for convenience" | Med | **Critical** — compromising one node lets an attacker mint peers for the whole cluster | Rule 1 and the Consequences; `sdev1-ca` is a separate binary precisely so it need not be deployed |

## Rollback

Reverting returns nodes to loading certificates once at startup and to having no
denial mechanism. ⚠ **Certificates issued by `sdev1-ca` keep working** — they are
ordinary X.509 and nothing about them depends on this record — so a rollback costs
rotation and revocation, not connectivity. Denial datoms already written become
inert facts in the reserved tenant rather than errors; nothing reads them and
nothing breaks.

## Follow-ups

- [ ] Automatic renewal, and something that watches an expiry before it stops a node (`BACKLOG.md` §18). ⚠ Today an operator must diary it.
- [ ] Replicate the reserved tenant so a denial reaches every node (`BACKLOG.md` §19). A denial that reaches one node out of five is a partial revocation, which is worse than it sounds.
- [ ] Revisit short-lived certificates once a control plane can issue online (`BACKLOG.md` §19). ★ That design needs no revocation at all, which is strictly better than doing revocation well.
- [ ] When retention arrives (`BACKLOG.md` §10), make rule 10 something the sweeper enforces rather than something a reader must remember.
