# Task ADR-007-T1: The envelope, the cipher, and the handle that names nobody

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `crypt.KeyID`, `crypt.Key`, `crypt.Keystore`, `crypt.CipherAES256GCM`, `crypt.EnvelopePrefixSize`, `crypt.Seal`, `crypt.Open`, `crypt.KeyIDOf`, `crypt.NewKeyID`, `crypt.ErrNotEncrypted`
**Consumes:** `segment.CipherID` from ADR-005
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the key handle carrying nothing derived from the subject`, `a fresh nonce for every seal`, `decryption resolving its key through the keystore rather than from an argument`

## Goal

Make a subject's bytes readable only through a key held elsewhere, and make the
bytes stored beside them say nothing about who the subject is.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/crypt/doc.go` | add | Package comment: why erasure is key destruction, why the ciphertext is permanent and therefore must name nobody, and how it fails and recovers. |
| `internal/core/crypt/crypt.go` | add | `KeyID`, `Key`, the `Keystore` interface, `CipherAES256GCM`, and the envelope layout. |
| `internal/core/crypt/envelope.go` | add | `Seal`, `Open`, `KeyIDOf`, and the AES-256-GCM implementation. |
| `internal/core/crypt/crypt_test.go` | add | The tests below. |

★ The `Keystore` interface is declared HERE and implemented in T2, because
`Open` is the consumer — accept interfaces at the point of use. It is also why
`Open` cannot take a key: the caller would then be the authority on whether a
shredded subject is still readable, and there would be as many authorities as
callers.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestKeyHandleNamesNobody`, `TestEverySealDrawsAFreshNonce`, `TestSealOpenRoundTrips`, `TestOpenResolvesItsKeyThroughTheKeystore`, `TestUnencryptedBytesAreRefused`, `TestTamperedCiphertextIsRefused`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `KeyID` as 16 opaque bytes and `NewKeyID` as a draw from a cryptographic source. ★It must be ALLOCATED, never derived. A handle computed from an entity identifier is a permanent, confirmable label for that identifier sitting beside permanent ciphertext.
3. [S3] Define `Key` as 256 bits, and the `Keystore` interface: allocate, fetch, resolve, destroy.
4. [S4] Define the envelope: `handle ‖ nonce ‖ ciphertext‖tag`, with `EnvelopePrefixSize` fixed, and `CipherAES256GCM` as the `segment.CipherID` that claims this layout. ★It prefixes the ciphertext rather than widening ADR-005's block header, so nothing already written is reinterpreted and no format version changes.
5. [S5] Implement `Seal`: draw a fresh random nonce per call, encrypt with AES-256-GCM, and write the envelope. ★A deterministic nonce is rejected — reuse under one key is catastrophic for GCM, and determinism makes safety depend on nothing ever re-encoding a block at the same coordinates.
6. [S6] Implement `Open(ks Keystore, sealed []byte)`: read the handle from the envelope, fetch the key through the keystore, and decrypt. It takes NO key argument.
7. [S7] Implement `KeyIDOf`, and refuse bytes too short to hold an envelope with `ErrNotEncrypted` rather than reading past them.
8. [S8] Write the package comment stating why the ciphertext is permanent and what that forces. [proof: human: a reader confirms the comment explains why an identifier stored beside un-erasable bytes is itself un-erasable personal data, not merely that handles are random]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/crypt/... -race -run 'TestKeyHandleNamesNobody|TestEverySeal|TestSealOpen|TestOpenResolves|TestUnencryptedBytes|TestTamperedCiphertext' -count=1 2>&1 | tee /tmp/adr007-t1.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr007-t1.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestKeyHandleNamesNobody` | `internal/core/crypt/crypt_test.go` | Two allocations for one subject differ, no subject bytes appear in a handle, and handles over many subjects are distinct — so a permanent handle beside permanent ciphertext identifies no one | — | S2 |
| `TestEverySealDrawsAFreshNonce` | `internal/core/crypt/crypt_test.go` | Sealing identical plaintext under one key many times yields distinct nonces and distinct ciphertexts, so nonce reuse cannot occur through repetition | — | S5 |
| `TestSealOpenRoundTrips` | `internal/core/crypt/crypt_test.go` | Property test over generated plaintexts and sizes: seal then open returns the original bytes | — | S4, S5, S6 |
| `TestOpenResolvesItsKeyThroughTheKeystore` | `internal/core/crypt/crypt_test.go` | `Open` reads the handle from the envelope and obtains the key from the keystore, taking no key argument — so a caller cannot supply one and a keystore refusal is final | — | S3, S6 |
| `TestUnencryptedBytesAreRefused` | `internal/core/crypt/crypt_test.go` | Bytes too short to hold an envelope, or carrying no envelope, yield `ErrNotEncrypted` rather than being read past | — | S7 |
| `TestTamperedCiphertextIsRefused` | `internal/core/crypt/crypt_test.go` | A flipped bit anywhere in the envelope yields an error rather than plaintext — GCM is authenticated, so a wrong key or altered bytes fail closed instead of producing plausible garbage | — | S5, S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The six tests above. |
| 2 — something selects it | `CipherAES256GCM` is the `segment.CipherID` a block header records, and it is the only value that claims this envelope layout; `Open` is the only path from stored bytes back to plaintext. |
| 3 — the caller can discover it | Exported doc comments and two named sentinels; `Open`'s signature states that the keystore, not the caller, decides readability. |
| 4 — it is used | Nothing measures this yet; no storage engine exists. |

