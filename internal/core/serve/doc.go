// Package serve puts a leaf behind a socket.
//
// It is the first thing in this module that moves a byte between two processes.
// Everything before it was in-process: ADR-008 cut the `routing.Cluster` seam and
// nothing implemented it, ADR-043 fixed the response envelope and nothing sent
// one. This is both halves of that — a server that answers, and a client that is
// a `routing.Cluster` and nothing more.
//
// # The server resolves the key ITSELF
//
// ⚠ A request carries a key and never a leaf, so the receiver descends it against
// its OWN routing table and decides from that. THE CLIENT'S BELIEF IS NEVER AN
// INPUT. That is what makes ADR-008 rule 4 implementable: a node asked for a key
// it does not hold can still say where the key went, because it can compute the
// leaf from the key. Handed a leaf name instead, it would hold a name it does not
// recognise, no way to invert it, and nothing to send but an error — leaving a
// stale client stuck being wrong, which is the state rule 4 exists to prevent.
//
// ★ So the node that is WRONG is the one that repairs the caller's map. That is
// not a consolation; it is the mechanism.
//
// # The client is one method
//
// [Client] implements exactly [github.com/atvirokodosprendimai/sdev1/internal/core/routing.Cluster]
// — one method, `Serve`. Redirect following, the epoch rule and the hop budget
// are `routing.Resolve`'s and are not reimplemented here.
//
// ⚠ Writing a redirect loop in the client is the natural thing to do, because the
// redirect is right there in the response. It would be a second copy of the epoch
// rule and the hop budget, and a second copy that is WRONG still redirects — so
// nothing fails visibly, and the client quietly stops honouring a rule the
// routing package is still enforcing on paper.
//
// # A write is refused BY NAME
//
// [ErrWriteNotServed]. Not a redirect, not an empty answer.
//
// ⚠ An empty ANSWER is the dangerous shape and the one a hurried implementation
// reaches for: a client reads zero rows and concludes the write landed and
// matched nothing. ADR-043 made the three outcomes distinct precisely so "I will
// not" cannot be confused with "not here" or with "here it is".
//
// The reason is not squeamishness. There is no leader — §19's consensus is
// unbuilt — so a write served here would be unfenced (ADR-009) and committed at a
// durability nobody has (ADR-020). Refusing is the only honest answer available.
//
// # One request per connection
//
// Accept, set both deadlines, read one framed request, write one response, close.
// No pooling and no multiplexing.
//
// ⚠ Deadlines are set PER CONNECTION, not on the listener. A listener deadline
// bounds `Accept`, and the goroutine a stranger can pin forever is the one after
// it.
//
// A connection carrying exactly one exchange has a failure model with nothing in
// it: no half-consumed stream, no correlation identifiers, nothing to reconcile
// after a drop. It is a real cost per read, taken deliberately, and the first
// thing to revisit once docs/adr/BACKLOG.md §16 can measure it.
//
// # The certificate says WHO; the grant set says WHAT
//
// Both ends present a certificate signed by an authority the operator declared,
// and the client certificate's Subject Common Name is the PRINCIPAL — the
// argument [github.com/atvirokodosprendimai/sdev1/internal/core/authz.Load] has
// always taken and which nothing could supply until there was a connection to
// take it from. ADR-033 was Accepted and unreachable for exactly that reason.
//
// ★★ NO AUTHORITY IS READ FROM THE CERTIFICATE, and that is ADR-046's whole
// decision. Putting the capability in an X.509 extension is faster, is a real
// pattern, and removes a lookup from the read path — and it destroys ADR-033. A
// certificate is valid until it expires, so revoking a capability carried in one
// means a CRL, an OCSP responder, or waiting out the lifetime. ⚠ Until then the
// revoked principal keeps reading and the retraction that was meant to stop them
// reports success. That is the same leak ADR-033 refused historical-grant
// authorization for, arriving by a different door.
//
// # Every fail-open default in crypto/tls is a ZERO VALUE
//
// ⚠ This is why [TLSConfig] sets four things that look like they could be left
// alone:
//
//   - `ClientAuth`'s zero is [crypto/tls.NoClientCert] — no client certificate is
//     asked for, so every caller is anonymous. [crypto/tls.VerifyClientCertIfGiven]
//     is the trap among its siblings: it reads as "verify client certificates"
//     and admits a caller presenting none.
//   - `ClientCAs` and `RootCAs` nil do NOT mean "trust nothing". They mean the
//     host's root store, so any public authority installed on the machine may
//     mint a peer for this cluster.
//   - `MinVersion` unset is whatever the Go release still tolerates, which is a
//     version nobody chose. TLS 1.3, because both ends are this codebase and
//     there is no compatibility to buy with a downgrade surface.
//
// ⚠ Nothing exposes `InsecureSkipVerify`, including for tests. A test-only escape
// hatch is a production escape hatch with a comment on it, and the tests here
// mint their own authority instead — which gives a stronger test, because a
// second, undeclared authority is what proves verification happens at all.
//
// # What is still missing
//
// ⚠ **An operator must run a CA.** This package consumes certificates and issues
// none; distribution, rotation and revocation of an IDENTITY are
// docs/adr/BACKLOG.md §18's. Revoking AUTHORITY works today and is a retraction.
//
// See docs/adr/ADR-045-a-leaf-is-served-over-a-stream.md and
// docs/adr/ADR-046-a-certificate-names-who.md.
package serve
