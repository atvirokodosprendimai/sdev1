# ADR-013: The agent tool surface is a projection of the query language, not a second way in

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-002-transaction-identity.md`, `docs/adr/ADR-010-subscribe-and-purge.md`, `docs/adr/ADR-011-query-language.md`, `docs/adr/ADR-012-observability.md`, `docs/adr/ADR-016-tenant-prefix.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/mcpsurface/**`
**Enforced-by:** `internal/core/mcpsurface/surface_test.go::TestEveryToolCompilesToAQuery`
**Invalidates:** none — checked; ADR-011 fixed what the language says and deliberately left every surface over it open, and ADR-016 fixed where a tenant lives in a key without saying who binds it
**Served-path change:** An agent reads entities and finds resembling subjects by calling declared tools over MCP, each of which compiles to an ADR-011 statement scoped to the session's tenant — where before there was no programmatic surface at all.

## Context

The engine is going to be read by models, not only by people. That is a public
contract, and it is the contract with the least forgiving caller: a model cannot
open `docs/`, cannot read a changelog, and cannot ask what an error meant. It has
the tool list and nothing else.

Which makes the tempting shape the dangerous one. A tool surface is easy to build
as a set of handlers that each reach into storage and answer the question in front
of them — a `get_entity` that fetches a row, a `get_history` that walks versions,
a `search` that scans. Each is simple in isolation. Together they are a second
query language: one with no grammar, no written semantics, a different time story
per tool, and its own bugs in every one.

⚠ **ADR-011's guarantees do not travel to a handler that skips it.** The defaults
table for the two time axes, the rule that an optional leg yields an unbound value
rather than dropping the row, the stated similarity metric and threshold — those
are properties of the language. A handler that queries storage directly has to
re-implement each one, and it will get a different answer on the cases nobody
tests: absent attributes, historical reads, empty results.

⚠ **And the caller is untrusted in a way an ordinary client is not.** A model's
next tool call is a function of the text it just read, and some of that text came
out of this store. If a tool takes a tenant as an argument, then any agent that
can call it can read any tenant, and the instruction to do so can arrive inside
the data — the read itself is the injection vector. This is not a hypothetical
about a hostile user; it is the normal operation of a model that reads a record
containing a sentence addressed to it.

## Existing Primitives Audit

- `internal/core/ql` (ADR-011): supplies `Statement`, `Select`, `ShapeQuery`,
  `TimeClause` and the defaults resolution. **Reused whole and made the only
  target** — a tool compiles to one of these or it is not a tool.
- `internal/core/addr` (ADR-016): supplies `TenantID`, `KeyOf` and `TenantOf`.
  **Reused whole** so the session's tenant becomes the leading bytes of the key
  by the same function every other caller uses, rather than by string handling
  here.
- `internal/core/temporal` (ADR-002): reached through `TimeClause.Resolve`, never
  directly. **Relied on** — a surface that resolved time itself would be the
  second implementation of the defaults table that ADR-011 exists to prevent.
- `modelcontextprotocol/go-sdk`: the protocol's own SDK. **Chosen but not yet a
  dependency** — see T2. Nothing in this record's implemented half imports it,
  which is deliberate: what a tool MEANS is decidable without a transport.
- A schema-validation library: **none.** Arguments are a small closed set of
  named strings; a validator would be a dependency in service of four checks.

## Decision

**Every tool an agent can call compiles to an ADR-011 statement, and the tenant
it runs as comes from the session rather than from the call.**

1. **A tool compiles to a `ql.Statement`, and nothing else is a tool.** ★This is
   the whole record. A tool that reaches past the language is a second query
   surface with its own semantics and its own bugs, and it silently loses every
   guarantee ADR-011 pinned.

2. **The tenant is bound at the session and a tool argument named `tenant` is
   ignored.** ⚠ Not "rejected as invalid" — *ignored*, because a refusal tells a
   caller the parameter exists. An agent's next call is a function of text it may
   have read out of this store, so a tenant it can name is a tenant it can be
   talked into naming.

3. **A session with no tenant refuses, and never defaults.** A default tenant is
   how every misconfigured agent quietly reads the same tenant's data, and the
   symptom is correct-looking answers.

4. **Time is an argument on EVERY tool, and a tool that lacks one is refused at
   registration.** ADR-011 made time a clause that composes with every term
   rather than a family of temporal verbs. The surface inherits that or it
   re-grows the verb family: a `read` beside a `read_history` beside a
   `read_as_of`, each with its own idea of what the default is.

5. **Tool names use the store's verbs, and mutation words are refused at
   registration.** ⚠ There is no `update` and no `delete`, for the same reason
   ADR-010 has none: the store appends. A tool called `update` teaches a model a
   data model this system does not have, and the model then reasons about
   history, retraction and erasure wrongly — a failure in the caller's reasoning
   rather than at the API, so nothing reports it.

6. **A refusal is a value the agent reads, not an error the transport carries.**
   ⚠ A refusal surfaced as a protocol error is indistinguishable from a dropped
   connection, and the correct response to a dropped connection is to retry. So
   an agent retries a refusal forever. A refusal is a result that names the tool
   and says why, which is something a model can act on.

7. **Every tool declares its refusals, and the description an agent reads carries
   them.** ★The description is the only documentation this caller will ever have.
   One that says what a tool does and not what it refuses produces exactly the
   loop in rule 6, one layer up.

8. **The surface is read-only because the language is.** Not as a policy — as a
   consequence of rule 1. ADR-011 has no write statement, so there is nothing for
   a write tool to compile to. When the language gains one (`BACKLOG.md` §20),
   the surface gains a verb, and rule 5 is what stops it being called `update`.

**What would falsify this.** A registered tool that does not compile to a
`ql.Statement`. That is the falsifier in `Enforced-by:`, it walks the registry
rather than a list kept beside it, and it is checkable with no server and no
storage engine.

## Alternatives Considered

- **Expose one `query` tool taking query text.** Maximally expressive, one tool,
  no compile step. Rejected on two counts. Its description must contain the whole
  grammar, so a model that guesses gets a parse error instead of an answer and
  spends turns on syntax. And rule 2 becomes unenforceable: a tenant prefix is
  part of a key, so text the model composes can name another tenant's subtree and
  there is nothing structural left to stop it.
- **Let each tool query storage directly, for speed and simplicity.** The obvious
  implementation. Rejected under rule 1: it re-implements ADR-002's defaults
  table, ADR-011's unbound-versus-dropped rule and the similarity threshold once
  per tool, and the divergence appears only on absent attributes and historical
  reads.
- **Take the tenant as a tool argument, so one agent can serve several.** Real
  operators do have multi-tenant agents. Rejected under rule 2: the argument is
  reachable by anything that can influence the model's context, including the
  store's own contents. A multi-tenant agent opens a session per tenant.
- **Use CRUD names because models have seen millions of them.** Genuinely helps a
  model guess right on the first call. Rejected under rule 5: it helps it guess
  right about the call and wrong about the data, and the second error is silent.
- **Add `get_history` beside `get`.** Reads naturally and matches what callers
  ask for. Rejected under rule 4: it is the temporal verb family ADR-011 rejected,
  and it multiplies with every future qualifier rather than composing.
- **Return refusals as protocol errors, which is what the transport is for.**
  Conventional. Rejected under rule 6: a model cannot distinguish a refusal from a
  broken pipe, and retries.
- **Hand-roll the JSON-RPC loop instead of taking the SDK.** No dependency.
  Rejected in T2: the protocol moves, a hand-rolled loop drifts from it silently,
  and the symptom is a client that lists no tools with nothing logged anywhere.

## Component / Boundary Impact

One new component, `internal/core/mcpsurface`, owning what an agent may ask and
what that means. It has one reason to change: the set of questions this engine
answers to a machine caller.

⚠ The boundary: it DECLARES and COMPILES. It serves nothing, speaks no protocol,
and reads no storage — a `Compile` returns a statement and the tenant-scoped key
that statement addresses, and stops. Keeping the meaning separate from the
transport is what makes rules 1 through 7 testable today, with no server, no SDK
and no evaluator.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `mcpsurface.Verb` | new — the closed set of things a tool does | T1 | T2 |
| `mcpsurface.Tool` | new — a declared tool with its refusals and arguments | T1 | T2 |
| `mcpsurface.Session` | new — what the caller does not choose | T1 | T2 |
| `mcpsurface.Call` | new — one invocation from an agent | T1 | T2 |
| `mcpsurface.Refusal` | new — a readable answer that is NOT an error | T1 | T2 |
| `mcpsurface.Compiled` | new — the statement and the tenant-scoped key it reads | T1 | T2 |
| `mcpsurface.Registry` | new — `Register`, `Tools`, `Compile` | T1 | T2 |
| `mcpsurface.Describe` | new — the text an agent actually reads | T1 | T2 |
| `mcpsurface.Standard` | new — the tools this engine declares | T1 | T2 |
| MCP server binary | new, `pending` — `cmd/sdev1-mcp` over the official SDK | T2 | operators |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `mcpsurface.Registry`, `mcpsurface.Compiled`, `mcpsurface.Describe` | T1 | T2 | No — T2 is written against T1 |

## Consequences

- **Positive:** Every guarantee ADR-011 pinned reaches the agent surface without
  being restated, because there is one path to an answer.
- **Positive:** Cross-tenant access is structurally unavailable rather than
  guarded — the argument does not exist, so there is nothing to bypass.
- **Positive:** The meaning of the surface is testable with no server. What
  remains for T2 is transport and evaluation, which is where the dependencies are.
- **Negative:** The surface can only ask what the language can express, so a
  question ADR-011 cannot phrase has no tool. That is the point, and it is a real
  cost: it turns some feature requests into language changes.
- **Negative:** A model gets typed tools rather than free text, which is less
  expressive per call. It is traded for descriptions that can state refusals and
  for rule 2 being enforceable at all.
- **Neutral:** Nothing is served. The registry and the compile step are decidable
  and their execution is not.

## Out of Scope

- Serving the surface over MCP, which needs the SDK (deferred: `docs/adr/BACKLOG.md` §25)
- Evaluating a compiled statement against storage (deferred: `docs/adr/BACKLOG.md` §20)
- Write tools, which need a write statement in the language (deferred: `docs/adr/BACKLOG.md` §20)
- Who the session's tenant belongs to — authentication and grants (deferred: `docs/adr/BACKLOG.md` §11)
- Rate limiting an agent's calls, which is ADR-015's budget applied to a new caller (deferred: `docs/adr/BACKLOG.md` §25)
- Emitting ADR-012 events per tool call (deferred: `docs/adr/BACKLOG.md` §25)
- What the protocol itself is (permanent: fact: MCP's transport, tool and result shapes are defined by the protocol and its Go SDK rather than here; citation: url https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp)
- Whether a model uses the surface well (permanent: boundary: this record decides what is offered and what it means; how a caller reasons about it is not something a data engine can settle)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A tool is added that queries storage directly because the language cannot phrase its question | High — it is the shortest path and each instance looks local | High — a second query surface with its own time semantics, discovered on historical reads | The falsifier walks the registry and compiles every tool; a tool that cannot compile cannot be registered |
| A `tenant` argument is added for a multi-tenant agent | Med | Critical — cross-tenant read reachable by text the model read out of the store | The argument is ignored rather than rejected, and the test passes a hostile `tenant` argument and asserts the compiled key still belongs to the session |
| A `get_history` tool is added beside `get` because it reads naturally | High | Med — the temporal verb family ADR-011 rejected, re-grown one tool at a time | Registration refuses a tool with no time argument |
| A refusal is returned as a transport error, and agents retry it forever | Med | High — a refused call becomes a loop, at the caller's expense and the cluster's | `Refusal` is a value with no `Error` method, and the test asserts it does not satisfy `error` |
| A tool named `update` or `delete` is added because callers expect one | Med | High — the model reasons about retraction and erasure wrongly, silently | Registration refuses mutation names by pattern, and lists what to use instead |

## Rollback

No persistent state and no format. The registry is constructed at startup and the
compile step is a pure function, so a revert is a code revert. An agent loses a
surface; nothing stored changes and nothing needs migrating.

## Follow-ups

- [ ] When the evaluator exists (`BACKLOG.md` §20), confirm a tool result carries ADR-011's unbound bindings rather than dropping them — the surface would otherwise re-introduce the exact conflation ADR-011 spent a rule preventing, at the layer with the least sophisticated reader.
- [ ] When the server lands (`BACKLOG.md` §25), confirm every refusal reaches the agent as a result rather than a protocol error; the SDK makes both easy and the wrong one is the default in most handler shapes.
- [ ] When the language gains a write statement (`BACKLOG.md` §20), add the verb under rule 5's naming constraint and re-run the mutation-name test with the new tool present.
- [ ] Measure whether a model calls these tools correctly from the descriptions alone before adding more of them; rule 7 is reasoned rather than observed, and the failure mode is a caller that never reports being confused.
