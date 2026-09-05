# ADR-043: A response is a closed tagged union, so a redirect cannot be read as an answer

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-005-segment-format.md`, `docs/adr/ADR-008-prefix-routing.md`, `docs/adr/ADR-025-datom-encoding.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/wire/**`
**Enforced-by:** `internal/core/wire/wire_test.go::TestARedirectCannotCarryAnAnswer`
**Invalidates:** none — ADR-008 establishes the property in Go's type system and says nothing about the wire; `BACKLOG.md` §18 names the risk and defers the format
**Served-path change:** None yet — there is no transport. This fixes the shape a response takes so that building one cannot silently lose ADR-008's central property.

## Context

ADR-008 rule 4 is the whole of that record: *"A stale route is answered with a
redirect, never with an error and never with data."* It is enforced in Go's type
system — `routing.Redirect` and `routing.Destination` are different types, and
`Redirect` carries nothing a caller could read as an answer.

⚠ **`BACKLOG.md` §18 names exactly how that property gets lost:** *"Whatever
carries a redirect must make it structurally impossible to mistake for a
successful answer. ADR-008 enforces that in Go's type system, and a wire format
that flattens both into one message shape would give the property back."*

★ **And the flattening is the DEFAULT outcome, not an unlikely one.** The ordinary
way to design this message is a struct with a payload and an optional redirect
field. Under every mainstream schema language a missing field decodes to a zero
value, so a client that receives a redirect and reads the payload gets an empty
successful answer — no error, no tag, nothing to notice. The stale route it was
being redirected away from has just served a result.

⚠ **This must be settled BEFORE a transport exists**, for the same reason ADR-032
settled the map's generation before callers existed. Afterwards there are wire
messages in flight and the change is a migration.

## Existing Primitives Audit

- `internal/core/routing` (ADR-008): supplies `Route`, `Redirect`, `Destination`
  and the epoch rule. **Reused as the meaning** — this record is how those cross a
  wire, not a second definition of them.
- `internal/core/datom` (ADR-025): supplies the datom run encoding, and the
  PRECEDENT for deciding a wire form with no transport. **Its rules are reused
  rather than restated**: a version prefix, a refusal on an unknown version, and
  nothing returned on any error. An answer's payload IS a datom run.
- `internal/core/segment` (ADR-005): supplies the block format. **Not touched** —
  a block is what is stored; this is what is said.
- A general RPC framework, or a schema language: **none.** See rule 6 — the
  property this record exists to keep is the one those make hardest to state.
- Framing, connections, timeouts: **deferred**, and explicitly. This decides what
  ONE response is, not how bytes arrive.

## Decision

**A response is exactly one of three outcomes, named by a required tag, and each
shape carries only what that outcome can mean.**

1. **Three outcomes and no others: ANSWER, REDIRECT, REFUSAL.** ★ They are
   ADR-008 rule 4's three, and the record is explicit that they are different
   answers: a stale route gets a redirect, *"never with an error and never with
   data"*. A format with two outcomes would have to fold one into another.

2. ⚠ **A REDIRECT frame has no payload field. Not empty — ABSENT.** ★ This is the
   whole record. An optional-and-empty payload is a field a caller can read, and
   what it reads is a successful empty answer. A field that does not exist in the
   shape cannot be read at all, which is how ADR-008's type-system property
   survives serialisation.

3. ⚠ **The tag is REQUIRED and its unknown values are REFUSED, never defaulted.**
   A decoder that treats an unrecognised outcome as "probably an answer" has
   rebuilt the flattening through the forward-compatibility door. This is ADR-025's
   unknown-version rule, and it is the same rule for the same reason.

4. ⚠ **Trailing bytes after any frame are a REFUSAL.** ★ This is what makes rule 2
   hold on the wire rather than only in the struct. "Ignore what you do not
   understand" is precisely how a payload smuggles itself into a redirect: append
   bytes, and a permissive decoder hands back a redirect while a tolerant caller
   finds data after it. ADR-025 already refuses trailing bytes; this is the same
   refusal at the frame level.

5. **A REDIRECT carries the route's EPOCH.** ⚠ Without it a redirect cannot be
   ordered, and ADR-008 rule 5 is what stops two stale nodes redirecting a client
   to each other forever. A wire form that dropped the epoch would keep the
   redirect and lose the loop protection, which is worse than losing both.

6. ⚠ **No optional fields anywhere in the envelope.** ★ Optionality is the
   mechanism by which rule 2 fails, so it is refused as a construct rather than
   avoided case by case. It is also why this record does not adopt a schema
   language whose wire model makes every field optional by default.

7. **Decoding returns NOTHING on any error.** ADR-025's rule. A partially decoded
   response is worse than none, because the part that decoded looks usable.

**What would falsify this.** A redirect that round-trips into something a caller
can obtain payload bytes from — including by appending bytes to the frame and
having them survive. That is the falsifier in `Enforced-by:`.

## Alternatives Considered

- **One message with a payload and an optional redirect field.** The obvious
  design, and what a schema language produces by default. Rejected under rules 2
  and 6: a missing field decodes to a zero value, so a client that receives a
  redirect and reads the payload gets an empty successful answer with nothing to
  notice — the exact property ADR-008 exists to hold.
- **A status code plus a payload, like HTTP.** Familiar, and the code is a tag.
  Rejected under rule 2: the payload field still exists on every response, so a
  redirect can carry one and a caller can read it. The tag being right does not
  help when the field is there to be read.
- **Two outcomes — answer and error — with redirect as a kind of error.**
  Simpler. Rejected under rule 1 and ADR-008 rule 4: refusing makes every topology
  change a fleet-wide outage, and a client that retries an error does not learn
  the new route. A redirect repairs the client; an error does not.
- **Adopt protobuf/Thrift/Cap'n Proto.** Mature, and nobody hand-writes a codec.
  Rejected under rule 6 for this envelope specifically: their wire models make
  fields optional and unknown fields ignorable, which are the two mechanisms rules
  2, 3 and 4 exist to refuse. ⚠ This is not a general rejection — it is a claim
  about the ONE message where those defaults are dangerous.
- **Tolerate trailing bytes for forward compatibility.** It is the standard way to
  extend a format. Rejected under rule 4: it is exactly how a payload reaches a
  redirect. Extension goes through the version prefix, which is refused when
  unknown rather than ignored.
- **Decide framing and connection handling here too.** It would make the transport
  buildable. Rejected as a different decision: this fixes what ONE response means,
  and that is the part §18 says must not be got wrong. Framing has no
  correctness property at stake that is lost by waiting.

## Component / Boundary Impact

One new component, `internal/core/wire`, owning one thing: what a response looks
like as bytes.

⚠ The boundary: it does not move bytes, hold a connection, or know what a request
is. It encodes and decodes one response, and it is testable with no network for
exactly that reason — which is the same reason `internal/core/segment` refuses to
touch a filesystem.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `wire.Outcome` / `wire.OutcomeAnswer` / `OutcomeRedirect` / `OutcomeRefusal` | new — the closed set | T1 | callers |
| `wire.Response` | new — the sealed interface, exactly three implementations | T1 | callers |
| `wire.Answer` / `wire.Redirect` / `wire.Refusal` | new — one shape per outcome | T1 | callers |
| `wire.Encode` / `wire.Decode` | new | T1 | a transport (`BACKLOG.md` §18) |
| `wire.FormatVersion` | new — refused when unknown | T1 | callers |
| `wire.ErrUnknownOutcome` / `ErrUnknownVersion` / `ErrTrailingBytes` / `ErrShortFrame` | new sentinels | T1 | callers |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `wire.Response`, `wire.Encode`, `wire.Decode` (T1) | T1 | a transport (`BACKLOG.md` §18) | No |

## Consequences

- **Positive:** ADR-008's central property survives the step that would otherwise
  have destroyed it, and it is fixed before there are messages in flight.
- **Positive:** A redirect physically cannot carry data — not by convention, but
  because the shape has nowhere to put it.
- **Positive:** The rules are ADR-025's, reused rather than re-argued: version
  prefix, refuse unknown, refuse trailing, return nothing on error.
- **Negative:** ⚠ A hand-written codec is code somebody must maintain, and this
  record rejects the frameworks that would remove that work. The rejection is
  scoped to this one message and stated as such — nothing here says the rest of a
  transport must be hand-written.
- **Negative:** ⚠ **Nothing sends or receives one.** There is no transport, so
  this is a shape with no wire, tested against itself. Recorded rather than
  implied, and it is the same position ADR-025 was in.
- **Negative:** Rule 6 forbids optional fields, so every future extension is a
  version bump rather than an added field. That is deliberate and it is not free.

## Out of Scope

- Framing, connections, timeouts, and how a request names its leaf (deferred: `docs/adr/BACKLOG.md` §18 — this decides what one response IS, which is the part §18 warns must not be got wrong)
- How a route reaches a node — gossip, control plane, or derived from the map (deferred: `docs/adr/BACKLOG.md` §18 — and §18 is right that it is a performance decision, since ADR-008 is correct under all of them)
- When a node may forget a route it no longer serves (deferred: `docs/adr/BACKLOG.md` §18 — a separate decidable question about route lifetime, not about response shape)
- What a REQUEST looks like (deferred: `docs/adr/BACKLOG.md` §18 — a request carries no property equivalent to rule 2, so it can wait for the transport that shapes it)
- Compression or encryption of a frame (deferred: `docs/adr/BACKLOG.md` §18)
- Choosing a serialisation framework for anything other than this envelope (permanent: boundary: rule 6's rejection is about the one message whose meaning their defaults destroy, and says nothing about the rest)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The envelope becomes a struct with an optional redirect | **High — it is what every schema language produces by default** | **Critical** — a redirect decodes as an empty successful answer, so a stale route serves a result with nothing to notice | Rules 2 and 6, and it is the record's falsifier |
| An unknown tag is treated as an answer | High — "be liberal in what you accept" | Critical — the flattening arrives through forward compatibility instead of through the schema | Rule 3, refusing by name |
| Trailing bytes are ignored | **High — it is the standard extension mechanism** | Critical — it is how a payload reaches a redirect frame | Rule 4, with a test that appends bytes to a redirect |
| The epoch is dropped from a redirect | Med — it looks like routing metadata rather than payload | High — the redirect survives and ADR-008's loop protection does not, which is worse than losing both | Rule 5 |
| A framework is adopted for this message | Med — hand-writing a codec is unattractive | Critical — optional fields and ignorable unknowns are its defaults, and they are the two things rules 2–4 refuse | Rule 6, scoped explicitly to this envelope |
| Nothing ever sends one | Med — there is no transport | Low now, High later — a shape nothing uses drifts from what a transport needs | Recorded in Consequences, with a follow-up to check it against the first real sender |

## Rollback

Nothing sends or receives a response, so removing this removes a shape rather than
a behaviour. ⚠ The cost of rollback is not symmetric with the cost of delay: after
a transport exists there are messages in flight, and rule 2 becomes a migration
instead of a decision.

## Follow-ups

- [ ] When a transport exists (`BACKLOG.md` §18), check rule 2 against the first real sender — an envelope nothing has used is a shape nobody has stress-tested, and the pressure to add "just one optional field" arrives with the first feature.
- [ ] Decide when a node may forget a route it no longer serves (`BACKLOG.md` §18's third bullet). It is decidable now and is a separate question: forgetting too early turns a redirect back into an error for the slowest client, so the bound is a declared age rather than a guess.
- [ ] If a serialisation framework is later adopted for other messages, re-read rule 6 before extending it to this one — the objection is specific and it does not generalise.
