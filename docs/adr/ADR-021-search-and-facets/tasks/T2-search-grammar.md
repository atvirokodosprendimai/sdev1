# Task ADR-021-T2: The SEARCH statement, and quoted identifiers to pay for it

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `ql.Search`, `ql.ErrNoSearchLimit`
**Consumes:** `ql.Statement`, `ql.TimeClause`, `ql.Lexer` from ADR-011
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `a SEARCH without a limit failing to parse`, `a quoted identifier surviving a keyword collision`, `the time clause on a search being the same clause every statement carries`

⚠ **This task modifies `internal/core/ql/**`, which ADR-011 governs.** ADR-021's
`Invalidates:` header already records the amendment: ordering and limiting stay
omitted for `SELECT` and are lifted for `SEARCH`, where ranking exists.

## Goal

Make search sayable, and pay the bill that adding five keywords to a language
with no quoting mechanism would otherwise leave for its users.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/ql/lex.go` | modify | Five new keywords, and backtick-quoted identifiers. |
| `internal/core/ql/search.go` | add | `Search`, its parser, and `ErrNoSearchLimit`. |
| `internal/core/ql/parse.go` | modify | Dispatch `SEARCH` alongside `SELECT` and `MATCH`. |
| `internal/core/ql/search_test.go` | add | The tests below. |
| `docs/QUERY-LANGUAGE.md` | modify | The statement, the new keywords, and quoting. |

★ **The quoting is not a nice-to-have bundled in — it is the cost of this task.**
Adding `SEARCH`, `IN`, `FACET`, `BY` and `LIMIT` reserves five ordinary English
words that were previously legal attribute names. In a language with no way to
escape a keyword, that silently makes any entity with a `limit` or `in`
attribute unreadable. Shipping the keywords without the escape hatch would break
data that parsed yesterday.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestSearchRoundTripsThroughTheAST`, `TestSearchWithoutALimitDoesNotParse`, `TestSearchCarriesTheSameTimeClause`, `TestQuotedIdentifierSurvivesAKeywordCollision`, `TestSearchFacetsAreOptional`, `TestNewKeywordsAreReserved`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Add backtick-quoted identifiers to the lexer: `` `limit` `` lexes as an identifier whatever the word inside. ⚠Do this BEFORE adding the keywords, so the escape hatch exists at the moment the collision is created rather than after. [proof: mutation]
3. [S3] Add `SEARCH`, `IN`, `FACET`, `BY` and `LIMIT` to the keyword set.
4. [S4] Define `Search` — the query text, the attributes searched, the facets requested, the limit, and a time clause.
5. [S5] Parse `SEARCH <text> IN <attrs> [FACET BY <attrs>] LIMIT <n> [timeclause]`, refusing a missing or non-positive limit by name. ★The limit is required for ADR-021 rule 7: an unranked, unlimited search is a full scan with extra steps. [proof: mutation]
6. [S6] Attach the SAME `TimeClause` every other statement carries, so a search inherits the two axes rather than growing a second spelling of them. [proof: mutation]
7. [S7] Document the statement, the five new keywords and the quoting rule in `docs/QUERY-LANGUAGE.md`. ★The coverage gate from ADR-011 T1 fails until `Search` and `ErrNoSearchLimit` appear there, so this step is enforced rather than remembered. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/ql/... -race -run 'TestSearchRoundTrips|TestSearchWithoutALimit|TestSearchCarriesTheSameTimeClause|TestQuotedIdentifier|TestSearchFacetsAreOptional|TestNewKeywordsAreReserved|TestQueryLanguageDocIsComplete' -count=1 2>&1 | tee /tmp/adr021-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr021-t2a.out \
  && go test ./internal/core/ql/... ./internal/core/search/... ./internal/core/temporal/... -race -count=1 2>&1 | tee /tmp/adr021-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr021-t2b.out
```

The second command re-runs ADR-011's and T1's suites, because this task changes a
shared lexer and must not be able to land by breaking either.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestSearchRoundTripsThroughTheAST` | `internal/core/ql/search_test.go` | A full statement parses to the `Search` a caller expects — query text, attributes, facets, limit | — | S4, S5 |
| `TestSearchWithoutALimitDoesNotParse` | `internal/core/ql/search_test.go` | A missing, zero or negative limit is refused by name rather than defaulted, so an unbounded ranked scan cannot be asked for | — | S5 |
| `TestSearchCarriesTheSameTimeClause` | `internal/core/ql/search_test.go` | All four time-clause shapes attach to a search and resolve through the same table every other statement uses | — | S6 |
| `TestQuotedIdentifierSurvivesAKeywordCollision` | `internal/core/ql/search_test.go` | An attribute named `limit`, `in` or `select` is addressable when backtick-quoted, in a `SELECT` projection and in a `WHERE` — the escape hatch that pays for the new keywords | — | S2 |
| `TestSearchFacetsAreOptional` | `internal/core/ql/search_test.go` | `FACET BY` may be omitted entirely, and when present accepts several attributes | — | S5 |
| `TestNewKeywordsAreReserved` | `internal/core/ql/search_test.go` | The five new words lex as keywords, so the collision this task creates is real and the quoting above is load-bearing rather than decorative | — | S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The six tests above. |
| 2 — something selects it | `Parse` dispatches `SEARCH`, so `Search` is reachable from the one entry point the language has. |
| 3 — the caller can discover it | The statement is in `docs/QUERY-LANGUAGE.md`, and the coverage gate fails until it is. |
| 4 — it is used | T3 executes a parsed search against an index. |

