# ADR-046: A certificate names WHO, never WHAT — so revocation still reaches a live connection

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-001-address-space.md`, `docs/adr/ADR-008-prefix-routing.md`, `docs/adr/ADR-016-tenant-prefix.md`, `docs/adr/ADR-033-grants-and-tenant-allocation.md`, `docs/adr/ADR-043-response-envelope.md`, `docs/adr/ADR-045-a-leaf-is-served-over-a-stream.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/serve/**`
**Enforced-by:** `internal/core/serve/authn_test.go::TestRevocationReachesALiveCertificate`
**Invalidates:** ⚠ **ADR-045 rule 7** — "one request per connection, closed after" becomes *one request in flight* per connection, and both ends now serve sequential exchanges over one. ADR-033's and ADR-045's authentication deferrals are closed rather than contradicted
**Served-path change:** A read over the wire now requires a client certificate and a live grant, and a client reuses a connection instead of dialling per request. An unauthenticated caller that could read any leaf a node held gets a TLS handshake failure instead.

## Context

ADR-045 put a leaf behind a socket and said, in its own record and in the
README, that **nothing authenticates**: anyone who could reach the address could
read any leaf the node held. It also took one request per connection
deliberately, and named the revisit condition.

ADR-033 decided authorization completely — grants are datoms in reserved tenant
`0000`, revocation is a retraction, and `authz.Set.Allow` takes no instant so
that a query `AS OF` last March is authorized by TODAY's grants. It then stopped
at one sentence, in its own Primitives Audit:

> wiring authorization into statement execution needs **a caller identity the
> language does not carry**, and inventing one here would decide an interface
> nobody has asked for. Deferred, explicitly.

★ **That deferral's condition is now met.** A transport is the thing that has a
caller. The identity does not have to be invented; it has to be *taken from
somewhere that already carries one*.

### Why these three are one record and not three

They look like three independent gaps — the README lists them in one row and they
were deferred together. Deciding them separately would get two of them wrong.

★ **mTLS is not a way to protect the auth mechanism; it IS the auth mechanism.**
A mutually-authenticated connection has already proved who the peer is, against a
CA the operator chose. Adding a token, an API key or a bearer header on top of it
would be a *second* identity that can disagree with the first — and when two
identities disagree, the interesting question is which one the authorization
check used.

★ **TLS is what makes pooling worth deciding.** Without it a connection costs a
TCP handshake, and ADR-045's one-per-connection rule was cheap. With mTLS it
costs a TLS 1.3 handshake plus certificate verification on both sides, which is
asymmetric-crypto work per read. Deciding pooling before TLS would have been
deciding against a cost model TLS invalidates — and deciding TLS without pooling
would ship a known, avoidable cost on the served path.

### The trap, and it is the reason for the title

⚠ **The tempting design is to put the capability in the certificate.** An X.509
extension carrying "may read tenant 000b" is a real pattern, it is fast, and it
removes a lookup from the read path. It also silently destroys ADR-033.

A certificate is valid until it expires. A grant is a datom, and **revocation is
a retraction that takes effect at the next read.** Put the capability in the
certificate and revoking it means revoking the certificate — a CRL, an OCSP
responder, or waiting out the lifetime. ⚠ **Until then the revoked principal
keeps reading, and the retraction that was supposed to stop it succeeded.** That
is precisely the failure ADR-033's rule 3 exists to prevent, arriving by a
different door.

So the certificate answers exactly one question — *who is this* — and every
question about *what they may do* is answered by reading the present grant set.

## Existing Primitives Audit

- `internal/core/authz` (ADR-033): supplies `Load`, `Set.Allow`, `Capability`,
  `SystemTenant`. **Reused entirely unchanged.** This record supplies the
  `principal` argument `Load` has always taken and nothing has ever been able to
  fill. ★ If this ADR needed to change `authz`, that would be evidence the
  identity had been designed wrong.
- `internal/core/addr` (ADR-001, ADR-016): supplies `TenantOf(Key)`, a prefix
  read. **Reused unchanged** — the tenant to authorize against is already in the
  request, because ADR-045 made a request carry a key.
- `internal/core/serve` (ADR-045): supplies `Server`, `Client`, `Options`.
  **Extended** — three new option fields, all of them refused when absent.
- `crypto/tls`, `crypto/x509` (stdlib): **used directly, no wrapper.** A helper
  that "simplifies" `tls.Config` is a helper that hides which of its fail-open
  defaults are in force, and those defaults are the entire risk surface here.
- A token, session, or bearer-credential mechanism: **none, and deliberately.**
  See the Context — a second identity is a second answer.
- A connection-pool library: **none.** A pool that holds at most N idle
  connections per node, hands one out, and discards it on any error is forty
  lines; a dependency here would bring reconnection and retry policy with it, and
  ADR-045's Stop Condition says retry is `routing.Resolve`'s or nobody's.

## Decision

**A certificate names WHO. The present grant set says WHAT. A connection is
reused only while its stream state is known.**

1. **Both directions authenticate, and the client certificate carries the
   principal.** The server sets `tls.RequireAndVerifyClientCert`; the client
   verifies the server against the same declared pool. The principal is the peer
   certificate's Subject Common Name, and ⚠ **an empty one is refused** rather
   than treated as an anonymous caller.

2. ⚠ **The CA pool is DECLARED, never the system roots.** `tls.Config` with a nil
   `RootCAs` or `ClientCAs` falls back to the host's trust store, which means any
   of ~150 public CAs can mint a peer for this cluster. That is the fail-open
   default and it looks like no configuration at all. A node with no declared pool
   refuses to start.

3. **TLS 1.3 minimum.** ⚠ Not 1.2. Both ends are this codebase, so there is no
   compatibility to buy with a downgrade surface — and a `MinVersion` left unset
   is another fail-open default, not a neutral one.

4. **Authorization reads the PRESENT grant set, through ADR-033 unchanged.** The
   tenant comes from `addr.TenantOf(request.Key)`, the principal from the
   certificate, and the decision from `authz.Set.Allow`, which takes no instant.
   ★ A request may ask `AS OF` any moment; the grant that permits it is always
   today's.

5. ⚠ **A node with no declared grant source REFUSES EVERY READ.** Not "allows
   everything" and not "starts and fails later". ADR-033 rule 5 says no grant
   means refused, and the dangerous reading of it is that an unreachable or
   unconfigured grant store is a special case. It is not: that is exactly when a
   system fails open.

6. ⚠ **A request naming reserved tenant `0000` is refused BY NAME**, before any
   store is touched. The grants are datoms in that tenant, so a node that happened
   to hold that leaf would otherwise serve the grant table through the ordinary
   read path — and `Set.Allow` refusing that tenant does not help, because reading
   it is a read like any other.

7. **A connection is returned to the pool only after a COMPLETE, successfully
   decoded exchange.** ⚠ Any error at any stage — write, read, frame, decode,
   deadline — discards it. A connection whose stream position is unknown is worse
   than no connection: reusing it means resynchronising onto a frame boundary
   nothing can verify, and the first frame read from the wrong offset is a length
   prefix a stranger's bytes chose.

8. **Still ONE exchange in flight per connection — but no longer one exchange
   per connection.** ⚠ **This AMENDS ADR-045 rule 7, and pretending otherwise
   would be the dishonest version.** That rule was two claims wearing one
   sentence: *one in flight* (no correlation identifiers, nothing to reconcile
   after a drop) and *one in total* (the stream can never be half-consumed,
   because it is never used twice). The first is kept and is what rules out
   multiplexing. The second is given up — deliberately, because it is what the
   handshake is being paid for — and rule 7 above is what replaces it. ★ The
   difference is that "the stream is at a frame boundary" changes from a property
   you get for free into one you must *maintain*, and maintaining it is exactly
   "discard on any error".

   ⚠ **Both ends change.** A server that closed after one exchange would make
   client-side pooling useless — the client would keep a connection its peer had
   already hung up, and every second read would fail before succeeding on a
   redial. So the server reads requests in a loop until the connection ends or a
   deadline passes.

9. ⚠ **The pool's bounds are DECLARED, never defaulted** — idle count and idle
   lifetime. The same discipline as ADR-045's frame bound, for the same reason:
   an unbounded pool and an unconfigured one look identical from outside, and the
   unbounded one is a file-descriptor leak that only appears under load.

**What would falsify this.** A principal whose grant is retracted continuing to
read **over a connection that is already open, with a certificate that is still
valid**. That is the falsifier in `Enforced-by:`, it is ADR-033's own falsifier
carried across the wire, and it is exactly what a capability-bearing certificate
produces.

## Alternatives Considered

- **Put the capability in the certificate** (an X.509 extension, or a signed
  macaroon-style token). Fast, removes a lookup from the read path, and a real
  pattern. ⚠ Rejected under rule 1 and the Context: a certificate is valid until
  it expires, so revocation would need a CRL or OCSP and the retraction that was
  meant to stop the caller would silently not. It is the same leak ADR-033
  rejected historical-grant authorization for, entering by a different door.
- **A bearer token or API key over TLS**, with TLS only for confidentiality.
  Conventional and familiar. Rejected under rule 1: the connection has already
  proved an identity, so a token is a *second* identity that can disagree with
  it, and the disagreement is invisible unless something checks both. It also
  needs a credential store, an issuance path and a rotation story — three
  decisions bought to avoid using one that mTLS gives away.
- **`tls.VerifyClientCertIfGiven`,** so unauthenticated callers can still reach
  read-only paths. ⚠ Rejected: it is the fail-open spelling. The check that would
  say no is the one that is skipped, and the resulting node serves anonymous
  reads while its configuration reads as "TLS is on".
- **Trust the system root pool,** so an operator can use a public CA. Rejected
  under rule 2: it makes the set of entities that can mint a valid peer equal to
  the set of public CAs, which is not a decision an operator would make on
  purpose but is exactly what leaving the field nil does.
- **Allow reads when the grant source is unreachable,** so a partition does not
  become an outage. Rejected under rule 5, and ADR-033 rejected the same
  alternative in the same words: it fails open precisely when the thing that
  would say no is unreachable.
- **Fetch grants over the transport from whichever node holds tenant `0000`.**
  It removes the local-replica requirement. Rejected as circular: that read would
  itself need authorizing, and the node it asks would need to authorize it, so
  the mechanism depends on itself. The grant source is local and its replication
  is `BACKLOG.md` §19's, stated rather than solved.
- **Multiplex several requests over one connection.** It is what pooling usually
  becomes and it saves more. Rejected under rule 8: it needs correlation
  identifiers and reintroduces the half-consumed stream ADR-045 rule 7 removed,
  and the handshake — not the connection — is what TLS made expensive.
- **Keep one request per connection and accept the handshake.** Honest, and it is
  what ADR-045 shipped. Rejected because TLS changed the number: a fresh
  asymmetric handshake per read is a cost this record itself introduces, and
  introducing it without paying it down is leaving a known regression on the
  served path.

## Component / Boundary Impact

No new component and no new boundary. `internal/core/serve` gains a trust
boundary it did not have — the point where a stranger's bytes become a named
principal — and that boundary is one function, `PrincipalOf`, so there is one
place to review.

⚠ **`internal/core/authz` is NOT modified.** It is consumed exactly as ADR-033
left it. The Module Map gains no entry; `serve` already appears there and its
dependencies grow by `authz` and `crypto/tls`.

## Wiring & Contract Changes

| What | Change | Consumer |
|------|--------|----------|
| `serve.Options` | adds `TLS TLSConfig`, `Grants ports.Reader` | `cmd/sdev1-serve` |
| `serve.ClientOptions` | adds `TLS TLSConfig`, `Pool PoolBounds` | any client |
| `serve.TLSConfig` | new — cert, key, CA pool, all required | both |
| `serve.PoolBounds` | new — `MaxIdlePerNode`, `IdleTimeout`, both required | client |
| `serve.ErrNoTLS`, `ErrNoPrincipal`, `ErrNoGrants`, `ErrNoPoolBounds`, `ErrSystemTenant` | new sentinels | callers |
| `cmd/sdev1-serve` | adds `--cert`, `--key`, `--ca`, `--grants`; all required | operators |
| Wire format | **unchanged.** ADR-043's envelope and ADR-045's framing are untouched — TLS is underneath them, and the principal never travels in a frame. |

⚠ **Every new option is required.** An existing caller does not silently get an
unauthenticated node; it gets a refusal naming what it did not declare.

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `serve.TLSConfig`, `serve.PrincipalOf`, the TLS dial (T1) | T1 | T2, T3 | **Yes** — every existing `serve` test dials in the clear and must be converted |
| `serve.Pool`, `serve.PoolBounds` (T2) | T2 | T3 | No — but T3's falsifier is weak without it |

⚠ **Authorization is LAST, and that ordering is forced by the falsifier rather
than chosen.** "A revoked principal keeps reading" is provable against a fresh
dial, and that proof is weak: it passes even if authority were established at
handshake time, which is the design being ruled out. Only a read, a revocation,
and a second read **on the same open connection** separates them — so the pool
has to exist before the record's central claim can be tested at full strength.

## Implementation

Three tasks, strictly in order. See
`docs/adr/ADR-046-a-certificate-names-who/tasks/README.md`.

## Consequences

- **Positive:** ADR-033 becomes enforceable for the first time — it has been
  Accepted and unreachable since it was written, because nothing could supply a
  principal. Its falsifier now runs over a socket.
- **Positive:** ADR-045's stated blocker on exposure is closed. The README's
  "⚠ do not expose this to a network you do not own" is replaced by a statement
  of what it now requires.
- **Positive:** A read costs one handshake per *pooled connection* rather than
  one per read.
- **Negative:** ⚠ **An operator must now run a CA.** Certificates must be issued,
  distributed and rotated, and this record provides none of that — it consumes
  certificates and does not make them. That is a real operational burden this
  decision imposes, and `BACKLOG.md` §18 carries it.
- **Negative:** ⚠ **Rotation is not solved.** A certificate that expires stops a
  node, and nothing here reloads one without a restart.
- **Negative:** ⚠ **Nothing revokes a certificate.** No CRL, no OCSP. Revoking a
  *principal* works — it is a retraction, and that is rule 4 — but revoking the
  *identity* means reissuing the CA. Acceptable only because authority, not
  identity, is what changes day to day.
- **Negative:** The grant source is a local reader per node, so a node's view of
  the grant set is as fresh as whatever put it there. Replicating it is §19's.

## Out of Scope

- Certificate issuance, distribution or rotation (deferred: `docs/adr/BACKLOG.md` §18)
- Certificate revocation, CRL and OCSP (deferred: `docs/adr/BACKLOG.md` §18)
- Replicating the grant leaf between nodes (deferred: `docs/adr/BACKLOG.md` §19)
- Who allocates a tenant identifier (deferred: `docs/adr/BACKLOG.md` §11 — ADR-033 already defers it to §19)
- Authorizing a WRITE over the wire (permanent: boundary: ADR-045 refuses a served write by name, so there is no write path to authorize; `authz.Write` exists and this record deliberately does not reach for it)
- Multiplexing (permanent: boundary: rule 8 — it reintroduces the half-consumed stream ADR-045 rule 7 removed, and the handshake is what TLS made expensive)
- Retry and backoff (permanent: boundary: ADR-045's Stop Condition — `routing.Resolve` owns the only cluster policy that exists)
- Measuring what the handshake and the pool actually cost (deferred: `docs/adr/BACKLOG.md` §16)
- Authorizing the agent tool surface and the filesystem projection (deferred: `docs/adr/BACKLOG.md` §11)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The capability is put in the certificate | **High — it is faster, it is a real pattern, and it removes a lookup from the read path** | **Critical** — revocation stops reaching the caller, so a retraction succeeds while the revoked principal keeps reading until the certificate expires | Rule 1 and the record's title; the falsifier revokes against a live certificate |
| A fail-open `tls.Config` default is left in place | **High — every one of them is a ZERO VALUE**: nil `ClientCAs`, unset `MinVersion`, `NoClientCert` | **Critical** — a node that reads as "TLS is on" serves anonymous callers, or trusts any public CA to mint a peer | Rules 2 and 3, asserted directly on the built config rather than inferred from a working handshake |
| The fixture uses one CA | Med — it is the obvious way to write it | High — an implementation that verified NOTHING would pass every test | T1's risk note: a second, untrusted CA is required |
| The principal is read from `PeerCertificates` | Med — it is the field that looks right | **Critical** — that is what the peer SENT, not what was proved, so an unverified claim becomes an identity | Rule 1 via `VerifiedChains`, and T1's test asserts it |
| A grant source that is absent or unreachable permits | Med — "don't break the cluster during a partition" is a sympathetic argument | **Critical** — fails open exactly when the thing that would say no is unreachable | Rule 5, refused at construction; ADR-033 rejected the same alternative |
| The grant leaf is served through the ordinary read path | Med — it is a leaf like any other, and `Allow` refusing the tenant does NOT cover reading it | **Critical** — the grant table becomes readable, and a reader can see every principal's authority | Rule 6, refused by name before any store is touched |
| A connection is returned to the pool after a decode error | **Med — the transport read succeeded, so it feels like a caller-side problem** | High — the stream position is unknown, so the next frame is read from an offset nothing verified | Rule 7; T2's test asserts the NEXT read succeeds |
| The pool grows a general retry loop | Med — the single pre-send retry looks like a natural place to start | High — a retry after the request was sent may re-run something already served, and it duplicates a policy `routing.Resolve` owns | T2 S5 and its Stop Condition: retry only before the first byte is written |

## Rollback

The wire format does not change, so a node and a client from before this record
still speak to each other — but not to one from after it, because the new options
are required rather than optional. ⚠ **That is deliberate and it means rollback is
a fleet-wide flip, not a rolling one.** Reverting the commits restores an
unauthenticated node; there is no persistent state to migrate, and no data written
under this record differs in any way from data written before it.

## Follow-ups

- [ ] Certificate issuance, distribution and rotation (`BACKLOG.md` §18). ⚠ This record consumes certificates and makes none, so an operator must run a CA and nothing here helps them; an expired certificate stops a node with no reload path.
- [ ] Revocation of an IDENTITY — CRL or OCSP (`BACKLOG.md` §18). Revoking authority works today and is a retraction; revoking a certificate means reissuing the CA.
- [ ] Measure what the handshake costs and what the pool saves (`BACKLOG.md` §16). ★ §16 said its numbers "cannot be taken yet because nothing serves a read" — that reason expired with ADR-045, and this record adds the first cost worth measuring.
- [ ] Replicate the grant leaf between nodes (`BACKLOG.md` §19). Each node reads a local grant source, so its view is as fresh as whatever put it there.
- [ ] Re-check rule 8 when a measurement exists: multiplexing is refused on a failure-model argument, and only §16 can say what refusing it costs.
