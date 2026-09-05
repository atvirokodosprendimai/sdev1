# Task ADR-036-T1: WITHOUT on a read — and never requiring what it excludes

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `ql.Read.Without`, the `WITHOUT` keyword
**Consumes:** `ql.Parse`, `ql.Read`, `ql.ErrJoinNotSupported` (ADR-035 T1); `ports.Carried` from ADR-003; `eval.latestVisible`, `eval.readInbound` from ADR-027/ADR-035
**Data dependency:** hermetic — a reader the test controls, and a real leaf
**Proof map:** v1
**Rests-on:** `an excluded attribute filtering without also being required`, `absence being the negation of what an entity CARRIES, so a retracted attribute is absent`, `WHERE and WITHOUT conjoining without a boolean operator`

## Goal

Make "has this and lacks that" sayable and runnable, without adding `AND` to the
grammar — and without the excluded attribute silently becoming a required one.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/ql/lex.go` | modify | `WITHOUT` joins the keyword table. |
| `internal/core/ql/ast.go` | modify | `Read.Without`. |
| `internal/core/ql/parse.go` | modify | The clause, between `WHERE` and the page. |
| `internal/core/ql/without_test.go` | add | The parser tests below. |
| `internal/core/eval/eval.go` | modify | `readOne` applies the filter. |
| `internal/core/eval/inbound.go` | modify | `memberOf` applies it, and the drop rule must not reach it. |
| `internal/core/eval/without_test.go` | add | The evaluator tests below. |
| `docs/QUERY-LANGUAGE.md`, `README.md` | modify | The clause, the grammar, the keyword list, and the snapshot-relative warning. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestAnExcludedAttributeIsNotAlsoRequired`, `TestAbsenceIsWhatAnEntityDoesNotCarry`, `TestWhereAndWithoutConjoin`, `TestWithoutObeysTheAttributeMarker`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Add the `WITHOUT` keyword and parse `WITHOUT a[, b]` into `Read.Without`, positioned after `WHERE` and before the page clause. Attributes obey ADR-035 rule 3's marker, checked by the same `checkMarkers` — one rule about what an attribute name means, applied everywhere one appears. [proof: mutation]
3. [S3] Filter on absence using the negation of `ports.Carried`, computing nothing new. ★A second definition of "does not have" would drift from the first, and the two would disagree about exactly the retracted attributes this clause exists to find. [proof: mutation]
4. [S4] ⚠EXEMPT the excluded attributes from ADR-035 rule 4's drop. A `WITHOUT` attribute is NAMED in order to be absent, so requiring its presence makes the clause unsatisfiable — and it fails by returning NOTHING, which is indistinguishable from a correct answer about data that does not exist. [proof: mutation]
5. [S5] Apply the clause to a read of one entity as well: same meaning, return the entity only if it lacks the named attributes. One rule rather than two. [proof: mutation]
6. [S6] Document the clause on both pages, INCLUDING that absence is snapshot-relative — "does not have one now", never "never had one". [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/eval/... ./internal/core/ql/... -race -run 'TestAnExcludedAttributeIsNotAlsoRequired|TestAbsenceIsWhatAnEntityDoesNotCarry|TestWhereAndWithoutConjoin|TestWithoutObeysTheAttributeMarker|TestQueryLanguageDocIsComplete|TestPublishedExamplesParse' -count=1 2>&1 | tee /tmp/adr036-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr036-t1a.out \
  && go test ./... -race -count=1 2>&1 | tee /tmp/adr036-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr036-t1b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAnExcludedAttributeIsNotAlsoRequired` | `internal/core/eval/without_test.go` | **The falsifier ADR-036 names in `Enforced-by:`.** `READ ->name FROM [staff] WITHOUT ->thirdname` returns the members carrying `name` and no `thirdname`. ⚠ It must return SOMETHING: applying the drop rule to the excluded attribute makes the clause unsatisfiable and empties the result, which looks exactly like a correct answer | — | S4 |
| `TestAbsenceIsWhatAnEntityDoesNotCarry` | `internal/core/eval/without_test.go` | Three members are absent for three DIFFERENT histories — never asserted, asserted then RETRACTED, and asserted over an interval not covering the instant — and one carrying the attribute is excluded. ★ The retracted case is the one a second definition of "has" would get wrong, and it is checked at two instants so the snapshot-relative rule is visible | — | S3 |
| `TestWhereAndWithoutConjoin` | `internal/core/eval/without_test.go` | `WHERE ->rank = 3 WITHOUT ->thirdname` returns only members satisfying BOTH, with each of the three near-misses excluded for its own reason — so the clauses conjoin with no operator, which is the point of rule 1 | — | S2 |
| `TestWithoutObeysTheAttributeMarker` | `internal/core/ql/without_test.go` | `WITHOUT thirdname` inside an inbound read and `WITHOUT ->thirdname` outside one are both `ErrJoinNotSupported`; the clause parses in both positions when spelled correctly; and `WITHOUT` with no attribute is refused | — | S2, S5 |
| `TestQueryLanguageDocIsComplete` | `internal/core/ql/doccoverage_test.go` | Existing gate | — | S6 |
| `TestPublishedExamplesParse` | `internal/core/ql/docexamples_test.go` | Existing gate. Every published `WITHOUT` example parses | — | S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests. |
| 2 — something selects it | `parseRead` reads the clause; `readOne` and `memberOf` both apply it. |
| 3 — the caller can discover it | Both published pages, with the snapshot-relative warning. |
| 4 — it is used | `cmd/sdev1-ql` runs it against a session or a leaf. |

## Mutation Log

- 2026-09-05 · 9a503d2* · mutant killed · exit 1 · `internal/core/eval/inbound.go` · lets ADR-035 rule 4 reach the excluded attributes, so a WITHOUT attribute becomes required as well as forbidden — the clause is unsatisfiable and fails by returning NOTHING, which is indistinguishable from a correct answer about data that does not exist · acceptance-sha256:e529e63ac7672630b408e42f83a14395d12c78c91d1f9cc743b5d8cf5c00b69c · covers:an excluded attribute filtering without also being required
- 2026-09-05 · 9a503d2* · mutant survived · exit 0 · `internal/core/eval/inbound.go` · answers "does it have this" by scanning the carried datoms for the NAME rather than by the map key, which is a second definition of having — it is equivalent today and is the shape that drifts, because it invites matching a datom that Carried has already suppressed · acceptance-sha256:e529e63ac7672630b408e42f83a14395d12c78c91d1f9cc743b5d8cf5c00b69c · covers:absence being the negation of what an entity CARRIES, so a retracted attribute is absent
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-05 · 9a503d2* · mutant killed · exit 1 · `internal/core/eval/inbound.go` · decides absence from the RAW visible datoms instead of what the entity CARRIES, so an attribute that was asserted and later RETRACTED still counts as present — the member is excluded on the strength of a fact that has been withdrawn, and WITHOUT can never find the entities that stopped having it · acceptance-sha256:e529e63ac7672630b408e42f83a14395d12c78c91d1f9cc743b5d8cf5c00b69c · covers:absence being the negation of what an entity CARRIES, so a retracted attribute is absent
- 2026-09-05 · 9a503d2* · mutant killed · exit 1 · `internal/core/ql/parse.go` · turns the absence clause into a predicate, overwriting any WHERE the caller wrote — so the two clauses stop conjoining and "has rank 3 and lacks a thirdname" silently becomes only the second half · acceptance-sha256:e529e63ac7672630b408e42f83a14395d12c78c91d1f9cc743b5d8cf5c00b69c · covers:WHERE and WITHOUT conjoining without a boolean operator

## Invariants

- Absence is the negation of `ports.Carried`, computed nowhere else.
- An excluded attribute is never also required.
- `WHERE` and `WITHOUT` conjoin, with no operator between them.
- The attribute marker rule is the same in `WITHOUT` as everywhere else.

## Risks

- ⚠ **Rule 4 is the whole task and its failure mode is silent.** ADR-035's drop rule, left untouched, drops every member for lacking the attribute the caller asked them to lack. The result is EMPTY, which is a completely plausible answer — so the test must assert on rows returned, never merely on the absence of an error.
- ⚠ **A test where every member lacks the excluded attribute proves nothing about the filter.** At least one member must CARRY it and be excluded, or the clause could be a no-op.
- ⚠ **The retracted case is the one that separates `ports.Carried` from a naive scan.** Without it, a second definition of "has" passes.
- Absence is snapshot-relative, so a test asserting it at one instant only is asserting less than it appears to. Two instants, with the answer changing between them.
- ⚠ **One mutant in the log SURVIVED, and it was the mutant that was wrong.** It replaced `carries`'s map-key lookup with a scan over the same map's VALUES for a matching attribute name — which is behaviourally identical, because `ports.Carried` keys by attribute. A surviving equivalent mutant is not a missing test; it is a mutant that changed no behaviour. ★ The mechanism was then bound by the mutant that actually breaks it: deciding absence from the RAW visible datoms instead of the carried set, so a RETRACTED attribute still counts as present. Recorded because the log shows both, and a reader should know which one was the finding.

## Stop Condition

Stop and ask before writing `WHERE NOT a`. That makes absence a predicate, and
"has `a` = x and lacks `b`" then needs `AND` — which is a grammar decision
`BACKLOG.md` §20 owns, not one to arrive at sideways.

## Out of Scope

- `WITHOUT` in a shape query (deferred: T2)
- `AND` / `OR` (deferred: `docs/adr/BACKLOG.md` §20)
- Indexing absence (deferred: `docs/adr/BACKLOG.md` §27)

## Verification Log
- 2026-09-05 · 9a503d2* · exit 0 · `set -o pipefail …` · acceptance-sha256:e529e63ac7672630b408e42f83a14395d12c78c91d1f9cc743b5d8cf5c00b69c · ms:4615
- 2026-09-05 · 9a503d2* · exit 0 · `set -o pipefail …` · acceptance-sha256:e529e63ac7672630b408e42f83a14395d12c78c91d1f9cc743b5d8cf5c00b69c · ms:4513
- 2026-09-05 · 9a503d2* · exit 0 · `set -o pipefail …` · acceptance-sha256:e529e63ac7672630b408e42f83a14395d12c78c91d1f9cc743b5d8cf5c00b69c · ms:4617
- 2026-09-05 · 9a503d2* · exit 0 · `set -o pipefail …` · acceptance-sha256:e529e63ac7672630b408e42f83a14395d12c78c91d1f9cc743b5d8cf5c00b69c · ms:4556
- 2026-09-05 · 9a503d2* · exit 0 · `set -o pipefail …` · acceptance-sha256:e529e63ac7672630b408e42f83a14395d12c78c91d1f9cc743b5d8cf5c00b69c · ms:4675
