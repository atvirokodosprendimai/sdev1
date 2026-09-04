# Task ADR-027-T2: Delete the session's own SELECT

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** S
**Owner:** unassigned
**Produces:** `session.Session.selectFrom` rewritten onto `eval.Select`
**Consumes:** `eval.Select`, `eval.Row` (T1)
**Data dependency:** hermetic — the session's own tests, and the built binary run once
**Proof map:** v1
**Rests-on:** `a WHERE clause filtering for a caller of the language`, `a session reader that honours the snapshot it is handed`, `a SELECT reading through the store when there is one`, `the built binary actually running rather than only compiling`

## Goal

Make the defect gone where a caller meets it, and leave exactly one thing in the
repository that turns a `SELECT` into rows.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/session/session.go` | modify | `selectFrom` calls `eval.Select`; a small `ports.Reader` over the session's own datoms. |
| `internal/core/session/session_test.go` | modify | `TestWhereFiltersForACaller`. |
| `internal/core/session/durable_test.go` | modify | `TestASelectWithAStoreReadsThroughTheStore`, `TestTheSessionReaderHonoursItsSnapshot`. |
| `docs/QUERY-LANGUAGE.md` | modify | The guide says `WHERE` parses and does not run; it does now. |
| `README.md` | modify | The capability table says the same. |

⚠ `internal/core/session/**` is governed by ADR-022. This task removes a second
implementation of a projection rather than changing any rule ADR-022 stated, and
the Acceptance re-runs its suites for that reason.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestWhereFiltersForACaller`, `TestASelectWithAStoreReadsThroughTheStore`, `TestTheSessionReaderHonoursItsSnapshot`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Add an unexported `ports.Reader` over the session's own datom map, so a session with no store still evaluates through the port rather than through a second path. ⚠It must filter by the snapshot it is handed, because that is what the port PROMISES — and no statement can see whether it does, since the evaluator filters again with the authoritative query. The test therefore calls it directly. [proof: mutation]
3. [S3] Rewrite `selectFrom` to resolve the instant and call `eval.Select`, and DELETE the projection it had. ⚠Deleting it is the point; leaving it beside the new one is how two implementations drift, and the one that drifts is whichever nobody reads. [proof: mutation]
4. [S4] Read through the STORE when there is one, so a `SELECT` costs one entity rather than the leaf the session happens to be holding. ⚠A session with a store ALSO holds a rehydrated copy, so both paths give the same answer and neither can be told from the other — the test appends to the store behind the session's back, which is the only observation that separates them. [proof: mutation]
5. [S5] Correct the guide and the README. ⚠Both currently say `WHERE` parses and nothing runs it; that was true and is now the opposite of true. [proof: human: whether a page describes what the code now does is a judgement about prose, and a test asserting it would be checking the page against itself]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/session/... -race -run 'TestWhereFiltersForACaller|TestASelectWithAStoreReadsThroughTheStore|TestTheSessionReaderHonoursItsSnapshot' -count=1 2>&1 | tee /tmp/adr027-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr027-t2a.out \
  && go test ./internal/core/session/... ./internal/core/ql/... ./internal/core/eval/... -race -count=1 2>&1 | tee /tmp/adr027-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr027-t2b.out \
  && D=$(mktemp -d) && go build -o "$D/sdev1-ql" ./cmd/sdev1-ql \
  && "$D/sdev1-ql" --dir "$D/leaf" --clock 1000 \
       --statements 'ASSERT planet-3 mass = "5"' \
       --statements 'ASSERT planet-3 radius = "6371"' > "$D/wrote.txt" 2>&1 \
  && "$D/sdev1-ql" --dir "$D/leaf" --clock 5000 \
       --statements 'SELECT * FROM planet-3 WHERE mass = "999"' > "$D/narrow.txt" 2>&1 \
  && grep -q 'no rows' "$D/narrow.txt" \
  && ! grep -q '6371' "$D/narrow.txt" \
  && "$D/sdev1-ql" --dir "$D/leaf" --clock 6000 \
       --statements 'SELECT * FROM planet-3 WHERE mass = "5"' > "$D/wide.txt" 2>&1 \
  && grep -q '6371' "$D/wide.txt" \
  && rm -rf "$D"
```

⚠ **The SELECT runs in its OWN process, so its output file holds only its answer.**
Running it beside the writes would put `6371` in the file from the `ASSERT` that
wrote it, and the grep proving the filter excluded it would match that instead —
a check that passes for a reason unrelated to what it claims.

The three greps are the point. `no rows` says the filter matched nothing; the
absent `6371` says the unrelated attribute did not come back anyway, which is the
exact shape of the defect — the query never failed, it answered wider than it was
asked. The third run is the control: a predicate that DOES match must still return
the row, or "filters everything" would pass the first two.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestWhereFiltersForACaller` | `internal/core/session/session_test.go` | Through `Session.Run`, a `WHERE` that matches nothing returns no rows, one that matches returns only the projected attributes, and a predicate on an unprojected attribute still filters | — | S2, S3, S4 |
| `TestASelectWithAStoreReadsThroughTheStore` | `internal/core/session/durable_test.go` | A fact appended to the STORE and absent from the session's rehydrated map is returned by a `SELECT`, so the read genuinely goes through the store. ⚠ It is not a supported topology — one leaf has one fenced writer — it is the read path stated as something a test can see, because with both sides populated the two answer identically | — | S4 |
| `TestTheSessionReaderHonoursItsSnapshot` | `internal/core/session/durable_test.go` | The session's reader is called DIRECTLY and drops a datom outside the snapshot's instant. ⚠ No statement can see this — `eval.Select` filters again with the query the parser resolved — so a mutant that removed the filter survived every other test here. The obligation is to the PORT, which promises datoms visible at a snapshot to any consumer | — | S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The test above, plus the binary run in the fence. |
| 2 — something selects it | `Session.Run` is the only way a statement reaches an evaluator, and there is now one evaluator. |
| 3 — the caller can discover it | The guide and the README stop saying `WHERE` does not run. |
| 4 — it is used | The fence runs the built binary and greps its OUTPUT for the rows that must not be there. |

## Mutation Log

- 2026-09-04 · ed55798* · mutant killed · exit 1 · `internal/core/session/session.go` · drops the predicate on the way to the evaluator, which is the shipped defect restated: the statement parses, the session succeeds, and every row comes back however narrow the question was · acceptance-sha256:3708a2728bca9b0291762b6e040b92230fc2c0adf0e8b0a9d6dc0705907e4fb6 · covers:a WHERE clause filtering for a caller of the language
- 2026-09-04 · ed55798* · mutant inconclusive · exit 1 · `internal/core/session/session.go` · makes the session reader ignore the snapshot it was handed, so it stops honouring the port contract and a time-qualified read answers about the wrong instant · acceptance-sha256:3708a2728bca9b0291762b6e040b92230fc2c0adf0e8b0a9d6dc0705907e4fb6 · covers:one projection implementation rather than two
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-04 · ed55798* · mutant survived · exit 0 · `internal/core/session/session.go` · always reads the session map instead of the store, so a SELECT costs the leaf the session is holding rather than one entity — and a session that had not rehydrated would answer nothing · acceptance-sha256:3708a2728bca9b0291762b6e040b92230fc2c0adf0e8b0a9d6dc0705907e4fb6 · covers:one projection implementation rather than two
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-04 · ed55798* · mutant killed · exit 1 · `internal/core/session/session.go` · drops the predicate on the way to the evaluator, which is the shipped defect restated: the statement parses, the session succeeds, and every row comes back however narrow the question was · acceptance-sha256:b237b667b268fe193d9a632cf54476ef1d652cd33073c8d8c5595a72b0f35403 · covers:a WHERE clause filtering for a caller of the language
- 2026-09-04 · ed55798* · mutant survived · exit 0 · `internal/core/session/session.go` · makes the session reader ignore the snapshot it was handed, so it stops honouring the port contract and a time-qualified read answers about the wrong instant · acceptance-sha256:b237b667b268fe193d9a632cf54476ef1d652cd33073c8d8c5595a72b0f35403 · covers:a session reader that honours the snapshot it is handed
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-04 · ed55798* · mutant killed · exit 1 · `internal/core/session/session.go` · always reads the session map instead of the store, so a SELECT costs the leaf the session is holding rather than one entity; it survived until a test appended to the store behind the session, because both sides hold the same facts · acceptance-sha256:b237b667b268fe193d9a632cf54476ef1d652cd33073c8d8c5595a72b0f35403 · covers:a SELECT reading through the store when there is one
- 2026-09-04 · ed55798* · mutant killed · exit 1 · `internal/core/session/session.go` · makes the session reader ignore the snapshot it was handed, so it stops honouring the port contract; no statement can see it, which is why the test calls the reader directly · acceptance-sha256:bcc47a89f74ced64c840d24bfdfec166dfab119e7186accd42caafe390abb056 · covers:a session reader that honours the snapshot it is handed
- 2026-09-04 · ed55798* · mutant killed · exit 1 · `internal/core/session/session.go` · drops the predicate on the way to the evaluator, which is the shipped defect restated: the statement parses, the session succeeds, and every row comes back however narrow the question was · acceptance-sha256:bcc47a89f74ced64c840d24bfdfec166dfab119e7186accd42caafe390abb056 · covers:a WHERE clause filtering for a caller of the language
- 2026-09-04 · ed55798* · mutant killed · exit 1 · `internal/core/session/session.go` · always reads the session map instead of the store, so a SELECT costs the leaf the session is holding rather than one entity; it survived until a test appended to the store behind the session, because both sides hold the same facts · acceptance-sha256:bcc47a89f74ced64c840d24bfdfec166dfab119e7186accd42caafe390abb056 · covers:a SELECT reading through the store when there is one

## Invariants

- Exactly one implementation turns a `SELECT` into rows.
- A session with no store behaves as a session with one, statement for statement.

## Risks

- ⚠ **The fence's second grep is what makes it a test.** A `SELECT` that errored, or returned nothing for the wrong reason, would satisfy `no rows` alone. Asserting the unrelated attribute is ABSENT is what distinguishes a filter from a failure.
- ⚠ **The store read path was UNPROVEN until a mutant said so.** Removing the store branch entirely left every test passing, because a session with a store also holds a rehydrated map and both give the same answer. A claim nothing can falsify is a claim nothing is holding.
- ★ **The reader's own filter is not load-bearing for a statement, and that is by design rather than by accident.** The evaluator filters with the query the parser RESOLVED; a snapshot is a lossy rendering of it, since `temporal.Query.Bounds` turns an open system axis into the largest identifier. The store's filter is an optimisation and the evaluator's is the meaning — which is why the reader's obligation had to be tested against the port directly rather than through a query.
- ⚠ **Leaving the old projection in place "for now" defeats the task.** Two implementations of one thing drift silently, and this record exists because a clause nobody ran looked exactly like a clause that worked.
- A session with a store reads through it, so its rehydrated map is no longer what `SELECT` answers from — `SEARCH` and `TRAVERSE` still use it. That split is recorded as a follow-up on the parent record.

## Stop Condition

Stop and ask before keeping the session's projection alongside the evaluator's.
The whole value of this task is that there stops being a second answer to "what
does a `SELECT` return".

## Out of Scope

- Moving `SEARCH` and `TRAVERSE` onto the reader (deferred: `docs/adr/BACKLOG.md` §20)
- Enumerating entities without a name (deferred: `docs/adr/BACKLOG.md` §20)
- Anything about how a leaf stores bytes (permanent: boundary: ADR-026 owns that, and this task names only the read port)

## Verification Log
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:3708a2728bca9b0291762b6e040b92230fc2c0adf0e8b0a9d6dc0705907e4fb6 · ms:4808
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:3708a2728bca9b0291762b6e040b92230fc2c0adf0e8b0a9d6dc0705907e4fb6 · ms:4707
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:3708a2728bca9b0291762b6e040b92230fc2c0adf0e8b0a9d6dc0705907e4fb6 · ms:4633
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:3708a2728bca9b0291762b6e040b92230fc2c0adf0e8b0a9d6dc0705907e4fb6 · ms:4519
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:b237b667b268fe193d9a632cf54476ef1d652cd33073c8d8c5595a72b0f35403 · ms:4498
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:b237b667b268fe193d9a632cf54476ef1d652cd33073c8d8c5595a72b0f35403 · ms:4598
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:b237b667b268fe193d9a632cf54476ef1d652cd33073c8d8c5595a72b0f35403 · ms:4535
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:b237b667b268fe193d9a632cf54476ef1d652cd33073c8d8c5595a72b0f35403 · ms:4622
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:bcc47a89f74ced64c840d24bfdfec166dfab119e7186accd42caafe390abb056 · ms:4637
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:bcc47a89f74ced64c840d24bfdfec166dfab119e7186accd42caafe390abb056 · ms:4697
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:bcc47a89f74ced64c840d24bfdfec166dfab119e7186accd42caafe390abb056 · ms:4598
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:bcc47a89f74ced64c840d24bfdfec166dfab119e7186accd42caafe390abb056 · ms:4563