## Mutation Log

- 2026-09-04 · 9b30287* · mutant killed · exit 1 · `internal/core/crypt/crypt.go` · stops drawing the handle from a cryptographic source so every handle is identical and predictable, which is the same failure as deriving one from the subject: a predictable handle beside permanent ciphertext is a confirmable label nobody can erase · acceptance-sha256:97db313eadda7b30cf03193c1221ba9408a1be00709e367de2af51ebd3b8cc8f · covers:the key handle carrying nothing derived from the subject
- 2026-09-04 · 9b30287* · mutant killed · exit 1 · `internal/core/crypt/envelope.go` · reuses one nonce for every seal under a key, which loses GCM confidentiality and integrity together and is the single most damaging mistake available in this package · acceptance-sha256:97db313eadda7b30cf03193c1221ba9408a1be00709e367de2af51ebd3b8cc8f · covers:a fresh nonce for every seal
- 2026-09-04 · 9b30287* · mutant killed · exit 1 · `internal/core/crypt/envelope.go` · swallows the keystore refusal so a read proceeds past it, which makes the keystore advisory rather than the authority and lets a shredded subject fail differently for every caller · acceptance-sha256:97db313eadda7b30cf03193c1221ba9408a1be00709e367de2af51ebd3b8cc8f · covers:decryption resolving its key through the keystore rather than from an argument

## Invariants

- A key handle is allocated from a cryptographic source and derived from nothing.
- A fresh nonce is drawn for every seal.
- `Open` takes no key argument; the keystore is the only authority on readability.
- The envelope prefixes the ciphertext; no header defined by ADR-005 is widened.
- Nothing in this package persists a key beside a ciphertext.

## Risks

- ⚠ A test that seals twice and compares ciphertexts would pass even with a fixed nonce if the plaintexts differed. `TestEverySealDrawsAFreshNonce` seals IDENTICAL plaintext under one key, which is the only shape that isolates the nonce.
- A handle test that only checks two handles differ would pass for a counter, which is derivable and predictable. The test also asserts no subject bytes appear in the handle and that handles are unpredictable across many subjects.
- GCM's authentication means a wrong key and altered bytes fail identically. That is desirable here and it also means this package cannot distinguish "shredded" from "corrupt" — T2's keystore is what names the first, and the record says so rather than leaving a caller to guess.

## Stop Condition

Stop and ask before storing anything derived from the subject anywhere in the
envelope, including for debugging. The whole record turns on the stored bytes
naming nobody, and a diagnostic field is the most likely way that is lost.

## Out of Scope

- The keystore implementation, destruction, and the audit record — that is T2.
- Where the keystore is persisted, rotation, and caching (deferred: `docs/adr/BACKLOG.md` §17)
- Anything that opens a file (deferred: `docs/adr/BACKLOG.md` §12)

## Verification Log
- 2026-09-04 · 9b30287* · exit 0 · `set -o pipefail …` · acceptance-sha256:97db313eadda7b30cf03193c1221ba9408a1be00709e367de2af51ebd3b8cc8f · ms:2114
- 2026-09-04 · 9b30287* · exit 0 · `set -o pipefail …` · acceptance-sha256:97db313eadda7b30cf03193c1221ba9408a1be00709e367de2af51ebd3b8cc8f · ms:2155
- 2026-09-04 · 9b30287* · exit 0 · `set -o pipefail …` · acceptance-sha256:97db313eadda7b30cf03193c1221ba9408a1be00709e367de2af51ebd3b8cc8f · ms:2039
- 2026-09-04 · 9b30287* · exit 0 · `set -o pipefail …` · acceptance-sha256:97db313eadda7b30cf03193c1221ba9408a1be00709e367de2af51ebd3b8cc8f · ms:2128
- 2026-09-04 · 9b30287* · exit 0 · `set -o pipefail …` · acceptance-sha256:97db313eadda7b30cf03193c1221ba9408a1be00709e367de2af51ebd3b8cc8f · ms:2066
