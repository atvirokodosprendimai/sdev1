# Task ADR-022-T2: A session that runs statements, and a binary that shows it

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `session.Session`, `session.New`, `session.Session.Run`, `session.Result`, `session.ErrUnsupported`, `cmd/sdev1-ql`
**Consumes:** `ql.Parse`, `ql.Write`, `ql.Select`, `ql.Search` (T1 and ADR-011/021); `ports.Datom` and `temporal.Visible` from ADR-002/003; `search.Index`, `search.Seal`, `search.Analyze` from ADR-021; `crypt.MemoryKeystore` from ADR-007; `tx.Minter` from ADR-002
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `a written fact being readable back through the language`, `a read at a past instant not seeing a later write`, `the session assigning transaction time rather than the caller`, `a search finding a fact that was asserted rather than indexed by hand`

## Goal

Close the loop. Make it possible to create a record, read it back, search it and
facet it — in one process, from statements a person typed.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/session/doc.go` | add | What the session is, and emphatically what it is not. |
| `internal/core/session/session.go` | add | `Session`, `New`, `Run`, `Result`, `ErrUnsupported`. |
| `internal/core/session/session_test.go` | add | The tests below. |
| `cmd/sdev1-ql/main.go` | add | Read statements, run them, print what happened. |
| `README.md` | modify | A quick start that creates records and searches them, with real output. |

★ **This exists because a system nobody can run is a system nobody can check.**
Twenty-two records were decidable and tested, and none of it was demonstrable —
a reader could not create a single fact. That is the gap this closes.

⚠ **It is NOT the storage engine.** Datoms live in a map, vanish on exit, span no
leaf and never reach a disk. It adds no rule of its own: every meaning it
implements — two-axis visibility, the erasure boundary, the facet refusal — comes
from the packages the records already govern. That constraint is what stops it
quietly becoming the specification.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestAssertThenSelectReadsItBack`, `TestAReadAtAPastInstantDoesNotSeeALaterWrite`, `TestTheSessionAssignsTransactionTime`, `TestAssertThenSearchFindsIt`, `TestRetractedFactIsNotReturned`, `TestUnsupportedStatementIsNamed`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Implement `Session` holding datoms per entity, a keystore and a search index. Nothing durable, and no leaf. [proof: acceptance]
3. [S3] Apply a `ql.Write` by minting a transaction with `tx.Minter` and appending a `ports.Datom`. ⚠The instant comes from the SESSION, never from the statement — T1 made it unsayable and this is the layer that must not reintroduce it. [proof: mutation]
4. [S4] Answer a `ql.Select` by filtering datoms through `temporal.Visible` at the resolved snapshot, so the two axes behave exactly as ADR-002's table says. [proof: mutation]
5. [S5] Index every asserted value as it is written, so a `ql.Search` finds facts that were ASSERTED rather than facts a test indexed by hand. ★An index fed by a separate path in the test proves nothing about the write path. [proof: mutation]
6. [S6] Refuse a statement the session cannot run — `MATCH SHAPE` needs a similarity metric — by name, rather than returning an empty result. ⚠An empty result is indistinguishable from "nothing matched", which is the wrong answer to "this is not implemented".
7. [S7] Build `cmd/sdev1-ql`: statements from a file, from arguments or from standard input; print rows, hits and facets. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/session/... -race -run 'TestAssertThenSelectReadsItBack|TestAReadAtAPastInstant|TestTheSessionAssignsTransactionTime|TestAssertThenSearchFindsIt|TestRetractedFactIsNotReturned|TestUnsupportedStatementIsNamed' -count=1 2>&1 | tee /tmp/adr022-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr022-t2a.out \
  && go build -o /dev/null ./cmd/sdev1-ql/... \
  && go run ./cmd/sdev1-ql --statements "ASSERT planet-7 mass = 5972 VALID FROM 100" --statements "SELECT * FROM planet-7" 2>&1 | tee /tmp/adr022-t2b.out \
  && grep -q "mass" /tmp/adr022-t2b.out
