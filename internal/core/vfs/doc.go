// Package vfs decides what a filesystem path means.
//
// A bitemporal append-only store already holds every version of every fact, so a
// filesystem over it is a natively snapshotting filesystem and costs almost
// nothing to expose: an entity is a directory, an attribute is a file, and an
// instant is a path prefix.
//
// ★ That is the reason it exists. A filesystem is a WORSE interface than the
// query language for nearly everything. It is worth building because it is the
// one interface tens of thousands of existing programs already speak — so
// `cp -r /.at/<instant>/e /backup` is a point-in-time export, and nothing
// involved had to learn anything about this system.
//
// # A path is a query
//
// [Path.Compile] turns a path into a
// [github.com/atvirokodosprendimai/sdev1/internal/core/ql] statement. Nothing
// here reads storage, and nothing here resolves what a time qualifier defaults
// to. Answering a path by reading a datom directly is shorter, and it is a second
// query surface with its own time semantics — which is the thing this package and
// the agent surface both refuse for the same reason.
//
// # It is read-only, and the refusal happens at open
//
// ⚠ Not at write, and never at close. A program that opens for writing, buffers,
// and fails at close(2) has already lost the data, and a great many programs do
// not check close at all. [Open] returns [EROFS] from the caller's INTENT before
// it considers the node kind, so opening a directory for writing is EROFS rather
// than EISDIR — EISDIR would tell the caller that opening a FILE for writing
// would have worked.
//
// ⚠ Writes are not a missing feature. POSIX gives a program no way to say that
// several attributes change together, and the entity is this store's transaction
// boundary. A writable projection would commit each write(2) as its own
// transaction and break that boundary silently, because every write succeeds.
//
// # Metadata has to tell the truth about time
//
// Callers read stat far more than they read contents: make compares mtimes,
// rsync compares mtime and size, a backup agent skips a file whose mtime has not
// moved. [StatAttr] therefore reports mtime as the datom's TRANSACTION time and
// atime as the read.
//
// ⚠ An mtime taken from the clock makes every file look modified on every pass,
// so every incremental tool over the mount copies everything, every time — a
// projection correct in its contents and useless in practice.
//
// # Absent and erased are the same answer
//
// [Stat] returns [ENOENT] for a shredded datom, identical to one that never
// existed.
//
// ⚠ Not EACCES. A permission error confirms the entity exists, and an oracle
// anyone can query by guessing a name is exactly the property crypto-shredding
// exists to remove. Not an empty file either: that would make erasure look like a
// blank value.
//
// # Dot segments are refused, not resolved
//
// [ParsePath] returns [EINVAL] for "." and "..", and path/filepath.Clean is
// deliberately not used.
//
// ⚠ Inside a snapshot prefix a resolved ".." climbs out of the snapshot, and the
// caller who asked for history gets a confident answer from the wrong time. A
// refusal is a failure the caller can see; resolving is one it cannot.
//
// # What this package does not do
//
// It mounts nothing and holds no state. Every function here is pure: it takes
// what the store said and returns what the kernel should be told. Keeping the
// mapping separate from the mount is what makes all of the above testable with no
// kernel, no FUSE library and no storage engine.
package vfs
