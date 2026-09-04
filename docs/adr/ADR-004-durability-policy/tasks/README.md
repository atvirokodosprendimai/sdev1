# ADR-004 Tasks

Implementation tasks for ADR-004: Express durability as a per-tier policy over a
declared failure domain, with a refusal floor. See the parent ADR for the
decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` /
`Covers` headers. This README is a derived index — when it disagrees with a task
file, the task file wins and the README must be regenerated.

## Execution Order

Two tasks, sequential.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | The policy type and its construction-time refusals | done | — | `go test ./internal/core/durability/... -run 'TestPolicy\|TestReplicated\|TestCoded\|TestTier'` |
| T2 | Feasibility against a map, and the runtime floor | done | — | `go test ./internal/core/durability/... -run 'TestValidate\|TestSatisfied\|TestPolicy\|TestReplicated\|TestCoded\|TestTier'` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `durability.Policy`, `durability.Tier`, `Policy.DomainsNeeded()` | T2 | T1 before T2 |

## Notes

- The record's central arithmetic is worth restating for anyone executing these:
  **an erasure code needs at least `k+m` independent failure domains at the level
  it is meant to survive.** A (8,2) code across ten racks survives two rack
  losses at 25% overhead; the same code across three servers survives nothing at
  the server level, however its fragments are arranged. The number of failure
  domains BOUNDS what coding can buy, and no configuration escapes it.
- ⚠ **Two is a durability floor and not a consensus floor.** Two voting members
  give a quorum of two, so losing either stops writes — a bare pair is LESS
  available than a single node while being more durable. The minimum viable
  live-tier shape is two data replicas plus one witness that votes and stores no
  data. This surprises people and the record says so rather than letting them
  find out.
- ⚠ When adding a test during implementation, check its name is SELECTED by the
  fence's `-run` filter before running any mutant. A falsifier beside the command
  rather than inside it proves nothing, and it produced several spurious
  surviving mutants earlier in this corpus.
