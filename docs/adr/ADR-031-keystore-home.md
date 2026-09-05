# ADR-031: The keystore is a separately deletable store, keys are never rotated, and a cache is evicted inside the shred

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-007-crypto-shredding.md`, `docs/adr/ADR-010-subscribe-and-purge.md`, `docs/adr/ADR-024-segment-store.md`, `docs/adr/ADR-026-leaf-store.md`, `docs/adr/ADR-029-compaction.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/crypt/dirkeystore.go`
**Enforced-by:** `internal/core/crypt/dirkeystore_test.go::TestADestroyedKeyIsGoneFromTheCacheAndTheDisk`
**Invalidates:** none — ADR-007 put the key in "a mutable keystore" and deliberately did not say where; `BACKLOG.md` §17 has carried the three questions since
**Served-path change:** Erasure survives a restart. Until now the only keystore was `MemoryKeystore`, which loses every key on exit — safe in the wrong direction, and unusable.

## Context

ADR-007 makes erasure the destruction of a per-subject key, and puts that key in a
mutable keystore deliberately separate from the immutable ciphertext. It does not
say where the keystore lives. `BACKLOG.md` §17 has carried three questions since,
and all three are now decidable.

★ **One of them has an answer this project could not have given a week ago, and
it is the interesting one.** A keystore must be genuinely DELETABLE. This system
now HAS a storage engine — and that storage engine is structurally the wrong place
for its own keys, because ADR-024 makes a sealed segment immutable and ADR-029
makes compaction preserve every fact. A key written there could never be
destroyed. ⚠ "Reuse the leaf store" is the obvious move, it would pass every test
in this repository, and it would silently make erasure impossible.

⚠ §17 also names the single easiest way to get crypto-shredding wrong, and it is
not a code question at all: **restoring a backup that holds the keystore beside
the data resurrects every key next to its ciphertext**, undoing every erasure the
backup contains.

## Existing Primitives Audit

- `internal/core/crypt` (ADR-007): supplies `Keystore`, `KeyID`, `Key`,
  `ErrKeyDestroyed`, `ErrUnknownKey` and `MemoryKeystore`. **Interface reused
  unchanged** — this record adds an implementation and no method. A wider
  interface would mean a second authority on whether a shredded subject is
  readable, which is what `Open` taking a keystore exists to prevent.
- `internal/core/leafstore` and `segstore` (ADR-024/026/029): **deliberately NOT
  used.** See rule 1 — immutability and compaction are exactly wrong here.
- ADR-010's purge (`subscribe.Shred`): **the caller.** It already drives
  destruction through the keystore; this record changes where the key was.
- An embedded key-value store: **none.** Most are log-structured, which keeps old
  versions — so a "delete" leaves the key recoverable, and the record's central
  promise would be false in a way nothing in the API reveals.

## Decision

**Keys live in their own directory, one file per key; destruction removes the file
and evicts the cache before returning; and keys are never rotated.**

1. **A keystore must be genuinely DELETABLE, which rules out this system's own
   storage.** ⚠ Segments are immutable and compaction preserves everything, so a
   key written to a leaf could never be destroyed. It also rules out a
   log-structured store that keeps old versions, and any store whose delete is a
   tombstone: a store that only marks a key deleted has destroyed nothing.

2. **One file per key, named by the handle, holding the key AND its subject.**
   ★ This is what makes ADR-007's "the mapping is destroyed with the key"
   structural rather than a thing to remember: one `unlink` removes both, and
   there is no state in which a key exists without its mapping.

3. **The key file is removed BEFORE the subject index entry.** ⚠ The orders are
   not symmetric. A crash after the key is gone leaves an index entry pointing at
   a destroyed handle — `Fetch` fails, so the subject is erased, and the entry is
   litter. A crash the other way leaves a READABLE key with no index, which is
   not erased at all while looking like it is.

4. **A cache is evicted INSIDE `Destroy`, before it returns, and a destroy that
   cannot evict FAILS.** ⚠ "Eventually" is not good enough for an erasure: a
   cached key outlives its destruction, so a shredded subject stays readable on
   whichever node happened to hold it. The invalidation is part of the shred, not
   beside it.

5. **Keys are never rotated.** Re-encrypting a subject under a new key means
   reading and rewriting every block it owns — the enumeration problem ADR-007
   exists to avoid, arriving by another door. ★ Compromise is handled by shredding
   the subject and re-ingesting it, which is the operation this system is already
   built around. Stated as a decision because §17 observed nobody had taken one.

6. ⚠ **The keystore must never share a backup, a snapshot or a replica with the
   data.** Restoring one that holds both puts the key back beside its ciphertext
   and undoes every erasure it contains. ★ This is a RETENTION decision, not a
   code one: no test in this repository can hold it, and it is written here
   because it is the single easiest way to get crypto-shredding wrong.

7. ⚠ **Removing a file does not destroy the bytes on the medium.** A journalling
   filesystem, a copy-on-write snapshot or a flash translation layer may keep
   them. The guarantee this package makes is at the level of its own API and the
   filesystem beneath it; media-level destruction needs full-disk encryption or an
   overwriting store, and it is an operational requirement rather than something
   code here can promise. Stated rather than implied, because "we delete the key"
   sounds like more than it is.

**What would falsify this.** A destroyed key still fetchable — from the cache that
served it a moment ago, or from a keystore reopened on the same directory. That is
the falsifier in `Enforced-by:`, and both halves are checkable with one directory.

## Alternatives Considered

- **Keep the key in the leaf beside the data it protects.** One store, one backup,
  one thing to operate. Rejected under rule 1 and rule 6, and it is the option
  most likely to be proposed: a sealed segment is immutable and compaction copies
  every fact forward, so the key could never be destroyed — and even if it could,
  the data's backup would carry it.
- **Use an embedded key-value store.** Mature, transactional, and it solves the
  index. Rejected in the audit: most are log-structured, so a delete leaves the old
  version recoverable until a compaction nobody controls. The record's central
  promise would be false in a way the API does not reveal.
- **Tombstone on destroy, and reclaim later.** It makes destruction O(1) and
  crash-safe. Rejected under rule 1: a tombstone has destroyed nothing, and the
  window between marking and reclaiming is a window in which the erasure has not
  happened while the system reports that it has.
- **Evict the cache asynchronously after the destroy returns.** Faster, and it is
  what a cache invalidation usually is. Rejected under rule 4: the gap is a period
  in which a shredded subject is still readable, and an erasure that is true
  eventually is not an erasure.
- **Remove the index entry first, then the key.** It leaves no danglers.
  Rejected under rule 3: the surviving state is a readable key that nothing
  resolves to, which is exactly "not erased" wearing the appearance of erased.
- **Support rotation by re-encrypting a subject's blocks.** It is what a key
  management story usually includes. Rejected under rule 5: it requires
  enumerating every block a subject owns, which is the problem ADR-007's whole
  design avoids — and shred-and-reingest already achieves the outcome.
- **Encrypt the key files under a master key.** It would make a stolen directory
  useless. Rejected as out of scope rather than wrong: it moves the problem to the
  master key's own home, and this record is about where a subject key lives and how
  it dies. Recorded as a follow-up.

## Component / Boundary Impact

No new component. `internal/core/crypt` gains a second `Keystore` implementation
beside `MemoryKeystore`, and the interface is unchanged — which is the point:
callers cannot tell which one they hold, and neither becomes a second authority on
whether a subject is readable.

⚠ The boundary: this decides where a key lives and how it is destroyed. It does
not encrypt anything, does not decide what a subject is, and does not know what a
segment is.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `crypt.DirKeystore` / `crypt.OpenDirKeystore` | new — keys in their own directory | T1 | callers, operators |
| `crypt.DirKeystore.Allocate` / `Fetch` / `Resolve` / `Destroy` | new — the `Keystore` interface, unchanged | T1 | callers |
| `crypt.DirKeystore.Shred` / `Shreds` | new — the audit trail, as `MemoryKeystore` has | T1 | callers |
| `crypt.ErrNotDeletable` | new sentinel — a directory that cannot be written or removed from | T1 | callers |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `crypt.DirKeystore` | T1 | a node that holds keys (`BACKLOG.md` §18) | No |

## Consequences

- **Positive:** Erasure survives a restart, and `BACKLOG.md` §17 closes.
- **Positive:** The one-file-per-key layout makes "the mapping dies with the key"
  a property of the filesystem rather than a thing to remember.
- **Positive:** Rotation is decided rather than pending, so nobody builds a
  re-encryption path that reintroduces enumeration.
- **Negative:** A `Fetch` is a file read when the cache misses. That is the cost of
  rule 4 — a cache that survived a destroy would be faster and wrong.
- **Negative:** The subject index accumulates dangling entries after a crash mid
  destroy. They are harmless by rule 3 and nothing reclaims them; a follow-up
  names it rather than leaving it to be discovered.
- **Negative:** Rule 6 is unenforceable here. It is an operational rule in a code
  repository, and the only thing this record can do is say so loudly.
- **Neutral:** `MemoryKeystore` stays, for tests and for a caller that genuinely
  wants everything gone at exit.

## Out of Scope

- Encrypting the key files under a master key (deferred: `docs/adr/BACKLOG.md` §17)
- Reclaiming dangling index entries after an interrupted destroy (deferred: `docs/adr/BACKLOG.md` §17)
- Replicating a keystore between nodes, and what a shred means across them (deferred: `docs/adr/BACKLOG.md` §18)
- Bounding the cache, or evicting for size (deferred: `docs/adr/BACKLOG.md` §24)
- Rotating a key (permanent: boundary: rule 5 — rotation requires enumerating every block a subject owns, which is the problem ADR-007's design exists to avoid, and shred-and-reingest reaches the same outcome)
- Destroying bytes on the physical medium (permanent: fact: `unlink` removes a directory entry and does not overwrite storage, so a journal, a snapshot or a flash translation layer may retain the bytes; citation: url https://pubs.opengroup.org/onlinepubs/9699919799)
- Keeping the keystore out of the data's backup (permanent: boundary: rule 6 is a retention decision taken by whoever operates the system, and no code in this repository can hold it)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The keystore is put in the leaf store | High — one store is simpler and it is right there | Critical — segments are immutable and compaction copies everything forward, so no key could ever be destroyed and erasure would silently stop working | Rule 1, stated first and argued from this project's own storage |
| A cache outlives a destroy | High — caching a key fetch is the obvious optimisation | Critical — a shredded subject stays readable wherever the key was cached | Rule 4, and the falsifier fetches from the same instance after destroying |
| The index entry is removed before the key | Med | Critical — a readable key that nothing resolves to is not erased, and looks erased | Rule 3, with the ordering argued from which crash state survives |
| A backup carries the keystore with the data | High — it is what a backup does | Critical — restoring undoes every erasure it contains, silently | Rule 6; unenforceable in code and therefore stated as loudly as possible |
| "We delete the key" is read as media destruction | Med | Med — a compliance claim stronger than the mechanism supports | Rule 7, stated as a permanent boundary with its citation |

## Rollback

Reverting means going back to `MemoryKeystore`, which loses every key on restart —
erasing everything. ⚠ That is safe in the wrong direction and it is not a rollback
anybody can take with data in the system, which is why the shape of the on-disk
layout is settled now rather than after a keystore has keys in it.

## Follow-ups

- [ ] Decide whether key files are encrypted under a master key (`BACKLOG.md` §17), and where THAT key lives — this record moves the problem rather than solving it, and says so.
- [ ] Decide what reclaims dangling index entries left by an interrupted destroy (`BACKLOG.md` §17); they are harmless and they accumulate, which is the same shape as ADR-029's orphaned inputs.
- [ ] When a transport exists (`BACKLOG.md` §18), decide what a shred means across nodes — rule 4 evicts one cache synchronously, and N caches is a different problem with the same deadline.
