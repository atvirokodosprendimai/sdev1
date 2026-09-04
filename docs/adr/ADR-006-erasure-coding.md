# ADR-006: Erasure-code a block into a stripe that records its own scheme, and checksum every fragment

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-004-durability-policy.md`, `docs/adr/ADR-005-segment-format.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/erasure/**`
**Enforced-by:** `internal/core/erasure/erasure_test.go::TestStripeCarriesItsOwnScheme`
**Invalidates:** none — checked; ADR-004 decides how many copies exist and ADR-005 what a block is, and neither says how a block becomes fragments
**Served-path change:** A sealed block survives the loss of `m` of its `k+m` fragments and is rebuilt from the survivors on read, instead of being lost with the disk that held it.

## Context

ADR-004 decided how much redundancy a tier gets and refuses a policy the
topology cannot satisfy. ADR-005 decided what a block is and made it verifiable
from its own bytes. Neither says how a block is turned into the fragments that
redundancy is spread over.

Three things force this record now.

**Storage cost.** Replicating sealed data three ways costs 3×. `RS(8,2)` — eight
data fragments and two parity — costs 1.25× for a scheme that tolerates any two
fragment losses. On the volume this system is meant to hold, that difference is
the difference between the design working and not.

**The scheme is operator-configurable**, which means it varies over the life of a
cluster. `RS(8,2)` today, `RS(10,4)` after the next capacity change. That is the
same shape as ADR-005's codec, and it has the same answer.

**A rotten fragment is not a missing fragment**, and erasure coding treats them
completely differently. This is the part that is easy to get wrong and expensive
to discover, so it is stated in full below.

Two open follow-ups named this record and are closed here. ADR-004 asked that the
coded tier's `k+m` be checked against the same domain count that record already
validates, rather than against a second definition — this record consumes
`durability.Policy` and adds no arithmetic of its own. ADR-005 asked that the
coder write its stage into the block header rather than anything assuming every
block in a sealed segment is coded — it does, per block, and a segment may hold
both coded and uncoded blocks.

### Erasures and errors are not the same failure

A Reed–Solomon code with `k` data fragments and `m` parity fragments corrects
either:

- **`m` erasures** — fragments known to be missing, where the code knows WHICH
  positions to solve for; or
- **`⌊m/2⌋` errors** — fragments present but wrong, where the code must first
  work out which ones are lying.

An error costs twice an erasure because locating the fault consumes as much
redundancy as repairing it. `RS(8,2)` therefore tolerates two lost fragments —
and **zero** silently corrupted ones. With one rotten fragment and no way to
identify it, the decoder returns a block that is wrong, with no error raised
anywhere. That is the worst failure this system can have: not data loss, which is
visible, but data corruption reported as success.

⚠ ADR-005's block checksum does not close this. It is computed over the block's
stored bytes and is only available AFTER reconstruction, so it tells you the
result is wrong without telling you which fragment to exclude — leaving a search
over subsets as the only recovery.

The fix is to make every fault an erasure before decoding begins. A checksum on
each fragment turns "present but wrong" into "known bad, treat as missing", which
restores the full `m` tolerance and removes the silent-wrong-answer case
entirely. Fragment checksums are not a nicety layered on top of the code; they
are what makes the code's stated tolerance true.

## Existing Primitives Audit

- `internal/core/durability` (ADR-004): supplies `Policy` with `DataShards`,
  `ParityShards`, `DomainLevel`, `MinSize`, and `DomainsNeeded()` /
  `Validate(topology.Map)` / `Satisfied(domains)`. **Reused whole.** This record
  adds no second opinion about how many failure domains a scheme needs — the one
  in ADR-004 is the definition, and a coder that disagreed with it would be a
  cluster that codes data it cannot place.
- `internal/core/segment` (ADR-005): supplies `Checksum`, `ErrCorruptBlock`, and
  `StageCoded`. **Reused whole.** The fragment checksum is the same function over
  different bytes, deliberately: one implementation, one polynomial, one place to
  change it.
- `internal/core/addr` (ADR-001, ADR-016): supplies `LeafID`. **Reused** to say
  which leaf a stripe belongs to.
- Reed–Solomon arithmetic itself: **not reimplemented.**
  `github.com/klauspost/reedsolomon` is pure Go with SIMD paths, from the author
  of the compressor ADR-005 already depends on, so it does not change how the
  project ships. Writing a Galois-field coder by hand would be a few hundred
  lines of code whose bugs are silent and whose test corpus does not exist here.

## Decision

**A stripe is one block's worth of fragments, and it records the scheme that
produced it.**

1. **The unit of coding is the BLOCK, not the segment.** ADR-005 made the block
   the unit of compression, of checksumming and of reclaim; coding at the same
   granularity means a repair reads one block's fragments and a dropped block
   drops its parity with it. A segment may hold coded and uncoded blocks
   together, and the block header's `StageCoded` flag says which is which.

2. **Every stripe carries `k`, `m`, the fragment size and the block's identity in
   a fixed-width stripe header.** The operator's configured scheme decides what
   NEW stripes use and nothing else. Changing `RS(8,2)` to `RS(10,4)` changes the
   next write; it reinterprets no existing stripe, because no existing stripe
   asks configuration what it is. ★This is the third time this corpus has made
   this decision — the fan-out (ADR-001), the codec (ADR-005), now the coding
   scheme — and it is the same rule each time: **a constant that is safe as
   POLICY is fatal as a FORMAT ASSUMPTION.**

3. **Every fragment carries its own checksum, verified before decoding.** A
   fragment that fails is treated as absent, never as data. This is what makes
   rule 4's tolerance a fact rather than a claim, for the reason given in the
   Context.

4. **A stripe decodes when at least `k` fragments verify, and refuses otherwise.**
   It does not attempt a best-effort reconstruction from fewer. Below `k` the
   information is not present — a decoder that returned something anyway would be
   returning invention.

5. **Reconstruction is verified end to end.** After rebuilding, the reassembled
   block is checked against ADR-005's block checksum before it is returned. Every
   surviving fragment having verified should make this redundant; it is kept
   because it is the only check that spans the whole path, and a coding matrix
   bug is exactly the class that passes every local check and fails this one.

6. **`k + m ≤ 255`.** The arithmetic is over `GF(2^8)`, which has 256 elements
   and admits at most 255 non-zero code positions. This is a property of
   byte-oriented Reed–Solomon, not a choice, and it is validated at construction
   rather than discovered when a large scheme silently misbehaves.

7. **Encoding is deterministic.** The same block, `k` and `m` produce
   byte-identical fragments on any node. Repair depends on this: a rebuilt
   fragment must be indistinguishable from the one it replaces, or two replicas
   of the same fragment differ and nothing can say which is right.

8. **This package performs no I/O.** It turns bytes into fragments and back.
   Where fragments go is ADR-004's policy and the placement service's job; when a
   damaged stripe is repaired is a scheduler's, and repair traffic is already
   `BACKLOG.md` §3.

**What would falsify this.** If a real deployment shows that `k` fragments'
worth of survivors is routinely unavailable within a repair window — that is,
that the failure model of correlated loss makes `m` fixed parity fragments the
wrong shape — then per-block fixed-rate coding is the wrong scheme and a rateless
one is worth its complexity. That evidence does not exist today: this cluster has
never run, so the criterion is recorded as the thing to measure rather than as a
threshold already met.

## Alternatives Considered

- **Replication only, no coding at all.** Simple, fast to repair, and already
  implemented as ADR-004's `Live` tier. Rejected FOR SEALED DATA only: 3× against
  1.25× for equivalent tolerance is the whole storage budget. It is not rejected
  as a design — the two coexist, chosen per tier, which is why ADR-004 expresses
  durability as a policy rather than a constant.
- **Stripe the whole segment rather than each block.** Fewer, larger fragments,
  better parity ratio and less per-unit overhead. Rejected because reclaim is
  per-block: a segment-wide stripe cannot drop one block without re-coding
  everything around it, which turns cheap reclamation into a rewrite and takes
  back the property ADR-005 was designed for.
- **Fountain / rateless codes (LDPC, RaptorQ).** Attractive for repair: any
  sufficient subset of an unbounded fragment stream reconstructs. Rejected on two
  grounds. There is no `k+m` to check against a failure-domain count, so ADR-004's
  refusal floor loses its meaning; and the pure-Go implementations are not
  comparable in maturity to the Reed–Solomon one, for a component where a subtle
  bug is silent.
- **Hold the scheme in cluster configuration instead of the stripe.** Smaller
  stripes, one place to look. Rejected for the reason in rule 2: it makes a
  configuration change retroactively reinterpret every stripe ever written, which
  is unreadable data rather than a misconfiguration.
- **Omit fragment checksums and rely on ADR-005's block checksum.** Saves four
  bytes per fragment. Rejected: it halves the effective tolerance from `m` to
  `⌊m/2⌋` and, worse, admits reconstruction that returns wrong bytes reporting
  success. The four bytes buy the difference between a code that detects its own
  faults and one that cannot.
- **Verify fragments only when a first decode attempt fails.** Cheaper on the
  happy path. Rejected: the happy path is exactly where a silent corruption would
  pass through, because nothing would have looked.

## Component / Boundary Impact

One new component, `internal/core/erasure`, owning the transformation between a
block and its fragments and nothing else. It has one reason to change: the coding
scheme's representation.

It depends on `internal/core/durability` for what a policy asks for,
`internal/core/segment` for the checksum and the stage flag, and
`internal/core/addr` for a leaf identity. Nothing depends on it yet — no storage
engine exists — which is stated rather than implied.

⚠ The boundary that matters: this component decides what fragments ARE. It does
not decide where they go. `durability.Policy.DomainLevel` and the placement
service already own that, and a coder that also placed would be two authorities
over one question.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `erasure.StripeHeader` | new — fixed-width, records `k`, `m`, fragment size, leaf, block index | T1 | T2, and any future segment writer |
| `erasure.Fragment` | new — index, checksum, bytes | T1 | T2 |
| `erasure.ErrInsufficientFragments` | new sentinel — fewer than `k` fragments verified | T2 | callers |
| `erasure.ErrSchemeTooWide` | new sentinel — `k+m > 255` | T1 | callers |
| `erasure.Encode` / `erasure.Reconstruct` | new — the pure transformation, no I/O and no configuration argument | T2 | callers |
| `segment.StageCoded` | consumed — set on the block header by the coder | T2 | ADR-005's readers |
| `durability.Policy` | consumed unchanged — `DataShards`, `ParityShards`, `DomainsNeeded()` | T2 | — |
| `go.mod` | add `github.com/klauspost/reedsolomon` | T2 | — |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `erasure.StripeHeader`, `erasure.Fragment`, the fragment checksum | T1 | T2 | No — T2 is written against T1 and does not exist before it |
| `erasure.Encode`, `erasure.Reconstruct` | T2 | none yet | No |

## Implementation

Two tasks, sequential. See `docs/adr/ADR-006-erasure-coding/tasks/README.md`.

## Consequences

- **Positive:** Sealed data survives `m` simultaneous fragment losses at
  `(k+m)/k` storage cost — 1.25× for `RS(8,2)` against 3× for triple
  replication.
- **Positive:** A corrupted fragment is identified as corrupt before it can enter
  a reconstruction, so the code's stated tolerance is its real tolerance and
  there is no path that returns wrong bytes reporting success.
- **Positive:** Changing the configured scheme is safe at any time. Old stripes
  keep decoding under the scheme they were written with, because they carry it.
- **Negative:** Reading one block of a damaged stripe costs `k` fragment reads
  instead of one, and those reads are spread across `k` failure domains. Reading
  an intact stripe does not, since the data fragments are the block in order —
  but a degraded read is materially more expensive than a replicated one, which
  is part of why ADR-004 keeps the live tier replicated.
- **Negative:** Encoding is CPU work on the write path that replication does not
  pay. It is bounded and SIMD-accelerated, and it is the price of the storage
  ratio.
- **Neutral:** Fragment count per block rises with `k+m`, so a cluster with more
  parity has more objects to track. That is a placement and metadata cost, not a
  format one.

## Out of Scope

- Where fragments are placed, and on which failure domains (permanent: boundary: ADR-004 owns the policy and the placement service owns the choice; a coder that also placed would be a second authority over one question)
- When a damaged stripe is repaired, and how much bandwidth repair may consume (deferred: `docs/adr/BACKLOG.md` §3)
- Anything that opens a file, including writing fragments out or reading them back (deferred: `docs/adr/BACKLOG.md` §12)
- Encryption of the block before coding (permanent: boundary: ADR-007 owns the cipher; ADR-005 fixed that it runs before coding and that the block header records it)
- Cryptographic authentication of a fragment against deliberate alteration (permanent: boundary: the fragment checksum is a detection code for accidental corruption, the same threat model ADR-005 chose; defending against an adversary who can write to a disk needs ADR-007's key material)
- Re-coding existing stripes when the configured scheme changes (deferred: `docs/adr/BACKLOG.md` §14)
- `k+m` above 255 (permanent: fact: byte-oriented Reed–Solomon is arithmetic over GF(2^8), which admits at most 255 non-zero code positions; citation: version `github.com/klauspost/reedsolomon@v1.14.2`)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A scheme is configured that the topology cannot spread — fewer failure domains than `k+m` | Med | High — data coded but unplaceable, discovered during a repair | ADR-004's `Validate` already refuses such a policy at load; this record consumes that check rather than adding a second one |
| A rotten fragment enters reconstruction and the result is wrong but reported as success | Low after this record, near-certain without it | Critical — silent corruption | Per-fragment checksums, verified before decode; the reassembled block re-checked against ADR-005's block checksum |
| The coder is non-deterministic across versions or platforms, so a rebuilt fragment differs from the one it replaces | Low | High — two replicas of one fragment disagree and nothing can adjudicate | Determinism is a stated invariant with a test that encodes the same block repeatedly and compares bytes |
| Encoding CPU becomes the write-path bottleneck | Med | Med | Coding applies to the sealed tier only; live writes are replicated, per ADR-004 |
| A future scheme needs more than 255 positions | Low | Med | Refused at construction with a named error rather than misbehaving; a wider field would be a format version change, which ADR-005 already has a mechanism for |

## Rollback

The stripe format is persistent state, so rollback is not a code revert.

Before any data is written under it, reverting is deleting the package. After,
it is not reversible in the sense of undoing: stripes exist and carry their
scheme. What IS available is disabling coding for new writes — ADR-004's policy
selects a replicated tier — after which existing stripes remain readable, because
every one of them carries the scheme it needs. That is precisely the property
rule 2 buys, and it is the reason a rollback exists at all rather than being a
migration.

## Follow-ups

- [ ] When the segment writer lands (`BACKLOG.md` §12), confirm a block's `StageCoded` flag and its stripe are written and read as one unit; a block flagged coded whose stripe header is missing is unreadable in a way neither record catches alone.
- [ ] When ADR-007 lands, confirm coding runs after encryption and that the fragment checksum therefore covers ciphertext — a checksum over plaintext fragments would leak structure and would not check the bytes on the disk.
- [ ] When a repair scheduler is designed, verify it treats a checksum-failing fragment as absent rather than as present-and-suspect; the tolerance arithmetic in this record assumes it.
