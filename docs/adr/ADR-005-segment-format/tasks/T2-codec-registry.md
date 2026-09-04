# Task ADR-005-T2: The codec registry, and the round trip through the pipeline

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `segment.Codec`, `segment.CodecID`, `segment.RegisterCodec`, `segment.EncodeBlock`, `segment.DecodeBlock`, `segment.ErrUnknownCodec`
**Consumes:** `segment.BlockHeader`, the checksum (T1)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the codec being resolved from the header rather than from configuration`, `the refusal of an unregistered codec`

## Goal

Encode and decode a block through the recorded pipeline, resolving the codec from
the block's own header so that a reader needs no configuration to read what a
writer produced.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/segment/codec.go` | add | `Codec`, `CodecID`, the registry, and the built-in identity codec. |
| `internal/core/segment/block.go` | add | `EncodeBlock` and `DecodeBlock`, applying the pipeline and its inverse. |
| `internal/core/segment/segment_test.go` | edit | The tests below. |

★ The registry exists so the format does not depend on any one compression
library being linked in. A block written with the identity codec is readable by a
build that has no compressor at all, and a build meeting a codec it does not have
refuses by name rather than returning wrong bytes.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestEncodeDecodeRoundTrips`, `TestDecodeResolvesCodecFromHeader`, `TestUnregisteredCodecIsRefused`, `TestDecodeVerifiesTheChecksum`, `TestCodecIdentityIsAlwaysAvailable`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `CodecID` and the `Codec` interface — compress and decompress over byte slices, nothing more, so a codec cannot reach the filesystem or the header.
3. [S3] Implement the registry, with the identity codec registered unconditionally so that a build with no compression dependency can still read and write blocks.
4. [S4] Implement `EncodeBlock`: apply the codec, record the codec identifier and both lengths in the header, checksum the STORED bytes, and return header plus body.
5. [S5] Implement `DecodeBlock`: verify the checksum FIRST, then resolve the codec from the header and apply its inverse. ★Verifying before decoding matters — handing rotten bytes to a decompressor produces a confusing failure at best and plausible garbage at worst.
6. [S6] Refuse an unregistered codec with `ErrUnknownCodec` naming the identifier, rather than returning the stored bytes as though they were the value.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/segment/... -run 'TestEncode|TestDecode|TestUnregistered|TestCodec|TestBlock|TestCorrupt|TestUnknown|TestHeader|TestStage' -count=1 2>&1 | tee /tmp/adr005-t2.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr005-t2.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestEncodeDecodeRoundTrips` | `internal/core/segment/segment_test.go` | Property test: encode then decode returns the original bytes, across generated payloads and every registered codec | — | S4, S5 |
| `TestDecodeResolvesCodecFromHeader` | `internal/core/segment/segment_test.go` | A block encoded under one codec decodes correctly with NO configuration supplied — the property that keeps a settings change from reinterpreting stored data | — | S5 |
| `TestUnregisteredCodecIsRefused` | `internal/core/segment/segment_test.go` | A header naming a codec this build lacks yields `ErrUnknownCodec` naming it, rather than the stored bytes returned as the value | — | S6 |
| `TestDecodeVerifiesTheChecksum` | `internal/core/segment/segment_test.go` | A flipped bit is caught BEFORE the codec runs, so rotten bytes never reach a decompressor | — | S5 |
| `TestCodecIdentityIsAlwaysAvailable` | `internal/core/segment/segment_test.go` | The identity codec is registered without any dependency, so a minimal build can still read and write; and a codec's interface is compress/decompress over byte slices alone, so it cannot reach the header or the filesystem | — | S2, S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | `EncodeBlock` and `DecodeBlock` are the only path to a block's bytes, and every test above goes through them. No production caller exists yet, which is stated rather than implied. |
| 3 — the caller can discover it | Exported doc comments and a named sentinel; a codec is registered by name, and `go doc ./internal/core/segment` lists what a build has. |
| 4 — it is used | Nothing measures this yet. |

## Mutation Log

- 2026-09-04 · 17256f5* · mutant killed · exit 1 · `internal/core/segment/block.go` · resolves the codec from a fixed choice instead of from the block own header, which is exactly the failure the format exists to prevent: a block written under one codec reinterpreted under another · acceptance-sha256:5972e96398551361054d424854f73896f0048bc209142960fbae0def54b50888 · covers:the codec being resolved from the header rather than from configuration
- 2026-09-04 · 17256f5* · mutant killed · exit 1 · `internal/core/segment/codec.go` · falls back to the identity codec instead of refusing, so a block this build cannot read returns its stored compressed bytes to the caller as though they were the value · acceptance-sha256:5972e96398551361054d424854f73896f0048bc209142960fbae0def54b50888 · covers:the refusal of an unregistered codec

## Invariants

- The codec is resolved from the block's header. `DecodeBlock` takes no configuration argument, so it cannot consult one.
- The checksum is verified before the codec runs.
- The identity codec is always registered; a build with no compression dependency can still read and write blocks.
- An unregistered codec is refused by name, never silently treated as identity.

## Risks

- A registry is global mutable state, and two packages registering the same identifier would be a collision nobody sees. Registration refuses a duplicate rather than overwriting, so the failure is at startup rather than at read time.
- The identity codec makes "no compression" a first-class codec rather than an absent one. That is deliberate: an absent codec would have to be represented by a zero value, and a zero value is exactly the configuration-shaped assumption this record rejects.

## Stop Condition

Stop and ask before adding a codec that requires cgo. The house preference is a
pure-Go build, and a codec that breaks it changes how the whole project ships
rather than just what a block contains.

## Out of Scope

- Encryption, which is the pipeline's second stage (permanent: boundary: ADR-007 owns the cipher and its keys; this task records that a cipher stage exists and leaves it unimplemented)
- Erasure coding, the third stage (permanent: boundary: ADR-006 owns it)
- Choosing a codec per write from the query language (permanent: boundary: ADR-011 owns the clause; this task makes the choice expressible by recording it per block)

## Verification Log
- 2026-09-04 · 17256f5* · exit 0 · `set -o pipefail …` · acceptance-sha256:5972e96398551361054d424854f73896f0048bc209142960fbae0def54b50888 · ms:836
- 2026-09-04 · 17256f5* · exit 0 · `set -o pipefail …` · acceptance-sha256:5972e96398551361054d424854f73896f0048bc209142960fbae0def54b50888 · ms:650
- 2026-09-04 · 17256f5* · exit 0 · `set -o pipefail …` · acceptance-sha256:5972e96398551361054d424854f73896f0048bc209142960fbae0def54b50888 · ms:653
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:5972e96398551361054d424854f73896f0048bc209142960fbae0def54b50888 · ms:678
