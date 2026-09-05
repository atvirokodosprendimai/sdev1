# Task ADR-031-T1: Put the keys in a directory, and make destruction reach the cache

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `crypt.DirKeystore`, `crypt.OpenDirKeystore`, `crypt.DirKeystore.Allocate`, `crypt.DirKeystore.Fetch`, `crypt.DirKeystore.Resolve`, `crypt.DirKeystore.Destroy`, `crypt.DirKeystore.Shred`, `crypt.DirKeystore.Shreds`, `crypt.ErrNotDeletable`
**Consumes:** `crypt.Keystore`, `crypt.KeyID`, `crypt.Key`, `crypt.NewKey`, `crypt.NewKeyID`, `crypt.ErrKeyDestroyed`, `crypt.ErrUnknownKey`, `crypt.ShredRecord` from ADR-007
**Data dependency:** hermetic — a directory the test owns
**Proof map:** v1
**Rests-on:** `a destroyed key gone from the cache that just served it`, `a destroyed key gone from the disk across a restart`, `the mapping destroyed with the key rather than beside it`, `a directory that cannot be written to being refused at open`

## Goal

Make erasure survive a restart, and make it reach the cache before it returns.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/crypt/dirkeystore.go` | add | `DirKeystore`, its layout, and the ordered destroy. |
| `internal/core/crypt/dirkeystore_test.go` | add | The tests below, against a real directory. |

★ A real directory, not a filesystem abstraction. The claims are about what
survives a restart and what an `unlink` leaves, and an abstraction would be
asserting the abstraction.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestADestroyedKeyIsGoneFromTheCacheAndTheDisk`, `TestAKeyOutlivesTheProcess`, `TestTheMappingDiesWithTheKey`, `TestADanglingIndexEntryIsNotAReadableKey`, `TestAnUnwritableDirectoryIsRefused`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Lay out one file per key, named by the handle, holding the key AND its subject. ★One `unlink` then removes both, so "the mapping dies with the key" is a property of the filesystem rather than a thing to remember. [proof: mutation]
3. [S3] Add a subject index — one small file per subject, named by a hash of it, holding the handle — so `Resolve` is a lookup rather than a scan of every key. [proof: mutation]
4. [S4] Implement `Destroy` to remove the KEY FILE FIRST and the index entry second. ⚠The orders are not symmetric: a crash after the key is gone leaves harmless litter, and a crash the other way leaves a readable key nothing resolves to — not erased, while looking erased. [proof: human: a reader confirms `os.Remove(d.keyPath(id))` precedes `os.Remove(d.subjectPath(...))`. ⚠**No test can see this ordering** — absent a crash both orders end with both files gone, and separating them needs a failure injected between the two removals. What IS proven is the consequence: the state the chosen order can leave behind is safe, by `TestADanglingIndexEntryIsNotAReadableKey`]
5. [S5] Evict the cache INSIDE `Destroy`, before it returns, and fail the destroy if the eviction cannot happen. ⚠A cached key outlives its destruction, so a shredded subject stays readable wherever it was cached. [proof: mutation]
6. [S6] Make `Resolve` treat an index entry whose key file is gone as UNKNOWN, so the litter S4 can leave never reads as a live subject. [proof: mutation]
7. [S7] Refuse a directory that cannot be written to OR removed from, with `ErrNotDeletable`, at OPEN. ⚠A keystore that cannot delete is not a keystore — it is a place erasure silently fails. ★The WRITE probe is mutation-proven. The REMOVE probe is not, and cannot be: on POSIX a directory that permits creating a file permits unlinking it, so the case it guards — an append-only or WORM mount, which is exactly the dangerous one for a keystore — is unconstructible in a unit test on an ordinary filesystem. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/crypt/... -race -run 'TestADestroyedKeyIsGoneFromTheCacheAndTheDisk|TestAKeyOutlivesTheProcess|TestTheMappingDiesWithTheKey|TestADanglingIndexEntryIsNotAReadableKey|TestAnUnwritableDirectoryIsRefused' -count=1 2>&1 | tee /tmp/adr031-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr031-t1a.out \
  && go test ./internal/core/crypt/... ./internal/core/search/... ./internal/core/subscribe/... -race -count=1 2>&1 | tee /tmp/adr031-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr031-t1b.out