```

⚠ `-o /dev/null` is not decoration. `go build ./cmd/<name>/...` writes the binary
into the working directory, so a fence that builds a command litters the
repository root — and on 2026-09-04 one was committed that way before anybody
noticed. `.gitignore` carries the backstop; this is the fix.

The last two segments build and RUN the binary, then check its output carries the
attribute that was written. A binary that compiles and does nothing would
otherwise pass.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAssertThenSelectReadsItBack` | `internal/core/session/session_test.go` | A fact written by a statement is returned by a statement — the loop this task exists to close | — | S3, S4 |
| `TestAReadAtAPastInstantDoesNotSeeALaterWrite` | `internal/core/session/session_test.go` | A `SELECT … AS OF` before a fact's validity does not see it, so the two axes are honoured rather than reimplemented | — | S4 |
| `TestTheSessionAssignsTransactionTime` | `internal/core/session/session_test.go` | Two writes get strictly increasing transaction identifiers minted by the session, and nothing a caller wrote reaches them | — | S3 |
| `TestAssertThenSearchFindsIt` | `internal/core/session/session_test.go` | `SEARCH` finds a fact that arrived through `ASSERT`, indexed on the write path rather than by the test | — | S5 |
| `TestRetractedFactIsNotReturned` | `internal/core/session/session_test.go` | A retraction removes the fact from a later read while remaining a datom, so "stopped being true" is not "never recorded" | — | S3, S4 |
| `TestUnsupportedStatementIsNamed` | `internal/core/session/session_test.go` | `MATCH SHAPE` returns `ErrUnsupported` naming what is missing, rather than an empty result that reads as "nothing matched" | — | S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The six tests above. |
| 2 — something selects it | `cmd/sdev1-ql` is the only caller of `Session`, and the fence RUNS it rather than only building it. |
| 3 — the caller can discover it | The README's quick start is these commands, with the output they actually print. |
| 4 — it is used | By anyone checking what this system can do, which was previously impossible. |

## Mutation Log

