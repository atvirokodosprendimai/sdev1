# Task ADR-021-T1: What a posting is, what a caller can see, and what a facet counts

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `search.Term`, `search.Analyze`, `search.Posting`, `search.Seal`, `search.Open`, `search.Visible`, `search.Match`, `search.ErrNoLimit`, `search.Facet`, `search.FacetResult`, `search.ErrFacetTooWide`
**Consumes:** `crypt.KeyID`, `crypt.Key`, `crypt.Keystore`, `crypt.Seal`, `crypt.Open`, `crypt.MemoryKeystore` from ADR-007; `tx.TxID` from ADR-002
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `a shredded subject having no visible postings`, `an undecryptable posting being absent rather than counted`, `a facet refused rather than estimated when its bound is exceeded`, `a search without a limit being refused`

## Goal

Settle the two things about search that cannot be changed later: that a posting
lives inside the erasure boundary, and that a count is exact or is not given.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/search/doc.go` | add | The package comment: why an ordinary inverted index undoes crypto-shredding, and what is done instead. |
| `internal/core/search/analyze.go` | add | `Term`, `Analyze` — text to terms, deterministically. |
| `internal/core/search/posting.go` | add | `Posting`, `Seal`, `Open`, `Visible`, `Match`, `ErrNoLimit`. |
| `internal/core/search/facet.go` | add | `Facet`, `FacetResult`, `ErrFacetTooWide`. |
| `internal/core/search/search_test.go` | add | The tests below, run against a real `crypt.MemoryKeystore`. |

★ The erasure test uses the REAL keystore rather than a stand-in. A fake that
returns "key missing" proves the code handles a missing key; only the real one
proves that shredding a subject actually produces that condition.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestAShreddedSubjectHasNoPostings`, `TestAnUndecryptablePostingIsNotCounted`, `TestVisibleReportsNoWithheldCount`, `TestAnalyzeIsDeterministic`, `TestFacetCountsAreExact`, `TestAFacetOverItsBoundIsRefused`, `TestASearchWithoutALimitIsRefused`, `TestPostingCarriesItsTransactionRange`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Term` and implement `Analyze` as a pure function — lower-case, split on non-alphanumerics, drop empties. ★Deliberately the simplest thing that is testable: stemming and stop words are a corpus decision, and guessing at them now would bake in a language.
3. [S3] Define `Posting` as a subject, its key handle, and the transaction range it held over. ★The range is a field rather than an afterthought because an index that only reflects *now* makes search the one surface unable to answer `AS OF`.
4. [S4] Implement `Seal` so a posting is sealed with the SUBJECT's own key, and `Open` so it is read back through the keystore. [proof: mutation]
5. [S5] Implement `Visible`: open what opens, drop what does not, and return NO count of what was dropped. ⚠A count of withheld results is an oracle for the existence of erased subjects, which is the property ADR-007 spent a whole record removing — and returning one is the natural thing to write when a decrypt fails inside a loop. [proof: mutation]
6. [S6] Implement `Match` so a search must carry a positive limit, refusing with `ErrNoLimit` otherwise. ★A ranked search with no limit is a full scan with extra steps.
7. [S7] Implement `Facet` to count exactly over a matched set, and refuse with `ErrFacetTooWide` when the set exceeds the declared bound rather than estimating or truncating. ⚠An approximate count that is not labelled approximate is a lie, and a facet count is exactly the number somebody reconciles against a total. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/search/... -race -run 'TestAShreddedSubject|TestAnUndecryptablePosting|TestVisibleReportsNoWithheld|TestAnalyzeIsDeterministic|TestFacetCountsAreExact|TestAFacetOverItsBound|TestASearchWithoutALimit|TestPostingCarriesItsTransactionRange' -count=1 2>&1 | tee /tmp/adr021-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr021-t1a.out \
  && go test ./internal/core/search/... ./internal/core/crypt/... -race -count=1 2>&1 | tee /tmp/adr021-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr021-t1b.out
```

