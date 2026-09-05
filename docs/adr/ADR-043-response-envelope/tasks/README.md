# ADR-043 Tasks

Implementation tasks for ADR-043: a response is a closed tagged union, so a
redirect cannot be read as an answer. See the parent ADR for the decision.

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
| T1 | Three outcomes, no optional fields, and nowhere for a redirect to put data | done | — | four envelope tests, then the wire, routing and datom suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `wire.Response`, `wire.Encode`, `wire.Decode` | a transport (`BACKLOG.md` §18) | none within this record |

## Notes

- ★ **ADR-008 rule 4 is the whole of that record:** *"A stale route is answered
  with a redirect, never with an error and never with data."* It is held in Go's
  type system — `routing.Redirect` and `routing.Destination` are different types.
- ⚠ **`BACKLOG.md` §18 names exactly how it gets lost:** *"a wire format that
  flattens both into one message shape would give the property back."*
- ★ **And the flattening is the DEFAULT outcome, not an unlikely one.** The
  ordinary design is a struct with a payload and an optional redirect field. Under
  every mainstream schema language a missing field decodes to a zero value — so a
  client that receives a redirect and reads the payload gets an empty SUCCESSFUL
  answer, with no error and nothing to notice. The stale route it was being sent
  away from has just served a result.
- ⚠ **So a redirect has no payload field: absent, not empty.** A field that does
  not exist cannot be read at all, which is how the type-system property survives
  serialisation.
- ⚠ **Three refusals make that true on the WIRE rather than only in the struct:**
  an unknown outcome tag, an unknown version, and TRAILING BYTES. The last is the
  important one — "ignore what you do not understand" is precisely how a payload
  smuggles itself into a redirect. ADR-025 already refuses trailing bytes; this is
  the same refusal one level up.
- ⚠ **A redirect carries its route's EPOCH.** Without it a redirect cannot be
  ordered, and ADR-008 rule 5 is what stops two stale nodes bouncing a client
  between them forever. Dropping the epoch keeps the redirect and loses the loop
  protection, which is worse than losing both.
- ★ **This is settled BEFORE a transport exists**, the same move ADR-032 made for
  the map's generation and ADR-025 for the datom run. Afterwards there are messages
  in flight and rule 2 is a migration rather than a decision.
- ⚠ **Nothing sends or receives one** (`BACKLOG.md` §18 — no transport). A shape
  with no wire, tested against itself.