## Mutation Log

- 2026-09-04 · fda1ea4* · mutant killed · exit 1 · `internal/core/ql/search.go` · gives LIMIT a default of 100 instead of requiring it, which reads as a sensible convenience and is how an unbounded ranked scan gets asked for by accident. The cost of a search stops being something the caller chose, and search is the largest fan-out a single request can cause in this system · acceptance-sha256:940603417b63e08732ed543c4b1604cb5aec3b0e096d8fccc64bf184f7f48035 · covers:a SEARCH without a limit failing to parse
- 2026-09-04 · fda1ea4* · mutant killed · exit 1 · `internal/core/ql/lex.go` · consults the keyword table inside the quotes, which looks like consistency and destroys the entire point: the escape hatch stops escaping exactly the words it exists for. An attribute named limit or in becomes unaddressable again, and the five keywords this statement added silently orphan any data carrying one · acceptance-sha256:940603417b63e08732ed543c4b1604cb5aec3b0e096d8fccc64bf184f7f48035 · covers:a quoted identifier surviving a keyword collision
- 2026-09-04 · fda1ea4* · mutant killed · exit 1 · `internal/core/ql/search.go` · parses the time clause and then discards it, so a search silently answers about NOW however the caller qualified it. The statement still parses and the qualifier still validates, which is why nothing looks wrong — and search becomes the one surface unable to answer the question this whole system exists to answer · acceptance-sha256:940603417b63e08732ed543c4b1604cb5aec3b0e096d8fccc64bf184f7f48035 · covers:the time clause on a search being the same clause every statement carries

## Invariants

- A `SEARCH` without a positive limit does not parse.
- A backtick-quoted word is an identifier whatever it spells.
- A search's time clause is the same type every other statement carries.

## Risks

- ⚠ **Adding keywords to a language with no escape is a silent breaking change.** Five ordinary English words stop being usable as attribute names, and the failure is a parse error on data that worked yesterday. The quoting lands in the same task, before the keywords, rather than being filed as a follow-up.
- ⚠ **A quoting test that only quotes a NON-keyword proves nothing.** The test quotes words that genuinely collide — `limit`, `in`, `select` — because quoting an ordinary identifier would pass even if the lexer ignored backticks entirely.
- ⚠ **"The limit is required" is easy to satisfy with a default.** The test asserts a missing limit is a parse FAILURE, not that a limit is present in the tree; a default would satisfy the second and defeat the rule.
- The query text is taken as a string and analysed later. Whether the query itself has syntax — phrases, negation, wildcards — is deliberately not decided here.

## Stop Condition

Stop and ask before giving `LIMIT` a default, however reasonable the number. The
limit exists so that the cost of a search is always something the caller chose,
and search is the largest fan-out a single request can cause in this system.

## Out of Scope

- Executing the search (this record's T3)
- Persisting an index, and confirming candidates against the datoms (T4)
- Query syntax inside the search text — phrases, negation, wildcards (deferred: `docs/adr/BACKLOG.md` §27)

## Verification Log
- 2026-09-04 · fda1ea4* · exit 0 · `set -o pipefail …` · acceptance-sha256:940603417b63e08732ed543c4b1604cb5aec3b0e096d8fccc64bf184f7f48035 · ms:3727
- 2026-09-04 · fda1ea4* · exit 0 · `set -o pipefail …` · acceptance-sha256:940603417b63e08732ed543c4b1604cb5aec3b0e096d8fccc64bf184f7f48035 · ms:3706
- 2026-09-04 · fda1ea4* · exit 0 · `set -o pipefail …` · acceptance-sha256:940603417b63e08732ed543c4b1604cb5aec3b0e096d8fccc64bf184f7f48035 · ms:3781
- 2026-09-04 · fda1ea4* · exit 0 · `set -o pipefail …` · acceptance-sha256:940603417b63e08732ed543c4b1604cb5aec3b0e096d8fccc64bf184f7f48035 · ms:3848
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:940603417b63e08732ed543c4b1604cb5aec3b0e096d8fccc64bf184f7f48035 · ms:3762
