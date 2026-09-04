# Task ADR-022-T1: ASSERT and RETRACT, with valid time and without transaction time

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `ql.Write`, `ql.WriteOp`, `ql.OpAssert`, `ql.OpRetract`, `ql.ErrTransactionTimeIsNotYours`, `ql.Write.Interval`
**Consumes:** `ql.Statement`, `ql.Lexer`, `ql.Parse` from ADR-011; `temporal.Interval` and `temporal.Forever` from ADR-002
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `a write refusing to carry a transaction qualifier`, `an omitted validity defaulting to the write's own instant rather than the beginning of time`, `a write naming exactly one entity`, `the write verbs being a closed pair`

## Goal

Let a caller state a fact and say when it was true, and make it structurally
impossible to say when it was recorded.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/ql/lex.go` | modify | The keywords `ASSERT`, `RETRACT`, `VALID`, `TO`. |
| `internal/core/ql/write.go` | add | `Write`, `WriteOp`, the parser, and the sentinels. |
| `internal/core/ql/parse.go` | modify | Dispatch the two write verbs. |
| `internal/core/ql/write_test.go` | add | The tests below. |
| `docs/QUERY-LANGUAGE.md` | modify | The statements, and why transaction time is absent. |

★ Backtick quoting already exists, so these four keywords cost nothing this time.
That is the earlier task paying off rather than luck.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestAWriteCannotSetTransactionTime`, `TestWriteRoundTripsThroughTheAST`, `TestOmittedValidityIsTheWriteInstantNotTheBeginningOfTime`, `TestAWriteNamesOneEntity`, `TestWriteVerbsAreAClosedPair`, `TestRetractCarriesItsInterval`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Add `ASSERT`, `RETRACT`, `VALID` and `TO` to the keyword set. [proof: acceptance]
3. [S3] Define `WriteOp` as a closed pair — `OpAssert` and `OpRetract` — with no third value and no CRUD spelling. ★The harm of an `update` verb is not at the API: it describes a data model this store does not have, so everything a caller infers about history and erasure is wrong. [proof: mutation]
4. [S4] Parse `ASSERT <entity> <attribute> = <value> [VALID FROM t [TO u]]`, and the same for `RETRACT`. One entity, one attribute — the grammar has nowhere to put a second. [proof: mutation]
5. [S5] REFUSE a `TRANSACTION` qualifier on a write, by name, rather than accepting and ignoring it. ⚠Silently ignoring an instruction is worse than refusing it, because the caller believes it took effect — and every read statement takes a `TRANSACTION` clause, so writing one here is what symmetry suggests. [proof: mutation]
6. [S6] Default an omitted validity to `[now, Forever)` where `now` is supplied by the caller executing the write, NOT to `[0, Forever)`. ⚠Defaulting to zero would silently claim every fact had been true since the beginning of time. [proof: mutation]
7. [S7] Document both statements in `docs/QUERY-LANGUAGE.md`, including why there is no transaction clause. ★The coverage gate fails until `Write`, `WriteOp` and the sentinels appear there. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/ql/... -race -run 'TestAWriteCannotSetTransactionTime|TestWriteRoundTrips|TestOmittedValidityIs|TestAWriteNamesOneEntity|TestWriteVerbsAreAClosedPair|TestRetractCarriesItsInterval|TestQueryLanguageDocIsComplete|TestPublishedExamplesParse' -count=1 2>&1 | tee /tmp/adr022-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr022-t1a.out \
  && go test ./internal/core/ql/... ./internal/core/temporal/... ./internal/core/command/... -race -count=1 2>&1 | tee /tmp/adr022-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr022-t1b.out
```

The second command re-runs ADR-011's and ADR-003's suites, because this task
changes a shared lexer and describes that package's write path.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAWriteCannotSetTransactionTime` | `internal/core/ql/write_test.go` | A `TRANSACTION` clause on a write is a parse failure naming why — **the falsifier ADR-022 names in `Enforced-by:`**. Also asserts the same clause is accepted on a READ, so the refusal is about writes and not about the keyword | — | S5 |
| `TestWriteRoundTripsThroughTheAST` | `internal/core/ql/write_test.go` | Both verbs parse to the `Write` a caller expects, with and without a validity clause, and with an open interval | — | S3, S4 |
| `TestOmittedValidityIsTheWriteInstantNotTheBeginningOfTime` | `internal/core/ql/write_test.go` | An omitted `VALID` clause resolves to `[now, Forever)` for a supplied `now`, and specifically NOT to `[0, Forever)` | — | S6 |
| `TestAWriteNamesOneEntity` | `internal/core/ql/write_test.go` | A second entity does not parse, so a shape that could never commit is refused at the point it is written | — | S4 |
| `TestWriteVerbsAreAClosedPair` | `internal/core/ql/write_test.go` | `INSERT`, `UPDATE`, `DELETE`, `SET` and `MERGE` do not parse as statements, and `WriteOp` has exactly two values | — | S3 |
| `TestRetractCarriesItsInterval` | `internal/core/ql/write_test.go` | A retraction states when the fact stopped holding, and an omitted clause retracts from the write's instant rather than rewriting history | — | S4, S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The six tests above. |
| 2 — something selects it | `Parse` dispatches both verbs, so a write is reachable from the one entry point the language has. |
| 3 — the caller can discover it | Both statements are in `docs/QUERY-LANGUAGE.md`, and the coverage gate fails until they are. |
| 4 — it is used | T2's session executes them, and `cmd/sdev1-ql` runs it. |

