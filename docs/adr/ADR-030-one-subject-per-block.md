# ADR-030: A compression block holds one subject's datoms, because a shared block is a compression oracle

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-005-segment-format.md`, `docs/adr/ADR-006-erasure-coding.md`, `docs/adr/ADR-007-crypto-shredding.md`, `docs/adr/ADR-010-subscribe-and-purge.md`, `docs/adr/ADR-024-segment-store.md`, `docs/adr/ADR-026-leaf-store.md`, `docs/adr/ADR-029-compaction.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/leafstore/compact.go`
**Enforced-by:** `internal/core/leafstore/subject_test.go::TestNoBlockMixesSubjects`
**Invalidates:** none — ADR-005 left the question open in its own implementation notes, and `BACKLOG.md` §13 has carried it since
**Served-path change:** None a caller can observe. Every segment written so far already holds one subject per block; this makes that a rule with a gate instead of an accident.

## Context

`BACKLOG.md` §13 has asked since ADR-005 whether one compression block may hold
several subjects. A codec finds redundancy across whatever is inside a block, so
packing subjects together compresses considerably better — datoms for different
subjects share attribute names and value shapes.

⚠ **Every segment this system has ever written already holds one subject per
block**, because that is what `leafstore` happened to do when it was built. It was
never decided. An undecided property that happens to hold is one refactor away
from not holding, and the refactor that breaks this one is a compression
improvement with a benchmark attached.

★ The cost of learning it late is a rewrite of every stored byte, which is why
§13 asked for it to be written down rather than assumed.

## Existing Primitives Audit

- `internal/core/segment` (ADR-005): defines a block as a self-describing unit
  with one codec, one cipher and one checksum. **Unchanged** — this record says
  what goes IN one, and adds no field.
- `internal/core/segstore` (ADR-024): maps ONE key to ONE block. **Relied on, and
  it already assumed the answer** — see rule 4.
- `internal/core/crypt` (ADR-007): encrypts under a subject's key. **Relied on**:
  the whole erasure argument requires a ciphertext to belong to one subject.
- `internal/core/leafstore` (ADR-026, ADR-029): writes the blocks. **Where the
  rule is enforced**, because it is the only thing that groups datoms into
  blocks.
- A dictionary shared across blocks, as a middle path: **rejected below**, and it
  is the option worth naming because it looks like it gets both.

## Decision

**A compression block holds the datoms of exactly one subject.**

1. ⚠ **A shared block is a COMPRESSION ORACLE.** A codec's output size is a
   function of everything inside it, so two subjects in one block make each
   subject's data a probe for the other's: write data you control, observe the
   block shrink, learn about data you do not control. That is a confidentiality
   property, and it is why this is not a tuning question wearing a performance
   costume.

2. **Erasure requires it.** ADR-007 shreds a subject by destroying its key. A
   block mixing subjects is either encrypted under ONE key — so shredding one
   subject means rewriting everything sharing its block, which is the
   find-and-delete model crypto-shredding replaced — or it is not a single
   ciphertext, which changes what ADR-005 says a block IS.

3. **Reclaim requires it.** Space is reclaimed by dropping a block whole. A block
   holding one subject is droppable when that subject is gone; a block holding a
   thousand is droppable when all thousand are, which in practice is never.

4. ★ **The container already assumed it.** ADR-024 keys a block by one key and
   `Get` is a lookup. A block holding many subjects would need a key that is a
   range or a list, and finding one subject would stop being a lookup and become
   a scan of whatever else shares its block.

5. **Read amplification.** Fetching one subject decompresses the whole block, so
   a shared block makes every read pay for its neighbours.

6. **The cost is accepted and named: worse compression.** Attribute names and
   value shapes repeat across subjects and a per-subject block cannot exploit
   that. ⚠ Recovering it is the interning question (`BACKLOG.md` §12), which is a
   decision about where a dictionary lives — not a licence to merge blocks.

**What would falsify this.** A block whose datoms name more than one entity. That
is the falsifier in `Enforced-by:`, it is checkable against a real sealed segment,
and it is exactly what a compression improvement would produce.

## Alternatives Considered

- **Pack many subjects into one block for the compression ratio.** It is the
  reason the question exists, and the gain is real and large. Rejected under rules
  1–3: it makes each subject's data a probe for its neighbours', it makes
  crypto-shredding a rewrite, and it makes reclaim effectively impossible.
- **Mix subjects but encrypt each subject's datoms separately inside the block.**
  It looks like it gets the compression AND the erasure. Rejected under rule 1 and
  under ADR-005: encrypting separately means compressing separately too — the
  codec cannot find redundancy across ciphertexts — so the compression gain
  evaporates, and what is left is a block that is no longer one ciphertext.
- **Compress with a dictionary SHARED across blocks, so each block stays
  single-subject but benefits from cross-subject redundancy.** ⚠ This is the
  option worth naming, because it appears to defeat the objection. Rejected here
  rather than dismissed: a shared dictionary is state a block depends on, so a
  block stops being readable from its own bytes — the property ADR-005 exists to
  hold. It is the same trade as interning, and it belongs to `BACKLOG.md` §12
  where that trade is being tracked, not to a rule about what goes in a block.
- **Decide it per deployment, as a tunable.** It would let a tenant with no
  erasure requirement buy the compression. Rejected: it makes the confidentiality
  property a configuration, so whether one subject can probe another depends on a
  flag — and a format that permits either is a format where the answer must be
  discovered from the data rather than known.
- **Leave it undecided, since the code already does the right thing.** Nothing
  breaks today. Rejected as the whole point: the code does it by accident, and
  the change that breaks it arrives with a benchmark showing an improvement.

## Component / Boundary Impact

No new component and no changed behaviour. `internal/core/leafstore` gains a test
that holds the rule it already follows, and the rule is stated where the grouping
happens rather than where a block is defined — ADR-005 owns what a block IS, and
this owns what goes in one.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| none | no API changes — this records and gates behaviour that already exists | T1 | — |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| none — single task, no new surface | T1 | — | No |

## Consequences

- **Positive:** `BACKLOG.md` §13 closes, and the property is held by a gate rather
  than by the order somebody happened to write a loop in.
- **Positive:** Crypto-shredding, reclaim and the container's key→block lookup all
  keep resting on something checked.
- **Negative:** Compression is worse than it could be, measurably so, and the gate
  will reject the change that fixes it. That is the trade this record makes on
  purpose, and the follow-up says where the ratio can be recovered instead.
- **Neutral:** Nothing changes at runtime. Every segment already satisfies this.

## Out of Scope

- Interning names or transaction identifiers to recover the ratio (deferred: `docs/adr/BACKLOG.md` §12)
- A dictionary shared across blocks (deferred: `docs/adr/BACKLOG.md` §12 — it is the same where-does-the-dictionary-live trade)
- How large one subject's block may get (deferred: `docs/adr/BACKLOG.md` §15)
- Which codec is used (permanent: boundary: ADR-005 records the codec per block and this record says nothing about which one, so a codec change needs no re-decision here)
- What a block IS (permanent: boundary: ADR-005 owns the block's shape, header and checksum; this owns only what a block may contain)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A compression improvement packs subjects together | High — the gain is real, large, and easy to demonstrate | Critical — crypto-shredding becomes a rewrite, reclaim stops working, and one subject's data becomes a probe for another's | The falsifier decodes every block of a real sealed segment and asserts each names one entity |
| The rule is stated but tested only on a fixture the writer controls | Med | High — the gate then tests the fixture rather than the writer | The falsifier seals through the ordinary write path and reads the file back through `segstore` |
| A shared dictionary is added as a middle path | Med — it appears to defeat the objection | High — a block stops being readable from its own bytes, which is ADR-005's central property | Named and rejected in the Alternatives, and pointed at §12 where the trade is tracked |
| Compaction merges subjects while rewriting | Med — it already rewrites everything | Critical — the same failure, arriving through the operation least likely to be re-read | The falsifier runs after a compaction as well as after a seal |

## Rollback

Nothing to roll back: no behaviour changes. ⚠ Reverting the RULE means removing
the gate, at which point the property is once again true only until somebody
improves compression.

## Follow-ups

- [ ] When interning is decided (`BACKLOG.md` §12), re-read rule 6 — a dictionary is how the compression ratio comes back, and where it lives determines whether a block stays readable from its own bytes.
- [ ] When erasure coding reaches sealed segments (`BACKLOG.md` §12), confirm a stripe never spans two subjects' blocks in a way that makes reconstructing one require another's fragments — this record covers the block, not the stripe.
- [ ] Measure what the rule actually costs on a real corpus before anyone proposes reversing it; "worse compression" is currently an argument rather than a number.
