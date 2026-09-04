# Task ADR-007-T2: The keystore, the destruction, and an audit record that names nobody

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `crypt.MemoryKeystore`, `crypt.ShredRecord`, `crypt.ErrKeyDestroyed`, `crypt.ErrUnknownKey`
**Consumes:** `crypt.KeyID`, `crypt.Key`, `crypt.Keystore`, `crypt.Open` (T1)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `a destroyed key making previously readable blocks unreadable`, `destruction removing the subject-to-handle mapping as well as the key`, `the audit record carrying no subject`

## Goal

Make erasure an act rather than a sweep: destroy one key, and every block that
subject ever wrote becomes unreadable at once, everywhere.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/crypt/keystore.go` | add | `MemoryKeystore`, `ShredRecord`, and the two sentinels. |
| `internal/core/crypt/keystore_test.go` | add | The tests below, including the falsifier named in ADR-007's `Enforced-by:`. |

★ `MemoryKeystore` is deliberately in-memory and deliberately not persistent.
Where a keystore actually lives is `BACKLOG.md` §17 and it is a bigger question
than this record: an in-memory store erases everything on restart, which is safe
in the wrong direction and would be a catastrophe in production. Naming it
`MemoryKeystore` rather than `Keystore` is part of the decision.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestShreddedSubjectIsUnreadable`, `TestShreddingForgetsTheSubjectMapping`, `TestShredRecordNamesNoSubject`, `TestDestroyIsIdempotentAndFinal`, `TestAllocateIsStableForOneSubject`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Implement `MemoryKeystore`: allocate a handle and a key per subject, fetch by handle, resolve subject to handle.
3. [S3] Make `Allocate` stable — one subject gets one handle for as long as it is not destroyed — so a subject's blocks share a key and die together.
4. [S4] Implement `Destroy(KeyID)`: remove the key AND the subject-to-handle mapping, and return `ErrKeyDestroyed` from every later `Fetch` of that handle. ★Removing only the key would leave a durable record binding a handle to a subject, which is the identifier this record exists to remove.
5. [S5] Distinguish `ErrUnknownKey` from `ErrKeyDestroyed`. ★A handle that was never issued and one that was destroyed are different facts, and an operator asking "was this erased?" needs the difference. Note the tension recorded in the Risks: the answer itself is information.
6. [S6] Make `Destroy` idempotent and final — a second call succeeds and nothing resurrects a key.
7. [S7] Implement `ShredRecord` as `{handle, when, request}` and NOTHING else. [proof: human: a reader confirms the struct has no subject field, no derived field, and no comment inviting one to be added]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/crypt/... -race -run 'TestShredded|TestShreddingForgets|TestShredRecord|TestDestroyIs|TestAllocateIsStable' -count=1 2>&1 | tee /tmp/adr007-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr007-t2a.out \
  && go test ./internal/core/crypt/... ./internal/core/segment/... -race -count=1 2>&1 | tee /tmp/adr007-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr007-t2b.out
```

The first command is this task's own work and can carry the verdict alone; the
second is the regression half, including T1's envelope tests and the segment
format this envelope claims a cipher identifier from.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestShreddedSubjectIsUnreadable` | `internal/core/crypt/keystore_test.go` | Blocks sealed and verified readable become unreadable after the key is destroyed, and no plaintext is recoverable from the stored bytes. **The falsifier ADR-007 names in `Enforced-by:`** | — | S4 |
| `TestShreddingForgetsTheSubjectMapping` | `internal/core/crypt/keystore_test.go` | After destruction the keystore cannot resolve the subject to its handle, so no durable record binds an identity to the permanent ciphertext | — | S4 |
| `TestShredRecordNamesNoSubject` | `internal/core/crypt/keystore_test.go` | A shred record's fields carry the handle, the time and the request reference — and a reflective check over the struct proves no field holds the subject, so a later addition fails rather than passing silently | — | S7 |
| `TestDestroyIsIdempotentAndFinal` | `internal/core/crypt/keystore_test.go` | A second destruction succeeds, a destroyed handle reports `ErrKeyDestroyed` rather than `ErrUnknownKey`, and re-allocating the subject issues a DIFFERENT handle that cannot read the old blocks | — | S5, S6 |
| `TestAllocateIsStableForOneSubject` | `internal/core/crypt/keystore_test.go` | Repeated allocation for one subject returns one handle, so a subject's blocks share a key and are erased together rather than one at a time | — | S2, S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | `MemoryKeystore` is the only implementation of the interface T1's `Open` accepts, so every decryption in the suite goes through it; deleting `Destroy` breaks `TestShreddedSubjectIsUnreadable`. |
| 3 — the caller can discover it | Exported doc comments and two named sentinels; the type's NAME says it is in-memory, which is the warning a caller needs before shipping it. |
| 4 — it is used | Nothing measures this yet; no request path exists, and ADR-010 owns it. |

