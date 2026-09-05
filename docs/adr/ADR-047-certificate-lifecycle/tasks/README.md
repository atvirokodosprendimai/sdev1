# ADR-047 Tasks

Implementation tasks for ADR-047: issuance is offline, rotation is per
connection, and revocation is a datom. See the parent ADR for the decision.

**Source of truth:** the task files' headers. This README is a derived index —
when it disagrees with a task file, the task file wins.

## Execution Order

Three tasks, strictly in order.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T2 |

T1 makes the certificates the other two rotate and deny. T3 needs T2 only in the
sense that both edit the same TLS construction; its falsifier needs ADR-046's
connection pool, which already exists.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | A command mints the authority and issues the certificates | done | — | four issuance tests, a `cmd/sdev1-ca` build, then two suites |
| T2 | A replaced certificate is picked up without a restart | pending | — | four rotation tests over real listeners, then two suites |
| T3 | A denied serial stops a certificate mid-connection | pending | — | four tests including the falsifier, a build, then three suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `certs.Authority`, `certs.Mint`, `certs.Issue` | T2, T3 | both replace hand-rolled test authorities with it |
| T2 | `certs.Source` | T3 | both reach the same TLS construction; T2 gets there first |

## Notes

- ★★ **ISSUANCE CANNOT BE AN ENDPOINT, and that is a proof rather than a
  preference.** Every request here is authenticated by a client certificate
  (ADR-046 rule 1), so an issuance endpoint would have to authenticate a requester
  that does not have one yet. Every version either weakens the transport for one
  path or invents a bootstrap token — the second identity ADR-046 rejected on the
  way in. ⚠ This is not a gap to apologise for: the first credential arrives out
  of band everywhere, by definition.
- ★★ **THE FALSIFIER IS ABOUT WHERE THE DENIAL IS CHECKED.** The obvious place is
  the handshake: cheap, once per connection, refuses before anything else runs. It
  also silently undoes ADR-046, whose central property is that a revocation
  reaches a caller whose connection is still live — and whose rule 8 made
  connections POOLED and long-lived. A handshake-only check lets a stolen
  certificate keep reading for as long as the pool holds its connection. The
  per-request check is the mechanism; a handshake check is an optimisation on top.
- ★ **REVOCATION IS HALF-BUILT ALREADY.** ADR-033 made *authority* revocable and
  ADR-046 made it reach a live connection — a principal whose grants are retracted
  can connect and read nothing. What was left is the **stolen key**: its holder
  still handshakes, still consumes resources, and is re-admitted the moment anyone
  grants that principal name again. That residual is what a denial addresses, and
  it is much smaller than a CRL.
- ⚠ **DENY BY SERIAL, NEVER BY PRINCIPAL.** A leaked key is ONE certificate.
  Denying the name punishes the legitimate holder, blocks their reissuance, and
  says "this person may no longer read" — which grant retraction already says,
  better and with a proper audit trail.
- ⚠ **A FAILED RELOAD KEEPS THE LAST GOOD MATERIAL.** Failing closed feels like
  the secure choice and it turns a routine rotation into a fleet outage: the file
  on disk is the thing in doubt, and the material already in hand has been working
  for a month. The test corrupts the file and asserts the node KEEPS SERVING —
  "an error was returned" would pass for an implementation that returned the error
  and dropped the certificate.
- ⚠ **AN EXPIRED CERTIFICATE IS REFUSED AT LOAD.** Installing one makes every
  handshake fail with an error naming the PEER, so the diagnosis points at the
  wrong machine entirely.
- ⚠ **A DENIAL MUST OUTLIVE NOTHING AND OUTLAST THE CERTIFICATE.** Its validity
  ends at the certificate's own `NotAfter`: swept earlier it silently re-admits
  the certificate, kept forever it fills the reserved tenant with facts about
  certificates nobody can present. The expiry is in the certificate, so the rule
  is derivable rather than guessed.
- ⚠ **`sdev1-ca` NEVER RUNS ON A NODE.** The CA private key stays where the
  command runs; a node is given a leaf, its key, and a copy of the CA certificate
  — never the CA's key. It is a separate binary precisely so it need not be
  deployed. A node that could mint peers turns one compromise into all of them.
- ⚠ **A SECOND RUN OF `mint` MUST NOT OVERWRITE.** Silently replacing an authority
  key invalidates every certificate ever issued from it, and the outage arrives
  later, elsewhere, looking like something else. The test asserts the key file is
  byte-identical afterwards — an error return with the file already truncated
  passes a weaker check and has still done the damage.
- ⚠ **A DENIAL ONLY REACHES THE NODES THAT HOLD IT.** Nothing replicates the
  reserved tenant (§19), so a denial applied to one node in five is a partial
  revocation — and partial is worse than it sounds, because it looks complete from
  the node that has it.