- 2026-09-04 · 97b0636* · mutant killed · exit 1 · `internal/core/session/session.go` · drops every write to an entity after its first, so the statement reports success and the fact is simply not there. The write path still returns a datom, so nothing looks wrong until somebody reads it back · acceptance-sha256:96ca63369ba5149d521ee9a6ee4f728b7a35962c660d62101c45cf266be83bc3 · covers:a written fact being readable back through the language
- 2026-09-04 · 97b0636* · mutant inconclusive · exit 1 · `internal/core/session/session.go` · ignores the visibility predicate, so every read returns the latest value whatever instant it asked about. Time travel silently becomes a no-op: AS OF parses, resolves, and changes nothing about the answer · acceptance-sha256:96ca63369ba5149d521ee9a6ee4f728b7a35962c660d62101c45cf266be83bc3 · covers:a read at a past instant not seeing a later write
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-04 · 97b0636* · mutant killed · exit 1 · `internal/core/session/session.go` · stops excluding datoms the snapshot cannot see, so every read returns the latest value whatever instant it asked about. Time travel becomes a silent no-op: AS OF still parses and still resolves, and the answer is identical either way · acceptance-sha256:96ca63369ba5149d521ee9a6ee4f728b7a35962c660d62101c45cf266be83bc3 · covers:a read at a past instant not seeing a later write
- 2026-09-04 · 97b0636* · mutant killed · exit 1 · `internal/core/session/session.go` · lets a statement VALID FROM clause move the TRANSACTION identifier — the exact forgery the language was shaped to make unsayable, reintroduced one layer down where nothing about the grammar would reveal it · acceptance-sha256:96ca63369ba5149d521ee9a6ee4f728b7a35962c660d62101c45cf266be83bc3 · covers:the session assigning transaction time rather than the caller
- 2026-09-04 · 97b0636* · mutant killed · exit 1 · `internal/core/session/session.go` · stops indexing on the write path, so SEARCH finds nothing that arrived through ASSERT. A test that populated the index itself would still pass, which is exactly why the test only ever touches the index through the two statements · acceptance-sha256:96ca63369ba5149d521ee9a6ee4f728b7a35962c660d62101c45cf266be83bc3 · covers:a search finding a fact that was asserted rather than indexed by hand
- 2026-09-04 · dba7be4* · mutant killed · exit 1 · `internal/core/session/session.go` · drops every write to an entity after its first, so the statement reports success and the fact is simply not there. The write path still returns a datom, so nothing looks wrong until somebody reads it back · acceptance-sha256:ff0999d85aad0970c822a832e84d3618b0a48a2ebefbe32a0b1a59411f82856e · covers:a written fact being readable back through the language
- 2026-09-04 · dba7be4* · mutant killed · exit 1 · `internal/core/session/session.go` · stops excluding datoms the snapshot cannot see, so every read returns the latest value whatever instant it asked about. Time travel becomes a silent no-op: AS OF still parses and still resolves, and the answer is identical either way · acceptance-sha256:ff0999d85aad0970c822a832e84d3618b0a48a2ebefbe32a0b1a59411f82856e · covers:a read at a past instant not seeing a later write
- 2026-09-04 · dba7be4* · mutant killed · exit 1 · `internal/core/session/session.go` · lets a statement VALID FROM clause move the TRANSACTION identifier — the exact forgery the language was shaped to make unsayable, reintroduced one layer down where nothing about the grammar would reveal it · acceptance-sha256:ff0999d85aad0970c822a832e84d3618b0a48a2ebefbe32a0b1a59411f82856e · covers:the session assigning transaction time rather than the caller
- 2026-09-04 · dba7be4* · mutant killed · exit 1 · `internal/core/session/session.go` · stops indexing on the write path, so SEARCH finds nothing that arrived through ASSERT. A test that populated the index itself would still pass, which is exactly why the test only ever touches the index through the two statements · acceptance-sha256:ff0999d85aad0970c822a832e84d3618b0a48a2ebefbe32a0b1a59411f82856e · covers:a search finding a fact that was asserted rather than indexed by hand
- 2026-09-04 · 09ec963 · mutant inconclusive · exit 1 · `internal/core/session/session.go` · ignores the visibility predicate, so every read returns the latest value whatever instant it asked about. Time travel silently becomes a no-op: AS OF parses, resolves, and changes nothing about the answer · acceptance-sha256:ff0999d85aad0970c822a832e84d3618b0a48a2ebefbe32a0b1a59411f82856e · covers:a read at a past instant not seeing a later write
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-04 · 09ec963* · mutant killed · exit 1 · `internal/core/session/session.go` · lets a statement VALID FROM clause move the TRANSACTION identifier, which is the exact forgery the language was shaped to make unsayable — reintroduced one layer down, where nothing about the grammar would reveal it · acceptance-sha256:ff0999d85aad0970c822a832e84d3618b0a48a2ebefbe32a0b1a59411f82856e · covers:the session assigning transaction time rather than the caller
- 2026-09-04 · 09ec963* · mutant killed · exit 1 · `internal/core/session/session.go` · stops indexing on the write path, so SEARCH finds nothing that arrived through ASSERT. A test that populated the index itself would still pass, which is why the test only ever touches the index through the two statements · acceptance-sha256:ff0999d85aad0970c822a832e84d3618b0a48a2ebefbe32a0b1a59411f82856e · covers:a search finding a fact that was asserted rather than indexed by hand

## Invariants

- A fact written through a statement is readable through a statement.
- Transaction identifiers are minted by the session and strictly increase.
- A statement the session cannot run is refused by name.

## Risks

- ⚠ **THE FENCE CAUGHT A HANG THAT NO UNIT TEST COULD HAVE.** The first `cmd/sdev1-ql` read standard input whenever it was not a terminal, which is correct-looking and wrong: under any test harness or CI runner, stdin is an open pipe nobody writes to, so the binary blocked forever. The acceptance run did not fail — it never returned. Standard input is now a FALLBACK, read only when no statement came from a flag or a file. ★This is the whole argument for a fence that RUNS the binary rather than only building it: the defect lives entirely in how a process is invoked, and nothing inside the package could see it.
- ⚠ **A session test that indexes by hand proves nothing about the write path.** `TestAssertThenSearchFindsIt` writes with `ASSERT` and searches with `SEARCH`, touching the index only through those.
- ⚠ **The session could quietly become the specification.** It builds only on packages the records govern and adds no rule of its own, so when the real engine lands it agrees with the RECORDS rather than with this. Recorded because the failure is slow and looks like progress.
- ⚠ **An in-memory demonstration invites being mistaken for a storage engine.** The package comment, this task and the README all say it is not one, in those words.
- The session evaluates `SELECT` and `SEARCH` only. `MATCH SHAPE` needs a similarity metric that has to be chosen against a corpus, and returning an empty result for it would be a lie.

