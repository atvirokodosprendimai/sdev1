# Task ADR-033-T1: Make authorizing against the past unwritable

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `authz.Capability`, `authz.Read`, `authz.Write`, `authz.SystemTenant`, `authz.Set`, `authz.Load`, `authz.Set.Allow`, `authz.GrantDatom`, `authz.RevokeDatom`, `authz.History`, `authz.Record`, `authz.ErrNotGranted`, `authz.ErrReservedTenant`
**Consumes:** `addr.TenantID`, `addr.TenantFromUint` from ADR-001/016; `ports.Reader`, `ports.Datom`, `ports.Snapshot`, `ports.Carried` from ADR-003; `temporal.Interval`, `temporal.Forever` from ADR-002
**Data dependency:** hermetic — a reader the test controls, and a real leaf
**Proof map:** v1
**Rests-on:** `a revoked grant refusing a query about the time it was live`, `an absent grant set refusing rather than permitting`, `the reserved tenant being unallocatable`, `an audit path that returns records rather than a decision`

## Goal

Make the tempting leak — authorizing a historical query against historical grants
— impossible to write, rather than a rule somebody has to remember.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/authz/doc.go` | add | Why the decision reads only the present, and why the signature is the enforcement. |
| `internal/core/authz/authz.go` | add | `Capability`, `Set`, `Load`, `Allow`, the grant datoms, `History`. |
| `internal/core/authz/authz_test.go` | add | The tests below. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestARevokedGrantCannotReadThePast`, `TestAnAbsentGrantSetRefuses`, `TestTheReservedTenantCannotBeGranted`, `TestHistoryAnswersWhoHadAccessWithoutAuthorizing`, `TestAGrantIsADatomAndRevocationIsARetraction`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Express a grant AS A DATOM in the reserved tenant: the principal is the entity, the tenant and capability are in the attribute, and a revocation is the retraction. ★Bitemporality, ordering and the transaction boundary then come for free rather than being re-decided. [proof: mutation]
3. [S3] Implement `Load` to build the CURRENT grant set, reducing through `ports.Carried` so a retracted grant is absent rather than present-and-withdrawn. [proof: mutation]
4. [S4] Give `Set.Allow` NO instant parameter. ⚠The signature is the enforcement: a caller cannot authorize against the past because there is nothing to ask with. [proof: mutation]
5. [S5] Refuse when no grant is held, with `ErrNotGranted`. ⚠An absent grant set must never mean permitted — that fails open exactly when the thing that would refuse is unreachable. [proof: mutation]
6. [S6] Refuse a grant naming the reserved tenant AT THE DECISION, with `ErrReservedTenant`. ⚠A tenant able to hold the grants could grant itself anything. ★A second filter in `Load` was written and REMOVED: `Allow` refuses that tenant whatever the set holds, so the filter was unreachable — a mutant proved it bound to nothing, and one guard at the decision beats two with one dead. [proof: mutation]
7. [S7] Implement `History` to return RECORDS for audit — never a decision — so "who had access in March" stays answerable without becoming a way to authorize. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/authz/... -race -run 'TestARevokedGrantCannotReadThePast|TestAnAbsentGrantSetRefuses|TestTheReservedTenantCannotBeGranted|TestHistoryAnswersWhoHadAccessWithoutAuthorizing|TestAGrantIsADatomAndRevocationIsARetraction' -count=1 2>&1 | tee /tmp/adr033-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr033-t1a.out \
  && go test ./internal/core/authz/... ./internal/core/ports/... ./internal/core/leafstore/... ./internal/core/temporal/... -race -count=1 2>&1 | tee /tmp/adr033-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr033-t1b.out
