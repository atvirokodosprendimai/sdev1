# Task ADR-035-T2: Answer it from the datoms — the port, the scan, and the drop

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** L
**Owner:** unassigned
**Produces:** `ports.Inbound`, `eval.ErrNoInboundIndex`, `leafstore.Store.Referrers`
**Consumes:** `ql.Read.Inbound`, `ql.Read.Page`, `ql.Page` (T1); `ports.Reader`, `ports.Datom`, `ports.Snapshot`, `ports.Carried` from ADR-003; `eval.Row`, `eval.latestVisible` from ADR-027
**Data dependency:** hermetic — a reader the test controls, and a real leaf on a temporary directory
**Proof map:** v1
**Rests-on:** `a member missing a named attribute contributing no rows`, `a retracted reference removing a member from the set`, `paging over members in a deterministic order after the drop`, `a reader that cannot scan refusing rather than answering empty`

## Goal

Evaluate the inbound read so that the DATOMS decide who is in the set, a member
that does not satisfy the whole statement contributes nothing, and a page means
what a caller expects.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/ports/ports.go` | modify | The `Inbound` port. Separate from `Reader` — rule 7. |
| `internal/core/ports/ports_test.go` | modify | `Inbound` is enumerated like `Reader` is, so widening it is a test failure. |
| `internal/core/eval/inbound.go` | add | `readInbound`: candidates, confirm, drop, sort, page. |
| `internal/core/eval/eval.go` | modify | `Read` dispatches on `sel.Inbound`. |
| `internal/core/eval/doc.go` | modify | How an inbound read is evaluated, and why the drop rule differs from `OPTIONAL`. |
| `internal/core/eval/inbound_test.go` | add | The tests below. |
| `internal/core/leafstore/leafstore.go` | modify | `Referrers`: the scan over a real leaf. |
| `internal/core/session/session.go` | modify | The memory reader answers `Referrers` too, or a session cannot run the statement it parses. |
| `docs/QUERY-LANGUAGE.md`, `README.md` | modify | What the statement returns, the drop rule, the paging rule, and the two new exports. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestAMemberMissingEitherAttributeIsSkipped`, `TestARetractedReferenceLeavesTheSet`, `TestAPageIsMembersInAStableOrderAfterTheDrop`, `TestAReaderThatCannotScanIsRefused`, `TestAnInboundReadRunsAgainstARealLeaf`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Add `ports.Inbound` with `Referrers(ctx, target, at) ([]string, error)`, SEPARATE from `Reader`. ⚠Rule 7: `Reader` is entity-addressed, and a reader that serves one entity cannot scan — putting it on `Reader` makes every future implementation claim a capability it may not have. [proof: mutation]
3. [S3] In `eval`, require `ports.Inbound` and refuse with `ErrNoInboundIndex` when the reader does not provide it. ⚠Rule 8: an empty slice would say "nothing points here" when the truth is "I cannot tell", which is ADR-027's discarded-`WHERE` defect wearing a different hat. [proof: mutation]
4. [S4] CONFIRM every candidate against its own datoms — an asserted reference to the target, visible at the snapshot — before it can contribute. ⚠Rule 6: a candidate list is a proposal, and the one thing it gets wrong is a RETRACTED edge, because an index that appends never un-proposes. [proof: mutation]
5. [S5] Drop a member missing the projected attribute, missing the predicate's attribute, or failing the comparison. ★All three drop it ENTIRELY rather than emitting it with a hole — rule 4, and deliberately the opposite of `OPTIONAL`. [proof: mutation]
6. [S6] Sort surviving members by entity name, then apply `OFFSET` and `LIMIT` over MEMBERS. ⚠Rule 5, and all three halves matter: after the drop, over members not rows, over a deterministic order. Paging an unordered set is not paging — it repeats and skips while still looking like a page. [proof: mutation]
7. [S7] Implement `leafstore.Store.Referrers` as a scan over the leaf's datoms, and give the session's memory reader the same method, so the statement runs on both paths a caller has. [proof: mutation]
8. [S8] Document what the statement returns and both new exports on the two published pages. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/eval/... -race -run 'TestAMemberMissingEitherAttributeIsSkipped|TestARetractedReferenceLeavesTheSet|TestAPageIsMembersInAStableOrderAfterTheDrop|TestAReaderThatCannotScanIsRefused|TestAnInboundReadRunsAgainstARealLeaf' -count=1 2>&1 | tee /tmp/adr035-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr035-t2a.out \
  && go test ./... -race -count=1 2>&1 | tee /tmp/adr035-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr035-t2b.out
