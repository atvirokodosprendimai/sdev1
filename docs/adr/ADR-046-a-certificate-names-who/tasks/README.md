# ADR-046 Tasks

Implementation tasks for ADR-046: a certificate names WHO, never WHAT — so
revocation still reaches a live connection. See the parent ADR for the decision.

**Source of truth:** the task files' headers. This README is a derived index —
when it disagrees with a task file, the task file wins.

## Execution Order

Three tasks, strictly in order.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T2 |

⚠ **Authorization is LAST, and the ordering is forced rather than chosen.** The
record's falsifier is "a revoked principal is refused **on a connection that is
already open**". Against a fresh dial the same test passes even if authority were
established at handshake time — which is the design being ruled out — so the pool
has to exist before the central claim can be tested at full strength.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Both ends authenticate, and the certificate carries the principal | done | — | five TLS tests over two CAs, a `cmd/sdev1-serve` build, then three suites |
| T2 | A connection is kept only while its stream position is known | done | — | four pool tests against a counting listener, then three suites |
| T3 | The grant set decides, and it is read at the present | pending | — | five authorization tests including the falsifier, a build, then four suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `serve.TLSConfig`, `serve.PrincipalOf`, the TLS dial | T2, T3 | the pool holds a `*tls.Conn`, so there must be one to hold; T3 needs a principal |
| T2 | `serve.Pool`, `serve.PoolBounds` | T3 | T3's falsifier needs a connection open across the revocation |

## Notes

- ★★ **THE DECISION IS ONE SENTENCE: the certificate answers WHO, the present
  grant set answers WHAT.** Putting the capability in the certificate is faster,
  is a real pattern, and removes a lookup from the read path — and it silently
  destroys ADR-033, because a certificate is valid until it expires while a grant
  is retractable now. Revocation would then need a CRL, and until it ran the
  retraction that was meant to stop the caller would succeed and change nothing.
- ★ **These three were deferred together and belong together.** mTLS is not
  protection *around* the auth mechanism, it IS the mechanism — a second identity
  (a token, a key) can disagree with the one the connection already proved. And
  TLS is what makes pooling worth deciding: it turns a connection from a TCP
  handshake into asymmetric crypto per read, so deciding pooling first would have
  been deciding against a cost model TLS invalidates.
- ★ **ADR-033 is consumed completely unchanged.** It has been Accepted and
  unreachable since it was written, because nothing could supply the `principal`
  argument `authz.Load` has always taken. ⚠ If this work needs to modify `authz`,
  that is evidence the identity was designed wrong — stop and re-read the record.
- ⚠ **Every fail-open default in `crypto/tls` is a ZERO VALUE.** Nil `ClientCAs`
  or `RootCAs` means the host's ~150 public CAs may mint a peer for this cluster;
  unset `MinVersion` is a version nobody chose; `NoClientCert` is the default
  `ClientAuth`. Each reads as "not configured" and behaves as "no restriction",
  so the tests assert the built config directly — a handshake that works proves
  nothing about what else would have.
- ⚠ **The fixture needs TWO certificate authorities.** With one, both peers are
  signed by the only trust anchor present and an implementation that verified
  nothing passes every test.
- ⚠ **A node with no grant source refuses every read.** ADR-033 rule 5's
  dangerous reading is that an unconfigured or unreachable grant store is a
  special case; it is exactly the case where a system fails open.
- ⚠ **Tenant `0000` is refused BY NAME over the wire**, before any store is
  touched. The grants live there, so a node holding that leaf would otherwise
  serve the grant table through the ordinary read path — and `Set.Allow` refusing
  that tenant does not help, because reading it is a read like any other.
- ⚠ **A connection is returned to the pool only after a complete, successfully
  decoded exchange.** Any error discards it. Reusing a connection whose stream
  position is unknown means reading a length prefix from the middle of somebody
  else's payload.
- ⚠ **Still one exchange in flight per connection.** Pooling reuses sequentially;
  it does not interleave. ADR-045 rule 7's failure model survives, and what is
  saved is the handshake — which is the part TLS made expensive.
- ⚠ **This record makes an operator run a CA** and provides nothing for issuing,
  distributing or rotating certificates. That burden is real, is stated in
  Consequences, and is `BACKLOG.md` §18's.