## Stop Condition

Stop and ask before adding any rule to the session that is not already in a
record. The moment it decides something on its own, the real engine has two
specifications and the one nobody wrote down is the one people run.

## Out of Scope

- Durability of any kind (deferred: `docs/adr/BACKLOG.md` §12)
- Evaluating `MATCH SHAPE` (deferred: `docs/adr/BACKLOG.md` §20)
- More than one leaf, or any network (deferred: `docs/adr/BACKLOG.md` §18)

## Verification Log
- 2026-09-04 · 97b0636* · exit 0 · `set -o pipefail …` · acceptance-sha256:96ca63369ba5149d521ee9a6ee4f728b7a35962c660d62101c45cf266be83bc3 · ms:3056
- 2026-09-04 · 97b0636* · exit 0 · `set -o pipefail …` · acceptance-sha256:96ca63369ba5149d521ee9a6ee4f728b7a35962c660d62101c45cf266be83bc3 · ms:3150
- 2026-09-04 · 97b0636* · exit 0 · `set -o pipefail …` · acceptance-sha256:96ca63369ba5149d521ee9a6ee4f728b7a35962c660d62101c45cf266be83bc3 · ms:2652
- 2026-09-04 · 97b0636* · exit 0 · `set -o pipefail …` · acceptance-sha256:96ca63369ba5149d521ee9a6ee4f728b7a35962c660d62101c45cf266be83bc3 · ms:2648
- 2026-09-04 · 97b0636* · exit 0 · `set -o pipefail …` · acceptance-sha256:96ca63369ba5149d521ee9a6ee4f728b7a35962c660d62101c45cf266be83bc3 · ms:2573
- 2026-09-04 · 97b0636* · exit 0 · `set -o pipefail …` · acceptance-sha256:96ca63369ba5149d521ee9a6ee4f728b7a35962c660d62101c45cf266be83bc3 · ms:2589
- 2026-09-04 · 97b0636* · exit 0 · `set -o pipefail …` · acceptance-sha256:96ca63369ba5149d521ee9a6ee4f728b7a35962c660d62101c45cf266be83bc3 · ms:2549
- 2026-09-04 · dba7be4* · exit 0 · `set -o pipefail …` · acceptance-sha256:ff0999d85aad0970c822a832e84d3618b0a48a2ebefbe32a0b1a59411f82856e · ms:3072
- 2026-09-04 · dba7be4* · exit 0 · `set -o pipefail …` · acceptance-sha256:ff0999d85aad0970c822a832e84d3618b0a48a2ebefbe32a0b1a59411f82856e · ms:2936
- 2026-09-04 · dba7be4* · exit 0 · `set -o pipefail …` · acceptance-sha256:ff0999d85aad0970c822a832e84d3618b0a48a2ebefbe32a0b1a59411f82856e · ms:2896
- 2026-09-04 · dba7be4* · exit 0 · `set -o pipefail …` · acceptance-sha256:ff0999d85aad0970c822a832e84d3618b0a48a2ebefbe32a0b1a59411f82856e · ms:2859
- 2026-09-04 · dba7be4* · exit 0 · `set -o pipefail …` · acceptance-sha256:ff0999d85aad0970c822a832e84d3618b0a48a2ebefbe32a0b1a59411f82856e · ms:2862
- 2026-09-04 · 09ec963 · exit 0 · `set -o pipefail …` · acceptance-sha256:ff0999d85aad0970c822a832e84d3618b0a48a2ebefbe32a0b1a59411f82856e · ms:3029
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:ff0999d85aad0970c822a832e84d3618b0a48a2ebefbe32a0b1a59411f82856e · ms:3045
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:ff0999d85aad0970c822a832e84d3618b0a48a2ebefbe32a0b1a59411f82856e · ms:2809
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:ff0999d85aad0970c822a832e84d3618b0a48a2ebefbe32a0b1a59411f82856e · ms:2791