```

The second command carries `ports`, `leafstore`, `session` and both documentation
gates: this task adds a port and implements it in two places, and a port whose
implementations disagree is worse than one nobody implemented.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAMemberMissingEitherAttributeIsSkipped` | `internal/core/eval/inbound_test.go` | **The falsifier ADR-035 names in `Enforced-by:`.** Over four members of `[staff]` — one with both `name` and `lastname`, one missing `lastname`, one missing `name`, one with both but the wrong `lastname` — `READ ->name FROM [staff] WHERE ->lastname = 'a'` returns exactly the first. ⚠ Each of the other three must be absent for a DIFFERENT reason, or the test passes on one rule and proves nothing about the others | — | S5 |
| `TestARetractedReferenceLeavesTheSet` | `internal/core/eval/inbound_test.go` | A member whose reference to the target is retracted is absent, **even when the candidate source still proposes it** — the reader hands back a stale candidate list on purpose, so this fails if confirmation is skipped. ★ That staleness is exactly what a real appended index produces | — | S4 |
| `TestAPageIsMembersInAStableOrderAfterTheDrop` | `internal/core/eval/inbound_test.go` | Over members whose candidate order is shuffled and of which some are dropped: `LIMIT 2` returns the first two SURVIVING members by name with ALL their projected attributes, `OFFSET 2` continues without repeating or skipping, and the two pages concatenate to the unpaged result. ⚠ Members carry two attributes each, so paging over rows would cut one in half and be caught | — | S6 |
| `TestAReaderThatCannotScanIsRefused` | `internal/core/eval/inbound_test.go` | A plain `ports.Reader` gives `ErrNoInboundIndex` and NO rows — not an empty success | — | S3 |
| `TestAnInboundReadRunsAgainstARealLeaf` | `internal/core/eval/inbound_test.go` | The same statement over a real `leafstore` leaf, sealed to disk, returns the same members — so `Referrers` is a scan over stored datoms rather than a convenience on a test double | — | S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests, over a controlled reader and a real leaf. |
| 2 — something selects it | `eval.Read` dispatches on `sel.Inbound`; `session` runs it for any parsed statement. |
| 3 — the caller can discover it | `ErrNoInboundIndex` is named, and both pages document what the statement returns. |
| 4 — it is used | `cmd/sdev1-ql --dir` runs it against a leaf on disk. |

## Mutation Log

- 2026-09-05 · d07eba2* · mutant killed · exit 1 · `internal/core/eval/inbound.go` · trusts the candidate list instead of confirming it against the datoms, so a member whose reference was RETRACTED is still returned — the one thing an index that only appends always gets wrong, and it looks like a live answer · acceptance-sha256:97c4871a081bac16c40f3c6d159c4dc9a333dc23c4bc225a8dad635f7802fccc · covers:a retracted reference removing a member from the set
- 2026-09-05 · d07eba2* · mutant killed · exit 1 · `internal/core/eval/inbound.go` · keeps a member that is missing a projected attribute, returning it with a hole instead of dropping it — so a table read stops being an inner join and a row appears for an entity that cannot answer the question asked · acceptance-sha256:97c4871a081bac16c40f3c6d159c4dc9a333dc23c4bc225a8dad635f7802fccc · covers:a member missing a named attribute contributing no rows
- 2026-09-05 · d07eba2* · mutant killed · exit 1 · `internal/core/eval/inbound.go` · pages the candidate source order instead of a total order, so LIMIT/OFFSET repeat a member already returned and skip one never returned — while the result still looks like a page · acceptance-sha256:97c4871a081bac16c40f3c6d159c4dc9a333dc23c4bc225a8dad635f7802fccc · covers:paging over members in a deterministic order after the drop
- 2026-09-05 · d07eba2* · mutant killed · exit 1 · `internal/core/eval/inbound.go` · answers a reader that cannot scan with an empty success instead of a refusal, so "I cannot tell you what points here" is reported as "nothing points here" — a narrow question getting a confident wrong answer with no error · acceptance-sha256:97c4871a081bac16c40f3c6d159c4dc9a333dc23c4bc225a8dad635f7802fccc · covers:a reader that cannot scan refusing rather than answering empty

