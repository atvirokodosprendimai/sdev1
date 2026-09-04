# Task ADR-012-T1: A closed event vocabulary where every kind names its reader

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `observe.Kind`, `observe.Event`, `observe.Declaration`, `observe.Register`, `observe.Declared`, `observe.Emit`, `observe.ErrUndeclaredKind`, `observe.ErrNoReader`
**Consumes:** `addr.LeafID` from ADR-001, `tx.TxID` from ADR-002
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `an undeclared kind being refused rather than emitted`, `a declaration without a named reader being refused`, `an event carrying typed fields rather than a message`

## Goal

Make the set of things a component may say closed and declared, and make each
one name who reads it.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/observe/doc.go` | add | Package comment: why a vocabulary is closed, why a counter nobody reads is a defect, and why emission never fails a request. |
| `internal/core/observe/observe.go` | add | `Kind`, `Event`, `Declaration`, `Register`, `Declared`, `Emit`, and the two sentinels. |
| `internal/core/observe/kinds.go` | add | The declarations themselves, each naming its reader. |
| `internal/core/observe/observe_test.go` | add | The tests below, including the falsifier named in ADR-012's `Enforced-by:`. |

★ `kinds.go` is what SELECTS an event kind. A kind that is not declared there
cannot be emitted, and `TestEveryEmittedEventIsDeclared` reads the registry
against what the package actually emits — so a kind added in code and not in the
declarations fails rather than becoming a consumer's problem.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestEveryEmittedEventIsDeclared`, `TestUndeclaredKindIsRefused`, `TestDeclarationWithoutAReaderIsRefused`, `TestEventCarriesTypedFields`, `TestDeclaredKindsAreStableAndOrdered`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Kind` as a declared string identity, and `Event` as a kind, a leaf, a transaction identifier and typed fields.
3. [S3] Define `Declaration`: the kind, the reader that consumes it, and the field names it carries.
4. [S4] Implement `Register`, refusing a duplicate and refusing a declaration with no reader (`ErrNoReader`). ★A kind with no reader is the whole point: without this rule a closed vocabulary just becomes a long list of things nobody looks at.
5. [S5] Implement `Emit`, refusing an undeclared kind with `ErrUndeclaredKind`. ★Refusing at emission rather than at read time makes vocabulary drift fail where it happens, instead of becoming a consumer's problem months later.
6. [S6] Declare the kinds this corpus already needs — a routing redirect, a write refused below the floor, a purge left incomplete, a stripe reconstructed from survivors — each naming its reader.
7. [S7] Write the package comment stating why a counter nobody reads is worse than no counter. [proof: human: a reader confirms the comment says a useless counter SURVIVES because deleting it looks risky, which is why the rule is at declaration time rather than a cleanup]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/observe/... -race -run 'TestEveryEmittedEvent|TestUndeclaredKind|TestDeclarationWithout|TestEventCarriesTyped|TestDeclaredKindsAreStable' -count=1 2>&1 | tee /tmp/adr012-t1.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr012-t1.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestEveryEmittedEventIsDeclared` | `internal/core/observe/observe_test.go` | Every kind this package emits is declared, and every declared kind names a reader — both directions, so neither an undeclared emission nor a declared-and-unread kind can hide. **The falsifier ADR-012 names in `Enforced-by:`** | — | S4, S6 |
| `TestUndeclaredKindIsRefused` | `internal/core/observe/observe_test.go` | Emitting a kind nobody declared yields `ErrUndeclaredKind` at emission, not a consumer parsing an unknown shape later | — | S5 |
| `TestDeclarationWithoutAReaderIsRefused` | `internal/core/observe/observe_test.go` | A declaration naming no reader is refused with `ErrNoReader`, so a closed vocabulary cannot become a long list of things nobody looks at | — | S4 |
| `TestEventCarriesTypedFields` | `internal/core/observe/observe_test.go` | An event's fields are named and retrievable rather than formatted into a message, and a field the declaration did not name is refused | — | S2, S3 |
| `TestDeclaredKindsAreStableAndOrdered` | `internal/core/observe/observe_test.go` | The declared set is returned in a stable order, so a consumer and a test see the same vocabulary each run rather than Go's randomised map order | — | S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | `Register` in `kinds.go` is the only way a kind becomes emittable, and `Emit` refuses anything else; the falsifier reads the registry against what is emitted. |
| 3 — the caller can discover it | `Declared()` lists the vocabulary with each kind's reader, so a consumer learns what exists and who else consumes it. |
| 4 — it is used | Nothing renders anything yet; the declared count and its readers are the measurement. |

