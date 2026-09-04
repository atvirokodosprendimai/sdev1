# ADR-007 Tasks

Implementation tasks for ADR-007: Erase a subject by destroying its key, and
never let the ciphertext name who it belonged to. See the parent ADR for the
decision.

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
| T1 | The envelope, the cipher, and the handle that names nobody | done | — | `go test ./internal/core/crypt/... -race -run 'TestKeyHandleNamesNobody\|TestEverySeal\|TestSealOpen\|TestOpenResolves\|TestUnencryptedBytes\|TestTamperedCiphertext'` |
| T2 | The keystore, the destruction, and an audit record that names nobody | done | — | `go test ./internal/core/crypt/... -race -run 'TestShredded\|TestShreddingForgets\|TestShredRecord\|TestDestroyIs\|TestAllocateIsStable'` then the crypt and segment suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `crypt.KeyID`, `crypt.Key`, `crypt.Keystore`, the envelope, `crypt.Seal`, `crypt.Open` | T2 | T1 before T2 |
| T2 | `crypt.MemoryKeystore`, `crypt.ErrKeyDestroyed` | T1's `Open` resolves through the interface | T1 declares the interface, T2 implements it |

## Notes

- ⚠ **The ciphertext is permanent, and that is the constraint everything follows
  from.** Whatever is stored beside those bytes is stored forever, in a form
  nobody can erase. If the block says whose data it is, the subject's identity
  survives the erasure that was meant to remove it — an identifier IS personal
  data, and a permanent record that this person's data was deleted is very nearly
  the opposite of what was asked. Hence: an opaque, allocated handle, derived
  from nothing.
- ⚠ **The key lives in a mutable keystore and the ciphertext in immutable
  storage, and they are never the same place.** An immutable segment cannot give
  up a key it contains.
- ⚠ **A backup holding both the keystore and the data defeats the whole
  mechanism**, because restoring it resurrects the key beside the ciphertext.
  This is the single easiest way to get crypto-shredding wrong, and it is carried
  into ADR-010's obligations rather than left as folklore.
- **`Open` takes the keystore, not a key.** The envelope says which key; the
  keystore says whether you may still have it. A caller that could supply a key
  would be a second authority on whether a shredded subject is readable, and
  there would be as many authorities as callers. Same shape as ADR-005's
  `DecodeBlock` taking no configuration.
- **The envelope prefixes the ciphertext rather than widening a header.** ADR-005
  put a per-block `CipherID` there precisely so the cipher could be decided later
  without a format change, and this record uses that slot instead of spending a
  format version.
- ⚠ **`MemoryKeystore` is named for what it is.** In-memory means every key is
  lost on restart, erasing everything — safe in the wrong direction, and a
  catastrophe in production. Persistence is `BACKLOG.md` §17.
- Crypto-shredding cannot un-disclose what already leaked. It makes data
  unreadable going forward, and no mechanism does more than that.