The second command re-runs ADR-007's suite, because this task's central claim is
a property of that package's erasure and must not be able to land by breaking it.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAShreddedSubjectHasNoPostings` | `internal/core/search/search_test.go` | Seal postings for two subjects against a real keystore, shred one, and assert its postings are gone from `Visible` while the other's survive — **the falsifier ADR-021 names in `Enforced-by:`** | — | S4, S5 |
| `TestAnUndecryptablePostingIsNotCounted` | `internal/core/search/search_test.go` | A shredded subject's postings are absent from the result AND from every length and count it exposes, so nothing reveals that something was withheld | — | S5 |
| `TestVisibleReportsNoWithheldCount` | `internal/core/search/search_test.go` | `Visible`'s signature returns no second value, so a count of dropped postings structurally cannot be surfaced — asserted on the signature, because a function that cannot return the number cannot leak it | — | S5 |
| `TestAnalyzeIsDeterministic` | `internal/core/search/search_test.go` | The same text yields the same terms in the same order, and case and punctuation are normalised away | — | S2 |
| `TestFacetCountsAreExact` | `internal/core/search/search_test.go` | Counts over a known matched set are the true counts, per value, with no rounding | — | S7 |
| `TestAFacetOverItsBoundIsRefused` | `internal/core/search/search_test.go` | A matched set larger than the declared bound returns `ErrFacetTooWide` and NO partial counts, rather than an estimate | — | S7 |
| `TestASearchWithoutALimitIsRefused` | `internal/core/search/search_test.go` | A zero or negative limit is `ErrNoLimit`, so an unbounded ranked scan cannot be asked for | — | S6 |
| `TestPostingCarriesItsTransactionRange` | `internal/core/search/search_test.go` | A posting records the range it held over and survives a seal/open round trip intact, so a search can be qualified in time | — | S3, S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The eight tests above. |
| 2 — something selects it | `Visible` is the only way a sealed posting becomes a result, and `Facet` the only way a matched set becomes counts; nothing in the package returns a posting any other way. |
| 3 — the caller can discover it | `Match` refuses an unlimited search by name, and `Facet` refuses an over-wide one by name, so both limits are learnable from the API rather than from a document. |
| 4 — it is used | Nothing indexes yet; T2 is `pending` on the storage engine. |

## Mutation Log

- 2026-09-04 · 9636aaf* · mutant killed · exit 1 · `internal/core/search/posting.go` · stores the posting in the clear instead of sealing it with the subject key, which is what every search library does and what the faster design wants. The index then survives the shred: destroying the key darkens the segments while the index still answers term to subject, turning the fastest structure in the system into a lookup for the person somebody asked to erase — and it cannot be undone later, because every posting already written is already plaintext in every replica and every backup · acceptance-sha256:4b2cea3a18456d5b0e5e8aef6bd32a52d2bcafa6896256294abe47acb9815ef2 · covers:a shredded subject having no visible postings
- 2026-09-04 · 9636aaf* · mutant killed · exit 1 · `internal/core/search/posting.go` · surfaces a placeholder where a posting could not be decrypted, which is what good diagnostics look like and what a developer writes when a decrypt fails inside a loop. The count of results then reveals that something was withheld, which is an oracle for the existence of erased subjects — the exact property crypto-shredding spent a whole record removing, restored through the result length rather than through the data · acceptance-sha256:4b2cea3a18456d5b0e5e8aef6bd32a52d2bcafa6896256294abe47acb9815ef2 · covers:an undecryptable posting being absent rather than counted
- 2026-09-04 · 9636aaf* · mutant killed · exit 1 · `internal/core/search/facet.go` · truncates the matched set to the bound and counts what is left, which always returns an answer and is what a system under load is pushed towards. The breakdown is then a sample presented as a total, with nothing marking it: a facet count is precisely the number people reconcile against a total, so an unlabelled partial count is acted on rather than questioned · acceptance-sha256:4b2cea3a18456d5b0e5e8aef6bd32a52d2bcafa6896256294abe47acb9815ef2 · covers:a facet refused rather than estimated when its bound is exceeded
- 2026-09-04 · 9636aaf* · mutant killed · exit 1 · `internal/core/search/posting.go` · treats an absent limit as "return everything" rather than refusing it, which reads as a helpful default and is how an unbounded ranked scan gets asked for by accident. One caller that omits the limit then touches every leaf holding the tenant subtree, and search is the largest fan-out a single request can cause in this system · acceptance-sha256:4b2cea3a18456d5b0e5e8aef6bd32a52d2bcafa6896256294abe47acb9815ef2 · covers:a search without a limit being refused

