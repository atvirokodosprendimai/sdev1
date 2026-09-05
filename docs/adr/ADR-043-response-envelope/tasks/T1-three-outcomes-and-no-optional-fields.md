# Task ADR-043-T1: Three outcomes, no optional fields, and nowhere for a redirect to put data

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `wire.Outcome`, `wire.OutcomeAnswer`, `wire.OutcomeRedirect`, `wire.OutcomeRefusal`, `wire.Response`, `wire.Answer`, `wire.Redirect`, `wire.Refusal`, `wire.Encode`, `wire.Decode`, `wire.FormatVersion`, `wire.ErrUnknownOutcome`, `wire.ErrUnknownVersion`, `wire.ErrTrailingBytes`, `wire.ErrShortFrame`
**Consumes:** `routing.Route` from ADR-008; the encoding discipline of `datom.Encode`/`Decode` from ADR-025
**Data dependency:** hermetic — encoding is a pure function over bytes
**Proof map:** v1
**Rests-on:** `a redirect having nowhere to carry payload bytes`, `an unknown outcome tag being refused rather than defaulted`, `trailing bytes after a frame being refused rather than ignored`, `a redirect carrying its route epoch`

## Goal

Fix the shape of a response so that building a transport cannot silently undo
ADR-008 rule 4 — and so that the three ways it would be undone are refusals rather
than conventions.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/wire/doc.go` | add | Why a redirect has no payload field, and why optionality is refused as a construct. |
| `internal/core/wire/wire.go` | add | `Outcome`, `Response` and its three implementations, `Encode`, `Decode`. |
| `internal/core/wire/wire_test.go` | add | The tests below. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestARedirectCannotCarryAnAnswer`, `TestAnUnknownOutcomeIsRefused`, `TestTrailingBytesAreRefused`, `TestARedirectCarriesItsEpoch`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Declare `Response` as a SEALED interface with exactly three implementations, so a type switch over the outcomes is exhaustive by construction and a fourth cannot be added from outside. [proof: mutation]
3. [S3] ⚠Give `Redirect` NO payload field — absent, not empty. ★An optional-and-empty payload is a field a caller can read, and what it reads is a successful empty answer; a field that does not exist cannot be read at all. [proof: mutation]
4. [S4] Refuse an unknown outcome tag with `ErrUnknownOutcome`, and an unknown version with `ErrUnknownVersion`. ⚠A decoder that guesses "probably an answer" has rebuilt the flattening through forward compatibility. [proof: mutation]
5. [S5] Refuse trailing bytes with `ErrTrailingBytes`. ★This is what makes S3 hold on the WIRE rather than only in the struct: append bytes to a redirect and a permissive decoder hands one back while a tolerant caller finds data after it. [proof: mutation]
6. [S6] Carry the route's EPOCH in a redirect. ⚠Without it a redirect cannot be ordered, and ADR-008 rule 5 is what stops two stale nodes redirecting a client to each other forever — keeping the redirect and losing the epoch is worse than losing both. [proof: mutation]
7. [S7] Return NOTHING on any decode error, as ADR-025 does. A partially decoded response is worse than none, because the part that decoded looks usable. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/wire/... -race -run 'TestARedirectCannotCarryAnAnswer|TestAnUnknownOutcomeIsRefused|TestTrailingBytesAreRefused|TestARedirectCarriesItsEpoch' -count=1 2>&1 | tee /tmp/adr043-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr043-t1a.out \
  && go test ./internal/core/wire/... ./internal/core/routing/... ./internal/core/datom/... -race -count=1 2>&1 | tee /tmp/adr043-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr043-t1b.out
```

The second command carries `routing` because the redirect's meaning is ADR-008's
and a change to `Route` would change what this encodes, and `datom` because an
answer's payload is a datom run whose encoding rules this one deliberately copies.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestARedirectCannotCarryAnAnswer` | `internal/core/wire/wire_test.go` | **The falsifier ADR-043 names in `Enforced-by:`.** A redirect round-trips to a `*Redirect` and never to an `*Answer`; the type carries no payload field; and appending payload bytes to a redirect frame is REFUSED rather than yielding a redirect a caller can read data after. ⚠ The third assertion is the one that matters — the first two are true of the struct, and only the third is true of the wire | — | S3, S5 |
| `TestAnUnknownOutcomeIsRefused` | `internal/core/wire/wire_test.go` | An unrecognised outcome tag is `ErrUnknownOutcome` and yields NO response — not a default, not an answer. Same for an unknown version. ★ "Be liberal in what you accept" is how the flattening arrives after the schema has been got right | — | S4, S7 |
| `TestTrailingBytesAreRefused` | `internal/core/wire/wire_test.go` | Every outcome refuses a frame with bytes appended, and refuses it identically. ⚠ Tested for ALL THREE, because refusing only on a redirect would leave the standard extension mechanism available on the shapes next to it | — | S5 |
| `TestARedirectCarriesItsEpoch` | `internal/core/wire/wire_test.go` | A redirect round-trips its route's epoch, prefix and next hops in order. ★ The epoch especially: without it ADR-008's loop protection is gone while the redirect still looks correct | — | S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests, over encode/decode round trips. |
| 2 — something selects it | `Decode` is the only way in and it dispatches on the tag; `Response` is sealed, so the three are all there are. |
| 3 — the caller can discover it | Four named sentinels, and the three shapes are the API — a caller cannot construct a redirect with a payload. |
| 4 — it is used | ⚠ **Nothing sends or receives one.** There is no transport (`BACKLOG.md` §18), so this is a shape with no wire, tested against itself — the same position ADR-025 was in, and for the same reason: the format has to be right before any byte moves. Recorded rather than implied. |