## Mutation Log

- 2026-09-04 · 15ace10* · mutant killed · exit 1 · `internal/core/observe/observe.go` · invents a declaration for an unknown kind instead of refusing, so vocabulary drift succeeds silently at the producer and becomes a consumer meeting a shape nobody registered, months later · acceptance-sha256:46ecf53985e1ad5bd25a0ae2d335f0554a31f5324f21a19ce2fdb0649a404bc7 · covers:an undeclared kind being refused rather than emitted
- 2026-09-04 · 15ace10* · mutant killed · exit 1 · `internal/core/observe/observe.go` · accepts a kind nobody consumes, after which the closed vocabulary is just a long tidy list of events that look like observability and are not · acceptance-sha256:46ecf53985e1ad5bd25a0ae2d335f0554a31f5324f21a19ce2fdb0649a404bc7 · covers:a declaration without a named reader being refused
- 2026-09-04 · 15ace10* · mutant killed · exit 1 · `internal/core/observe/observe.go` · accepts any field name whatever the declaration says, so a producer can attach a free-form field the consumer never expects and the typed vocabulary decays back into a log one field at a time · acceptance-sha256:46ecf53985e1ad5bd25a0ae2d335f0554a31f5324f21a19ce2fdb0649a404bc7 · covers:an event carrying typed fields rather than a message

## Invariants

- A kind that is not declared cannot be emitted.
- A declaration with no named reader is refused.
- An event carries named fields, never a formatted message.
- The declared set is returned in a stable order.

## Risks

- ⚠ A vocabulary check that only looks one way catches an undeclared emission and misses a declared kind nobody reads — which is the failure that actually accumulates. Both directions are asserted.
- A test that registers its own kinds and checks them proves nothing about the kinds the package really declares. `TestEveryEmittedEventIsDeclared` reads the package's OWN registry, and fails if it is empty.

## Stop Condition

Stop and ask before adding a free-form message field to `Event`. It is what
every caller will want during an incident, and it is how a declared vocabulary
becomes a log — after which the console is a grep again.

## Out of Scope

- Counters and the non-blocking stream — that is T2.
- Rendering a console, and the transport (deferred: `docs/adr/BACKLOG.md` §18)
- Export, sampling and retention (deferred: `docs/adr/BACKLOG.md` §21)

## Verification Log
- 2026-09-04 · 15ace10* · exit 0 · `set -o pipefail …` · acceptance-sha256:46ecf53985e1ad5bd25a0ae2d335f0554a31f5324f21a19ce2fdb0649a404bc7 · ms:1682
- 2026-09-04 · 15ace10* · exit 0 · `set -o pipefail …` · acceptance-sha256:46ecf53985e1ad5bd25a0ae2d335f0554a31f5324f21a19ce2fdb0649a404bc7 · ms:1865
- 2026-09-04 · 15ace10* · exit 0 · `set -o pipefail …` · acceptance-sha256:46ecf53985e1ad5bd25a0ae2d335f0554a31f5324f21a19ce2fdb0649a404bc7 · ms:1687
- 2026-09-04 · 15ace10* · exit 0 · `set -o pipefail …` · acceptance-sha256:46ecf53985e1ad5bd25a0ae2d335f0554a31f5324f21a19ce2fdb0649a404bc7 · ms:1724
- 2026-09-04 · 15ace10* · exit 0 · `set -o pipefail …` · acceptance-sha256:46ecf53985e1ad5bd25a0ae2d335f0554a31f5324f21a19ce2fdb0649a404bc7 · ms:1702
