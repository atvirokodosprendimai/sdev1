# TODO

Ordered by what blocks what, not by how interesting it is.

Every item points at its entry in [`docs/adr/BACKLOG.md`](docs/adr/BACKLOG.md),
which carries the reasoning and the constraints that must survive. This file is a
derived running order — **when it disagrees with the backlog, the backlog wins.**

---

## The two things everything else waits on

Almost every "pending" in this repository traces back to one of these. Nothing
downstream can be honestly finished first, and a stub in place of either would be
worse than the gap: a component that answers plausibly from nothing cannot be
told apart from a working one, by a caller or by a test.

### 1 · A storage engine — put a fact on a disk and read it back · §12

**The bottom half is done.** ADR-024 and `internal/core/segstore` write a run of
blocks to a file and find one again by key: a segment is built under a temporary
name and published by an atomic rename, its index is written last and located
from a fixed-width trailer, and a sealed segment is read through a memory
mapping. Data outlives a process for the first time.

⚠ **The constraint that had to survive did survive, and it is the reason for the
rename:** nothing is observable in a half-sealed state, so ADR-017's lock-free
read path holds on real files — an incomplete segment is not addressable rather
than being guarded by a flag every reader must remember to check.

**And a fact now has a wire form.** ADR-025 and `internal/core/datom` encode a run
of datoms and refuse everything that is not one. ⚠ Its central rule is the one
worth carrying forward: a partially filled datom has `Assert` false, which is a
RETRACTION — so a decoder that tolerates a short buffer withdraws a fact and
reports success. Every refusal is total, and no datoms come back with an error.

**And it is wired up.** ADR-026 and `internal/core/leafstore` make a leaf a
directory of segments, and `cmd/sdev1-ql --dir ./leaf` makes a fact written by one
process readable by the next. **§28 is closed.**

⚠ **The decision worth carrying forward is how a leaf MERGES its segments:** by the
datoms' own transaction identifiers, never by filename and never by the order the
files were opened. A directory listing is sorted by name, so merging in that order
reads as deterministic while actually making the answer depend on what the files
are CALLED — a rename, a copy or a restore then reorders history, and the wrong
answer is a plausible one rather than an error.

What is still missing above it: **§15** — when the live tail is sealed, and
compaction. ⚠ A read touches every segment in the leaf, which is linear in seal
count, and that is the cost that makes compaction matter rather than a cost
this design hides.

### 2 · A query planner and a similarity metric · §20

**Evaluation is done.** ADR-027 and `internal/core/eval` turn a `SELECT` into rows
against any `ports.Reader`, so the same statement answers from memory or from a
leaf and costs ONE entity read either way.

⚠ **It closed a live defect, and the defect is the lesson.** `WHERE` had parsed
since ADR-011 and been evaluated nowhere: `SELECT * FROM planet-3 WHERE mass =
"999"` returned every attribute of planet-3, with no error and no way to tell.
**A clause that parses and is silently discarded is worse than one that is
refused** — "not implemented" is an answer a caller can act on.

What genuinely remains under this number, each blocked on something real:

- **Planning** — which index, in what order, at what cost. There is no index
  (§15), and an execution strategy guessed against no storage layer is a guess.
- **Similarity**, for `MATCH SHAPE`. ⚠ A metric chosen against no corpus is a
  number nobody has reason to believe, and this repository has no corpus.
- **Enumeration** — the language reads a NAMED entity and cannot list them, which
  is what leaves the filesystem's root directory unimplementable. A leaf can now
  list its own entities; doing it from the language across leaves needs a planner
  and a routing decision.
- **`SEARCH` and `TRAVERSE` through the reader** — they answer from session state,
  and the split is invisible until a statement disagrees with itself.
- **Multi-hop traversal syntax**, which must keep the per-leg time clause rather
  than losing it in the recursion.

---

## Unblocked by a storage engine