## Mutation Log

- 2026-09-04 · 9b30287* · mutant killed · exit 1 · `internal/core/crypt/keystore.go` · makes the shred record an audit entry and destroy nothing, so a subject reported as erased stays fully readable — an erasure that returns success while erasing nothing is the worst outcome this package can produce · acceptance-sha256:8272cb67f3d958adbe089c10bba15f351e5e2ec5dff0c50aae3ace2aa47d905a · covers:a destroyed key making previously readable blocks unreadable
- 2026-09-04 · 9b30287* · mutant killed · exit 1 · `internal/core/crypt/keystore.go` · destroys the key but keeps the subject-to-handle mapping, leaving a durable record that binds an identity to ciphertext which lasts forever — the un-erasable identifier this record exists to prevent · acceptance-sha256:8272cb67f3d958adbe089c10bba15f351e5e2ec5dff0c50aae3ace2aa47d905a · covers:destruction removing the subject-to-handle mapping as well as the key
- 2026-09-04 · 9b30287* · mutant killed · exit 1 · `internal/core/crypt/keystore.go` · adds a subject field to the durable audit trail, which is the plausible-sounding change that voids the whole record: the trail outlives the erasure and would then name the person it erased · acceptance-sha256:8272cb67f3d958adbe089c10bba15f351e5e2ec5dff0c50aae3ace2aa47d905a · covers:the audit record carrying no subject

## Invariants

- Destroying a key removes the subject-to-handle mapping in the same operation.
- A destroyed handle is never resurrected, and a re-allocated subject gets a new handle.
- A shred record has no subject field and no field derived from one.
- `ErrKeyDestroyed` and `ErrUnknownKey` are distinct.
- Nothing here authorizes an erasure; it only performs one.

## Risks

- ⚠ **Distinguishing "destroyed" from "never existed" is itself information.** Answering "was this handle erased?" confirms the handle once existed. It is kept because an operator proving compliance needs it and the handle names nobody, but it is a deliberate disclosure rather than an oversight, and any future external surface must not expose it unauthenticated.
- A test asserting "the subject is no longer readable" can pass because the test never made it readable. `TestShreddedSubjectIsUnreadable` asserts the block WAS readable first, so the assertion is about the destruction rather than about a fixture that never worked.
- ⚠ `TestShredRecordNamesNoSubject` uses reflection over the struct rather than checking a known field list. A hand-written list passes when a field is ADDED, which is the exact change that breaks the guarantee.
- An in-memory keystore loses every key on restart, erasing everything. That is safe in the wrong direction and it is why the type's name says so; persistence is `BACKLOG.md` §17.

## Stop Condition

Stop and ask before adding any field to `ShredRecord`, or any way to recover a
destroyed key. Both are reasonable-sounding requests that void the record: the
first can reintroduce the identifier, and the second means the erasure was never
one.

## Out of Scope

- Who may request an erasure (deferred: `docs/adr/BACKLOG.md` §11)
- The fan-out of an erasure request to backups and other sinks (permanent: boundary: ADR-010 owns it, with per-sink acknowledgement; this task makes the bytes unreadable and claims nothing about who else was told)
- Keystore persistence, rotation and caching (deferred: `docs/adr/BACKLOG.md` §17)

## Verification Log
- 2026-09-04 · 9b30287* · exit 0 · `set -o pipefail …` · acceptance-sha256:8272cb67f3d958adbe089c10bba15f351e5e2ec5dff0c50aae3ace2aa47d905a · ms:3968
- 2026-09-04 · 9b30287* · exit 0 · `set -o pipefail …` · acceptance-sha256:8272cb67f3d958adbe089c10bba15f351e5e2ec5dff0c50aae3ace2aa47d905a · ms:3996
- 2026-09-04 · 9b30287* · exit 0 · `set -o pipefail …` · acceptance-sha256:8272cb67f3d958adbe089c10bba15f351e5e2ec5dff0c50aae3ace2aa47d905a · ms:3875
- 2026-09-04 · 9b30287* · exit 0 · `set -o pipefail …` · acceptance-sha256:8272cb67f3d958adbe089c10bba15f351e5e2ec5dff0c50aae3ace2aa47d905a · ms:3920
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:8272cb67f3d958adbe089c10bba15f351e5e2ec5dff0c50aae3ace2aa47d905a · ms:4060