```

The second command re-runs the packages that hold a keystore: search seals every
posting under one, and ADR-010's purge drives destruction through one.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestADestroyedKeyIsGoneFromTheCacheAndTheDisk` | `internal/core/crypt/dirkeystore_test.go` | A key is fetched (so it is certainly cached), destroyed, and then refused by the SAME instance and by one reopened on the same directory — **the falsifier ADR-031 names in `Enforced-by:`**. Both halves matter: the first is the cache, the second is the disk, and an implementation can get either alone right | — | S2, S5 |
| `TestAKeyOutlivesTheProcess` | `internal/core/crypt/dirkeystore_test.go` | A key allocated by one keystore is fetched, byte-identical, by another opened on the same directory — the thing `MemoryKeystore` cannot do, and the reason erasure was previously safe in the wrong direction | — | S2 |
| `TestTheMappingDiesWithTheKey` | `internal/core/crypt/dirkeystore_test.go` | After a destroy, `Resolve` does not know the subject, in the same instance and after a reopen — so nothing durable still binds the identity to the ciphertext | — | S2, S4, S6 |
| `TestADanglingIndexEntryIsNotAReadableKey` | `internal/core/crypt/dirkeystore_test.go` | With the key file removed and the index entry left behind — the state a crash mid-destroy leaves — `Resolve` reports unknown and `Fetch` on the recorded handle is `ErrKeyDestroyed`. ⚠ The state is built by deleting the file directly, so it is the real surviving state rather than a mock | — | S4, S6 |
| `TestAnUnwritableDirectoryIsRefused` | `internal/core/crypt/dirkeystore_test.go` | Two shapes are `ErrNotDeletable` at open: a path that cannot be created at all, and an existing directory that cannot be WRITTEN to. ⚠ The remove probe's own case is not covered and cannot be — see S7 — so the test skips under root, where permissions are bypassed and it would pass vacuously | — | S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above, against a real directory. |
| 2 — something selects it | `DirKeystore` satisfies `crypt.Keystore`, asserted at compile time, so anything holding a keystore can hold this one. |
| 3 — the caller can discover it | The interface is unchanged, and the refusals are ADR-007's existing sentinels plus one named at open. |
| 4 — it is used | ⚠ **Nothing constructs one yet.** The session builds a `MemoryKeystore`, and choosing a keystore for a deployment needs a configuration surface this record does not add. Recorded rather than implied. |

## Mutation Log

- 2026-09-05 · 20fc48e* · mutant killed · exit 1 · `internal/core/crypt/dirkeystore.go` · stops evicting the cache inside Destroy, so a key that was fetched before its destruction stays readable on this instance — an erasure that is true only for whoever had not looked yet · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · covers:a destroyed key gone from the cache that just served it
- 2026-09-05 · 20fc48e* · mutant killed · exit 1 · `internal/core/crypt/dirkeystore.go` · leaves the key file on disk, so a keystore reopened on the same directory serves the key again and the erasure did not survive a restart · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · covers:a destroyed key gone from the disk across a restart
- 2026-09-05 · 20fc48e* · mutant killed · exit 1 · `internal/core/crypt/dirkeystore.go` · stops checking that the key file still exists, so the litter a crash mid-destroy leaves resolves to a live subject and an erased identity reads as present · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · covers:the mapping destroyed with the key rather than beside it
- 2026-09-05 · 20fc48e* · mutant survived · exit 0 · `internal/core/crypt/dirkeystore.go` · stops proving the directory can be removed from at open, so a keystore that can write but never delete is accepted — and the failure surfaces at the moment somebody exercises their erasure right · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · covers:the key file removed before the subject index
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-05 · 20fc48e* · mutant killed · exit 1 · `internal/core/crypt/dirkeystore.go` · stops evicting the cache inside Destroy, so a key fetched before its destruction stays readable on this instance — an erasure true only for whoever had not looked yet · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · covers:a destroyed key gone from the cache that just served it
- 2026-09-05 · 20fc48e* · mutant killed · exit 1 · `internal/core/crypt/dirkeystore.go` · leaves the key file on disk, so a keystore reopened on the same directory serves the key again and the erasure did not survive a restart · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · covers:a destroyed key gone from the disk across a restart
- 2026-09-05 · 20fc48e* · mutant killed · exit 1 · `internal/core/crypt/dirkeystore.go` · stops checking the key file still exists, so the litter a crash mid-destroy leaves resolves to a live subject and an erased identity reads as present · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · covers:the mapping destroyed with the key rather than beside it
- 2026-09-05 · 20fc48e* · mutant killed · exit 1 · `internal/core/crypt/dirkeystore.go` · stops proving the directory can be written at open, so an unusable keystore is accepted and the failure surfaces when somebody exercises their erasure right · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · covers:a directory that cannot be written to being refused at open

