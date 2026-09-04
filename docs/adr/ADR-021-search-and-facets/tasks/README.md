# ADR-021 Tasks

Implementation tasks for ADR-021: Search is a derived index inside the erasure
boundary, and a facet is an exact count that refuses rather than estimates. See
the parent ADR for the decision.

**Source of truth:** the task files' headers. This README is a derived index —
when it disagrees with a task file, the task file wins.

## Execution Order

Two tasks, sequential.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | What a posting is, what a caller can see, and what a facet counts | done | — | `go test ./internal/core/search/... -race -run 'TestAShreddedSubject\|TestAnUndecryptablePosting\|TestVisibleReportsNoWithheld\|TestAnalyzeIsDeterministic\|TestFacetCountsAreExact\|TestAFacetOverItsBound\|TestASearchWithoutALimit\|TestPostingCarriesItsTransactionRange'` then the crypt suite |
| T2 | Build the index, and give search a grammar | pending | — | `go test ./internal/core/search/... -race -run 'TestRebuildFromTheLogReproducesTheIndex\|TestASearchResultIsConfirmedAgainstTheDatoms'` |

Status: `pending` | `partial` | `blocked` | `done`.

⚠ **T2 is `pending` on three things**: a storage engine (`BACKLOG.md` §12), a
query evaluator (§20) and a ranking function chosen against a real corpus (§27).
★That is not the same as ADR-021 being unfinished — what a posting MEANS and what
a facet may answer are settled in T1 and proved against the real keystore.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `search.Posting`, `search.Visible`, `search.Facet`, `search.Analyze` | T2 | T1 before T2 |

## Notes

- ⚠ **AN ORDINARY INVERTED INDEX UNDOES CRYPTO-SHREDDING, and that is the whole
  reason this record exists.** Erasure works because destroying a subject's key
  leaves ciphertext that nothing can read. An index is extracted PLAINTEXT: shred
  the key and the segments go dark while the index still answers
  `term → subject-42` — turning the fastest structure in the system into a lookup
  for the subject somebody asked to erase.
- **So a posting is sealed with the subject's own key.** Shredding makes it
  undecryptable, which removes it from the live index, every replica and every
  backup of the index at once, without finding or rewriting any of them. Erasure
  reaches the index by exactly the argument that makes it reach coded stripes.
- ⚠ **A posting that does not decrypt is ABSENT, never an error and never a
  count.** "3 results withheld" is an oracle for the existence of erased
  subjects — and `withheld++` inside a decrypt loop is the natural thing to write.
- ⚠ **Deleting a shredded subject's postings is NOT the answer**, even though it
  looks sufficient. It reintroduces a deletion that must find and visit every
  copy, and a replica that was offline during the purge keeps its own. That is the
  model crypto-shredding replaced.
- **The index is DERIVED and the log wins.** A result is a candidate, confirmed
  against the datoms before it is returned; an index fed by subscription is always
  behind, so trusting it returns entities that no longer match with nothing able
  to detect it.
- **Postings carry the transaction range they held over**, so search can be
  qualified in time. Otherwise search is the one surface unable to answer the
  question the whole system exists to answer. The price is that postings
  accumulate with history rather than with data.
- ⚠ **A facet is EXACT or REFUSED.** An approximate count that is not labelled
  approximate is a lie, and a facet count is precisely the number people
  reconcile against a total. Over-wide returns a named error and no partial
  counts.
- **A facet inherits the query's time clause and cannot carry its own** — two
  instants in one answer is a number describing no moment.
- ⚠ **`SEARCH` is ranked and limited, which AMENDS ADR-011.** That record listed
  ordering and limiting as deliberate omissions; they stand for `SELECT`, where
  there is no ranking to order by. An unranked, unlimited search is a full scan
  with extra steps.
- ⚠ **A rare term is still disclosive.** This design confines the leak from the
  subject to the term; it does not remove it, because a dictionary is shared and a
  sufficiently rare term approximates an identifier. Recorded as a permanent
  boundary rather than claimed as solved.
