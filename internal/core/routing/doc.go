// Package routing answers one question: which node should this request go to,
// right now.
//
// # A trie prefix is a routable prefix
//
// That observation is the whole design. The address space is a 256-way trie, so
// a node holding everything beneath one prefix can advertise that prefix once
// instead of the millions of leaves under it. A lookup takes the DEEPEST
// matching prefix, which means a subtree can be carved out of a larger route by
// advertising a deeper one — without withdrawing the parent, and without anyone
// having to enumerate what is below.
//
// The table is therefore bounded by how much placement VARIES, not by how much
// data exists. A uniform cluster of any size aggregates to very few routes,
// because wherever all 256 children of a node share their next hops, the
// children are replaced by the parent.
//
// This is what makes the alternatives unnecessary. A client does not hold the
// cluster's map, which would be proportional to the cluster and would have to be
// pushed to every client on every change. It does not ask a metadata service,
// which would put a synchronous hop and a single point of failure in front of
// every read. It starts at one frontdoor and learns.
//
// # A stale route is a redirect, never an error and never an answer
//
// A cached route goes out of date the moment a leaf moves, and a client cannot
// know when that happened. There are three things the receiving node could do
// and only one of them is right.
//
// Refusing would turn every topology change into an outage for every client that
// had not yet heard. Serving the request anyway would be silently WRONG — an
// answer from a node that no longer holds the leaf. Redirecting costs one hop,
// and the client keeps what it learned.
//
// So staleness becomes a performance cost that repairs itself, rather than a
// correctness problem somebody has to prevent.
//
// ⚠ A redirect that is not ORDERED is a loop. Two nodes with opposing stale
// views will bounce a client between them forever, and each redirect looks
// exactly as authoritative as the last. Every route therefore carries a
// monotonically increasing epoch and a client installs one only if it is
// strictly newer. That makes a loop impossible in a correct cluster; a hop
// budget bounds it in an incorrect one, and the resulting error names the chain
// so the node that is lying can be found. Both are needed, and neither
// substitutes for the other.
//
// # Routing is not placement, and they are allowed to disagree
//
// Placement is canonical and computed: it says where a leaf SHOULD live, and any
// two clients agree without coordinating. Routing is observed and cached: it says
// where a request should go now.
//
// They differ exactly while a repair or a rebalance is in flight, and that
// difference is information rather than a fault. A diagnostic showing both is
// more useful than one that picks a side.
//
// # What this package does not do
//
// It performs no I/O. It does not decide where data should live, and it does not
// decide who may write to it — reaching a node is not permission to change
// anything there. And nothing here distributes a route between machines; that,
// and the transport itself, are still unbuilt.
package routing
