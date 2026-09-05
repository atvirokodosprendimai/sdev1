# Task ADR-045-T1: A request names a key, and a frame declares its bound

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `wire.Request`, `wire.EncodeRequest`, `wire.DecodeRequest`, `wire.MaxFrame`, `wire.WriteFrame`, `wire.ReadFrame`, `wire.ErrFrameTooLarge`, `wire.ErrNoFrameBound`
**Consumes:** `addr.Key` from ADR-001; `wire.Encode`/`Decode` and the refusal discipline from ADR-043
**Data dependency:** hermetic — encoding is a pure function, and framing is tested over an in-memory pipe
**Proof map:** v1
**Rests-on:** `a request carrying a key rather than a leaf identifier`, `an oversized frame being refused rather than allocated`, `a frame bound being declared rather than defaulted`

## Goal

Make a request say what it WANTS rather than where it thinks the answer lives,
and make a length field a stranger controls safe to read.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/wire/request.go` | add | `Request`, its codec, and the framing. |
| `internal/core/wire/request_test.go` | add | The tests below. |
| `internal/core/wire/doc.go` | modify | Why a request names a key, and why a length is a number a stranger chose. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestARequestNamesAKeyNotALeaf`, `TestAnOversizedFrameIsRefusedNotAllocated`, `TestAFrameBoundIsRequired`, `TestARequestRoundTripsThroughAStream`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Request` carrying an `addr.Key` and the statement text. ★It names the KEY: a node that does not hold the leaf can compute a redirect from a key and cannot from a leaf name, which is the whole of ADR-045 rule 1. [proof: mutation]
3. [S3] Encode and decode it with ADR-043's discipline — a version prefix refused when unknown, and trailing bytes refused rather than ignored. [proof: mutation]
4. [S4] Frame with a 4-byte big-endian length. ⚠`ReadFrame` REFUSES a length over the declared bound BEFORE allocating: the length is a number a stranger chose, and reading-then-allocating is how one packet exhausts a node. [proof: mutation]
5. [S5] Refuse a non-positive frame bound with `ErrNoFrameBound`. ⚠"No bound" and "not configured" are indistinguishable, and the safe-looking default is unbounded. [proof: mutation]
6. [S6] Document in `doc.go` why the request names a key — the reason is not obvious and the obvious alternative silently breaks ADR-008. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/wire/... -race -run 'TestARequestNamesAKeyNotALeaf|TestAnOversizedFrameIsRefusedNotAllocated|TestAFrameBoundIsRequired|TestARequestRoundTripsThroughAStream' -count=1 2>&1 | tee /tmp/adr045-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr045-t1a.out \
  && go test ./internal/core/wire/... ./internal/core/addr/... ./internal/core/routing/... -race -count=1 2>&1 | tee /tmp/adr045-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr045-t1b.out
```

