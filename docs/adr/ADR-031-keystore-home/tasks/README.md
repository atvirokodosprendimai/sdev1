# ADR-031 Tasks

Implementation tasks for ADR-031: the keystore is a separately deletable store,
keys are never rotated, and a cache is evicted inside the shred. See the parent
ADR for the decision.

**Source of truth:** the task files' headers. This README is a derived index —
when it disagrees with a task file, the task file wins.

## Execution Order

One task.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Put the keys in a directory, and make destruction reach the cache | done | — | `go test ./internal/core/crypt/... -race -run '…five tests…'` then the search and subscribe suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `crypt.DirKeystore` | a node that holds keys (`BACKLOG.md` §18) | none within this record |

## Notes

- ★ **The interesting answer is one this project could not have given a week ago.**
  A keystore must be genuinely DELETABLE — and the storage engine built in
  ADR-024/026/029 is structurally the WRONG place for its own keys, because a
  sealed segment is immutable and compaction copies every fact forward. A key
  written there could never be destroyed. ⚠ "Reuse the leaf store" is the obvious
  move, it would pass every test in this repository, and it would silently make
  erasure impossible.
- ⚠ **The same argument rules out most embedded key-value stores**: they are
  log-structured, so a delete leaves the old version recoverable until a
  compaction nobody controls. The record's central promise would be false in a way
  the API does not reveal.
- ★ **One file per key, holding the key AND its subject.** ADR-007 says the
  mapping is destroyed with the key; this makes that a property of the filesystem
  — one `unlink` removes both, and there is no state where a key exists without
  its mapping.
- ⚠ **The key file is removed BEFORE the index entry, and the orders are not
  symmetric.** A crash after the key is gone leaves an index entry pointing at a
  destroyed handle: `Fetch` fails, the subject is erased, and the entry is litter.
  A crash the other way leaves a READABLE key that nothing resolves to — not
  erased at all, while looking erased.
- ⚠ **A cache is evicted INSIDE the destroy, before it returns.** "Eventually" is
  not good enough for an erasure: a cached key outlives its destruction, so a
  shredded subject stays readable wherever the key was cached.
- **Keys are never rotated.** Re-encrypting means enumerating every block a
  subject owns — the problem ADR-007's design exists to avoid, arriving by another
  door. Compromise is handled by shredding and re-ingesting.
- ⚠ **THE RULE NO CODE HERE CAN HOLD: the keystore must never share a backup with
  the data.** Restoring one that holds both puts the key back beside its
  ciphertext and undoes every erasure it contains. `BACKLOG.md` §17 calls it the
  single easiest way to get crypto-shredding wrong. It is a retention decision,
  and the only thing available is to say so as loudly as possible.
- ⚠ **"We delete the key" sounds like more than it is.** `unlink` removes a
  directory entry; a journal, a snapshot or a flash translation layer may keep the
  bytes. Media-level destruction needs full-disk encryption or an overwriting
  store, and it is stated as a permanent boundary rather than claimed.
