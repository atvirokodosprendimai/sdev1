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
// # NOTHING AUTHENTICATES
//
// ⚠ Anyone who can reach the socket can read any leaf the node holds. ADR-033
// decided the grant rule and left the enforcement point to whatever gains a
// caller identity; this does not provide one, and it must not be exposed to a
// network the operator does not own until docs/adr/BACKLOG.md §18 closes it.
//
// See docs/adr/ADR-045-a-leaf-is-served-over-a-stream.md.
package serve