```

The second command re-runs the packages a grant is built out of: it is a datom
read through a port, reduced by the same rule everything else uses.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestARevokedGrantCannotReadThePast` | `internal/core/authz/authz_test.go` | A grant live from instant 100, revoked at 200, refuses at 300 — **the falsifier ADR-033 names in `Enforced-by:`**. ⚠ And the grant's own history still SHOWS it was live at 150, so the test proves the decision ignores what the audit path can see rather than proving the data is gone | — | S3, S4 |
| `TestAnAbsentGrantSetRefuses` | `internal/core/authz/authz_test.go` | An empty set, and a set holding a grant for a DIFFERENT tenant or a different capability, all refuse with `ErrNotGranted` — so the default is no, and a grant is not accidentally broad | — | S5 |
| `TestTheReservedTenantCannotBeGranted` | `internal/core/authz/authz_test.go` | Building a grant for tenant `0000` is `ErrReservedTenant`, and a hand-made datom claiming it permits nothing — the forged grant may enter the set, and the DECISION refuses it regardless | — | S6 |
| `TestHistoryAnswersWhoHadAccessWithoutAuthorizing` | `internal/core/authz/authz_test.go` | Asked AT 150 `History` answers with the grant alone — alice HAD access then — and asked at 300 it shows the revocation too. ★ The point is the pair: the audit path says she had access at 150 and the decision refuses now, both true, and only the second gates a read including a read ABOUT 150. It returns records rather than a boolean, so it cannot be used as a decision | — | S7 |
| `TestAGrantIsADatomAndRevocationIsARetraction` | `internal/core/authz/authz_test.go` | A grant round-trips through a real `leafstore` leaf, and a revocation is a datom with `Assert` cleared rather than an absent one — so grants inherit bitemporality and the transaction boundary rather than re-deciding them | — | S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above, over a controlled reader and a real leaf. |
| 2 — something selects it | `Allow` is the only decision, and `Load` the only way a set is built. |
| 3 — the caller can discover it | Two named sentinels, and `Allow`'s signature says what it will not consider. |
| 4 — it is used | ⚠ **Nothing calls `Allow` yet.** The language carries no caller identity and there is no transport, so the enforcement POINT is deferred. Recorded rather than implied. |

## Mutation Log

- 2026-09-05 · 40b8932* · mutant killed · exit 1 · `internal/core/authz/authz.go` · builds the grant set from the RAW visible datoms instead of what the principal currently carries, so the earlier assertion of a revoked grant is still there and a revoked party keeps their access · acceptance-sha256:0d9c72b1e9f61058364b40f21636e45cc0a8ed07117fff9654faabd5b58e7a61 · covers:a revoked grant refusing a query about the time it was live
- 2026-09-05 · 40b8932* · mutant killed · exit 1 · `internal/core/authz/authz.go` · permits when no grant is held, so the default becomes yes — and it fails open exactly when the grant store is empty or unreachable · acceptance-sha256:0d9c72b1e9f61058364b40f21636e45cc0a8ed07117fff9654faabd5b58e7a61 · covers:an absent grant set refusing rather than permitting
- 2026-09-05 · 40b8932* · mutant survived · exit 0 · `internal/core/authz/authz.go` · stops refusing the reserved tenant at the READING end, so a hand-made grant datom naming tenant 0000 is honoured — and a tenant holding the grants can grant itself anything · acceptance-sha256:0d9c72b1e9f61058364b40f21636e45cc0a8ed07117fff9654faabd5b58e7a61 · covers:the reserved tenant being unallocatable
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-05 · 40b8932* · mutant killed · exit 1 · `internal/core/authz/authz.go` · reports every audit record as a grant, so a revocation reads as a grant and the audit trail says access was never withdrawn · acceptance-sha256:0d9c72b1e9f61058364b40f21636e45cc0a8ed07117fff9654faabd5b58e7a61 · covers:an audit path that returns records rather than a decision
- 2026-09-05 · 40b8932* · mutant killed · exit 1 · `internal/core/authz/authz.go` · stops refusing the reserved tenant at the decision, so a hand-made grant datom naming tenant 0000 is honoured — and a tenant holding the grants can grant itself anything · acceptance-sha256:0d9c72b1e9f61058364b40f21636e45cc0a8ed07117fff9654faabd5b58e7a61 · covers:the reserved tenant being unallocatable

## Invariants