The second command carries `addr` because a request names an `addr.Key` and a
change to what a key IS would change what a request means, and `routing` because
the key is what a redirect is computed from.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestARequestNamesAKeyNotALeaf` | `internal/core/wire/request_test.go` | A `Request` carries an `addr.Key` that round-trips byte-identically, and a node holding a DIFFERENT leaf can still descend that key to a leaf of its own — which is the redirect being computable at the receiver. ⚠ The second half is the point: a leaf name would give the receiver nothing to compute from | — | S2 |
| `TestAnOversizedFrameIsRefusedNotAllocated` | `internal/core/wire/request_test.go` | A frame header claiming more than the bound is `ErrFrameTooLarge`, and the claim is refused BEFORE the body is read — asserted by offering a header whose body never arrives, so a reader that allocated first would block or over-read instead of returning | — | S4 |
| `TestAFrameBoundIsRequired` | `internal/core/wire/request_test.go` | A zero or negative bound is `ErrNoFrameBound`; a frame exactly at the bound is accepted and one byte over is not | — | S5 |
| `TestARequestRoundTripsThroughAStream` | `internal/core/wire/request_test.go` | A request written to a pipe and read back is identical, and an unknown version or trailing bytes inside the frame are refused as ADR-043 refuses them | — | S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests, over pure codecs and an in-memory pipe. |
| 2 — something selects it | `ReadFrame`/`WriteFrame` are the only stream path; `DecodeRequest` the only way a request is read. |
| 3 — the caller can discover it | Two named sentinels, and the bound is a required argument with no usable zero. |
| 4 — it is used | T2's server and T3's client speak exactly this, over a real socket. |

## Mutation Log

- 2026-09-05 · fc8793f* · mutant killed · exit 1 · `internal/core/wire/request.go` · Carry only the top byte of the key — exactly as much as a depth-1 leaf identifier needs. If the test passes with that, it never checked that a WHOLE key travels, and the receiver would be descending a key it did not fully receive. · acceptance-sha256:674dc34976b44113e100b5d88a95867e0c5f1550d8cafc03508c945174f04277 · covers:a request carrying a key rather than a leaf identifier
- 2026-09-05 · fc8793f* · mutant killed · exit 1 · `internal/core/wire/request.go` · Keep the refusal, move it AFTER the allocation and the body read. The bound is still enforced and the sentinel is still returned for a complete oversized frame, so a test that only asserts ErrFrameTooLarge survives this untouched. Only a test that offers a header whose body never arrives can tell the two apart — and the allocation is the attack, not the oversized message. · acceptance-sha256:674dc34976b44113e100b5d88a95867e0c5f1550d8cafc03508c945174f04277 · covers:an oversized frame being refused rather than allocated
- 2026-09-05 · fc8793f* · mutant killed · exit 1 · `internal/core/wire/request.go` · Adopt the suggested bound when none was declared — the single most natural "helpful" change anyone would make to this function, and the one that turns a documented value into a default applied on an operators behalf. Nothing looks wrong afterwards: frames are still bounded, so only a test that asserts the REFUSAL of a non-positive bound notices that "not configured" stopped being distinguishable from "configured". · acceptance-sha256:674dc34976b44113e100b5d88a95867e0c5f1550d8cafc03508c945174f04277 · covers:a frame bound being declared rather than defaulted

## Invariants

- A request carries a key, never a leaf identifier.
- A length over the bound is refused before anything is allocated.
- A frame bound is declared, never defaulted.
- ADR-043's refusals apply inside the frame unchanged.

## Risks

- ⚠ **Naming the leaf is the tempting design** — the client resolved a route, so it has one. The test must show the receiver can compute a redirect from what it was given, which a leaf name does not permit.
- ⚠ **"Refused before allocating" is not shown by refusing.** A reader that allocates then errors returns the same error. The test offers a header whose body never arrives, so an eager reader blocks or over-reads instead of returning promptly.
- ⚠ **A bound that defaults to unbounded looks configured.** The refusal must be at construction, where a caller sees it, not at the first oversized frame.
- Framing and the request codec are separate concerns in one file; keep the refusals distinct so a mutant on one does not mask the other.

## Stop Condition

Stop and ask before putting a leaf identifier in a request. It is the one change
that makes ADR-008 rule 4 unimplementable, and it will look like an optimisation
because the client already knows the leaf.

## Out of Scope

- The server and the client (deferred: T2, T3)
- Serving writes (deferred: `docs/adr/BACKLOG.md` §19)
- TLS (deferred: `docs/adr/BACKLOG.md` §18)

## Verification Log
- 2026-09-05 · fc8793f* · exit 0 · `set -o pipefail …` · acceptance-sha256:674dc34976b44113e100b5d88a95867e0c5f1550d8cafc03508c945174f04277 · ms:3548
- 2026-09-05 · fc8793f* · exit 0 · `set -o pipefail …` · acceptance-sha256:674dc34976b44113e100b5d88a95867e0c5f1550d8cafc03508c945174f04277 · ms:3601
- 2026-09-05 · fc8793f* · exit 0 · `set -o pipefail …` · acceptance-sha256:674dc34976b44113e100b5d88a95867e0c5f1550d8cafc03508c945174f04277 · ms:3591
- 2026-09-05 · fc8793f* · exit 0 · `set -o pipefail …` · acceptance-sha256:674dc34976b44113e100b5d88a95867e0c5f1550d8cafc03508c945174f04277 · ms:3398
