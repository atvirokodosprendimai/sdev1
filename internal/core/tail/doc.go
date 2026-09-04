// Package tail holds the live, mutable end of a leaf's log and publishes it to
// readers without a lock.
//
// # Publication replaces guarding
//
// Everywhere else in this system a reader races nothing, because the data is
// immutable: a sealed segment is written once and never changed. The tail is the
// exception — a reader is walking a structure a writer is appending to — and it
// is the reason this package exists.
//
// The obvious answer is a read-write lock, and it is the wrong one. A reader
// would then block the writer for the length of a scan, so a long read on a busy
// leaf becomes a write stall on exactly the leaf that is busy; and the cost is
// paid on every read, forever, to protect a window of a few nanoseconds.
//
// So an unfinished entry is not protected. It is UNREACHABLE.
//
// A writer fills an entry's slot completely, and only then advances a watermark
// with one atomic store. A reader loads that watermark once and walks only what
// is below it. There is nothing to guard, because nothing points at a
// half-written entry until it is whole. The store is the release and the load is
// the acquire, so everything the writer did before publishing is visible to a
// reader that observes the publication, and everything after it is out of reach.
//
// That is the entire mechanism, and its shape is a constraint rather than an
// implementation detail: anything that later joins this read path must be
// publishable in one atomic step. A structure that has to be rebalanced in place
// to be read cannot go here.
//
// # What a reader is guaranteed
//
// One acquire-load, then no synchronization at all. No mutex, no reference
// count, no epoch to enter and leave.
//
// A watermark loaded once is a stable prefix: reading it twice gives the same
// answer, and appends made afterwards are simply not in it. Repeatable reads are
// therefore free rather than built, and a scan does not have to hold anything
// open to stay consistent.
//
// # How it fails, and how it recovers
//
// The failure this package is built against is a torn read: an entry observed
// before it was finished, returning data that never existed. Nothing downstream
// recovers from that, which is why the publish step is last and why the tests
// run under the race detector with readers and a writer actually overlapping.
//
// A writer that stops leaves the watermark where it is. Readers keep serving the
// prefix that was published — a stalled writer degrades ingest, never reads.
//
// Chunks are never moved once written, so growth cannot invalidate what a reader
// is holding. A reader that captured an older chunk index still addresses valid
// memory; it simply sees a shorter tail, which is the same guarantee its
// watermark already gave it.
//
// # What this package does not do
//
// It does not decide who may write — ADR-003 gives a leaf one writer and ADR-009
// decides who that is. It does not make anything durable; publishing an entry is
// not the same as it being safe, and how many copies exist is a durability
// policy's business. And it does not seal: turning a tail into an immutable
// segment is a separate transition that nothing here triggers.
package tail