- A decision reads only the current grant set.
- No grant means refused.
- The reserved tenant cannot be granted.
- Audit returns records; enforcement returns a decision.

## Risks

- ⚠ **A revocation test that only checks "now" proves nothing about the leak.** The whole failure is about a query ABOUT THE PAST, so the test asserts the refusal while the grant's history still shows it was live at the instant asked about. Without that second half it is a test of ordinary revocation.
- ⚠ **`Allow` taking no instant is the mechanism, and a test cannot assert a signature.** What the tests can do is show that no reachable call permits the past; the structural guarantee is that there is no parameter to pass. Stated so a later "helpful" overload that takes one is recognised as reopening this.
- ⚠ **A grant for the wrong tenant or the wrong capability must also refuse**, or `Allow` is really answering "does this principal have any grant at all".
- ⚠ **A SECOND GUARD WAS DEAD, AND A MUTANT FOUND IT.** The first version also filtered reserved-tenant grants out of the set at load. That filter is unreachable — `Allow` refuses that tenant whatever the set holds — so a mutant removing it SURVIVED, correctly. It was deleted rather than kept "for safety": a guard nothing can falsify is a guard nobody should trust, and two guards where one is dead is worse than one, because a reader cannot tell which is load-bearing.
- Nothing calls `Allow`, so this task adds a rule and not an enforcement point. Recorded on the parent record as a consequence rather than hidden.

## Stop Condition

Stop and ask before giving `Allow` an instant, however natural it looks at a call
site that already holds a snapshot. That parameter is the leak: it makes revoking
access today leave the revoked party reading last year, forever, while the
revocation reports success.

## Out of Scope

- Who a principal is, and how they prove it (deferred: `docs/adr/BACKLOG.md` §18)
- Calling `Allow` from statement execution (deferred: `docs/adr/BACKLOG.md` §20)
- Roles, groups and scopes (deferred: `docs/adr/BACKLOG.md` §11)
- Allocating an identifier to a new tenant (deferred: `docs/adr/BACKLOG.md` §19)
- Reusing a tenant identifier (permanent: boundary: it requires proving a subtree holds nothing readable, which is the enumeration problem ADR-007's design exists to avoid)

## Verification Log
- 2026-09-05 · 40b8932* · exit 0 · `set -o pipefail …` · acceptance-sha256:0d9c72b1e9f61058364b40f21636e45cc0a8ed07117fff9654faabd5b58e7a61 · ms:4175
- 2026-09-05 · 40b8932* · exit 0 · `set -o pipefail …` · acceptance-sha256:0d9c72b1e9f61058364b40f21636e45cc0a8ed07117fff9654faabd5b58e7a61 · ms:4140
- 2026-09-05 · 40b8932* · exit 0 · `set -o pipefail …` · acceptance-sha256:0d9c72b1e9f61058364b40f21636e45cc0a8ed07117fff9654faabd5b58e7a61 · ms:4198
- 2026-09-05 · 40b8932* · exit 0 · `set -o pipefail …` · acceptance-sha256:0d9c72b1e9f61058364b40f21636e45cc0a8ed07117fff9654faabd5b58e7a61 · ms:4109
- 2026-09-05 · 40b8932* · exit 0 · `set -o pipefail …` · acceptance-sha256:0d9c72b1e9f61058364b40f21636e45cc0a8ed07117fff9654faabd5b58e7a61 · ms:4126
- 2026-09-05 · 40b8932* · exit 0 · `set -o pipefail …` · acceptance-sha256:0d9c72b1e9f61058364b40f21636e45cc0a8ed07117fff9654faabd5b58e7a61 · ms:4170
- 2026-09-05 · 40b8932* · exit 0 · `set -o pipefail …` · acceptance-sha256:0d9c72b1e9f61058364b40f21636e45cc0a8ed07117fff9654faabd5b58e7a61 · ms:4176
- 2026-09-05 · 40b8932* · exit 0 · `set -o pipefail …` · acceptance-sha256:0d9c72b1e9f61058364b40f21636e45cc0a8ed07117fff9654faabd5b58e7a61 · ms:4145
