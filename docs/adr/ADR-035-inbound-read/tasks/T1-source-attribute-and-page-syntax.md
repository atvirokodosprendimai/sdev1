# Task ADR-035-T1: Say it in the language — `[e]`, `->a`, LIMIT and OFFSET

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `ql.Read.Inbound`, `ql.Read.Page`, `ql.Page`, `ql.ErrJoinNotSupported`
**Consumes:** `ql.Parse`, `ql.ParseError`, `ql.RefMarker`, `ql.Predicate` from ADR-011/ADR-034
**Data dependency:** hermetic — parsing is a pure function
**Proof map:** v1
**Rests-on:** `a bracketed source parsing as a set rather than an entity`, `a bare attribute inside an inbound read being refused rather than treated as a member's`, `a paging clause being refused where there is nothing to page`

## Goal

Make the statement sayable, and make every combination the evaluator would have
to guess about a parse-time refusal instead.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/ql/lex.go` | modify | `OFFSET` joins the keyword table. `LIMIT` and `[`/`]` already lex. |
| `internal/core/ql/ast.go` | modify | `Read.Inbound`, `Read.Page`, and the `Page` type. |
| `internal/core/ql/parse.go` | modify | The bracketed source, `->` attributes, the page clause, and the three refusals. |
| `internal/core/ql/inbound_test.go` | add | The tests below. |
| `docs/QUERY-LANGUAGE.md`, `README.md` | modify | The grammar, the keyword list, the worked examples, and `ql.Page` / `ql.ErrJoinNotSupported` in the guide. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestABracketedSourceIsASet`, `TestABareAttributeInAnInboundReadIsRefused`, `TestAPageClauseNeedsSomethingToPage`, `TestPageValuesAreRecordedAsWritten`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Parse `FROM [ident]` into `Entity` plus `Inbound`, so the identifier is stored once and the brackets are a property of the source rather than part of the name. ⚠An unclosed `[` is refused with a position, like every other incomplete construct. [proof: mutation]
3. [S3] Parse `->ident` in a projection and in a predicate, storing the attribute name WITHOUT the marker — the marker is grammar, not part of the name, exactly as the backticks around a quoted identifier are not. [proof: mutation]
4. [S4] Refuse `->a` outside an inbound read and a bare `a` inside one, both with `ErrJoinNotSupported` and a message naming the form that was meant. ⚠This is rule 3 and it is the step that keeps the join addable: synonyms today would change the meaning of existing statements when the join arrives. [proof: mutation]
5. [S5] Parse `LIMIT n [OFFSET m]` into `Page`, refuse it on a non-inbound read — a read of one entity has nothing to page — and refuse a negative or non-integer bound. [proof: mutation]
6. [S6] Record the page AS WRITTEN, with an explicit `Has` flag, so "no page clause" and "`LIMIT 0`" are different states. ⚠They mean opposite things — everything, and nothing — and a zero value that conflated them would make the more dangerous one the default. [proof: mutation]
7. [S7] Update both published pages: the EBNF, the keyword list, worked examples, and the two new exports. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/ql/... -race -run 'TestABracketedSourceIsASet|TestABareAttributeInAnInboundReadIsRefused|TestAPageClauseNeedsSomethingToPage|TestPageValuesAreRecordedAsWritten|TestQueryLanguageDocIsComplete|TestPublishedExamplesParse' -count=1 2>&1 | tee /tmp/adr035-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr035-t1a.out \
  && go test ./... -race -count=1 2>&1 | tee /tmp/adr035-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr035-t1b.out
```

The second command is the whole suite because a grammar change reaches every
package that writes a statement, and the two documentation gates run inside the
first.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestABracketedSourceIsASet` | `internal/core/ql/inbound_test.go` | `READ ->name FROM [staff]` sets `Inbound` and stores `staff` without brackets, while `READ name FROM staff` leaves `Inbound` clear — one identifier, two sources. ⚠ An unclosed `[` is refused with a position rather than silently reading to end of input | — | S2, S3 |
| `TestABareAttributeInAnInboundReadIsRefused` | `internal/core/ql/inbound_test.go` | `READ name FROM [staff]` and `READ ->name FROM staff` both match `errors.Is(err, ErrJoinNotSupported)`, in the projection AND in the predicate. ★ Both directions, because a refusal in only one leaves the other spelling meaning two things | — | S4 |
| `TestAPageClauseNeedsSomethingToPage` | `internal/core/ql/inbound_test.go` | `READ * FROM planet-7 LIMIT 5` is refused — one entity's attributes are not a page — and `OFFSET` without `LIMIT` is refused too | — | S5 |
| `TestPageValuesAreRecordedAsWritten` | `internal/core/ql/inbound_test.go` | `LIMIT 20 OFFSET 40` records 20 and 40 with `Has` set; no clause leaves `Has` clear; and `LIMIT 0` parses with `Has` set and `Limit` zero — so "no page" and "no rows" stay distinguishable | — | S6 |
| `TestQueryLanguageDocIsComplete` | `internal/core/ql/doccoverage_test.go` | Existing gate. `Page` and `ErrJoinNotSupported` are new exports | — | S7 |
| `TestPublishedExamplesParse` | `internal/core/ql/docexamples_test.go` | Existing gate. Every published inbound example parses, and the refusals published as `-- refused` really are refused | — | S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests parse each form. |
| 2 — something selects it | `p.parseRead()` is the only entry, and it dispatches on the source form. |
| 3 — the caller can discover it | `ErrJoinNotSupported` names the form that was meant; both pages carry worked examples. |
| 4 — it is used | T2 evaluates what this parses; nothing else consumes an unparsed statement. |

