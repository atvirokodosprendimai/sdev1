# ADR-013 Tasks

Implementation tasks for ADR-013: The agent tool surface is a projection of the
query language, not a second way in. See the parent ADR for the decision.

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
| T1 | The declared tools, what they compile to, and the tenant a call cannot name | done | — | `go test ./internal/core/mcpsurface/... -race -run 'TestEveryToolCompilesToAQuery\|TestTenantComesFromTheSessionNotTheCall\|TestAnUnboundSessionIsRefusedNotDefaulted\|TestARefusalIsNotAnError\|TestARefusalNamesTheToolAndTheReason\|TestAToolWithoutATimeArgumentIsRefused\|TestMutationNamesAreRefusedAtRegistration\|TestDescriptionCarriesTheRefusals\|TestStandardToolsAreRegistered'` then the ql and addr suites |
| T2 | Serve the surface over MCP | pending | — | `go test ./internal/core/mcpsurface/... -race -run 'TestServedToolListIsTheRegistry\|TestARefusalIsServedAsAResultNotAProtocolError\|TestEveryServedToolCarriesItsRefusalsInItsDescription'` then `go build ./cmd/sdev1-mcp/...` |

Status: `pending` | `partial` | `blocked` | `done`.

⚠ **T2 is `pending` on two things that do not exist**, and saying so is the point
rather than an apology. It needs a query evaluator (`BACKLOG.md` §20, itself on a
storage engine, §12) and the MCP SDK dependency (§25). ★That is not the same as
ADR-013 being unfinished: the decision — every tool compiles to a query, the
tenant comes from the session, a refusal is a value — is settled and T1's mutants
prove it. What waits is delivery, not meaning.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `mcpsurface.Registry`, `mcpsurface.Compiled`, `mcpsurface.Describe`, `mcpsurface.Standard` | T2 | T1 before T2 |

## Notes

- ⚠ **A tool that reaches past the language is a second query surface.** ADR-011
  pinned the two-axis defaults table, the unbound-versus-dropped rule and the
  similarity threshold; a handler that queries storage directly re-implements each
  one and diverges exactly on absent attributes and historical reads — the cases
  nobody writes a test for.
- ⚠ **The tenant is IGNORED as an argument, not rejected.** A rejection tells the
  caller the parameter exists. This caller composes its next call from text it may
  have read out of the store, so a tenant it can name is a tenant it can be talked
  into naming.
- ⚠ **A refusal is a value, not an error.** A refusal a transport carries as an
  error is indistinguishable from a dropped connection, and the correct response
  to a dropped connection is to retry — so an agent retries a refusal forever.
- ⚠ **There is no `update` and no `delete`,** for ADR-010's reason: the store
  appends. A tool called `update` teaches a model a data model this system does
  not have, and the model then reasons about history and erasure wrongly — a
  failure in the caller's reasoning rather than at the API, so nothing reports it.
- **Time is an argument on every tool, enforced at registration.** ADR-011 made
  time a clause that composes rather than a family of verbs; a surface that did
  not inherit that would re-grow `read`, `read_history` and `read_as_of`, each
  with its own default.
- **The surface is read-only as a CONSEQUENCE, not a policy.** ADR-011 has no
  write statement, so there is nothing for a write tool to compile to.
- **A tool's description is the only documentation its caller will ever have**, so
  every tool declares its refusals and `Describe` renders them. One that says what
  a tool does and not what it refuses produces a caller that retries forever, one
  layer above the refusal rule.