## Invariants

- The datoms decide membership; a candidate list only proposes.
- A member missing any named attribute contributes no rows.
- Members are sorted by entity name before paging, and paging follows the drop.
- A reader without the capability refuses; it never answers empty.

## Risks

- ⚠ **A test whose candidate source is already correct cannot prove confirmation happens.** `TestARetractedReferenceLeavesTheSet` hands back a deliberately STALE candidate list, because that is the only shape in which skipping confirmation fails. A test built on an honest source passes with rule 6 deleted.
- ⚠ **The drop rule needs all three reasons separately.** One member missing the projection, one missing the predicate's attribute, one failing the comparison. A test with a single excluded member passes while two of the three rules are broken.
- ⚠ **Go map iteration is randomised**, so an implementation that pages a map is nondeterministic and a single-run test may pass. The order assertion must be on the VALUES, and the paged and unpaged results must be compared.
- ⚠ **N+1 reads.** One scan plus one load per candidate. That is the price of rule 6 and it is recorded on the parent record rather than optimised away here — an index is a later change that cannot alter an answer.
- ⚠ **Two implementations of `Referrers`** — the leaf and the session's memory reader. They must agree, which is why the fence runs the whole suite rather than `eval` alone.

## Stop Condition

Stop and ask before trusting a candidate list without confirming it against the
datoms. A retracted reference is invisible to any index that only appends, and the
result of skipping the check is a member that looks live and is not.

## Out of Scope

- An actual inbound INDEX (deferred: `docs/adr/BACKLOG.md` §27 — rule 6 makes it pure optimisation)
- Cross-leaf inbound reads (deferred: `docs/adr/BACKLOG.md` §18)
- The join (deferred: `docs/adr/BACKLOG.md` §20)
- Absence as a predicate (deferred: `docs/adr/BACKLOG.md` §20)
- `ORDER BY` (deferred: `docs/adr/BACKLOG.md` §20)

## Verification Log
- 2026-09-05 · d07eba2* · exit 0 · `set -o pipefail …` · acceptance-sha256:97c4871a081bac16c40f3c6d159c4dc9a333dc23c4bc225a8dad635f7802fccc · ms:4725
- 2026-09-05 · d07eba2* · exit 0 · `set -o pipefail …` · acceptance-sha256:97c4871a081bac16c40f3c6d159c4dc9a333dc23c4bc225a8dad635f7802fccc · ms:4775
- 2026-09-05 · d07eba2* · exit 0 · `set -o pipefail …` · acceptance-sha256:97c4871a081bac16c40f3c6d159c4dc9a333dc23c4bc225a8dad635f7802fccc · ms:4774
- 2026-09-05 · d07eba2* · exit 0 · `set -o pipefail …` · acceptance-sha256:97c4871a081bac16c40f3c6d159c4dc9a333dc23c4bc225a8dad635f7802fccc · ms:4594
- 2026-09-05 · d07eba2* · exit 0 · `set -o pipefail …` · acceptance-sha256:97c4871a081bac16c40f3c6d159c4dc9a333dc23c4bc225a8dad635f7802fccc · ms:4659
