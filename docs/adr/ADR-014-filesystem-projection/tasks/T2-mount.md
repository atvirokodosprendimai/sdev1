# Task ADR-014-T2: Mount it

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `cmd/sdev1-mount`, `vfs.Mount`
**Consumes:** `vfs.ParsePath`, `vfs.Open`, `vfs.Stat`, `vfs.StatAttr` (T1), a query evaluator (`docs/adr/BACKLOG.md` §20), a FUSE library
**Data dependency:** needs a running store and a kernel — a mount answers by evaluating a compiled statement, and there is nothing to evaluate against and nothing to mount onto in a hermetic test
**Proof map:** v1
**Rests-on:** `the kernel receiving EROFS from open rather than from write`, `a directory listing being derived from the store rather than cached at mount`

## Status

⚠ **`pending`, and it is blocked on three things.** Recorded rather than started,
with the reasons written down.

- **A query evaluator** (`BACKLOG.md` §20, itself on a storage engine, §12). T1
  turns a path into a statement. Mounting means answering, and answering means
  evaluating.
- **A FUSE library** (`BACKLOG.md` §26). None is chosen, and choosing one is a
  decision about which platforms the mount works on — a real question rather than
  a dependency bump.
- **Enumeration** (`BACKLOG.md` §20). The language reads a NAMED entity; it cannot
  list them. So `/e` has no query behind it, and a mount that answered by inventing
  entries would be worse than one that returns nothing.

★ **This is not the same as ADR-014 being unfinished.** What a path MEANS, what it
refuses, and what `stat` says about time are all settled and proved by T1's
mutants — none of which needed a kernel. What waits here is delivery.

## Goal

Expose T1's grammar as a real mount, so a program that walks directories reads
this store at any instant without knowing it exists.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `go.mod` | modify | Add the chosen FUSE library. |
| `internal/core/vfs/mount.go` | add | `Mount`: bind T1's pure functions to the library's callbacks. |
| `internal/core/vfs/mount_test.go` | add | The tests below. |
| `cmd/sdev1-mount/main.go` | add | The binary: bind a tenant, mount a directory, serve until unmounted. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestOpenForWriteFailsBeforeAnyWrite`, `TestDirectoryListingIsDerivedNotCached`. Run the Acceptance fence and confirm it is red. [proof: acceptance]
2. [S2] Choose a FUSE library and pin it exactly, recording which platforms the mount then supports. [proof: acceptance]
3. [S3] Bind `Open` so a write intent fails the `open` callback itself. ⚠ Several FUSE bindings report a filesystem's read-only nature at the write callback by default, which would undo rule 3 of the record without changing a line of T1. [proof: mutation]
4. [S4] Derive a directory listing from the store on each `readdir` rather than caching it at mount. [proof: mutation]
5. [S5] Evaluate the compiled statement and return the attribute's bytes. [proof: human: a reader confirms this step is blocked on the query evaluator and a storage engine, and that no stub stands in for either]
6. [S6] Bind the mount's tenant from a flag, never from the path. [proof: human: a reader confirms the tenant reaches the mount outside the path namespace, so no path a caller constructs can select a tenant]
7. [S7] Report a shredded subject as `PresenceShredded` from the layer below, so `Stat` can make it identical to absent. [proof: human: a reader confirms the storage layer distinguishes shredded from absent internally while the mount does not, since rule 7 is only as good as what is reported to it]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/vfs/... -race -run 'TestOpenForWriteFailsBeforeAnyWrite|TestDirectoryListingIsDerivedNotCached' -count=1 2>&1 | tee /tmp/adr014-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr014-t2a.out \
  && go build ./cmd/sdev1-mount/... 2>&1 | tee /tmp/adr014-t2b.out \
  && ! grep -qE "^FAIL|cannot find|undefined" /tmp/adr014-t2b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestOpenForWriteFailsBeforeAnyWrite` | `internal/core/vfs/mount_test.go` | The `open` callback itself refuses, so no handle is ever returned for writing and no program reaches a buffered `close` | — | S3 |
| `TestDirectoryListingIsDerivedNotCached` | `internal/core/vfs/mount_test.go` | An entity that appears after the mount is listed — the listing is derived on each call rather than taken once | — | S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The two tests above. |
| 2 — something selects it | `cmd/sdev1-mount` is the only caller of `Mount`; without the binary the grammar is a library nothing runs. |
| 3 — the caller can discover it | The kernel discovers it by mounting; every program that walks a directory then finds it. |
| 4 — it is used | `pending` — blocked on the evaluator. |

## Mutation Log

## Invariants

- No handle is ever returned for a write.
- A directory listing reflects the store at the time of the call.
- The tenant is never selectable from a path.

## Risks

- ⚠ **A FUSE binding that refuses writes at the write callback passes a naive test** — the open succeeds, the write fails, and the filesystem is read-only in every sense except the one that matters. The test asserts the open callback returned no handle.
- ⚠ **A listing test that only lists what existed at mount time cannot fail.** The test adds an entity AFTER the mount, which is the only shape that distinguishes derived from cached.
- Choosing a FUSE library is a portability decision, not a dependency bump: the platforms it supports become the platforms the mount supports, and that belongs in the record for whoever chooses.

## Stop Condition

Stop and ask before serving a directory listing that is not derived from the
store — an invented `/e` listing is indistinguishable from a real one to every
caller, including a backup that would then record it as truth.

## Out of Scope

- Writes (permanent: boundary: ADR-014 rule 3; POSIX cannot express that several attributes change together, so a writable mount breaks the entity transaction boundary silently)
- The evaluator (deferred: `docs/adr/BACKLOG.md` §20)
- Who may mount, and as which tenant (deferred: `docs/adr/BACKLOG.md` §11)

## Verification Log
