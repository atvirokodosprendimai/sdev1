# ADR-033: A grant is a datom, authorization reads only the present, and a tenant identifier is never reused

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-001-address-space.md`, `docs/adr/ADR-002-transaction-identity.md`, `docs/adr/ADR-003-transaction-boundary.md`, `docs/adr/ADR-007-crypto-shredding.md`, `docs/adr/ADR-016-tenant-prefix.md`, `docs/adr/ADR-027-evaluator.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/authz/**`
**Enforced-by:** `internal/core/authz/authz_test.go::TestARevokedGrantCannotReadThePast`
**Invalidates:** none — ADR-016 made a tenant a contiguous subtree and deliberately left who assigns the bytes open; `BACKLOG.md` §11 has carried it since
**Served-path change:** A read can be refused. Until now nothing in this system enforced anything, and a tenant boundary is usually wanted in order to enforce something.

## Context

ADR-016 makes a tenant the leading bytes of a key, and therefore a contiguous
subtree. It does not say who assigns those bytes, or what the boundary is FOR.
`BACKLOG.md` §11 leaves two questions open and names the trap in the first one.

★ **The trap is a symmetry that is very tempting and is a leak.** This is a
bitemporal store, so a caller can ask what was true last March. The obvious
extension is to authorize that question against the grants that were in force last
March — the data is historical, so why not the permissions.

⚠ **Revoking access today would then leave the revoked party able to read last
year, forever.** Their grant was live then, so a query `AS OF` then is permitted,
and the revocation accomplishes nothing except for the present. The system would
report the revocation as successful.

The second question is reuse. A reused tenant identifier inherits the previous
tenant's subtree — anything marked but not yet swept, anything still sitting in a
coded stripe — so it is a data-exposure question rather than a bookkeeping one.

## Existing Primitives Audit

- `internal/core/addr` (ADR-001, ADR-016): supplies `TenantID`, `TenantBytes` and
  `TenantSubtree`. **Reused unchanged** — this record decides who may hold one,
  not what one is.
- `internal/core/ports` (ADR-003): supplies `Reader`, `Datom`, `Snapshot`.
  **Consumed** — grants are read through the same port as everything else,
  because they ARE everything else. See rule 1.
- `internal/core/temporal` (ADR-002): supplies visibility. **Reached only for the
  audit path**, never for the enforcement path, and rule 3 is how that is kept
  true structurally rather than by discipline.
- `internal/core/eval` (ADR-027): the evaluator. **Not modified** — wiring
  authorization into statement execution needs a caller identity the language does
  not carry, and inventing one here would decide an interface nobody has asked
  for. Deferred, explicitly.
- A policy language, roles, or hierarchical scopes: **none.** A grant here is a
  principal, a tenant and a capability. Anything richer is a language, and a
  language needs a use before it needs a syntax.

## Decision

**Grants are datoms in a reserved tenant; a decision reads only the CURRENT grant
set; and a tenant identifier is never reused.**

1. **A grant is a DATOM**, in tenant `0000`, which is reserved. ★ It costs nothing
   and it buys everything: a grant is bitemporal, so "who had access at time T" is
   answerable; retractable, so **revocation is a retraction**; and inside the
   transaction boundary, so granting and revoking are ordinary writes with
   ordinary identifiers. A separate permission store would have to re-decide all
   of that and would get at least one wrong.

2. **Tenant `0000` is RESERVED and can never be allocated.** It holds the grants,
   so a tenant able to hold it could grant itself anything.

3. ⚠ **An authorization decision reads ONLY the current grant set, whatever
   instant the query asks about — and the deciding function TAKES NO INSTANT.**
   ★ This is the record, and the signature is the enforcement: a caller cannot
   authorize against the past because there is no parameter with which to ask.
   Making it structurally unwritable is worth more than a rule people remember,
   because the tempting version looks principled.

4. **The audit question is answered by a DIFFERENT function that returns records
   rather than a decision.** "Who had access in March" is a legitimate and useful
   question; it is answered by reading grant history, and what it returns cannot
   be mistaken for permission because it is not a yes or a no.

5. **No grant means REFUSED.** ⚠ The zero value of an absent grant set must never
   be "allowed". A system whose default is permission fails open, and it fails
   open exactly when the grant store is unreachable.

6. **A tenant identifier is NEVER reused.** ⚠ A reused identifier inherits
   whatever of the previous tenant's subtree remains: data marked but not yet
   swept, and ciphertext still sitting in a coded stripe. Reuse would require
   proving the subtree holds nothing readable — and nothing in this system can
   prove that, because proving it is the enumeration problem ADR-007's design
   exists to avoid.

7. ⚠ **The identifier space is therefore a finite budget: 65,536 for the life of a
   deployment**, and creating-then-destroying tenants consumes it permanently.
   ★ Stated because it is a real operational limit that follows from rule 6 and
   from ADR-001's two-byte prefix, and because widening the prefix is an
   address-space change that cannot be retrofitted once keys exist. A deployment
   that will churn tenants needs to know this before it starts, not after.

**What would falsify this.** A principal whose grant was revoked today still
reading data as of a time when the grant was live. That is the falsifier in
`Enforced-by:`, it needs one grant and one revocation, and it is exactly what
authorizing against historical grants produces.

## Alternatives Considered

- **Authorize a historical query against the grants in force at that instant.**
  It is symmetric with how the data is read, and it is the version somebody will
  propose as more principled. Rejected under rule 3: revocation would then never
  reach the past, so revoking access leaves the revoked party able to read
  everything up to the moment of revocation, forever — and the system reports the
  revocation as successful.
- **Keep grants in a separate permission store.** Conventional, and it keeps
  system data out of the tenant space. Rejected under rule 1: it would have to
  re-decide bitemporality, retraction, identifiers and the transaction boundary,
  and "who had access in March" would need a second history mechanism.
- **Give the enforcement function an instant, and pass the current one.**
  Flexible, and it makes the audit and enforcement paths share code. Rejected
  under rule 3: the leak then depends on every caller passing the right value, and
  the wrong value is the one that looks most natural at a call site that already
  has a snapshot in hand.
- **Reuse tenant identifiers once erasure has run.** It solves the exhaustion in
  rule 7. Rejected under rule 6: erasure destroys keys and leaves ciphertext, so
  "nothing readable remains" is a claim about every byte in a subtree, and
  establishing it is exactly the enumeration this design avoids. ⚠ Revisit only
  with a mechanism that can PROVE it, not with a policy that assumes it.
- **Default to allow when the grant set cannot be read.** It keeps a cluster
  serving during a partition. Rejected under rule 5: it fails open precisely when
  the thing that would say no is unreachable.
- **Roles, groups or hierarchical scopes.** Every real system grows them.
  Rejected as premature: a grant here is a principal, a tenant and a capability,
  and a policy language needs a use before it needs a syntax.

## Component / Boundary Impact

One new component, `internal/core/authz`, owning one thing: whether a principal
may do something in a tenant. It has one reason to change — what a grant is.

⚠ The boundary: it decides nothing about WHO a principal is. Authentication,
identity and how a caller proves who they are belong to a transport that does not
exist. This package is handed a name and answers a question about it.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `authz.Capability` / `authz.Read` / `authz.Write` | new — what a grant permits | T1 | callers |
| `authz.SystemTenant` | new — the reserved tenant `0000` | T1 | callers |
| `authz.Set` / `authz.Load` | new — the CURRENT grant set | T1 | callers |
| `authz.Set.Allow` | new — the decision, taking no instant | T1 | callers |
| `authz.GrantDatom` / `authz.RevokeDatom` | new — a grant and its retraction, as datoms | T1 | callers |
| `authz.History` | new — grant records for AUDIT, which cannot authorize | T1 | operators |
| `authz.ErrNotGranted` / `authz.ErrReservedTenant` | new sentinels | T1 | callers |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `authz.Set`, `authz.Set.Allow` | T1 | a served path with a caller identity (`BACKLOG.md` §18/§25) | No |

## Consequences

- **Positive:** `BACKLOG.md` §11's named trap cannot be written. The signature
  refuses it rather than a reviewer having to notice it.
- **Positive:** Revocation is a retraction, so it is ordinary, ordered and
  auditable — and "who had access in March" stays answerable without weakening
  what a revocation does.
- **Positive:** Grants are datoms, so nothing new had to be built to store,
  order, or query them.
- **Negative:** The identifier space is a finite budget under rule 6, and a
  deployment that churns tenants exhausts it. That is now written down instead of
  discovered.
- **Negative:** Nothing calls `Allow` yet. The language carries no caller
  identity and the transport does not exist, so this decides the rule and leaves
  the enforcement point to whatever gains a caller. Stated rather than implied.
- **Neutral:** No roles, no scopes, no policy language.

## Out of Scope

- Who a principal IS, and how they prove it (deferred: `docs/adr/BACKLOG.md` §18 — authentication needs a transport)
- Enforcing a grant inside statement execution (deferred: `docs/adr/BACKLOG.md` §20 — the language carries no caller identity)
- Roles, groups and hierarchical scopes (deferred: `docs/adr/BACKLOG.md` §11)
- Allocating an identifier to a new tenant, and who may (deferred: `docs/adr/BACKLOG.md` §19 — it is a cluster-wide decision needing consensus)
- Reusing a tenant identifier (permanent: boundary: rule 6 — reuse requires proving a subtree holds nothing readable, which is the enumeration problem ADR-007's design exists to avoid; revisit only with a mechanism that can prove it)
- Widening the tenant prefix (permanent: fact: the prefix is the leading bytes of every key, so changing its width changes every key ever computed; citation: file `internal/core/addr/addr.go:33`)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A historical query is authorized against historical grants | High — it is symmetric with how the data is read, and sounds more principled | Critical — revocation never reaches the past, so a revoked party reads everything up to the revocation forever, and the revocation reports success | Rule 3: the deciding function takes no instant, so the question cannot be asked |
| An absent or unreadable grant set defaults to allow | Med — it keeps a cluster serving | Critical — it fails open exactly when the thing that would refuse is unreachable | Rule 5, with a test over an empty set |
| A tenant identifier is reused after a tenant is deleted | High — the space is finite and reuse looks like tidying | Critical — the new tenant inherits unswept data and coded ciphertext from the old one | Rule 6, and rule 7 states the cost of not reusing so the trade is visible |
| The reserved tenant is allocated to somebody | Med | Critical — a tenant holding the grants can grant itself anything | Rule 2, with a named refusal |
| Audit and enforcement share a code path | Med — they read the same datoms | High — the instant leaks back into the decision through the shared function | Rule 4: audit returns RECORDS, enforcement returns a decision, and they are different functions |

## Rollback

Nothing enforces anything today, so reverting removes a capability rather than a
behaviour. ⚠ Rule 6 is the exception and it is not revertible: identifiers already
handed out cannot be un-handed, and a deployment that assumed reuse would have
allocated differently from the start.

## Follow-ups

- [ ] When a caller identity exists (`BACKLOG.md` §18), decide where `Allow` is called — once, at the edge, or per statement — and confirm the historical-query rule survives the wiring, because a call site holding a snapshot is exactly where the tempting version reappears.
- [ ] Decide who may allocate a tenant identifier (`BACKLOG.md` §19); rule 6 says they are never reused and says nothing about who issues them.
- [ ] Revisit rule 6 only alongside a mechanism that can PROVE a subtree holds nothing readable (`BACKLOG.md` §11) — the exhaustion in rule 7 is a real pressure and it must not be relieved by assuming.
