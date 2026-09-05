# Task ADR-034-T1: Make READ the verb and make typing SELECT say so

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `ql.Read`, `ql.ErrSelectRenamed`, `eval.Read`
**Consumes:** `ql.Parse`, `ql.ParseError`, `ql.TimeClause` from ADR-011; `eval.Row` from ADR-027
**Data dependency:** hermetic — parsing is a pure function, and the documentation gates read files in the repository
**Proof map:** v1
**Rests-on:** `typing the old verb producing an error that names the new one`, `the old verb remaining addressable as a quoted attribute name`, `the published documentation parsing under the new verb`

## Goal

Replace `SELECT` with `READ` everywhere — keyword, AST type, evaluator entry
point, tests and both published pages — and leave `SELECT` reserved so that
typing it produces an error naming `READ` rather than a parse failure inside the
projection.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/ql/lex.go` | modify | `READ` joins the keyword table; `SELECT` stays in it so it can be refused by name. |
| `internal/core/ql/ast.go` | modify | `Select` → `Read`. Rule 2: the type is renamed with the keyword. |
| `internal/core/ql/parse.go` | modify | `parseRead`, the refusal for `SELECT`, `ErrSelectRenamed`, and `ParseError.Err` so the sentinel is reachable through `errors.Is`. |
| `internal/core/ql/read_test.go` | add | The falsifier and the quoting case. |
| `internal/core/eval/eval.go` | modify | `Select` → `Read`. |
| `internal/core/eval/doc.go` | modify | The package comment names the statement. |
| `internal/core/session/session.go` | modify | Dispatch on `*ql.Read`. |
| `internal/core/ql/*_test.go`, `internal/core/eval/eval_test.go`, `internal/core/session/*_test.go`, `internal/core/leafstore/compact_test.go` | modify | Every statement under test is written in the language, so every one changes. |
| `README.md`, `docs/QUERY-LANGUAGE.md` | modify | The two gated pages. `QUERY-LANGUAGE.md` also documents `ErrSelectRenamed` and publishes the refusal as an example marked `-- refused`. |
| `internal/core/ports/ports.go`, `internal/core/observe/kinds.go`, `internal/core/leafstore/leafstore.go`, `internal/core/session/doc.go`, `internal/core/ql/doc.go`, `cmd/sdev1-ql/main.go`, `TODO.md` | modify | Prose and identifiers that name the verb. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestSelectIsRefusedByName`, `TestReadReplacesSelect`, `TestSelectIsStillAddressableAsAnAttribute`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Add `READ` to the keyword table and keep `SELECT` there. ⚠Removing `SELECT` is the tempting three-line version and it is what rule 3 rejects: an unreserved `SELECT` lexes as an identifier, so the statement fails inside the projection with a message about attribute names and never mentions the verb. [proof: mutation]
3. [S3] Rename the AST type to `ql.Read` and the evaluator entry point to `eval.Read`. ★ADR-032 is the precedent — a type called `Select` behind a keyword called `READ` is a name that says one thing and means another. [proof: mutation]
4. [S4] Refuse a statement beginning with `SELECT`, with an error that NAMES `READ`, and make it match `errors.Is(err, ql.ErrSelectRenamed)` by giving `ParseError` an `Err` field and an `Unwrap`. [proof: mutation]
5. [S5] Rewrite every statement in the test suite, both published pages and the prose that names the verb. Publish the refusal in `docs/QUERY-LANGUAGE.md` as an example marked `-- refused`, so the gate asserts the refusal rather than skipping it. [proof: acceptance]
6. [S6] Confirm `` `select` `` still lexes as an identifier, so reserving the word did not take an attribute name away. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/ql/... -race -run 'TestSelectIsRefusedByName|TestReadReplacesSelect|TestSelectIsStillAddressableAsAnAttribute|TestQueryLanguageDocIsComplete|TestPublishedExamplesParse' -count=1 2>&1 | tee /tmp/adr034-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr034-t1a.out \
  && go test ./... -race -count=1 2>&1 | tee /tmp/adr034-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr034-t1b.out \
  && ! grep -rnE '\bSELECT\b[[:space:]]+([*`a-zA-Z]|FROM)' README.md docs/QUERY-LANGUAGE.md | grep -qv refused
```

The second command is the whole suite rather than three packages: a verb rename
reaches every test that writes a statement, and the point of the fence is that a
partial rename fails.

The third is the documentation half, and it matches `SELECT` **used as a verb** —
followed by whitespace and a projection — rather than every occurrence of the
word. ⚠ The blunt version was written first and was wrong in both directions: it
fired on the keyword table's own entry and on the sentence explaining the rename,
so it would have had to be silenced with an exclusion list that grew every time
the pages explained anything. What it must catch is a page still TEACHING the old
verb, and that is the shape it now matches. Every hit must carry `refused`.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestSelectIsRefusedByName` | `internal/core/ql/read_test.go` | `SELECT * FROM planet-3` returns an error whose text contains `READ` and which matches `errors.Is(err, ErrSelectRenamed)` — **the falsifier ADR-034 names in `Enforced-by:`**. ⚠ Asserting only "it errors" would pass with the keyword deleted, which is the rejected alternative, so the assertion is on the MESSAGE | — | S2, S4 |
| `TestReadReplacesSelect` | `internal/core/ql/read_test.go` | `READ * FROM planet-3 WHERE mass > 5 AS OF 1000` parses to a `*Read` carrying the same projection, entity, predicate and time clause the old verb produced — the rename changed a word and nothing else | — | S3 |
| `TestSelectIsStillAddressableAsAnAttribute` | `internal/core/ql/read_test.go` | `` READ `select` FROM planet-3 `` projects the attribute named `select`, and `` ASSERT planet-3 `select` = 1 `` writes it — reserving a word must not take an attribute name away | — | S6 |
| `TestQueryLanguageDocIsComplete` | `internal/core/ql/doccoverage_test.go` | Existing gate. `ErrSelectRenamed` is a new export, so the guide fails until it is documented | — | S5 |
| `TestPublishedExamplesParse` | `internal/core/ql/docexamples_test.go` | Existing gate. Every published example parses under `READ`, and the one marked `-- refused` is asserted to really be refused | — | S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestReadReplacesSelect` parses a full statement under the new verb. |
| 2 — something selects it | `p.statement()` dispatches on the keyword; the whole test suite and both pages run through `Parse`. |
| 3 — the caller can discover it | The refusal names `READ`, and `docs/QUERY-LANGUAGE.md` is gated on documenting the sentinel. |
| 4 — it is used | `cmd/sdev1-ql` runs statements, and `session` dispatches `*ql.Read` on the served path. |

## Mutation Log

- 2026-09-05 · db6f53e* · mutant killed · exit 1 · `internal/core/ql/lex.go` · removes SELECT from the keyword table, so it lexes as an ordinary identifier and the statement dies inside the projection with a message about attribute names — never naming the verb the caller got wrong · acceptance-sha256:0eb89ad7393030b2e76ba5778e5e7bb6de7805ef9910c0d6c07944ff2945fbf8 · covers:typing the old verb producing an error that names the new one
- 2026-09-05 · db6f53e* · mutant killed · exit 1 · `internal/core/ql/lex.go` · consults the keyword table inside backticks, so reserving SELECT takes the attribute named `select` away — it becomes unreadable, unwritable, and unmigratable, which is the silent breaking change ADR-021 added quoting to prevent · acceptance-sha256:0eb89ad7393030b2e76ba5778e5e7bb6de7805ef9910c0d6c07944ff2945fbf8 · covers:the old verb remaining addressable as a quoted attribute name
- 2026-09-05 · db6f53e* · mutant inconclusive · exit 1 · `internal/core/ql/parse.go` · accepts SELECT as an alias for READ instead of refusing it — rule 4 — so the published example marked `-- refused` quietly parses and the documentation teaches a second spelling of the verb · acceptance-sha256:0eb89ad7393030b2e76ba5778e5e7bb6de7805ef9910c0d6c07944ff2945fbf8 · covers:the published documentation parsing under the new verb
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-05 · db6f53e* · mutant killed · exit 1 · `internal/core/ql/parse.go` · accepts SELECT as an alias for READ instead of refusing it — rule 4 — so the published example marked `-- refused` quietly parses, and the documentation ships a second spelling of the verb that nothing says is not canonical · acceptance-sha256:0eb89ad7393030b2e76ba5778e5e7bb6de7805ef9910c0d6c07944ff2945fbf8 · covers:the published documentation parsing under the new verb

## Invariants

- The refusal for `SELECT` names `READ`.
- A backticked `` `select` `` is an identifier, whatever the keyword table holds.
- Nothing about what the statement MEANS changes: same projection, same `FROM`, same `WHERE`, same time clauses.

## Risks

- ⚠ **A test asserting only that `SELECT` errors is satisfied by deleting the keyword** — which is the alternative rule 3 rejects, because the error then lands inside the projection and never names the verb. The assertion is on the message and on the sentinel, not on the fact of failure.
- ⚠ **A rename is easy to do partially, and the partial state compiles.** Go's compiler catches the Go half; nothing but the two documentation gates catches the markdown half, which is why the fence greps the published pages as well as running them.
- ⚠ **Reserving a word costs an attribute name unless quoting still reaches it.** ADR-021 paid for that in advance; this task must show the payment still works rather than assume it.
- The `-- refused` marker means a published example is asserted to fail. If the refusal is later softened into an alias, that example flips to a failure — which is the gate working, and is how rule 4 gets defended without anyone remembering it.

## Stop Condition

Stop and ask before accepting `SELECT` as an alias for `READ`. Rule 4 rejects it:
a migration aid that parses is a second spelling of the verb, permanently, and
both then have to be documented and tested.

## Out of Scope

- Reading a set of entities rather than one named entity (deferred: `docs/adr/BACKLOG.md` §20)
- Paging (deferred: `docs/adr/BACKLOG.md` §20)
- Renaming any other verb (permanent: boundary: `ASSERT`, `RETRACT`, `SEARCH` and `TRAVERSE` already say what they do)
- Changing what the statement means (permanent: boundary: ADR-011 owns the grammar and ADR-027 owns evaluation)

## Verification Log
- 2026-09-05 · db6f53e* · exit 0 · `set -o pipefail …` · acceptance-sha256:0eb89ad7393030b2e76ba5778e5e7bb6de7805ef9910c0d6c07944ff2945fbf8 · ms:4566
- 2026-09-05 · db6f53e* · exit 0 · `set -o pipefail …` · acceptance-sha256:0eb89ad7393030b2e76ba5778e5e7bb6de7805ef9910c0d6c07944ff2945fbf8 · ms:4538
- 2026-09-05 · db6f53e* · exit 0 · `set -o pipefail …` · acceptance-sha256:0eb89ad7393030b2e76ba5778e5e7bb6de7805ef9910c0d6c07944ff2945fbf8 · ms:4542
- 2026-09-05 · db6f53e* · exit 0 · `set -o pipefail …` · acceptance-sha256:0eb89ad7393030b2e76ba5778e5e7bb6de7805ef9910c0d6c07944ff2945fbf8 · ms:4508
- 2026-09-05 · db6f53e* · exit 0 · `set -o pipefail …` · acceptance-sha256:0eb89ad7393030b2e76ba5778e5e7bb6de7805ef9910c0d6c07944ff2945fbf8 · ms:4468