## Invariants

- A posting is sealed with the subject's key, never stored in the clear.
- A posting that does not open is absent, and nothing counts it.
- A facet is exact or refused; there is no third outcome.
- A search carries a positive limit.

## Risks

- ⚠ **An erasure test against a FAKE keystore proves the wrong thing.** It shows the code handles a missing key; it does not show that shredding produces a missing key. The test uses `crypt.MemoryKeystore` and calls its real `Shred`, so the whole path is exercised.
- ⚠ **"The shredded subject is absent" is easy to satisfy by returning nothing at all.** The test seals postings for TWO subjects and asserts the survivor is still returned, so a `Visible` that dropped everything would fail rather than pass.
- ⚠ **A withheld count is the natural thing to write.** A decrypt failing inside a loop invites `withheld++`, and it reads as good diagnostics. The signature check makes it impossible rather than discouraged.
- **The analyzer is deliberately naive** — no stemming, no stop words, no language detection. That is a deferral, not an oversight, and it is recorded in the parent record's Out of Scope.
- ⚠ **A rare term in the dictionary remains disclosive.** This task confines the leak from the subject to the term; it does not remove it. The parent record states the residue as a permanent boundary rather than claiming to have solved it.

## Stop Condition

Stop and ask before storing any posting, term or attribute value in the clear,
however much cheaper the scan becomes. That is the one change this record exists
to prevent, and it cannot be undone later: every posting already written would
already be plaintext, in every replica and every backup of the index.

## Out of Scope

- Building or persisting an index (deferred: `docs/adr/BACKLOG.md` §27)
- The `SEARCH` grammar (deferred: `docs/adr/BACKLOG.md` §27)
- Ranking (deferred: `docs/adr/BACKLOG.md` §27)
- Confirming candidates against the datoms (deferred: `docs/adr/BACKLOG.md` §20)

## Verification Log
- 2026-09-04 · 9636aaf* · exit 0 · `set -o pipefail …` · acceptance-sha256:4b2cea3a18456d5b0e5e8aef6bd32a52d2bcafa6896256294abe47acb9815ef2 · ms:3791
- 2026-09-04 · 9636aaf* · exit 0 · `set -o pipefail …` · acceptance-sha256:4b2cea3a18456d5b0e5e8aef6bd32a52d2bcafa6896256294abe47acb9815ef2 · ms:3710
- 2026-09-04 · 9636aaf* · exit 0 · `set -o pipefail …` · acceptance-sha256:4b2cea3a18456d5b0e5e8aef6bd32a52d2bcafa6896256294abe47acb9815ef2 · ms:3908
- 2026-09-04 · 9636aaf* · exit 0 · `set -o pipefail …` · acceptance-sha256:4b2cea3a18456d5b0e5e8aef6bd32a52d2bcafa6896256294abe47acb9815ef2 · ms:3813
- 2026-09-04 · 9636aaf* · exit 0 · `set -o pipefail …` · acceptance-sha256:4b2cea3a18456d5b0e5e8aef6bd32a52d2bcafa6896256294abe47acb9815ef2 · ms:3905
