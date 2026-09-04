// Package commit decides when a write has been accepted, and makes that the
// moment it becomes visible.
//
// # What a memory commit buys, and what it does not
//
// A write is acknowledged once N replicas hold it IN MEMORY, across distinct
// failure domains. The flush to disk happens afterwards and does not gate the
// acknowledgement.
//
// That is genuine durability against the failures that dominate: a process
// crashing, a panic, an out-of-memory kill, a binary being restarted. Another
// node still has the data and nothing waited on a disk.
//
// ⚠ It is NOT durability against CORRELATED loss. Two nodes sharing a power feed,
// a rack PDU or a transfer switch lose everything unflushed at the same instant —
// and nothing reports it, because the write was acknowledged and the client moved
// on. N copies protect against INDEPENDENT failures only.
//
// # So domains are counted, not acknowledgements
//
// Whether failures are independent is a placement question, not a count. Three
// replies from three processes on one power feed is ONE failure domain wearing
// three names, and it reads as triple durability right up until the feed drops.
//
// ★ And the domain level for a memory commit is POWER, not rack. Rack is the
// right unit for disk durability, where the failure being guarded against is a
// machine or a disk. For unflushed memory the failure is power, and a rack can
// span feeds while a feed can span racks — the two overlap without coinciding, so
// using one for the other gives a guarantee that is nominal.
//
// # The watermark is the commit point
//
// Atomicity needs no new mechanism. The tail's watermark already makes an
// unpublished entry UNREACHABLE rather than half-visible, and a reader loads it
// once. So this package does not add a way to see whether something is
// committed — it decides when the watermark advances, which makes the watermark
// itself the commit point.
//
// ⚠ Two definitions of "committed" drift, and the drift shows up only under
// partial failure — which is exactly when nobody is reading test output. The one
// a reader uses would not be the one the writer waited for.
//
// # A shortfall is refused
//
// Not acknowledged with a warning. The warning is read by nobody at the moment it
// matters, and acknowledging what was achieved rather than what was asked is how
// a cluster ends up holding data at a durability nobody chose.
//
// The three ways a commit fails are named separately, because they need three
// different responses: below the floor means restore capacity, one domain means
// fix placement, and a stale epoch means this writer lost the leaf and should
// stop.
//
// # What this package does not do
//
// It replicates nothing — there is no transport — and it flushes nothing. It does
// not decide how many copies are wanted, which is a durability policy's business;
// it says what each of those copies must have DONE, which is a different
// question. And it does not decide who may write: it only declines to count an
// acknowledgement made to a writer that has since been superseded.
package commit
