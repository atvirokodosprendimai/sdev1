# Task ADR-013-T1: The declared tools, what they compile to, and the tenant a call cannot name

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `mcpsurface.Verb`, `mcpsurface.Tool`, `mcpsurface.Arg`, `mcpsurface.Session`, `mcpsurface.Call`, `mcpsurface.Refusal`, `mcpsurface.Compiled`, `mcpsurface.Registry`, `mcpsurface.Describe`, `mcpsurface.Standard`
**Consumes:** `ql.Statement`, `ql.Select`, `ql.ShapeQuery`, `ql.TimeClause` from ADR-011, `addr.TenantID`, `addr.KeyOf`, `addr.TenantOf` from ADR-016
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `every registered tool compiling to a query rather than reaching past it`, `the tenant coming from the session rather than from the call`, `a refusal being a value rather than an error a caller retries`, `time being an argument every tool takes rather than a tool of its own`

## Goal

Make "an agent asks this engine a question" mean exactly one thing: a declared
tool, compiled into an ADR-011 statement, addressed at a key whose tenant the
caller did not choose.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/mcpsurface/doc.go` | add | The package comment: what a tool is and what it is not. |
| `internal/core/mcpsurface/surface.go` | add | `Verb`, `Tool`, `Arg`, `Session`, `Call`, `Refusal`, `Compiled`, `Registry`, `Describe`. |
| `internal/core/mcpsurface/standard.go` | add | `Standard`, the tools this engine actually declares — the registry is reachable rather than merely constructible. |
| `internal/core/mcpsurface/surface_test.go` | add | The tests below. |

★ `standard.go` is what makes the component SELECTED rather than just present. A
registry with no declared tools passes every test in this file and offers an agent
nothing, which is this pipeline's most common shipped defect.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestEveryToolCompilesToAQuery`, `TestTenantComesFromTheSessionNotTheCall`, `TestAnUnboundSessionIsRefusedNotDefaulted`, `TestARefusalIsNotAnError`, `TestARefusalNamesTheToolAndTheReason`, `TestAToolWithoutATimeArgumentIsRefused`, `TestMutationNamesAreRefusedAtRegistration`, `TestDescriptionCarriesTheRefusals`, `TestStandardToolsAreRegistered`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Verb` as a closed set — `VerbRead` and `VerbResemble` — and `Tool` as a name, a verb, a summary, declared refusals and arguments. ★The set is closed for ADR-012's reason: with an open one, the thing on the other end pattern-matches strings.
3. [S3] Implement `Registry.Compile` so every verb produces a `ql.Statement` — `VerbRead` a `*ql.Select`, `VerbResemble` a `*ql.ShapeQuery`. A verb with no statement is unreachable by construction rather than by review. [proof: mutation]
4. [S4] Take the tenant from `Session` and IGNORE any argument named `tenant`; refuse an unbound session by name rather than defaulting. ★Ignoring rather than rejecting matters: a rejection tells the caller the parameter exists, and this caller composes its next call from text it may have read out of the store.
5. [S5] Make `Refusal` a value that names the tool and the reason, with no `Error` method. ⚠A refusal a transport carries as an error is indistinguishable from a dropped connection, and the correct response to a dropped connection is to retry — so an agent retries a refusal forever. [proof: mutation]
6. [S6] Refuse at registration: a tool with no time argument, and a tool whose name uses a mutation word the store does not have. ★Both are startup defects rather than agent mistakes, so both are Go errors rather than refusals.
7. [S7] Implement `Describe` so the text an agent reads carries every declared refusal. ★It is the only documentation this caller will ever have.
8. [S8] Declare `Standard` — the tools this engine offers — and assert they register.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/mcpsurface/... -race -run 'TestEveryToolCompilesToAQuery|TestTenantComesFromTheSessionNotTheCall|TestAnUnboundSessionIsRefusedNotDefaulted|TestARefusalIsNotAnError|TestARefusalNamesTheToolAndTheReason|TestAToolWithoutATimeArgumentIsRefused|TestMutationNamesAreRefusedAtRegistration|TestDescriptionCarriesTheRefusals|TestStandardToolsAreRegistered' -count=1 2>&1 | tee /tmp/adr013-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr013-t1a.out \
  && go test ./internal/core/mcpsurface/... ./internal/core/ql/... ./internal/core/addr/... -race -count=1 2>&1 | tee /tmp/adr013-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr013-t1b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestEveryToolCompilesToAQuery` | `internal/core/mcpsurface/surface_test.go` | Every tool in the registry compiles to a non-nil `ql.Statement` — walked from the registry rather than from a list kept beside it | — | S2, S3 |
| `TestTenantComesFromTheSessionNotTheCall` | `internal/core/mcpsurface/surface_test.go` | A call carrying a hostile `tenant` argument still compiles to a key belonging to the session's tenant | — | S4 |
| `TestAnUnboundSessionIsRefusedNotDefaulted` | `internal/core/mcpsurface/surface_test.go` | A zero `Session` refuses by name instead of quietly addressing tenant zero | — | S4 |
| `TestARefusalIsNotAnError` | `internal/core/mcpsurface/surface_test.go` | `*Refusal` does not satisfy `error`, so no transport can carry it as one | — | S5 |
| `TestARefusalNamesTheToolAndTheReason` | `internal/core/mcpsurface/surface_test.go` | A refusal says which tool refused and why, rather than being generic | — | S5 |
| `TestAToolWithoutATimeArgumentIsRefused` | `internal/core/mcpsurface/surface_test.go` | Registration refuses a tool that takes no time argument, which is what stops a `get_history` growing beside a `get` | — | S6 |
| `TestMutationNamesAreRefusedAtRegistration` | `internal/core/mcpsurface/surface_test.go` | `update`, `delete`, `set`, `patch`, `modify` and `drop` are refused as tool names, with the reason naming the append-only model | — | S6 |
| `TestDescriptionCarriesTheRefusals` | `internal/core/mcpsurface/surface_test.go` | Every declared refusal appears in the text an agent reads | — | S7 |
| `TestStandardToolsAreRegistered` | `internal/core/mcpsurface/surface_test.go` | `Standard` is non-empty and registers cleanly, so the surface offers an agent something | — | S8 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The nine tests above. |
| 2 — something selects it | `Standard` declares the tools and `TestStandardToolsAreRegistered` fails if it is emptied — a registry nothing populates is the failure this rung is for. |
| 3 — the caller can discover it | `Registry.Tools` lists them and `Describe` renders each with its refusals, which is the entire discovery surface a model has. |
| 4 — it is used | Nothing serves them yet; T2 is `pending` on the SDK and the evaluator. |

