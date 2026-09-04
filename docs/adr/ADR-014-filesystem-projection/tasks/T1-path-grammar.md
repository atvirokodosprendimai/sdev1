# Task ADR-014-T1: What a path means, what it refuses, and what stat says about time

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `vfs.Kind`, `vfs.Path`, `vfs.Errno`, `vfs.ParsePath`, `vfs.Path.Compile`, `vfs.OpenFlags`, `vfs.Open`, `vfs.Presence`, `vfs.Stat`, `vfs.Datom`, `vfs.Attr`, `vfs.StatAttr`, `vfs.Mode`
**Consumes:** `ql.Statement`, `ql.Select`, `ql.TimeClause` from ADR-011
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `a path compiling to a query rather than to a storage lookup`, `a write refused at open rather than at write`, `a shredded datom being indistinguishable from an absent one`, `a parent reference refused rather than resolved`, `a modification time taken from the transaction rather than from the read`

## Goal

Make a filesystem path mean exactly one thing — a query — and make everything the
kernel is told about a node true, including the parts callers read instead of the
contents.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/vfs/doc.go` | add | The package comment: why the projection exists and why it is read-only. |
| `internal/core/vfs/path.go` | add | `Kind`, `Path`, `Errno`, `ParsePath`, `Compile`. |
| `internal/core/vfs/stat.go` | add | `OpenFlags`, `Open`, `Presence`, `Stat`, `Datom`, `Attr`, `StatAttr`, `Mode`. |
| `internal/core/vfs/vfs_test.go` | add | The tests below. |

★ The split is by question, not by size: `path.go` answers "what does this path
name", `stat.go` answers "what is the kernel told about it". The second is where
the failures nobody notices live, because callers read `stat` far more than they
read contents.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestAPathCompilesToAQuery`, `TestASnapshotPathIsAnOrdinaryPath`, `TestAWriteIsRefusedAtOpen`, `TestAShreddedDatomIsIndistinguishableFromAnAbsentOne`, `TestAParentReferenceIsRefusedNotResolved`, `TestModTimeIsTheTransactionNotTheRead`, `TestNoModeCarriesAWriteBit`, `TestBeyondAnAttributeIsNotADirectory`, `TestThereIsNoFourthNodeKind`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Kind` as exactly three node kinds and `Errno` as the dispositions returned. ★A fourth kind is how a control file enters, and a control file is a write surface behind the read-only refusal.
3. [S3] Implement `ParsePath`: the `/e/<entity>/<attribute>` grammar with the `/.at/<instant>` prefix, refusing `.` and `..` with `EINVAL` rather than resolving them. ⚠Do not reach for `path/filepath.Clean`; resolving `..` inside a snapshot prefix climbs out of the snapshot, and the caller who asked for history gets a confident answer from the wrong time. [proof: mutation]
4. [S4] Implement `Compile` so every path that names data becomes a `ql.Select` carrying the instant as a time clause, unresolved. [proof: mutation]
5. [S5] Implement `Open` so any write intent is `EROFS`, decided BEFORE the node kind is considered. ★`open("/e/x", O_WRONLY)` must be `EROFS` and not `EISDIR`: reporting `EISDIR` tells the caller that opening a file for writing would have worked. [proof: mutation]
6. [S6] Implement `Stat` so `PresenceAbsent` and `PresenceShredded` return the SAME errno. ⚠`EACCES` for a shredded subject is an oracle for its existence, answerable by anyone who can guess a name — which is the property ADR-007 spent a record removing. [proof: mutation]
7. [S7] Implement `StatAttr` so `mtime` is the datom's transaction time and `atime` is the read. ★Callers read `stat`, not contents: `make` compares mtimes and `rsync` compares mtime and size, so an mtime from the clock makes every incremental pass copy everything. [proof: mutation]
8. [S8] Give every mode a read-only bit set, so rule 3 is visible in metadata and not only in `Open`.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/vfs/... -race -run 'TestAPathCompilesToAQuery|TestASnapshotPathIsAnOrdinaryPath|TestAWriteIsRefusedAtOpen|TestAShreddedDatomIsIndistinguishableFromAnAbsentOne|TestAParentReferenceIsRefusedNotResolved|TestModTimeIsTheTransactionNotTheRead|TestNoModeCarriesAWriteBit|TestBeyondAnAttributeIsNotADirectory|TestThereIsNoFourthNodeKind' -count=1 2>&1 | tee /tmp/adr014-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr014-t1a.out \
  && go test ./internal/core/vfs/... ./internal/core/ql/... -race -count=1 2>&1 | tee /tmp/adr014-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr014-t1b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAPathCompilesToAQuery` | `internal/core/vfs/vfs_test.go` | Every path naming data compiles to a `ql.Select` with the right entity and attributes — the falsifier | — | S3, S4 |