## Mutation Log

- 2026-09-04 · 97b0636* · mutant killed · exit 1 · `internal/core/ql/write.go` · lets a write carry a TRANSACTION clause, which is what symmetry with the read statements suggests. A caller can then state when a fact was RECORDED, claiming to have known something earlier than they did — and no query can detect it, because every query evidence is the value that was forged · acceptance-sha256:55f29920f75edfa95f611d6a1123684d302cdd344bc1afb1abf6ceee336db472 · covers:a write refusing to carry a transaction qualifier
- 2026-09-04 · 97b0636* · mutant killed · exit 1 · `internal/core/ql/write.go` · defaults an omitted VALID clause to instant zero instead of the write own instant, silently claiming every fact had been true since the beginning of time. Nothing about the resulting datom looks unusual, so it is invisible until somebody asks a historical question · acceptance-sha256:55f29920f75edfa95f611d6a1123684d302cdd344bc1afb1abf6ceee336db472 · covers:an omitted validity defaulting to the write's own instant rather than the beginning of time
- 2026-09-04 · 97b0636* · mutant killed · exit 1 · `internal/core/ql/write.go` · accepts a second entity name and quietly rebinds the write to it, so a statement that could never respect the one-entity transaction boundary parses cleanly and fails much later at commit — if it fails at all · acceptance-sha256:55f29920f75edfa95f611d6a1123684d302cdd344bc1afb1abf6ceee336db472 · covers:a write naming exactly one entity
- 2026-09-04 · 97b0636* · mutant killed · exit 1 · `internal/core/ql/parse.go` · accepts the CRUD spellings alongside the two real verbs, which is the direction a language decays in once callers ask for familiar words. UPDATE and DELETE describe in-place mutation, which this store does not do — so a caller who uses them reasons wrongly about history, retraction and erasure, and nothing reports it · acceptance-sha256:55f29920f75edfa95f611d6a1123684d302cdd344bc1afb1abf6ceee336db472 · covers:the write verbs being a closed pair

## Invariants

- No write statement admits a transaction qualifier.
- An omitted validity is the write's own instant, never zero.
- A write names one entity and one attribute.
- There are exactly two write verbs.

## Risks

- ⚠ **Adding `TRANSACTION` to writes for symmetry is the single most likely change here**, because every read statement takes one and writes look inconsistent without it. The falsifier asserts the clause does not parse on a write AND that it still parses on a read, so a lazy fix that removed the keyword entirely would fail too.
- ⚠ **"An omitted validity is fine" is easy to satisfy with the zero value**, which silently claims the fact was true since the beginning of time. The test asserts the resolved interval starts at the supplied instant and explicitly not at zero.
- ⚠ **A closed verb pair is easy to assert by listing the two that work.** The test also asserts the five CRUD spellings do NOT parse, because that is the direction the language decays in.
- The value is taken as a literal, and typing it — number, string, or a reference to another entity — is deliberately not decided here. References are `ADR-023`'s.

## Stop Condition

Stop and ask before letting any caller-supplied value reach transaction time. It
is the record of when this system was told, and a caller who can forge it makes
every historical answer a claim rather than a record — retroactively, for data
already stored.

## Out of Scope

- Executing a write (this record's T2)
- Typed values and references between entities (deferred: `docs/adr/ADR-023-links-and-traversal.md`)
- Writing several attributes in one statement (deferred: `docs/adr/BACKLOG.md` §28)

## Verification Log
- 2026-09-04 · 97b0636* · exit 0 · `set -o pipefail …` · acceptance-sha256:55f29920f75edfa95f611d6a1123684d302cdd344bc1afb1abf6ceee336db472 · ms:3889
- 2026-09-04 · 97b0636* · exit 0 · `set -o pipefail …` · acceptance-sha256:55f29920f75edfa95f611d6a1123684d302cdd344bc1afb1abf6ceee336db472 · ms:3672
- 2026-09-04 · 97b0636* · exit 0 · `set -o pipefail …` · acceptance-sha256:55f29920f75edfa95f611d6a1123684d302cdd344bc1afb1abf6ceee336db472 · ms:3718
- 2026-09-04 · 97b0636* · exit 0 · `set -o pipefail …` · acceptance-sha256:55f29920f75edfa95f611d6a1123684d302cdd344bc1afb1abf6ceee336db472 · ms:3894
- 2026-09-04 · 97b0636* · exit 0 · `set -o pipefail …` · acceptance-sha256:55f29920f75edfa95f611d6a1123684d302cdd344bc1afb1abf6ceee336db472 · ms:3771