## Mutation Log

- 2026-09-05 · d07eba2* · mutant killed · exit 1 · `internal/core/ql/parse.go` · reports every source as inbound, so FROM e and FROM [e] become the same question and a read of one entity silently becomes a read of everything pointing at it · acceptance-sha256:23c78fd569391097b1b44e7c4b57841760530dba98b2dabd019ab4fb41d3181c · covers:a bracketed source parsing as a set rather than an entity
- 2026-09-05 · d07eba2* · mutant killed · exit 1 · `internal/core/ql/parse.go` · accepts a bare attribute inside an inbound read and a marked one outside it, making the two spellings synonyms — which spends the notation a join needs, so adding the join later changes what already-written statements mean instead of failing them · acceptance-sha256:23c78fd569391097b1b44e7c4b57841760530dba98b2dabd019ab4fb41d3181c · covers:a bare attribute inside an inbound read being refused rather than treated as a member's
- 2026-09-05 · d07eba2* · mutant killed · exit 1 · `internal/core/ql/parse.go` · allows LIMIT on a read of one entity, whose attributes are a shape rather than a sequence — so the parser accepts a statement the evaluator would have to invent an order for · acceptance-sha256:23c78fd569391097b1b44e7c4b57841760530dba98b2dabd019ab4fb41d3181c · covers:a paging clause being refused where there is nothing to page

## Invariants

- The bracketed identifier is stored without its brackets; `Inbound` carries the difference.
- The `->` marker is grammar and never part of an attribute name.
- A statement that parses is one the evaluator can run without guessing.
- "No page clause" and "`LIMIT 0`" are different states.

## Risks

- ⚠ **Accepting both `a` and `->a` inside an inbound read is the friendly-looking mistake.** It spends the notation a join needs, and because both spellings would already parse, adding the join later would change what existing statements mean rather than failing them. The refusal is the decision.
- ⚠ **`LIMIT` already exists for `SEARCH`.** Reusing the keyword is right, but it must not become reachable on a non-inbound read just because the parser has a paging routine in hand.
- ⚠ **`Has` is not decoration.** Without it the zero value of `Page` reads as `LIMIT 0`, which returns nothing — so a bug in the flag makes the safest-looking default the one that silently empties every result.
- `->` is the reference marker in a write. Position disambiguates, but the parser must not accept it as a VALUE where an attribute is expected, or a malformed write would parse as a read-shaped nonsense.

## Stop Condition

Stop and ask before making a bare attribute inside an inbound read mean the
member's attribute. That is rule 3, and the cost is not the parse — it is that the
join can no longer be added without changing statements already written.

## Out of Scope

- Evaluating any of this (deferred: T2 — this task decides what may be said)
- The join itself (deferred: `docs/adr/BACKLOG.md` §20)
- `ORDER BY` (deferred: `docs/adr/BACKLOG.md` §20)
- Absence as a predicate (deferred: `docs/adr/BACKLOG.md` §20)

## Verification Log
- 2026-09-05 · d07eba2* · exit 0 · `set -o pipefail …` · acceptance-sha256:23c78fd569391097b1b44e7c4b57841760530dba98b2dabd019ab4fb41d3181c · ms:4655
- 2026-09-05 · d07eba2* · exit 0 · `set -o pipefail …` · acceptance-sha256:23c78fd569391097b1b44e7c4b57841760530dba98b2dabd019ab4fb41d3181c · ms:4606
- 2026-09-05 · d07eba2* · exit 0 · `set -o pipefail …` · acceptance-sha256:23c78fd569391097b1b44e7c4b57841760530dba98b2dabd019ab4fb41d3181c · ms:4510
- 2026-09-05 · d07eba2* · exit 0 · `set -o pipefail …` · acceptance-sha256:23c78fd569391097b1b44e7c4b57841760530dba98b2dabd019ab4fb41d3181c · ms:4425
