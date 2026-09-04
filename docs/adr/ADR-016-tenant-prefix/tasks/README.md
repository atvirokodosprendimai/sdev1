# ADR-016 Tasks

Implementation task for ADR-016: Put the tenant in the leading bytes of the key.
See the parent ADR for the decision.

**Source of truth:** the task file's `Depends-on` / `Produces` / `Consumes` /
`Covers` headers. This README is a derived index — when it disagrees with the
task file, the task file wins and the README must be regenerated.

## Execution Order

One task.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | The tenant prefix in the key | done | — | `go test ./internal/core/addr/... -run 'TestTenant\|TestKey\|TestDifferent\|TestFanOut\|TestDescend\|TestLeafID'` then the whole module, then build and run the binary |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `addr.TenantID`, `addr.KeyOf(tenant, entity)`, `addr.TenantOf` | none in this record | `KeyOf`'s signature change breaks every existing caller, and they are updated in the same change |

## Notes

- ⚠ **This record changes an ACCEPTED and IMPLEMENTED format.** ADR-001 rule 1
  hashed the entity identifier alone; this narrows it, which is why ADR-016
  declares `Invalidates: ADR-001`. The change is being made now specifically
  because the repository holds no data: today it is one edit and a test update,
  and after data exists it is a full re-ingest.
- The acceptance fence deliberately runs the WHOLE module and then builds and
  runs the binary. A green `addr` package would say nothing about whether the
  callers were updated, and this task's entire risk is in the callers.
- ⚠ When adding a test during implementation, check its name is SELECTED by the
  fence's `-run` filter before running any mutant. A falsifier beside the command
  rather than inside it proves nothing.
