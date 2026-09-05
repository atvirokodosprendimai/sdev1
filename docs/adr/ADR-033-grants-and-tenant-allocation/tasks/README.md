# ADR-033 Tasks

Implementation tasks for ADR-033: a grant is a datom, authorization reads only the
present, and a tenant identifier is never reused. See the parent ADR for the
decision.

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
| T1 | Make authorizing against the past unwritable | done | — | `go test ./internal/core/authz/... -race -run '…five tests…'` then the ports, leafstore and temporal suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `authz.Set`, `authz.Set.Allow` | a served path with a caller identity (`BACKLOG.md` §18/§25) | none within this record |

## Notes

- ⚠ **THE TRAP, and `BACKLOG.md` §11 named it before there was anything to trap.**
  This is a bitemporal store, so a caller can ask what was true last March. The
  obvious extension is to authorize that question against the grants in force last
  March — the data is historical, so why not the permissions. ★ **Revoking access
  today would then leave the revoked party able to read last year, forever**, and
  the system would report the revocation as successful.
- ★ **So the deciding function TAKES NO INSTANT.** The signature is the
  enforcement: a caller cannot authorize against the past because there is nothing
  to ask with. Making it unwritable is worth more than a rule people remember,
  because the tempting version looks more principled, not less.
- ★ **The audit question stays answerable, by a DIFFERENT function that returns
  records rather than a decision.** "Who had access in March" is legitimate and
  useful; what it returns cannot be mistaken for permission because it is not a
  yes or a no.
- **A grant is a DATOM in reserved tenant `0000`.** It costs nothing and buys
  bitemporality, ordering, the transaction boundary, and revocation-as-retraction.
  ⚠ The reserved tenant can never be allocated: a tenant able to hold the grants
  could grant itself anything.
- ⚠ **No grant means REFUSED.** A default of permission fails open exactly when
  the thing that would say no is unreachable.
- ⚠ **A tenant identifier is NEVER reused.** A reused one inherits whatever of the
  previous tenant's subtree remains — data marked but not yet swept, ciphertext
  still in a coded stripe. Reuse would require proving the subtree holds nothing
  readable, which is the enumeration problem ADR-007's design exists to avoid.
- ⚠ **So the identifier space is a finite budget: 65,536 for the life of a
  deployment**, and creating-then-destroying tenants consumes it permanently.
  Widening the prefix changes every key ever computed, so it cannot be
  retrofitted. A deployment that will churn tenants needs to know this at the
  start rather than at the end.
- ⚠ **Nothing calls `Allow` yet.** The language carries no caller identity and
  there is no transport, so this decides the RULE and leaves the enforcement POINT
  to whatever gains a caller.