| | |
|---|---|
| **§15** | ✅ mostly done — ADR-028 decides when a tail seals (size or age, first to trip) and ADR-029 merges segments so a read stops costing one block lookup per seal. ⚠What remains: TIERING (merging everything into one is quadratic over a leaf's lifetime), reclaiming orphaned inputs after an interrupted compaction, an index over a leaf, and who calls `ShouldSeal`/`ShouldCompact` |
| **§13** | ✅ done — ADR-030. A compression block holds ONE subject's datoms. ⚠A shared block is a compression oracle: two subjects in one block make each subject's data a probe for the other's, and it also makes crypto-shredding a rewrite and reclaim impossible. The cost is worse compression, recoverable through interning (§12) rather than by merging blocks |
| **§17** | ✅ mostly done — ADR-031. Keys live in their own deletable directory, one file per key holding the key AND its subject, so the mapping dies with the key. A cache is evicted inside the destroy, and keys are never rotated. ⚠What remains: a master key over the key files, reclaiming dangling index entries, and what a shred means across nodes (§18). ⚠**And the rule no code can hold: the keystore must never share a backup with the data** |
| **§23** | ✅ done — ADR-028. The seal trigger IS the flush bound, so a size bound and an age bound decide it together, and `leafstore.Exposure` reports the OLDEST unsealed datom rather than a mean. ⚠An average is smallest when the tail is fullest, so it looks best exactly when the risk is highest |
| **§14** | Re-coding existing stripes when the configured scheme changes |

## Unblocked by the evaluator

| | |
|---|---|
| **§25** | Serve the agent surface over MCP, rate-limit it, and report what it did. Also needs the SDK dependency, pinned exactly |
| **§26** | Mount the filesystem projection. Also needs a FUSE library — a portability decision, not a dependency bump — and enumeration from §20 |
| **§8** | Test a real domain against the one-entity transaction boundary. Until something real is modelled against it, the boundary is reasoned rather than validated |
| **§27** | ✅ mostly done — the index is a rebuildable, idempotent projection of the log and `search.Confirm` drops candidates the datoms no longer support. ⚠What remains: the session's `SEARCH` does not call `Confirm` yet (it needs a reader and a snapshot on the search path — the same work as §20), and the index itself is not written to a disk (§15) |
| **§28** | ✅ done — `sdev1-ql --dir ./leaf` keeps a leaf across runs, and a session rehydrates from it. What remains under this number is a write tool on the agent surface, and several attributes in one `ASSERT` |

| **§29** | ✅ done — `ASSERT a orbits = ->b` writes a link and `TRAVERSE a DEPTH 2 AS OF 150` walks one. What remains under this number is inbound edges: nothing answers "what points AT this" without a scan |

---

## A transport, and what it unblocks · §18

There is no network anywhere in this repository. Everything is decided and tested
in-process. That is what makes the decisions provable today, and it is also why
nothing distributed has been *run*.

Once a transport exists:

| | |
|---|---|
| **§19** | Consensus is decided but unbuilt — nothing elects, replicates, or remembers a grant. One group per subtree |
| **§21** | Export, sample, retain and watch the event stream; the console |
| **§24** | Cache a prefetched block, evict one, and decide when to prefetch at all. ⚠ A read must still work with every prefetched block evicted, or the prefetch has become load-bearing |
| **§16** | Measure how slow a degraded read actually is. Several decisions currently rest on reasoning about it |
| **§22** | What happens when every replica sheds, and which reads matter more |
| **§3** | Bound repair traffic |
| **§4** | Police clock skew between nodes |
| **§5** | Closed timestamps, without which bounded-staleness reads cannot be offered |

⚠ **The chaos suite's composed-cluster half waits here too**, and its hardest
requirement is that the 8 GB test budget must not *manufacture* findings: a
container the kernel's out-of-memory killer stops looks exactly like the node
crash being injected. A run that cannot tell those apart produces failure reports
about the harness.

---

## Independent of all of the above

These need no engine, no evaluator and no network. They are open because they are
genuinely undecided, not because something is missing.

| | |
|---|---|
| **§11** | Tenant identifiers have no allocation, reuse or authorization story. ⚠ Carries the trap that a query `AS OF` a past instant must be authorized by **today's** grants and never that instant's |
| **§1** | Trie depth policy is fixed at authoring time rather than adaptive |
| **§2** | Hot-entity write throughput has no mitigation |
| **§6** | ✅ mostly done — ADR-032. A map carries a GENERATION (a `tx.TxID`, authored not assigned), and `placement.Resolve` refuses a map that cannot say which it is. ⚠The `Map.Version` field was renamed to `FormatVersion` because it meant the FILE FORMAT and was exactly where somebody would put the identity — every map would then claim the same one forever with nothing failing. What remains: recording the generation in a segment header, which waits on something that actually places a segment |
| **§7** | Spare servers have no claim or release policy |
| **§10** | What a cluster does with leaves below the durability floor |

---

## Fixed while writing the documentation, and worth not repeating

Both found on 2026-09-04 by running `cmd/sdev1-addr` twice by hand to check a
README example. Both are recorded in full in ADR-001's `T3-placement.md`.

**Placement scoring used a per-process random seed.** `maphash.MakeSeed()`
returns a new seed in every process, so two clients placed the same leaf on
different servers — one writes where the other will never look. The comment
directly above the line asserted the opposite. ⚠ Every test passed, because a Go
test binary is ONE process and the seed is constant within it; the invariant is
cross-process, and only a check on VALUES can hold it.

⚠ **And a mutant bound to the claim `the fixed scoring seed` had been killed.**
It introduced a per-*call* seed, which the in-process determinism test does
catch — so it proved something real, just not the property it named. The mutant
and the test shared the same blind spot. A claim can read as proved while the
mechanism it names was never exercised.

**The first fix then introduced a distribution bug.** FNV-1a is deterministic and
its avalanche is weak enough that the ranking tracked the target's *name*: over
256 leaves one server won 107 times and another 37, against a fair share of 64.
Determinism and distribution are separate requirements and only the first was
ever declared. Now SHA-256 truncated to 64 bits, with a fair-share band checked
by `TestScoringSpreadsAcrossTargets` — written only after confirming FNV-1a fails
it at both ends.

- [ ] **Re-audit the other `Rests-on` claims for the same shape** — a claim whose
      mutant exercises a narrower property than the claim names. This one was
      found by accident, which is not a strategy.
- [ ] **Decide whether SHA-256 per target per placement is acceptable at scale.**
      It is correct and unmeasured; the alternative is a mixing finalizer over a
      cheap hash, which needs a distribution measurement of its own.

## Documentation and tooling

| | |
|---|---|
| ✅ | A comprehensive README, schematics, and a query-language guide with a tutorial |
| ✅ | **`cmd/sdev1-ql`** — runs statements against an in-memory session and prints what they did, the way `sdev1-addr` makes addressing visible in one command. Shipped under ADR-022 T2 with its own record and evidence |
| | **A worked end-to-end tutorial** — currently impossible past parsing, because the tutorial would have to stop where the evaluator does |
| | **Measure whether a model calls the agent tools correctly from their descriptions alone.** Every refusal is written into a tool's description on the reasoning that it is the only documentation that caller gets. That is reasoned, not observed, and the failure mode is a caller that never reports being confused — it just calls the wrong thing |
| | **Measure an incremental tool over the mount** — two `rsync -n` passes with no writes between them, confirming the second copies nothing. The mtime rule is reasoned, and the failure it prevents is invisible except at scale |

---

## Not on this list, on purpose

- **`UPDATE` and `DELETE`.** The store appends. A retraction is an assertion and
  erasure is the destruction of a key. These are absent by decision, not backlog.
- **Writes through the filesystem.** POSIX cannot express that several attributes
  change together, so a writable mount would break the entity transaction
  boundary silently — every `write(2)` would succeed.
- **Re-encoding existing data from a policy clause.** Every block records how it
  was written; a policy governs the next write only.