## Mutation Log

- 2026-09-05 · b21dc19* · mutant killed · exit 1 · `internal/core/wire/wire.go` · ignores bytes appended after a redirect, which is the standard forward-compatibility gesture and is exactly how a payload smuggles itself into a redirect — a permissive decoder hands back a redirect while a tolerant caller reads the data that follows, so the stale route the client was being sent away from has served it a result · acceptance-sha256:df75d3662480a18a6fd18bc4814ced81aa32b8f89a40128f128e2401a96a1ef0 · covers:a redirect having nowhere to carry payload bytes
- 2026-09-05 · b21dc19* · mutant killed · exit 1 · `internal/core/wire/wire.go` · treats an unrecognised outcome tag as an empty answer — "be liberal in what you accept" — so a redirect from a future version decodes as a successful empty result and the flattening arrives through forward compatibility rather than through the schema · acceptance-sha256:df75d3662480a18a6fd18bc4814ced81aa32b8f89a40128f128e2401a96a1ef0 · covers:an unknown outcome tag being refused rather than defaulted
- 2026-09-05 · b21dc19* · mutant killed · exit 1 · `internal/core/wire/wire.go` · refuses trailing bytes on a redirect but ignores them on an ANSWER, leaving the standard extension mechanism open on the shape beside it — so a payload can still reach a client through a frame that a stale node meant as something else · acceptance-sha256:df75d3662480a18a6fd18bc4814ced81aa32b8f89a40128f128e2401a96a1ef0 · covers:trailing bytes after a frame being refused rather than ignored
- 2026-09-05 · b21dc19* · mutant killed · exit 1 · `internal/core/wire/wire.go` · drops the route's epoch on the wire, so a redirect still redirects and nothing fails immediately — what fails is ADR-008 rule 5's loop protection, later, under exactly the stale-view conditions redirecting exists for: two nodes with opposing views bounce a client between them and each redirect looks as authoritative as the last · acceptance-sha256:df75d3662480a18a6fd18bc4814ced81aa32b8f89a40128f128e2401a96a1ef0 · covers:a redirect carrying its route epoch

## Invariants

- A redirect has no payload field.
- An unknown tag or version is refused, never defaulted.
- Trailing bytes are refused, on every outcome.
- A decode error returns no response.

## Risks

- ⚠ **The falsifier must test the WIRE, not the struct.** "A `Redirect` has no payload field" is true of the Go type and proves nothing about a decoder that ignores what it does not understand. The assertion that matters is appending bytes to a redirect frame and requiring a refusal.
- ⚠ **Trailing bytes must be refused on ALL THREE outcomes.** Refusing only on a redirect leaves the standard extension mechanism open on the shapes beside it, and a payload can reach a client through an answer frame that was supposed to be a redirect.
- ⚠ **An unknown tag must yield NO response.** Returning a zero-valued answer alongside the error is the flattening with an error attached, and callers that check the value before the error — which is most of them — see an empty success.
- ⚠ **The epoch is easy to treat as metadata and drop.** A redirect without one still redirects, so nothing fails immediately; what fails is the loop protection, later, under exactly the stale-view conditions redirecting exists for.
- Nothing sends one, so this task fixes a shape and adds no behaviour. Recorded on the parent record.

## Stop Condition

Stop and ask before adding an optional field to any of the three shapes — most of
all a payload on a redirect. That single field is how ADR-008's property is lost,
and it will be proposed as a convenience rather than as a change of meaning.

## Out of Scope

- Framing, connections, timeouts (deferred: `docs/adr/BACKLOG.md` §18)
- What a request looks like (deferred: `docs/adr/BACKLOG.md` §18)
- When a node may forget a route (deferred: `docs/adr/BACKLOG.md` §18)
- Compression or encryption of a frame (deferred: `docs/adr/BACKLOG.md` §18)

## Verification Log
- 2026-09-05 · b21dc19* · exit 0 · `set -o pipefail …` · acceptance-sha256:df75d3662480a18a6fd18bc4814ced81aa32b8f89a40128f128e2401a96a1ef0 · ms:3491
- 2026-09-05 · b21dc19* · exit 0 · `set -o pipefail …` · acceptance-sha256:df75d3662480a18a6fd18bc4814ced81aa32b8f89a40128f128e2401a96a1ef0 · ms:3554
- 2026-09-05 · b21dc19* · exit 0 · `set -o pipefail …` · acceptance-sha256:df75d3662480a18a6fd18bc4814ced81aa32b8f89a40128f128e2401a96a1ef0 · ms:3357
- 2026-09-05 · b21dc19* · exit 0 · `set -o pipefail …` · acceptance-sha256:df75d3662480a18a6fd18bc4814ced81aa32b8f89a40128f128e2401a96a1ef0 · ms:3314
- 2026-09-05 · b21dc19* · exit 0 · `set -o pipefail …` · acceptance-sha256:df75d3662480a18a6fd18bc4814ced81aa32b8f89a40128f128e2401a96a1ef0 · ms:3342
