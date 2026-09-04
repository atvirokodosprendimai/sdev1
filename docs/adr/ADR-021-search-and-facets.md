# ADR-021: Search is a derived index inside the erasure boundary, and a facet is an exact count that refuses rather than estimates

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-003-transaction-boundary.md`, `docs/adr/ADR-007-crypto-shredding.md`, `docs/adr/ADR-010-subscribe-and-purge.md`, `docs/adr/ADR-011-query-language.md`, `docs/adr/ADR-015-admission-control.md`, `docs/adr/ADR-016-tenant-prefix.md`, `docs/adr/ADR-017-lock-free-read-path.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/search/**`
**Enforced-by:** `internal/core/search/search_test.go::TestAShreddedSubjectHasNoPostings`
**Invalidates:** none — but it **amends ADR-011**, which listed ordering, limiting and aggregation as things the language deliberately cannot say. Those stand for `SELECT`; a ranked search without a limit is not a search, and a facet is an aggregate, so this record lifts them for `SEARCH` only and says why
**Served-path change:** A caller can find entities by the text inside them and get counts per attribute value alongside the matches, instead of having to know an entity's name before reading it.

## Context

Everything in this system so far requires you to already know what you are
looking for. `SELECT` reads a **named** entity. `MATCH SHAPE` needs a subject to
resemble. There is no way to ask "which entities mention this", and no way to ask
"how do the matches break down".

Those are the two most common questions anyone asks of a store this shape, and
adding them is not a feature bolt-on. It changes what is written at write time,
it adds a second structure that must agree with the log, and — the part that
decides the whole design — it takes plaintext out from behind the encryption.

⚠ **AN INVERTED INDEX SILENTLY UNDOES CRYPTO-SHREDDING.** ADR-007 erases a
subject by destroying its per-subject key: the ciphertext remains and becomes
permanently unreadable, which is what lets erasure reach a coded stripe across ten
domains, a replica offline for a month, and a backup on a shelf without visiting
any of them. That argument holds *only because nothing readable sits beside the
ciphertext.*

An inverted index is, by construction, extracted plaintext. Index a subject and
the index holds their terms in the clear, keyed to them. Destroy the key
afterwards and the segments go dark while the index still answers
`term → subject-42`. The erasure is undone, and it is undone in the worst
possible way: the index is the *fastest* structure in the system for finding the
subject somebody asked to have erased.

★ This is not a detail to handle later. It is the constraint the design has to be
built around, because an index built the ordinary way cannot be retrofitted into
the erasure boundary — every posting ever written would already be plaintext.

## Existing Primitives Audit

- `internal/core/crypt` (ADR-007): supplies `KeyID`, `Keystore`, `Seal` and
  `Open`. **Reused whole, and load-bearing** — a posting is sealed with the
  subject's own key, so `Open` failing after a shred is exactly what removes it
  from results. Nothing new is invented for erasure.
- `internal/core/ql` (ADR-011): supplies `Statement`, `TimeClause` and the parser
  shape. **Extended, not bypassed** — `SEARCH` becomes a third statement, so the
  time clause composes with it the way it composes with everything else.
- `internal/core/subscribe` (ADR-010): supplies the one subscription primitive.
  **Reused** — the index is a read model fed by the same mechanism that feeds
  streaming backup and the console, rather than a third way to follow the log.
- `internal/core/addr` (ADR-016): supplies the tenant prefix. **Relied on** so an
  index shard is a subtree and a search structurally cannot cross tenants.
- `internal/core/admit` (ADR-015): supplies the read budget. **Relied on** — a
  search is the largest fan-out any single request can cause.
- A search library: **none.** ⚠ Every mature one assumes it owns its storage and
  stores terms in the clear, which is precisely the assumption this record
  refuses. Adopting one would mean re-deciding erasure, not saving work.
- A stemmer or language model for analysis: **none yet.** Deferred rather than
  bundled; the analyzer here is deliberately the simplest thing that is testable.

## Decision

**The index is a derived read model that lives inside the erasure boundary; a
facet is an exact count over a bounded matched set, refused when the bound is
exceeded.**

1. **A posting is sealed with the subject's own key.** ★ The erasure story needs
   no new mechanism: destroying the key makes the posting undecryptable, so it
   stops being a result everywhere at once — in the live index, in every replica,
   and in every backup of the index — without finding or rewriting any of them.

2. **A posting that does not decrypt is ABSENT, never an error.** A search
   returns what it could open. ⚠ Reporting "3 results you may not see" would
   restore exactly the oracle ADR-007 removed, and it is the natural thing to
   write when a decrypt fails inside a loop.

3. **The index is DERIVED and never authoritative. The log wins.** A search
   result is a set of *candidates*, confirmed against the datoms before it is
   returned. ⚠ Without that, a stale index returns entities that no longer match
   and nothing can tell — and an index is stale by construction, because it is
   built by subscription and therefore always behind.

4. **The index is rebuildable from the log at any time**, and nothing may exist
   only in it. It is a projection, so losing it entirely is a performance event
   rather than a data-loss event.

5. **Postings carry the transaction range over which they held**, so a search can
   be qualified in time like every other read. ⚠ An index that only reflects
   *now* would make search the one surface unable to answer the question this
   whole system exists to answer.

6. **`SEARCH` is a third statement, not a predicate.** Its input is a query over
   an index and its output is *ranked* — neither fits the one-predicate `WHERE`,
   and forcing it there would require the compound predicates the language
   deliberately does not have.

7. **A search is RANKED and LIMITED, and the limit is required.** ⚠ This amends
   ADR-011, which listed ordering and limiting as deliberate omissions. They
   remain omissions for `SELECT`, where there is no ranking to order by. An
   unranked, unlimited search is not a search: it is a full scan with extra steps.

8. **Facets are EXACT, and a facet over a matched set larger than its declared
   bound is REFUSED rather than estimated.** ⚠ An approximate count that is not
   labelled approximate is a lie, and every facet UI treats counts as truth —
   people reconcile them against totals. Refusing returns a caller to a narrower
   query, which works; an unlabelled estimate produces a number somebody acts on.

9. **A facet inherits the query's time clause and cannot carry its own.** An
   aggregate over a bitemporal store is only meaningful at a stated instant, and
   two instants in one answer is a number that describes no moment.

10. **An index shard is a tenant subtree, so a search cannot cross tenants**, and
    search bytes count against the READ budget for ADR-015's reason: the link does
    not care which bytes are background, and search is the largest amplifier a
    single request has.

**What would falsify this.** A shredded subject still appearing in search
results. That is the falsifier named in `Enforced-by:`, it is checkable today
against the real keystore with no index and no storage engine, and it is the
mistake every ordinary search implementation makes by default.

## Alternatives Considered

- **Store the index in the clear, like every search engine does.** Simplest,
  fastest, and what a library would give us. Rejected under rule 1: it makes the
  index an un-erasable plaintext copy of everything, and turns the fastest
  structure in the system into a lookup for subjects somebody asked to erase.
  There is no later fix — every posting already written would be plaintext.
- **Delete a subject's postings when it is shredded.** The obvious repair, and it
  reads as sufficient. Rejected: it reintroduces exactly what crypto-shredding
  exists to avoid — a deletion that must *find and visit* every copy. A replica
  that was offline during the purge, and every backup of the index, keep theirs.
  Erasure that depends on reaching all copies is the model ADR-007 replaced.
- **Do not index anything sensitive.** Cheap and it sounds prudent. Rejected as
  the primary mechanism: it makes correctness depend on classifying every field
  correctly forever, the failure is silent, and it is discovered only when the
  wrong thing turns up in an index. It survives as advice, not as the guarantee.
- **Report that undecryptable results were withheld.** More honest-seeming.
  Rejected under rule 2: a count of hidden results is an oracle for the existence
  of erased subjects, which is the property ADR-007 spent a whole record removing.
- **Let the index be authoritative, and skip confirming against the log.** One
  read instead of two. Rejected under rule 3: an index built by subscription is
  always behind, so it would return entities that no longer match with nothing
  able to detect it.
- **Index only the current state.** Far cheaper — postings never accumulate.
  Rejected under rule 5: search becomes the one surface that cannot answer
  "as of", in a store whose entire premise is that it can.
- **Approximate facet counts above a threshold.** How large systems normally do
  it, and it always returns an answer. Rejected under rule 8: an estimate that is
  not labelled is a lie, and a facet count is precisely the number people
  reconcile against a total.
- **Make `SEARCH` a `WHERE` predicate for grammatical uniformity.** Tempting given
  how much this corpus values one way to say a thing. Rejected under rule 6:
  ranking and limiting have no meaning in a `SELECT`, so it would smuggle a second
  result model in through a predicate.

## Component / Boundary Impact

One new component, `internal/core/search`, owning what a search means and what a
facet counts. It has one reason to change: how this engine answers a question
whose subject is not known in advance.

⚠ The boundary: it ANALYSES, MODELS and COUNTS. It builds no index, persists
nothing, and reads no storage. `Analyze` turns text into terms, `Postings`
decides which sealed postings a caller can actually see, and `Facet` counts a
matched set it is handed. Keeping that separate from the index build is what
makes the erasure property — the one thing that must not be got wrong — provable
today, against the real keystore, with no cluster.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `search.Term` | new — one analysed token | T1 | T2 |
| `search.Analyze` | new — text to terms, deterministically | T1 | T2 |
| `search.Posting` | new — a subject, its key handle and the transaction range it held over | T1 | T2 |
| `search.Seal` / `search.Open` | new — a posting sealed with the subject's own key | T1 | T2 |
| `search.Visible` | new — the postings a caller can actually open, silently dropping the rest | T1 | T2 |
| `search.ErrNoLimit` | new — a search without a limit is refused | T1 | callers |
| `search.Facet` / `search.FacetResult` | new — exact counts over a bounded matched set | T1 | callers |
| `search.ErrFacetTooWide` | new — refused rather than estimated | T1 | callers |
| `ql.Search` statement | new — `SEARCH … IN … FACET BY … LIMIT …` with a time clause | T2 | callers |
| index build and persistence | new, `pending` | T2 | operators |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `search.Term`, `search.Posting`, `search.Visible`, `search.Facet` | T1 | T2 | No — T2 is written against T1 |

## Consequences

- **Positive:** Erasure reaches the index for free, by the same argument that
  makes it reach coded stripes and shelved backups. Nothing has to find anything.
- **Positive:** The index can be thrown away and rebuilt, so an index bug is
  never a data-loss bug.
- **Positive:** Search inherits the time clause, so "what mentioned this last
  March" is expressible rather than being a second system.
- **Negative:** A sealed posting cannot be scanned as cheaply as a plaintext one —
  every candidate costs a decrypt. That is the price of rule 1 and it is not
  small.
- **Negative:** Postings accumulate, because nothing is deleted and every posting
  carries the range it held over. Index size grows with history, not with data.
- **Negative:** Confirming candidates against the log makes every search two
  round trips rather than one.
- **Negative:** Refusing a too-wide facet is a worse experience than an estimate,
  right up until somebody acts on the estimate.
- **Neutral:** Nothing is indexed. The meaning is decidable and the machinery is
  not.

## Out of Scope

- Building, persisting or updating an index (deferred: `docs/adr/BACKLOG.md` §27)
- The `SEARCH` statement's grammar and parser (deferred: `docs/adr/BACKLOG.md` §27)
- The ranking function, which needs a corpus to be chosen against (deferred: `docs/adr/BACKLOG.md` §27)
- Stemming, stop words and language detection (deferred: `docs/adr/BACKLOG.md` §27)
- Confirming candidates against the datoms, which needs the evaluator (deferred: `docs/adr/BACKLOG.md` §20)
- Which attributes are indexed at all, and who decides (deferred: `docs/adr/BACKLOG.md` §27)
- Whether a rare term in the dictionary is itself disclosive (permanent: fact: an inverted index over free text cannot hide that a term was indexed, because the dictionary is a shared structure and a sufficiently rare term approximates an identifier; this record confines the leak to the term rather than the subject and records the residue rather than claiming to remove it; citation: file `docs/adr/ADR-021-search-and-facets.md:1`)
- Ordering and limiting for `SELECT` (permanent: boundary: ADR-011's omission stands there; this record lifts it only for `SEARCH`, where ranking exists)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Postings are written in the clear, because that is what every index does | High — it is the default in every library and the faster design | Critical — the index becomes an un-erasable plaintext copy and the fastest way to find an erased subject | The falsifier shreds a subject and asserts it has no postings, run against the real keystore |
| A failed decrypt is surfaced as an error or a withheld count | Med | Critical — restores the existence oracle ADR-007 removed | `Visible` drops silently and returns no count of what it dropped; the test asserts the two cases are byte-identical |
| The index becomes authoritative because confirming is a second round trip | Med | High — stale results nothing can detect | Rule 3, and a follow-up when the evaluator lands |
| Facets are made approximate under load | Med | High — an unlabelled estimate is reconciled against a total by someone who trusts it | Over-wide is a named refusal; the test asserts no partial count is ever returned |
| A search crosses a tenant boundary through a shared term dictionary | Low | Critical | An index shard is a tenant subtree; the dictionary is per-shard, so there is nothing shared to cross |
| Index growth is mistaken for a leak, because postings never shrink | Med | Low — operational confusion rather than a defect | Recorded here as a stated consequence of rule 5 |

## Rollback

The index is derived, so removing it deletes a projection and loses nothing. No
format is shared with stored data, and postings live in their own structure. The
statement grammar would revert with the code. This is the cheapest rollback in the
corpus, and that is a property of rule 4 rather than luck.

## Follow-ups

- [ ] When the evaluator exists (`BACKLOG.md` §20), confirm every search result is checked against the datoms before it is returned — rule 3 is the one that decays quietest, because skipping the check makes searches faster and the damage only shows on data that changed.
- [ ] When the index is built (`BACKLOG.md` §27), confirm a full rebuild from the log reproduces it exactly; rule 4 is worth nothing unproven.
- [ ] Measure the decrypt cost per candidate before choosing a ranking function — rule 1's price is real and unmeasured, and a ranker that needs to score thousands of candidates may be unaffordable at that price.
- [ ] Decide which attributes are indexed, and record the residual disclosure a rare term carries. Not indexing high-cardinality identifiers is advice today and should become a rule with a named owner.