| `TestASnapshotPathIsAnOrdinaryPath` | `internal/core/vfs/vfs_test.go` | A `/.at/<instant>` prefix parses to the same node as the bare path with the instant attached, and the instant reaches the statement unresolved | — | S3, S4 |
| `TestAWriteIsRefusedAtOpen` | `internal/core/vfs/vfs_test.go` | Every write intent on every node kind is `EROFS` at open, including on a directory where `EISDIR` would be the tempting answer | — | S5 |
| `TestAShreddedDatomIsIndistinguishableFromAnAbsentOne` | `internal/core/vfs/vfs_test.go` | Absent and shredded produce the identical errno, so `stat` is not an oracle for who was erased | — | S6 |
| `TestAParentReferenceIsRefusedNotResolved` | `internal/core/vfs/vfs_test.go` | `..` and `.` are `EINVAL`, including inside a snapshot prefix where resolving would silently return live data | — | S3 |
| `TestModTimeIsTheTransactionNotTheRead` | `internal/core/vfs/vfs_test.go` | Two reads of an unchanged fact report the same mtime, which is what every incremental tool depends on | — | S7 |
| `TestNoModeCarriesAWriteBit` | `internal/core/vfs/vfs_test.go` | No node's mode has a write bit, so read-only is visible in metadata and not only at open | — | S8 |
| `TestBeyondAnAttributeIsNotADirectory` | `internal/core/vfs/vfs_test.go` | A path deeper than an attribute is `ENOTDIR` rather than a deeper node | — | S2, S3 |
| `TestThereIsNoFourthNodeKind` | `internal/core/vfs/vfs_test.go` | Every successful parse is one of three kinds; a dot-prefixed name at the ROOT is `ENOENT` rather than a control node, while one under `/e` is an ordinary entity | — | S2, S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The nine tests above. |
| 2 — something selects it | `ParsePath` is the only way a path becomes a node, and `Compile` the only way a node becomes a query; nothing in the package answers a path any other way. |
| 3 — the caller can discover it | The kernel discovers it the way it discovers any filesystem — by `stat` and `open` — and both are implemented here as pure functions returning what it would be told. |
| 4 — it is used | Nothing mounts yet; T2 is `pending` on a FUSE library and the evaluator. |

## Mutation Log

- 2026-09-04 · 535b086* · mutant killed · exit 1 · `internal/core/vfs/path.go` · answers an attribute path without producing a statement, which is the shape a path takes the moment it is served by reading a datom directly — shorter to write, and a second query surface with its own time semantics that diverges exactly on historical reads · acceptance-sha256:0f0d97aa4a04bcc9430ef4df4f4c5e55da846770af26ad835be9e39b7f8ecc91 · covers:a path compiling to a query rather than to a storage lookup
- 2026-09-04 · 535b086* · mutant killed · exit 1 · `internal/core/vfs/stat.go` · decides the node kind before the caller intent, so a directory opened for writing reports EISDIR — which tells the caller that opening a FILE for writing would have worked, and is the reading that leads a program to buffer and lose the data at close · acceptance-sha256:0f0d97aa4a04bcc9430ef4df4f4c5e55da846770af26ad835be9e39b7f8ecc91 · covers:a write refused at open rather than at write
- 2026-09-04 · 535b086* · mutant killed · exit 1 · `internal/core/vfs/stat.go` · reports EACCES for a shredded subject, which is the more informative answer and is an oracle: a permission error confirms the entity existed, so anyone who can guess a name can ask the filesystem who was erased — the exact property crypto-shredding exists to remove · acceptance-sha256:0f0d97aa4a04bcc9430ef4df4f4c5e55da846770af26ad835be9e39b7f8ecc91 · covers:a shredded datom being indistinguishable from an absent one
- 2026-09-04 · 535b086* · mutant killed · exit 1 · `internal/core/vfs/path.go` · stops refusing dot segments, so a path may carry .. — and inside a snapshot prefix a resolved parent reference climbs out of the snapshot, answering a historical question from live data with no error the caller can see · acceptance-sha256:0f0d97aa4a04bcc9430ef4df4f4c5e55da846770af26ad835be9e39b7f8ecc91 · covers:a parent reference refused rather than resolved
- 2026-09-04 · 535b086* · mutant killed · exit 1 · `internal/core/vfs/stat.go` · reports mtime from the read instead of the assertion, which is what a naive stat does and what most filesystem examples show — every file then looks modified on every pass, so make rebuilds everything, rsync re-copies everything, and every incremental backup silently becomes a full one · acceptance-sha256:0f0d97aa4a04bcc9430ef4df4f4c5e55da846770af26ad835be9e39b7f8ecc91 · covers:a modification time taken from the transaction rather than from the read