## Mutation Log

- 2026-09-04 · 535b086* · mutant killed · exit 1 · `internal/core/mcpsurface/surface.go` · leaves the read verb with no compile arm, so a declared tool falls through to a refusal and returns a nil statement — the shape a tool takes the moment it answers by reaching past the language instead of through it · acceptance-sha256:713c94df040746c9adf57a741497c0f515b29678b7f39ec91a2e5451f9642cf1 · covers:every registered tool compiling to a query rather than reaching past it
- 2026-09-04 · 535b086* · mutant killed · exit 1 · `internal/core/mcpsurface/surface.go` · honours a tenant argument the call supplied, which is the change a reasonable contributor makes to support a multi-tenant agent — and it hands any caller that can influence the model context, including this store own contents, a read of any tenant · acceptance-sha256:713c94df040746c9adf57a741497c0f515b29678b7f39ec91a2e5451f9642cf1 · covers:the tenant coming from the session rather than from the call
- 2026-09-04 · 535b086* · mutant killed · exit 1 · `internal/core/mcpsurface/surface.go` · gives Refusal an Error method, so it satisfies error and every transport is free to carry a refusal in an error position — which is exactly what a dropped connection looks like, and the correct response to a dropped connection is to retry, so the agent retries the refusal forever · acceptance-sha256:713c94df040746c9adf57a741497c0f515b29678b7f39ec91a2e5451f9642cf1 · covers:a refusal being a value rather than an error a caller retries
- 2026-09-04 · 535b086* · mutant killed · exit 1 · `internal/core/mcpsurface/surface.go` · lets a tool register without a time argument, which is how a read_history grows beside a read and the temporal verb family the query language rejected comes back one tool at a time, each with its own idea of what the default instant is · acceptance-sha256:713c94df040746c9adf57a741497c0f515b29678b7f39ec91a2e5451f9642cf1 · covers:time being an argument every tool takes rather than a tool of its own

## Invariants

- Every registered tool compiles to a non-nil statement.
- The compiled key's tenant is the session's, whatever the call says.
- A `Refusal` never satisfies `error`.
- A tool with no time argument, or a mutation name, cannot be registered.

## Risks

- ⚠ **"Every tool compiles" is easy to test against a hand-written list of tools**, which passes while the registry holds something else. The test walks `Registry.Tools()` — the same source the surface serves from — so a tool that exists and does not compile is caught.
- ⚠ **A `tenant` argument test that passes no `tenant` proves nothing.** The test passes a `tenant` argument naming a DIFFERENT tenant and asserts the compiled key still belongs to the session's; an implementation that read the argument would pass a test that never supplied one.
- ⚠ **"A refusal is not an error" cannot be checked by reading the signature**, because a later `Error() string` method would satisfy `error` without changing any call site. The test does the type assertion at runtime.
- `Describe` is prose and its adequacy is not mechanically checkable. The test checks the refusals are present, which is the part with a defined answer; whether the summary is clear is the follow-up that has to be measured against a real model.

## Stop Condition

Stop and ask before adding a tool that cannot be expressed as an ADR-011
statement, however reasonable the question is. The right move is a language
change, and taking the shortcut here is precisely the second query surface this
record exists to prevent — with the divergence appearing only on historical reads
and absent attributes.

## Out of Scope

- Serving anything over MCP (deferred: `docs/adr/BACKLOG.md` §25)
- Evaluating a compiled statement (deferred: `docs/adr/BACKLOG.md` §20)
- Authenticating the session's tenant (deferred: `docs/adr/BACKLOG.md` §11)

## Verification Log
- 2026-09-04 · 535b086* · exit 0 · `set -o pipefail …` · acceptance-sha256:713c94df040746c9adf57a741497c0f515b29678b7f39ec91a2e5451f9642cf1 · ms:3682
- 2026-09-04 · 535b086* · exit 0 · `set -o pipefail …` · acceptance-sha256:713c94df040746c9adf57a741497c0f515b29678b7f39ec91a2e5451f9642cf1 · ms:3587
- 2026-09-04 · 535b086* · exit 0 · `set -o pipefail …` · acceptance-sha256:713c94df040746c9adf57a741497c0f515b29678b7f39ec91a2e5451f9642cf1 · ms:3634
- 2026-09-04 · 535b086* · exit 0 · `set -o pipefail …` · acceptance-sha256:713c94df040746c9adf57a741497c0f515b29678b7f39ec91a2e5451f9642cf1 · ms:3751
- 2026-09-04 · 535b086* · exit 0 · `set -o pipefail …` · acceptance-sha256:713c94df040746c9adf57a741497c0f515b29678b7f39ec91a2e5451f9642cf1 · ms:3552
