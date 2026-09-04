# ADR-014 Tasks

Implementation tasks for ADR-014: A filesystem path is a query, the tree is
read-only, and history is a path prefix. See the parent ADR for the decision.

**Source of truth:** the task files' headers. This README is a derived index —
when it disagrees with a task file, the task file wins.

## Execution Order

Two tasks, sequential.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | What a path means, what it refuses, and what stat says about time | done | — | `go test ./internal/core/vfs/... -race -run 'TestAPathCompilesToAQuery\|TestASnapshotPathIsAnOrdinaryPath\|TestAWriteIsRefusedAtOpen\|TestAShreddedDatomIsIndistinguishableFromAnAbsentOne\|TestAParentReferenceIsRefusedNotResolved\|TestModTimeIsTheTransactionNotTheRead\|TestNoModeCarriesAWriteBit\|TestBeyondAnAttributeIsNotADirectory\|TestThereIsNoFourthNodeKind'` then the ql suite |
| T2 | Mount it | pending | — | `go test ./internal/core/vfs/... -race -run 'TestOpenForWriteFailsBeforeAnyWrite\|TestDirectoryListingIsDerivedNotCached'` then `go build ./cmd/sdev1-mount/...` |

Status: `pending` | `partial` | `blocked` | `done`.

⚠ **T2 is `pending` on three things**: a query evaluator (`BACKLOG.md` §20, itself
on a storage engine, §12), a FUSE library (§26), and enumeration — the language
reads a NAMED entity and cannot list them, so `/e` has no query behind it. ★That
is not the same as ADR-014 being unfinished: what a path means, what it refuses,
and what `stat` says about time are settled and proved by T1's mutants, none of
which needed a kernel.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `vfs.Path`, `vfs.ParsePath`, `vfs.Open`, `vfs.Stat`, `vfs.StatAttr` | T2 | T1 before T2 |

## Notes

- ★ **The reason to build this at all** is that a bitemporal append-only store is
  already a snapshotting filesystem, and `/.at/<instant>/e/...` is an ORDINARY
  path — so `cp -r`, `rsync`, `diff -r` and `grep -r` get time travel with no
  knowledge of this system. A filesystem is a worse API than the query language
  for nearly everything; it is worth having because tens of thousands of existing
  programs already speak it.
- ⚠ **Read-only, refused at `open` and never at `close`.** A program that opens
  for writing, buffers, and fails at `close(2)` has already lost the data, and
  many programs do not check `close`. The refusal is decided from the caller's
  INTENT before the node kind, so `open("/e/x", O_WRONLY)` is `EROFS` and not
  `EISDIR` — `EISDIR` would say that opening a file for writing would have worked.
- ⚠ **Writes are not a missing feature.** POSIX gives no way to say that several
  attributes change together, so a writable projection commits each `write(2)` as
  its own transaction and breaks ADR-003's entity boundary — silently, because
  every write succeeds.
- ⚠ **A shredded datom is `ENOENT`, identical to one that never existed.** Not
  `EACCES`: a permission error confirms the entity exists, and an oracle anyone
  can query by guessing a name is exactly what ADR-007 spent a record removing.
  Not an empty file either, which would make erasure look like a blank value.
- ⚠ **`.` and `..` are refused rather than resolved**, and `path/filepath.Clean`
  is not used. Inside a snapshot prefix a resolved `..` climbs out, and the caller
  who asked for history gets a confident answer from the wrong time.
- ⚠ **`mtime` is the datom's transaction time and two reads of an unchanged fact
  agree.** Callers read `stat` far more than contents — `make` compares mtimes,
  `rsync` compares mtime and size — so an mtime taken from the clock makes every
  incremental pass copy everything, and the mount looks broken rather than wrong.
- **Exactly three node kinds and no fourth.** A control file that changes
  behaviour when written is a write surface behind the read-only refusal, and it
  makes a path's meaning depend on hidden state.
- ⚠ **But an entity whose name begins with a dot is an ORDINARY entity.** The
  guard applies at the root, where a special name would be interpreted specially;
  nothing under `/e` is interpreted at all, which is what actually makes a control
  file impossible there. Refusing such a name would make that entity unreachable
  through the mount, and data you cannot read is a worse failure than a name `ls`
  hides by convention. T1's Risks records that the test asserted the opposite
  first and the code was right.