## Invariants

- A destroyed key is unfetchable from the cache and from the disk.
- The subject mapping is destroyed with the key.
- The key file is removed before the index entry.
- A directory that cannot delete is refused at open.

## Risks

- ⚠ **A destroy test that only reopens the store misses the cache**, and one that only refetches from the same instance misses the disk. An implementation can pass either alone. The falsifier does both, and fetches BEFORE destroying so the key is certainly cached.
- ⚠ **The dangling-entry test must build the state by deleting a file**, not by calling a helper that pretends to crash. The point is that the real surviving state is safe.
- ⚠ **`ErrNotDeletable` at open is worth more than a failure at destroy.** A keystore that discovers it cannot delete at the moment somebody exercises their erasure right has already failed.
- ⚠ **Nothing in this task can hold the backup rule.** ADR-031 rule 6 — the keystore must never share a backup with the data — is a retention decision, and the only thing available here is to state it. It is in the record and in the package comment, and no test claims it.
- ⚠ **THE REMOVE PROBE IS UNFALSIFIABLE HERE, and a mutant found that rather than a person.** Deleting the probe's `os.Remove` check SURVIVED the suite — correctly, because on POSIX any directory that permits creating a file permits unlinking it, so no ordinary filesystem produces the write-but-not-delete case. It is kept because that case is real on an append-only or WORM mount, which is precisely the mount a keystore must refuse, and it is recorded as unprovable rather than propped up.
- Removing a file does not destroy the bytes on the medium. Stated as a permanent boundary on the parent record rather than tested, because no test in this repository can observe it.

## Stop Condition

Stop and ask before putting keys in a leaf, a segment, or any store built on
them. Sealed segments are immutable and compaction copies every fact forward, so
a key written there could never be destroyed — and every test in this repository
would still pass.

## Out of Scope

- Encrypting key files under a master key (deferred: `docs/adr/BACKLOG.md` §17)
- Reclaiming dangling index entries (deferred: `docs/adr/BACKLOG.md` §17)
- Choosing a keystore for a deployment, and the configuration to do it (deferred: `docs/adr/BACKLOG.md` §18)
- Bounding the cache (deferred: `docs/adr/BACKLOG.md` §24)
- Keeping the keystore out of the data's backup (permanent: boundary: it is a retention decision taken by whoever operates the system, and no code here can hold it)

## Verification Log
- 2026-09-05 · 20fc48e* · exit 0 · `set -o pipefail …` · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · ms:3891
- 2026-09-05 · 20fc48e* · exit 0 · `set -o pipefail …` · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · ms:3911
- 2026-09-05 · 20fc48e* · exit 0 · `set -o pipefail …` · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · ms:3827
- 2026-09-05 · 20fc48e* · exit 0 · `set -o pipefail …` · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · ms:3893
- 2026-09-05 · 20fc48e* · exit 0 · `set -o pipefail …` · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · ms:3854
- 2026-09-05 · 20fc48e* · exit 0 · `set -o pipefail …` · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · ms:3983
- 2026-09-05 · 20fc48e* · exit 0 · `set -o pipefail …` · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · ms:3819
- 2026-09-05 · 20fc48e* · exit 0 · `set -o pipefail …` · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · ms:3792
- 2026-09-05 · 20fc48e* · exit 0 · `set -o pipefail …` · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · ms:3901
- 2026-09-05 · 20fc48e* · exit 0 · `set -o pipefail …` · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · ms:3964
- 2026-09-05 · 20fc48e* · exit 0 · `set -o pipefail …` · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · ms:3914
- 2026-09-05 · 20fc48e* · exit 0 · `set -o pipefail …` · acceptance-sha256:b6a5d84f30c9f53cdd0016b0798f3f9534d16bf0f27d779cc1655d5d9b3edc89 · ms:4009