## Invariants

- A path naming data compiles to a statement, never to a direct read.
- Any write intent is `EROFS`, decided before the node kind.
- Absent and shredded are the same answer.
- `.` and `..` are refused, never resolved.
- `mtime` is the transaction time; two reads of an unchanged fact agree.

## Risks

- ⚠ **A read-only test that only opens files proves nothing about directories**, and `EISDIR` is the tempting answer there. The test opens every node kind for writing and requires `EROFS` from all of them.
- ⚠ **A `..` test outside a snapshot prefix proves the wrong thing.** Refusing `..` matters because of what it would escape; the test walks `..` out of `/.at/<instant>` specifically, which is the case where resolving returns live data for a historical question.
- ⚠ **"Absent and shredded both fail" is not the property.** Two different failures would still be an oracle. The test asserts the two errnos are EQUAL, which is the observable form of indistinguishable.
- ⚠ **An mtime test against a single read cannot fail**, because any value equals itself. The test reads the same unchanged datom twice with different read times and compares the two mtimes — which is exactly what an incremental tool does.
- ⚠ **The control-file guard first asserted that ANY dot-prefixed name is `ENOENT`, and the test caught the code disagreeing at `/e/.control`.** The code was right. Entity names are arbitrary strings, so refusing one that begins with a dot would make that entity unreachable through the mount — and data you cannot read is a worse failure than a name `ls` hides by convention. The guard now applies at the ROOT, where a special name would be interpreted specially; nothing under `/e` is interpreted at all, which is what actually makes a control file impossible there. The expectation was wrong, not the implementation, and it is recorded because a guard narrowed after the fact looks the same as one that was always narrow.
- Nothing here proves the kernel is actually told these answers; that is T2's, and the follow-up in the record says so.

## Stop Condition

Stop and ask before making any node writable, however small the write. There is no
way in POSIX to say that several attributes change together, so a writable
projection commits each `write(2)` as its own transaction and breaks ADR-003's
entity boundary — and it breaks it silently, because every write succeeds.

## Out of Scope

- Mounting anything (deferred: `docs/adr/BACKLOG.md` §26)
- Evaluating the compiled statement (deferred: `docs/adr/BACKLOG.md` §20)
- Listing entities, which the language cannot express (deferred: `docs/adr/BACKLOG.md` §26)

## Verification Log
- 2026-09-04 · 535b086* · exit 0 · `set -o pipefail …` · acceptance-sha256:0f0d97aa4a04bcc9430ef4df4f4c5e55da846770af26ad835be9e39b7f8ecc91 · ms:3567
- 2026-09-04 · 535b086* · exit 0 · `set -o pipefail …` · acceptance-sha256:0f0d97aa4a04bcc9430ef4df4f4c5e55da846770af26ad835be9e39b7f8ecc91 · ms:3543
- 2026-09-04 · 535b086* · exit 0 · `set -o pipefail …` · acceptance-sha256:0f0d97aa4a04bcc9430ef4df4f4c5e55da846770af26ad835be9e39b7f8ecc91 · ms:3525
- 2026-09-04 · 535b086* · exit 0 · `set -o pipefail …` · acceptance-sha256:0f0d97aa4a04bcc9430ef4df4f4c5e55da846770af26ad835be9e39b7f8ecc91 · ms:3549
- 2026-09-04 · 535b086* · exit 0 · `set -o pipefail …` · acceptance-sha256:0f0d97aa4a04bcc9430ef4df4f4c5e55da846770af26ad835be9e39b7f8ecc91 · ms:3548
- 2026-09-04 · 535b086* · exit 0 · `set -o pipefail …` · acceptance-sha256:0f0d97aa4a04bcc9430ef4df4f4c5e55da846770af26ad835be9e39b7f8ecc91 · ms:3538
