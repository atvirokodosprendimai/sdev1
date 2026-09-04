# Task ADR-013-T2: Serve the surface over MCP

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `cmd/sdev1-mcp`, `mcpsurface.Serve`
**Consumes:** `mcpsurface.Registry`, `mcpsurface.Compiled`, `mcpsurface.Describe` (T1), a query evaluator (`docs/adr/BACKLOG.md` §20), `github.com/modelcontextprotocol/go-sdk/mcp`
**Data dependency:** needs a running store — the server answers by evaluating a compiled statement, and there is nothing to evaluate against
**Proof map:** v1
**Rests-on:** `a refusal reaching the agent as a result rather than a protocol error`, `the tool list served being the registry rather than a copy`

## Status

⚠ **`pending`, and it is blocked on two things that do not exist.** This is
recorded rather than started, and the record says why.

- **A query evaluator** (`BACKLOG.md` §20). T1 compiles a call into a
  `ql.Statement` and a key. Serving means answering, and answering means
  evaluating that statement against a storage engine (`BACKLOG.md` §12). There is
  no honest partial step: a server that returned fabricated rows would be a worse
  artifact than no server.
- **The SDK dependency** (`BACKLOG.md` §25). `github.com/modelcontextprotocol/go-sdk`
  is not in `go.mod`, deliberately — nothing in T1 imports it, which is what lets
  the meaning of the surface be tested with no transport at all.

★ **This is not the same as ADR-013 being unfinished.** The decision — that every
tool compiles to a query, that the tenant comes from the session, that a refusal
is a value — is settled and proved by T1's mutants. What waits here is delivery.

## Goal

Expose T1's registry over the Model Context Protocol so an agent can list the
tools, read their refusals, and get answers back.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `go.mod` | modify | Add `github.com/modelcontextprotocol/go-sdk`. |
| `internal/core/mcpsurface/serve.go` | add | `Serve`: bind the registry to the SDK's tool and result types. |
| `internal/core/mcpsurface/serve_test.go` | add | The tests below. |
| `cmd/sdev1-mcp/main.go` | add | The binary: build a session, register `Standard`, serve on stdio. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestServedToolListIsTheRegistry`, `TestARefusalIsServedAsAResultNotAProtocolError`, `TestEveryServedToolCarriesItsRefusalsInItsDescription`. Run the Acceptance fence and confirm it is red. [proof: acceptance]
2. [S2] Add the SDK to `go.mod` and pin an exact version. ★A protocol SDK on a floating version changes the wire shape between builds, and the symptom is a client that lists no tools with nothing logged. [proof: acceptance]
3. [S3] Implement `Serve`: derive the served tool list from `Registry.Tools()` at request time rather than from a slice built at startup. ⚠ A copy taken once drifts the moment anything registers conditionally, and the drift is invisible — the agent simply never sees the tool. [proof: mutation]
4. [S4] Map a `*Refusal` onto a tool RESULT carrying the reason, never onto a protocol error. ⚠ A protocol error is what a dropped connection looks like, and an agent retries a dropped connection. [proof: mutation]
5. [S5] Evaluate the compiled statement and render ADR-011 rows, preserving unbound bindings rather than dropping them. [proof: human: a reader confirms this step is blocked on the query evaluator and a storage engine, and that no stub stands in for either — a stub that answers plausibly is what this task's Stop Condition forbids]
6. [S6] Bind the session's tenant outside the protocol — a server flag or the connection, never a tool argument. [proof: human: a reader confirms the tenant reaches the server without passing through a tool argument, since who may claim a tenant is not decidable in this record]
7. [S7] Emit an ADR-012 event per call, declaring the kind first. [proof: human: a reader confirms the kind is DECLARED before anything emits it, because ADR-012's vocabulary is closed and an undeclared kind is refused at emission]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/mcpsurface/... -race -run 'TestServedToolListIsTheRegistry|TestARefusalIsServedAsAResultNotAProtocolError|TestEveryServedToolCarriesItsRefusalsInItsDescription' -count=1 2>&1 | tee /tmp/adr013-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr013-t2a.out \
  && go build ./cmd/sdev1-mcp/... 2>&1 | tee /tmp/adr013-t2b.out \
  && ! grep -qE "^FAIL|cannot find|undefined" /tmp/adr013-t2b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestServedToolListIsTheRegistry` | `internal/core/mcpsurface/serve_test.go` | Registering a tool after the server is built still shows it in the served list — the list is derived, not copied | — | S3 |
| `TestARefusalIsServedAsAResultNotAProtocolError` | `internal/core/mcpsurface/serve_test.go` | A refused call comes back as a result the agent can read, with no protocol-level error set | — | S4 |
| `TestEveryServedToolCarriesItsRefusalsInItsDescription` | `internal/core/mcpsurface/serve_test.go` | The description the SDK publishes is `Describe`'s output, refusals included | — | S3, S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The three tests above. |
| 2 — something selects it | `cmd/sdev1-mcp` constructs the registry from `Standard` and serves it; without the binary the surface is a library nothing runs. |
| 3 — the caller can discover it | An agent's `tools/list` returns them with descriptions; that is the entire discovery path. |
| 4 — it is used | `pending` — blocked on the evaluator. |

## Mutation Log

## Invariants

- The served tool list is `Registry.Tools()` at request time.
- No refusal reaches the wire as a protocol error.
- The tenant is never a tool argument.

## Risks

- ⚠ **The SDK makes returning an error easy and returning a refusal-shaped result slightly harder**, so the wrong one is the default in most handler shapes. The test asserts the protocol error field is unset on a refused call, which is the observable difference.
- ⚠ **A tool list copied at startup passes every test written against a static registry.** The test registers a tool AFTER the server is built, which is the only shape that distinguishes derived from copied.
- Stdio transport makes the server single-tenant per process. That is a real limit and rule 2 of the record is why it is acceptable: a multi-tenant agent opens a session per tenant.

## Stop Condition

Stop and ask before serving anything that returns rows without an evaluator behind
it — a stub that answers plausibly is worse than a server that is honestly absent,
because a caller cannot tell the difference and neither can a test.

## Out of Scope

- The evaluator itself (deferred: `docs/adr/BACKLOG.md` §20)
- Deciding who may claim a tenant (deferred: `docs/adr/BACKLOG.md` §11)
- Rate limiting agent calls under ADR-015's budget (deferred: `docs/adr/BACKLOG.md` §25)

## Verification Log
