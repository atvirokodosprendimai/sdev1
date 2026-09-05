# sdev1 — Deferred work and open obligations

Every `(deferred: docs/adr/BACKLOG.md …)` disposition in a decision record has an
entry here, written in the same commit as the deferral and naming its source
record. `adr-debt docs/adr` sweeps this file so deferrals resurface at the next
authoring session.

A pointer that resolves is not a pointer that was honoured. An entry that names a
destination which never heard of it passes every mechanical check and delivers
nothing, so the entry is written **here**, not merely pointed at from elsewhere.

## Open

### §1 — Trie depth policy is fixed at authoring time, not adaptive

**Source:** ADR-001 (`docs/adr/ADR-001-address-space.md`), Out of Scope.

ADR-001 fixes the live depth of the address trie as a cluster-wide configured
value and does not decide how a subtree grows one more byte when it becomes hot.
The addressing model admits per-subtree depth — a descent reads bytes until it
reaches a leaf, so nothing in the key format prevents one subtree being deeper
than its siblings — but the *policy* that decides when to split, and the
mechanism that migrates data during a split, are unowned.

This is real work and not a boundary. Until it is decided, a cluster rebalances
by moving whole leaves between servers rather than by subdividing them, which is
sufficient up to the point where one leaf exceeds a single server's capacity.

What a decision here must answer: what triggers a split (size, request rate, or
both), whether a split is online, who authorises it, and how in-flight writes to
the splitting subtree are ordered relative to it.

### §2 — Hot-entity write throughput has no mitigation

**Source:** ADR-001 (`docs/adr/ADR-001-address-space.md`), Out of Scope.

An entity hashes to exactly one leaf, and the write path for one entity is
single-writer by design. A single very hot entity is therefore capped at one
leaf's write throughput, and **adding leaves does not help** — the key hashes to
the same place however many leaves exist.

Known options, none chosen: accept the cap and document it; shard a hot entity by
`(entity, attribute-group)` so distinct attribute families land on distinct
leaves; or admit a per-entity write batcher that amortises consensus rounds.

The second breaks the "everything about one entity is one leaf" property that
ADR-001 exists to provide, so it is not a free choice.

### §3 — Repair traffic is unbounded

**Source:** ADR-001 (`docs/adr/ADR-001-address-space.md`), Out of Scope. Will be
inherited by ADR-006 when erasure coding is decided.

Rebuilding a lost erasure-coded fragment requires reading every surviving data
fragment of its stripe. At cluster scale, with devices failing continuously,
repair traffic can exceed client traffic, and nothing in the current design
throttles or prioritises it. Local reconstruction codes are the known remedy and
are not in scope for the first release.

### §4 — Skew is bounded and refused before absorption; the bound VALUE is open

**Source:** ADR-002 (`docs/adr/ADR-002-transaction-identity.md`), Out of Scope.

A hybrid logical clock never moves backwards, but it does move *forward* to match
the fastest clock it hears from. A node whose wall clock jumps hours ahead drags
every timestamp it touches with it, permanently — the cluster cannot come back,
because monotonicity is the property that forbids it.

~~Nothing currently bounds this.~~ **Answered by ADR-042**
(`docs/adr/ADR-042-clock-skew-admission.md`) — two of the three questions below,
and the third in shape if not in value.

★ **The irreversibility named above is not context around the decision, it IS the
decision.** If a skewed remote is adopted "permanently — the cluster cannot come
back", then a check performed AFTER absorbing is not a check; it is a report of
damage. So `Clock.Admit` checks first and leaves the clock byte-identical when it
refuses, and the record's falsifier asserts the CLOCK rather than the error —
merging and then returning an error looks identical from a caller's side.

**How it is measured without trusting the misbehaving node.** By the RECEIVER,
against its own wall reading. A node whose clock is wrong is exactly the node
whose self-assessment is wrong. ⚠ And the honest limit is recorded rather than
mitigated: this measures the DIFFERENCE between two clocks, not either one's
error, so a receiver whose own clock is wrong refuses correct peers — confidently.
Where one node is wrong that is right; where the majority is wrong it is exactly
backwards, and nothing here can tell those apart.

**Refused, evicted, or alarmed.** The MESSAGE is refused, the node is not evicted,
and the refusal is observable (`observe.KindClockSkewRefused`). A skewed node is
otherwise healthy — its data is correct and its storage is fine, only its
timestamps are wrong — so refusing its messages already stops the spread, and
evicting additionally loses a working replica over a clock.

★ **And a distinction that would otherwise have been got wrong:** the bound
applies to a timestamp arriving from another NODE, never to one read back from
DURABLE STORAGE. `tx.Minter.Observe` rehydrates history from a leaf; bounding
that path would make a leaf written by a formerly-skewed node permanently
unreadable — a clock problem converted into data loss, over skew that already
happened. So `Clock.Merge` stays unbounded and `Admit` is the network path.

⚠ **Still open: THE MAXIMUM SKEW ITSELF.** A datacentre and a wide-area link
tolerate different amounts, so ADR-042 requires a bound and deliberately invents
none.

⚠ **Also still open: nothing calls `Admit`** — there is no transport (§18) — and
a PERSISTENTLY skewed node is not yet an obligation. One refusal is a transient;
only a sustained one is somebody's problem, and that needs ADR-040's grace.

Until the bound is chosen and wired, the cluster's timestamp quality is still set
by its worst clock.

### §5 — Closed timestamps do not exist, so bounded-staleness reads cannot be offered

**Source:** ADR-002 (`docs/adr/ADR-002-transaction-identity.md`), Out of Scope;
will be inherited by ADR-009 when replication is decided.

Serving reads from a non-voting replica requires telling the reader how stale the
answer may be. That needs each leaf to periodically publish a closed timestamp —
an instant below which it guarantees nothing will ever commit — so a replica can
honestly answer "as of T".

Without it, reads from a replica are stale by an unbounded and unstatable amount.
The capability is still available; its consistency is simply not describable,
which is worse than not offering it.

This is a prerequisite for the independently-scaled read tier the design
requires, so it is a dependency of that requirement rather than a refinement.

### §6 — The map carries a generation; retention and per-segment recording are open

**Source:** ADR-001 T3 (`docs/adr/ADR-001-address-space/tasks/T3-placement.md`),
Stop Condition. Raised 2026-09-04.

Placement is currently a function of `(leaf, current map)`. But a segment written
a year ago was placed under *that year's* map, and finding it requires resolving
against the map as it stood then — so placement is really a function of
`(leaf, map version)`, and a segment header must record the version it was placed
under.

⚠ **THE HEADING WAS STALE AND IS CORRECTED.** It read *"The topology map is not
versioned"*. **Answered by ADR-032** (`docs/adr/ADR-032-map-generation.md`):
`topology.Map.Generation` is a `tx.TxID`, so map versions are ordered by ADR-002's
machinery exactly as this section predicted, and `placement.Resolve` REFUSES a map
carrying no generation with `ErrNoGeneration` rather than resolving against an
unidentified one. The signature question this section said must be settled before
callers exist was settled before callers existed.

⚠ **Still open, and this section named all of it:** recording in a SEGMENT HEADER
the generation it was placed under (deferred by ADR-032, and it needs §12's
layout); how an old map is retained and for how long; and what happens to a
segment whose placement map has been retired.

### §7 — Spare servers have no claim or release policy

**Source:** ADR-001 T3 (`docs/adr/ADR-001-address-space/tasks/T3-placement.md`),
Out of Scope. Raised 2026-09-04.

The requirement is a pool of declared spare servers per datacenter that hold no
leaves until a failure, at which point one is claimed and becomes the
re-replication target. The reference model is ZFS spare vdevs.

The part that is genuinely undecided is what happens **afterwards**, and ZFS is
opinionated about it: a ZFS spare is *temporary*, and when the failed device is
replaced the spare detaches and returns to the pool. Whether an sdev1 spare
detaches on repair or is simply absorbed as a permanent member is a real choice
with different operational shapes — detaching keeps the spare pool at its declared
size without operator action, absorbing avoids a second data movement.

What a decision here must answer: who declares a server dead and after how long,
which spare is chosen, whether the claim is automatic or authorised, whether the
spare detaches on repair, and what happens when the spare pool is exhausted.

### §8 — Tested against a real registry and it held; a conserved-quantity domain is untested

**Source:** ADR-003 (`docs/adr/ADR-003-transaction-boundary.md`), Out of Scope
and its stated falsifier.

ADR-003 confines a transaction to one entity, and that single constraint is what
removes the need for distributed commit from the entire system. Its falsifier is
correspondingly large: the decision fails if a legitimate domain operation cannot
be expressed within one entity.

~~Nothing has tested it. The refusal is implemented and proven to fire, but no
real domain has been modelled against it.~~ **Answered by ADR-044**
(`docs/adr/ADR-044-the-boundary-against-a-real-domain.md`), against a corpus M
supplied on 2026-09-05: 548,547 Lithuanian public-procurement legal entities
(`juridiniai.jsonl`, 178 MB, git-ignored and never committed).

★ **The corpus armed the falsifier in the registry's OWN vocabulary.** 277
entities carry a `legalStatus` naming a legal act spanning several companies —
`Reorganizuojamas` (139), `Dalyvaujantis reorganizavime` (74), `Dalyvaujantis
atskyrime` (56), plus cross-border mergers and splits. ⚠ *Dalyvaujantis* means
**participating**: a status whose entire content is "I am one party to an act
involving others" is the domain stating that multi-entity operations are real
here, in a word this project did not predict.

★ **And the boundary HELD, because the ACT IS AN ENTITY.** A reorganisation has a
date, a kind and participants, so registering it is one transaction on the act
carrying references to the companies — no cross-entity write, no distributed
commit. The cross-entity refusal is shown still firing in the same test, so the
act fits the boundary rather than circumventing it.

⚠ **It is only USABLE because of ADR-035's inbound read, and nobody planned
that.** "Which companies are in reorganisation 7" is `READ ->name FROM [reorg-7]`.
Before that record — built the same day for an unrelated reason — the normalised
model was storable and unqueryable. **ADR-003's liveability rests on ADR-035**, a
dependency nobody designed and therefore nobody is maintaining.

⚠ **Where the registry denormalises**, putting the status on each participant,
reproducing it takes two transactions. ★ Bitemporality pays for the missing
atomicity: both facts carry the act's real-world date as `Valid.From`, so a reader
on the VALID axis sees a consistent world however the writes interleaved. The
tearing exists only on the transaction axis, which is the audit axis.

★★ **The rule that generalises, and it is the durable output: bitemporality
substitutes for cross-entity atomicity exactly when the operation is a statement
about the WORLD — which has its own instant — rather than about the SYSTEM.** A
reorganisation happened on a date; the write order is a fact about the database,
not about Lithuania.

⚠ **STILL OPEN, and this is where the boundary would genuinely fail: a domain
with an invariant that must hold at every TRANSACTION instant** — a balance
transfer, a double-entry ledger, a conserved sum. No real-world instant makes the
intermediate state acceptable there. This registry conserves nothing; it records
what happened. **One domain is one domain**, and ADR-044 answers §8 narrowly on
purpose, naming the property a future domain must be checked for rather than
declaring the boundary universally liveable.

⚠ **A second finding the corpus produced, unrelated to the boundary:** every
registry identifier is ALL-NUMERIC, and a bare numeric cannot be an entity name —
it lexes as a number. ADR-021's backticks cover it (`` FROM `111756039` ``), but
the guide presents quoting as being about KEYWORDS, and a domain whose primary
keys are integers is entirely ordinary. Found by pointing the language at real
data rather than by reading the grammar.

What a decision here must answer: model at least one non-trivial domain against
the boundary, and for any operation that resists it, decide between expressing it
as several transactions plus a compensating one, widening the boundary (which
pulls in distributed commit and reopens ADR-003's central choice), or declaring
the operation out of scope for this engine.

⚠ Widening later is additive and therefore cheap; narrowing later is not. The
cost of leaving this open is bounded, and the cost of guessing wrong in the
permissive direction is not.

### §10 — A below-floor leaf is reported and stays readable; re-replication is open

**Source:** ADR-004 (`docs/adr/ADR-004-durability-policy.md`), Out of Scope.

ADR-004 refuses writes to a leaf holding fewer than `MinSize` durable copies.
It does not decide what happens next, and "the write is refused" is only half an
answer: the leaf is still readable, still degraded, and still degrading.

~~What a decision here must answer: whether the cluster re-replicates
automatically or waits for an operator, how a leaf below the floor is surfaced,
whether such a leaf is evicted from the read path, and how an operator
distinguishes "briefly degraded during a restart" from "genuinely short of
copies".~~ **Three of the four are answered by ADR-040**
(`docs/adr/ADR-040-below-the-floor.md`).

**How it is surfaced.** `durability.Watchdog` reports every short leaf with its
age, oldest first, and raises an ADR-038 obligation once it has been short longer
than a declared grace.

**Whether it is evicted from the read path.** No. ⚠ A below-floor leaf is
degraded, not wrong: its data is readable and correct, so eviction trades a
durability risk for a certain outage — and removes exactly the copies that still
exist. Same argument ADR-015 uses for a shed write.

★ **How an operator tells a restart from a shortfall — and this section's own
wording contained the answer.** It said the two *"look identical for the first few
seconds"*. They do not merely look identical: **instantaneously they ARE the same
observation.** A leaf holding two of three racks is holding two of three, whatever
the reason. No richer measurement separates them, because the difference is
entirely in what happens NEXT — so the discriminator can only be TIME, and it is
declared rather than guessed.

⚠ And the grace withholds the OBLIGATION, never the STATUS. Suppressing both is
the obvious single rule and it makes a genuine shortfall invisible for the grace;
an operator watching a rolling restart wants to see the dip and its recovery, they
simply do not want to be answerable for it.

⚠ **Still open: whether the cluster re-replicates automatically or waits for an
operator.** It needs consensus to decide who acts (§19), and a wrong choice is
exercised only during the failure that makes it matter. The report is what makes
that choice measurable rather than argued.
### §11 — Reuse and authorization are decided; allocation is open

**Source:** ADR-016 (`docs/adr/ADR-016-tenant-prefix.md`), Out of Scope and its
Follow-up.

ADR-016 makes a tenant the leading bytes of a key and therefore a contiguous
subtree. It does not decide who assigns those bytes.

⚠ **THE HEADING WAS STALE AND IS CORRECTED.** Two of the three are answered by
ADR-033 (`docs/adr/ADR-033-grants-and-tenant-allocation.md`).

**Reuse: answered.** ~~The safe answer may be that identifiers are never
reused.~~ It is: rule 6 says a tenant identifier is NEVER reused, because a
reused one inherits whatever of the previous tenant's subtree remains, and proving
otherwise is the enumeration problem ADR-007's design exists to avoid. ⚠ Rule 7
records the cost that follows — the identifier space is a finite budget of 65,536
for the life of a deployment, and creating-then-destroying tenants consumes it
permanently.

**Authorization: answered.** Grants are datoms in reserved tenant `0000`,
revocation is a retraction, and no grant means refused.

⚠ **Still open: ALLOCATION.** Who assigns an identifier to a new tenant, and under
what authority — ADR-033 defers it to §19, because it is a cluster-wide decision
needing consensus.

★ **And the constraint below was not merely carried into the record, it is
STRUCTURAL there.** `authz.Set.Allow` takes no instant at all, so authorizing
against the grants in force at a past moment is not a rule somebody must remember
— it is a question with no parameter to ask it with.

⚠ One design constraint is already known and should survive into whatever record
closes this: a query `AS OF` a past instant must be authorized against the
CURRENT grant set, never the grants in force at that instant. The symmetry is
tempting — the data is historical, so why not the permissions — and it is a leak:
revoking access today would otherwise leave the revoked party able to read last
year. Grants are naturally datoms in a reserved system tenant, which makes "who
had access at time T" answerable and makes revocation a retraction.

### §12 — Segments reach a disk; the block index, the layout and interning are open

**Source:** ADR-005 (`docs/adr/ADR-005-segment-format.md`), Out of Scope; and
both its task files, whose Out of Scope defers anything that opens a file here.

⚠ **THE HEADING WAS STALE AND IS CORRECTED.** It read *"Nothing writes a segment
to a disk, or finds a block inside one"*, which stopped being true when ADR-024
and ADR-026 landed — and was still being read as a statement of fact, including
by records deferring work to it. ★ A backlog entry describing a gap that is
already closed is the same defect as a declared reader that never runs: it is
believed, and it is wrong.

`internal/core/segment` decides what a block IS and refuses to touch a
filesystem. That was deliberate — the format has to be right before any byte
reaches a disk, and keeping the package to byte slices is what makes every
property testable with no storage engine.

**The writer.** ~~Blocks must be packed into a segment, the segment made durable,
and the moment it becomes readable defined.~~ **Answered by ADR-024**
(`docs/adr/ADR-024-segment-store.md`): `segstore.Writer` packs blocks and `Seal`
publishes by atomic rename from a temporary name in the SAME directory, so a
segment is either absent or complete and never partially visible.
`segstore.Reader` mmaps a sealed one. "Sealed" is the state transition and it is
owned there. ADR-026 (`docs/adr/ADR-026-leaf-store.md`) assembles segments into a
leaf, and ADR-029/ADR-030 seal and compact it.

★ **And the trap this section named is answered by construction.** It warned that
*"whatever names a segment file must not encode anything a reader needs in order
to interpret it"*. `segstore` names a segment with sixteen random hex bytes: the
name carries no codec, no tenant, no version and no ordering, so there is nothing
in a path for a reader to depend on. It means nothing on purpose.

**The block index.** ⚠ **Partly answered, and the open half is the one that
matters.** A segment carries its own index — key to span — so finding a block
inside ONE segment is a lookup rather than a stride, and the index is checksummed
in the trailer. What is still open is the layer above: what maps a subject to the
SEGMENT holding it, across a leaf and then across leaves. Today a leaf consults
its segments in turn. That is the question the layered-index discussion was
circling, it belongs with §27's index work, and it should be answered with it
rather than separately.

**The on-disk layout.** Roughly four megabytes per block and a nested directory
path were both discussed as the shape. Neither is decided — `segstore` writes flat
files into a directory it is given — and the reclaim argument, many small units
droppable whole rather than one file to rewrite, constrains it more than the
numbers do.

**Interning is the fourth, and it is the largest saving available.** Every datom
carries an entity and an attribute, and a segment repeats the same handful of
attribute names across every datom it holds. A per-segment dictionary mapping
those to small integers shrinks the data before any codec runs, and a general
compressor recovers only part of it. It is deferred rather than dismissed
because a dictionary is state that the block-is-self-describing rule reaches:
either the dictionary lives in the segment header — making a block readable only
in the context of its segment, which weakens the property deliberately and
knowingly — or it is duplicated per block, which costs most of the saving. That
trade is a decision, not an optimisation.

⚠ One trap is already visible: whatever names a segment file must not encode
anything a reader needs in order to interpret it. The whole record rests on a
block being readable from its own bytes, and a filename is not part of the
block. A layout that puts the codec, the tenant or the version in the path
recreates exactly the configuration dependency this format refuses.

### §13 — Answered: a compression block holds one subject's datoms

**Source:** ADR-005, raised during implementation of T2.

A codec finds redundancy across whatever is inside one block, so packing many
subjects together compresses better — often much better, since datoms for
different subjects share attribute names and value shapes. Three things push the
other way, and none of them is a tuning question.

**Reclaim.** Space is reclaimed by dropping a block whole. A block holding one
subject is droppable when that subject is gone; a block holding a thousand is
droppable when all thousand are, which in practice is never.

**Erasure.** ADR-007 will encrypt per subject. A block that mixes subjects is
either encrypted under one key — which makes crypto-shredding one subject
impossible without rewriting everything sharing its block — or is not a single
ciphertext at all, which changes what a block is.

**Read amplification.** Fetching one subject decompresses the whole block.

~~The answer is likely "a block holds one subject's datoms, and a segment holds
many blocks", paying compression ratio for the other three.~~ **Answered by
ADR-030** (`docs/adr/ADR-030-one-subject-per-block.md`), which is exactly that —
and it found a FOURTH argument this section did not have, stronger than the three
below.

★ **A shared block is a COMPRESSION ORACLE.** A codec's output size is a function
of everything inside it, so two subjects in one block make each subject's data a
probe for the other's: write data you control, observe the block shrink, learn
about data you do not control. That is a confidentiality property, which is why
the question was never a tuning one wearing a performance costume.

★ And a fifth: the container already assumed it. ADR-024 keys a block by ONE key
and `Get` is a lookup; a block holding many subjects would need a key that is a
range or a list, and finding one subject would stop being a lookup.

The cost is accepted and named: worse compression, because attribute names repeat
across subjects and a per-block codec can no longer exploit that. `BACKLOG.md`
§12's interning entry is where that saving is recovered, if it is.

### §14 — Nothing re-codes existing stripes when the configured scheme changes

**Source:** ADR-006 (`docs/adr/ADR-006-erasure-coding.md`), Out of Scope; and its
T2, which defers the same thing.

ADR-006 makes the coding scheme a property of each stripe, so changing the
operator's configured scheme is safe: old stripes keep decoding under the scheme
they carry. That is a correctness guarantee and it is deliberately not a
migration.

What it leaves open is that a cluster configured from `RS(8,2)` to `RS(10,4)`
then holds two populations indefinitely, with different tolerances and different
storage costs, and nothing reports the split or converges it. Three questions
have no answer yet.

**Whether to converge at all.** Re-coding a stripe means reading `k` fragments,
re-encoding and writing `k+m` — the most expensive operation this system can
perform, over data that is by definition cold. Leaving old stripes alone may
simply be correct, in which case the answer is a report rather than a migration.

**How a mixed population is observed.** An operator who cannot see how much data
sits at the old tolerance cannot decide the first question. This wants a count
per scheme, and it belongs with whatever ADR-012's console reports.

**What happens when a scheme is REDUCED.** Moving to fewer parity fragments
lowers the tolerance of new writes while old stripes stay stronger, which is
harmless. Moving the other way is the case that tempts a migration, and it
competes for exactly the bandwidth repair needs — so it cannot be designed
independently of §3.

⚠ Whatever closes this must not be allowed to reach back and rewrite the scheme
recorded in an existing stripe's header in place. The header is what makes that
stripe readable; a converter that edits it before the fragments beneath it have
actually changed produces a stripe that describes something that does not exist.

### §15 — Sealing has a trigger; the tail index and the durability transition are open

**Source:** ADR-017 (`docs/adr/ADR-017-lock-free-read-path.md`), Out of Scope; and
both its task files.

ADR-017 makes the live tail readable without locks and ADR-005 makes a sealed
segment immutable. Neither says how one becomes the other, and two open
questions sit in that gap.

**When sealing happens.** ~~A size threshold, an age, a transaction count, or an
operator's instruction — each gives a different tail length.~~ **Answered for one
leaf by ADR-028** (`docs/adr/ADR-028-seal-policy.md`): `leafstore.Policy`
carries `MaxBytes`, `MaxAge` and `MaxSegments`, `ShouldSeal` decides against them,
and `Exposure` reports what is at stake — and a policy bounding NOTHING is refused
with `ErrNoBound` rather than silently never sealing.

⚠ **Still open, and this section named it:** sealing is also a DURABILITY
transition, the moment a leaf's data moves from ADR-004's replicated policy to a
coded one — not only a layout change. Nothing yet re-codes at that boundary
(§14), and nothing measures what the transition costs.

**How an index over the tail is published.** Walking a tail to find one subject
is linear, so an index is wanted — and ADR-017's rule constrains what kind. A
structure that must be rebalanced IN PLACE to be read cannot be published
atomically and therefore cannot go on this read path. The rule was written down
first precisely so this question is answered before something is built that
cannot satisfy it. Whatever closes this must show its index being published by a
single atomic store, or argue explicitly for changing the rule.

⚠ The trap in sealing is that it is TWO publications, not one: the segment
becomes readable and the tail entries it replaces become redundant. A reader
holding a snapshot taken before the seal must still see a consistent view, which
means the tail's entries cannot be dropped the moment the segment appears. The
safe shape is that both are reachable until no snapshot predates the swap — and
that is a reclamation question ADR-017 deliberately did not open.

### §16 — Nothing measures how slow a degraded read is

**Source:** ADR-019 (`docs/adr/ADR-019-chaos-and-the-failure-catalogue.md`), Out of
Scope; and both its task files.

The failure catalogue records whether a fault is survived. It says nothing about
what surviving costs, and for a read that is most of the operational story.

ADR-006 already names the shape: reading one block of a damaged stripe means `k`
fragment fetches across `k` failure domains instead of one local read. That is a
large constant, and it arrives exactly when a cluster is already degraded and
already under repair traffic — so the three interact and none of them is
measured.

⚠ The trap is that "recovers" reads like "is fine". A stripe that reconstructs
correctly but takes fifty times as long has not failed by any assertion in the
catalogue, and has failed by every measure a caller cares about. A system whose
degraded mode is correct and unusably slow looks healthy to every gate in this
corpus.

Three things want numbers, and none of them can be taken yet because nothing
serves a read: the latency multiplier of a degraded read against an intact one;
how that multiplier moves as repair traffic competes for the same bandwidth
(§3); and whether ADR-015's admission control sheds the right work when both are
happening at once, or sheds the repair that would end the degradation.

Whatever closes this should extend the catalogue rather than sit beside it — a
disposition says whether the data came back, and a cost column would say what it
took, which is the pair an operator actually needs.

### §17 — The keystore has a home now; rotation and caching are still open

**Source:** ADR-007 (`docs/adr/ADR-007-crypto-shredding.md`), Out of Scope and
its Follow-ups; and both its task files.

ADR-007 makes erasure the destruction of a per-subject key and puts that key in a
mutable keystore, deliberately separate from the immutable ciphertext. It does
not say where the keystore actually lives, and three questions follow.

**Persistence.** ~~The shipped implementation is `MemoryKeystore`, and its name is
the warning: every key is lost on restart, which erases everything.~~
**Answered by ADR-031** (`docs/adr/ADR-031-keystore-home.md`):
`crypt.DirKeystore` keeps each key in its own file under a directory that is
separately deletable, and `OpenDirKeystore` probes at open time that the directory
can be both WRITTEN to and REMOVED from — because a store that can only create is
one where erasure fails at the moment it matters.

⚠ The unusual requirement it inherited is honoured: a key file is UNLINKED rather
than tombstoned, and the cache entry is evicted inside the shred rather than
after it, so a destroyed key cannot be served from memory. `MemoryKeystore`
remains for tests, where losing keys on exit is the correct behaviour.

⚠ **Still open, and unchanged: the keystore must not share a backup with the
data.** Restoring one that holds both resurrects the key beside the ciphertext,
silently undoing every erasure it contains. This is the single easiest way to get
crypto-shredding wrong, it is a retention decision rather than a code one, and
giving the keystore a directory of its own makes it easier to get right without
making it automatic.

⚠ **The keystore must not share a backup with the data.** Restoring one that
holds both resurrects the key beside the ciphertext, silently undoing every
erasure it contains. This is the single easiest way to get crypto-shredding
wrong, and it is a retention decision rather than a code one.

**Rotation.** Re-encrypting a subject under a new key means reading and rewriting
every block it owns — the enumeration problem ADR-007 exists to avoid, arriving
by another door. It may simply be that keys are never rotated and compromise is
handled by shredding and re-ingesting, but that is a decision nobody has taken.

**Caching.** Every read of an encrypted subject costs a key fetch, and caching is
the obvious answer. It is also the obvious way to break erasure: a cached key
outlives its destruction, so a shredded subject stays readable on whichever node
happened to hold it. Any cache needs an invalidation that is part of the shred
rather than beside it, and "eventually" is not good enough for an erasure.

### §18 — The response envelope is fixed; the transport and route distribution are open

**Source:** ADR-008 (`docs/adr/ADR-008-prefix-routing.md`), Out of Scope; and both
its task files.

ADR-008 decides what a route MEANS, how a lookup resolves one, and what a stale
one does. It decides nothing about how bytes move between machines, which leaves
three questions and one whole missing layer.

**The transport itself.** Framing, connection management, timeouts, and how a
request identifies the leaf it is for. This is the single largest unbuilt piece
of the system and several other records wait behind it — ADR-018's read-ahead,
ADR-019's composed chaos suite, and anything that measures a degraded read (§16).

⚠ Whatever carries a redirect must make it structurally impossible to mistake
for a successful answer. ADR-008 enforces that in Go's type system, and a wire
format that flattens both into one message shape would give the property back.

★ **ANSWERED by ADR-043** (`docs/adr/ADR-043-response-envelope.md`), and settled
BEFORE the transport for the same reason ADR-032 settled the map's generation
before callers existed: afterwards there are messages in flight and it is a
migration rather than a decision.

★ **The flattening is the DEFAULT outcome, not an unlikely one.** The ordinary
design is a struct with a payload and an optional redirect field, and under every
mainstream schema language a missing field decodes to a zero value — so a client
that receives a redirect and reads the payload gets an empty SUCCESSFUL answer,
with no error and nothing to notice. The stale route it was being sent away from
has just served it a result.

So: three outcomes and no others; a `wire.Redirect` with NO payload field —
absent, not empty; and three refusals that make the shape hold against BYTES
rather than only against a struct definition — an unknown outcome tag, an unknown
version, and ⚠ **trailing bytes, which is the important one**, because "ignore
what you do not understand" is precisely how a payload smuggles itself into a
redirect. A redirect also carries its route's EPOCH, without which ADR-008 rule 5's
loop protection is gone while the redirect still looks correct.

⚠ **Still open, and this is the bulk of it:** framing, connection management,
timeouts, and what a REQUEST looks like. None of those carries a correctness
property that is lost by waiting, which is why the envelope went first and they did
not. `wire.Encode`/`Decode` have no caller.

**How a route reaches a node.** Gossip, a control plane, or something derived
from the topology map — each has a different staleness profile, and ADR-008 is
deliberately correct under all of them because a stale route is a redirect rather
than an error. So this is a performance decision rather than a correctness one,
which is unusual enough to be worth saying: choosing badly here costs hops, not
answers.

**When a node may forget.** A node must know where a leaf WENT in order to
redirect for it, so it holds routing state about leaves it no longer serves. That
state has to age out or it grows forever, and nothing says when. Forgetting too
early turns a redirect back into an error for whichever client was slowest.

### §19 — Consensus is decided but unbuilt: nothing elects, replicates or remembers a grant

**Source:** ADR-009 (`docs/adr/ADR-009-fenced-leases.md`), Out of Scope; and both
its task files.

ADR-009 lands the fencing half — an epoch that orders claims and a resource that
refuses stale ones — and that half is what closes the catalogue's open failure.
The other half is decided in prose and built nowhere.

**Raft itself.** Log replication, elections, membership changes. It needs the
transport §18 owns, and picking a library before there is a transport to run it
over would be choosing on no information.

**Heartbeat coalescing.** One consensus group per leaf subtree means many mostly-
idle groups over the same few nodes, and the design only works if their
heartbeats share a message. What that message looks like is a wire question and
waits with §18.

**Where a registry lives, and whether it remembers.** Today's is in-process, so
every epoch is forgotten on restart. ⚠ A granter that restarts and REISSUES an
epoch it already granted voids fencing completely, because the token stops
ordering anything. ADR-009's tail refuses any epoch not strictly above what it
has SEEN, so a confused granter is caught at the resource — but a granter that
forgets everything cannot grant safely at all, and that is the gap.

⚠ One thing must survive into whatever closes this: **nothing on the write path
may consult liveness.** No heartbeat, no timeout, no health check gating an
append. The whole value of an epoch is that the resource refuses correctly while
knowing nothing about who is alive, and a consensus layer is exactly where
somebody will be tempted to add a liveness check "for safety".

### §20 — A query is evaluated; planning, similarity and the unbounded scan are open

**Source:** ADR-011 (`docs/adr/ADR-011-query-language.md`), Out of Scope; and both
its task files.

**Partly answered.** ADR-027 (`docs/adr/ADR-027-evaluator.md`) built the
evaluator. ADR-034 (`docs/adr/ADR-034-read-verb.md`) punted "reading a SET rather
than one named entity" and "paging a result" here, and ADR-035
(`docs/adr/ADR-035-inbound-read.md`) answered BOTH for the bounded case:
`READ ->a FROM [e] WHERE … LIMIT n OFFSET m` reads the entities that point at `e`.

★ It is answered for a set that an ADDRESS bounds. What remains open below is the
unbounded case — "every entity" — which still needs a planner and routing, and
which is a different problem rather than a bigger version of the same one.

ADR-011 decides what a caller may WRITE and what it MEANS. Running it is a
different job and it waits on storage.

**Evaluation.** A parsed statement has to become datom reads bounded by the
resolved qualifiers. ⚠ Whatever does that must pass the parser's resolved
qualifiers straight to `temporal.Visible` and re-derive nothing: re-deriving is
exactly where the two time axes get conflated again, which is the defect ADR-002
was written against and a predecessor project shipped.

**Planning.** Which index to use, in what order to evaluate terms, and what a
term costs. None of it can be decided before there is an index (§15) or a stored
byte (§12), and guessing now would fix an execution strategy against a storage
layer nobody has built.

**Similarity.** ADR-011 requires a shape query to STATE its metric and threshold
rather than defaulting them, which makes a query reproducible. It does not say
what any particular metric computes, and that needs real data to be worth
choosing — a metric picked against no corpus is a number nobody has reason to
believe.

**The graph operator past one hop.** ADR-011 fixes that a time clause may attach
per leg, which is why time is a clause at all. A multi-hop traversal syntax is
deferred, and whatever adds it must keep that property rather than quietly
losing it in the recursion.

**Still open after ADR-035:** a general JOIN — reading a member's attributes
alongside the index entity's own. ⚠ ADR-035 rule 3 reserves the notation for it:
inside `FROM [e]`, `->a` is a member's attribute and a bare `a` is REFUSED rather
than treated as a synonym, precisely so that a join can be added later without
changing what already-written statements mean.

**Also still open:** more than one predicate on a read, boolean combination, and
`ORDER BY`. ADR-035 fixes ONE member order — entity name — because paging is
incoherent without a total order, not because that order is the interesting one.

### §21 — Watching exists; export, sampling and retention are open

**Source:** ADR-012 (`docs/adr/ADR-012-observability.md`), Out of Scope; and both
its task files. ADR-010 also defers its purge escalation here.

ADR-012 decides what a component may SAY and proves every declared thing has a
reader. Four things sit past that.

**Export.** Reaching an external metrics system means choosing a format, and
choosing one before there is a transport (§18) or anything consuming the stream
would be choosing on no information.

**Sampling and aggregation.** A stream that emits per request cannot be kept in
full at planetary scale, so windows and rates are needed. ⚠ Sampling interacts
badly with the drop counter: a sampled stream and a dropped stream look identical
to a consumer unless the two are reported separately, and conflating them turns
"we shed load" into "we lost data" or the reverse.

**Retention.** How long the stream is kept is ADR-010's `Horizon` applied to a
different sink, and it should reuse that rather than growing a second retention
notion.

**Watching, which is the one that matters.** ~~ADR-010 leaves a purge INCOMPLETE
when a sink has not acknowledged, and deliberately does not escalate — it defers
that here. ADR-012 can now EXPRESS it as a declared event with a named reader.
Nothing yet looks.~~ **Answered by ADR-038**
(`docs/adr/ADR-038-obligations.md`): `watch.Ledger` keeps what is outstanding, and
`watch.FromPurge` turns an incomplete purge into an obligation naming the sinks
ADR-010 already identified.

★ **The test above ruled out the obvious design, which is the finding.** A watcher
over the event stream fails three ways: the stream DROPS by design (ADR-012), so
it loses the events that matter under the load that produces them; a stream does
not persist, so a month-old event is gone; and it would need retention, which is
the trap below. So an incomplete purge is a STATE rather than an event — the event
announces it, the obligation survives it, and only an acknowledgement clears it.

⚠ **THE TRAP THIS SECTION WALKS TOWARD while being right about something else.**
The retention bullet above says reuse ADR-010's `Horizon` rather than growing a
second notion — correct, for the LOG. Applied to the OBLIGATION it inverts what
age means: a thirty-one-day-old incomplete purge stops being reported under a
thirty-day horizon, and the system answers "nothing is outstanding" precisely
BECAUSE the problem got old. `watch.Ledger.Outstanding` therefore takes no horizon
at all — the signature is the enforcement.

★ ADR-038 rule 5 also settles this section's SAMPLING warning for obligations,
structurally rather than by argument: an obligation is raised by the emitter and
never travels on the stream, so no drop or sample policy can lose one. The
stream's own sampling is untouched and still needs the separate counts named
above.

⚠ **STILL OPEN: the ledger is in memory, so a restart loses it.** The honest
reading of the test today is "a month-old obligation reaches a person, in a
process that has been up a month". A store exists now (§12), so closing this is a
real next step rather than a blocked one — and ADR-038 records the rejected
"obligation as a datom" alternative to revisit alongside it.

⚠ **Also still open: nothing reads the ledger.** There is no console and no
transport (§18/§25). `Raise` is not yet called on a served path either.

### §22 — Which reads matter more is decided; the all-withdrawn response is open

**Source:** ADR-015 (`docs/adr/ADR-015-admission-control.md`), Out of Scope; and
both its task files.

ADR-015 lets a saturating node stop pulling read work, which turns saturation
into a routing outcome rather than an error. Two questions sit past it and both
appear under exactly the load that makes them urgent.

**Every replica sheds at once.** Withdrawal removes capacity from the queue, so a
cluster-wide load spike can leave a queue with nowhere to put work. Nothing in
ADR-015 prevents that and it says so. The plausible answers differ sharply — a
floor on how many replicas may be withdrawn at once, an admission decision that
looks at the group rather than only at itself, or accepting that the queue backs
up and letting it — and choosing between them needs a cluster to observe rather
than an argument.

⚠ The trap is that the obvious fix reintroduces the problem: a node that refuses
to withdraw because its peers already have is a node that keeps taking work it
cannot serve, which is the error-returning behaviour ADR-015 rejected, arrived at
by a different route.

★ **THE TRAP IS ANSWERED BY ADR-039; THE POLICY IS STILL OPEN, AND SEPARATING
THEM IS WHAT MADE THE FIRST HALF DECIDABLE.** "Should I withdraw" is a question
about THIS node; "what do we do when everyone has" is a question about the fleet.
Conflating them is the only way to write the trap. So `Controller.Decide` takes no
peer state — the signature is the enforcement — and `admit.Fleet` holds replica
STATES rather than controllers, so there is nothing to reach back through. The
all-withdrawn condition is REPORTED, as an ADR-038 obligation naming the replicas,
and answered by nobody.

⚠ **Still open: which of the three responses is right.** ADR-039 rule 3 defers it
here deliberately, and the obligation is what makes the choice measurable rather
than argued.

**Which reads matter more.** ~~A repair read and a user query compete for the same
budget, and shedding treats them alike.~~ **Answered by ADR-039**
(`docs/adr/ADR-039-shed-what-can-be-rerouted.md`): a withdrawn node refuses
`ClassUser` reads and keeps serving `ClassRepair` reads.

★ **And ADR-015 already contained the ordering principle — it is ELASTICITY, not
importance.** ADR-015 sheds reads and never writes because any replica can serve a
read, so shedding it RE-ROUTES the work, while a leaf has one writer so a shed
write is an outage. One level down: a user read is elastic, and a repair read is
not, because it reads the fragments THIS node holds. Shedding a repair read does
not move the work — it cancels it. §16's cost argument points the same way and is
corroboration rather than the reason.

★ Three tiers — writes always, repair while withdrawn, user only while joined —
out of ONE budget, one utilisation and one state. The constraint below is
honoured: the class is an ORDER, not a second budget dimension, and no per-class
ceiling exists.

Whatever closes this must not grow a second budget dimension per class; ADR-015
refuses a third budget kind deliberately, and priority within a budget is a
different mechanism from a budget per priority.

⚠ **Still open, and it ships open: the starvation risk.** A node saturated by
repair work stays withdrawn and keeps refusing user reads. What bounds that is a
bound on repair traffic — §3, still open. ADR-039 rule 7 names §3 as the owner
rather than inventing a cap nobody can justify.

### §23 — The unflushed window is measured and bounded; nothing flushes yet

**Source:** ADR-020 (`docs/adr/ADR-020-commit-point.md`), Out of Scope and its
Consequences; and both its task files.

ADR-020 acknowledges a write once N memory replicas in distinct power domains
hold it, and flushes afterwards. That is deliberate and it is the whole
performance argument. It leaves an exposure window nothing measures or bounds.

**How long.** Between acknowledgement and flush, the data survives independent
failures and not correlated ones. The size of that window is how long a block
takes to fill and be flushed — which depends on write rate, block size and the
sealing trigger (§15), none of which is decided.

**What bounds it.** ~~Whatever closes this probably needs both, and the pair is a
decision rather than two constants.~~ **Answered by ADR-041**
(`docs/adr/ADR-041-unflushed-exposure.md`): `commit.Bound` requires BOTH halves
and refuses either alone with `ErrIncompleteBound`.

★ And the reason is exactly the one below, made precise: size-only leaves a QUIET
tenant unbounded in TIME, because its single committed entry never reaches the
size; age-only leaves a BUSY one unbounded in BYTES, because an arbitrary volume
fits inside the interval. Neither alone bounds anything. ⚠ This deliberately
differs from ADR-028's sealing policy, which requires at least one — a sealing
policy with one bound still seals.

**What reports it.** ~~ADR-020's `Pending` counts entries written and not yet
committed. Nothing counts entries COMMITTED and not yet flushed.~~ **Answered:**
`commit.Meter` does, and the record keeps the two windows apart — `Pending` is
data nobody was promised, and the exposure is a promise that could still be
broken.

⚠ The trap is stating the window as an average. The number that matters is the
worst case at the moment the power goes, which correlates with load — so the
exposure is largest exactly when a correlated failure is most likely, and an
average hides that completely.

★ **Answered, and the instantaneous reading turned out to have the same defect
one step removed:** asked after a burst it reports the calm. `Meter.Peak` reports
what the window REACHED, alongside `Current`.

⚠ **And a mutant found that the first design made that peak VACUOUS.** Tracking
the window as a running total that only a full flush could reset means it never
falls, so the peak and the present value are the same number by construction — a
safeguard that cannot fail because it cannot differ from what it guards. The fix
was to the design: a flush is PARTIAL, entries committed while it runs survive it,
and the peak resets only when the window EMPTIES.

⚠ **Still open: nothing flushes** (§12), so the window's closing edge is supplied
by a caller that does not exist. And the peak lives in memory, so it dies with the
process — which is the very event it exists to describe. Exporting it is §21's
half.

⚠ **Also still open: the two bound VALUES.** ADR-041 requires both and
deliberately invents neither; what they should be is a property of a deployment's
hardware and its tolerance for loss.

### §24 — A block cache exists; when to prefetch, and a better policy, are open

**Source:** ADR-018 (`docs/adr/ADR-018-read-ahead.md`), Out of Scope; and both its
task files.

ADR-018 decides WHICH fragments a read should ask for and whether it may. Three
things past that are open, and they interact.

**The cache.** ~~Fetched blocks have to live somewhere and be found again.~~
**Answered by ADR-037** (`docs/adr/ADR-037-block-cache.md`): `prefetch.Cache`,
bounded in BYTES. ⚠ The constraint below survives and is now the record's
falsifier: a read must still work with every prefetched block evicted, and
`TestEvictingEverythingChangesNoAnswer` empties the cache before every read of a
sequence and requires the same answers. If it stops working, the prefetch has
become load-bearing and the failure appears only under memory pressure — which is
precisely when eviction happens, so the bug and its trigger arrive together.

**Eviction.** ~~Least-recently-used is the obvious answer and it is wrong for a
sequential scan, which evicts exactly what it is about to read. A scan-resistant
policy is a real decision and it needs a workload to choose against.~~
**Half-answered by ADR-037, and the half that was wrong is worth naming.**
Choosing among ARC, 2Q and CLOCK-Pro does need a workload. The ordering underneath
them does not: ★ a prefetched block is a GUESS and a demanded block is EVIDENCE,
and evicting evidence to keep guesses is wrong on every workload. ADR-037 evicts
speculative entries first, least-recently-used within each class, and PROMOTES a
speculative entry that is read.

⚠ **Still open:** everything the ordering does not cover. Two speculative entries
are ranked only by recency, so a scan still evicts its own useful guesses, and
nothing tells "read once" from "read repeatedly". That is what a workload would
let us fix, and it is where ARC would earn its complexity.

**When to prefetch at all.** Prefetching a random-access read is pure waste: it
pulls `k` fragments per block for blocks nobody will ask for, and it spends the
read budget doing it. Detecting sequentiality is the usual answer and it is a
heuristic — so it will be wrong sometimes, and what it costs when wrong is the
part worth deciding rather than the detection itself.

★ **The cost is now decided, and the detection is still open.** ADR-037 rule 6
bounds what a wrong guess may cost: bandwidth already counted against the read
budget (ADR-018 rule 7), plus ZERO demanded evictions — a bad prefetch cannot take
another reader's working set. So the deferred detection is a deferred
optimisation rather than a deferred risk.

**And the balance tension ADR-018 recorded.** Choosing the nearest `k` is right
for latency and wrong for load distribution: every reader near a node picks the
same node. Spreading instead would raise latency for everyone. Neither is
obviously right, and the answer probably depends on whether the cluster is closer
to its bandwidth ceiling (§22) or to its latency target.

### §25 — Nothing serves the agent surface, rate-limits it, or reports what it did

**Source:** ADR-013 (`docs/adr/ADR-013-agent-tool-surface.md`), Out of Scope; and
`ADR-013-agent-tool-surface/tasks/T2-serve-over-mcp.md`.

ADR-013 decides what an agent may ask and what the asking MEANS. Everything
between that and an agent actually getting an answer is open.

**The server.** `github.com/modelcontextprotocol/go-sdk` is not a dependency yet,
deliberately: nothing in the implemented half imports it, which is what lets the
meaning of the surface be proved with no transport at all. Adding it is T2's first
step, and the version must be pinned exactly — ⚠ a protocol SDK on a floating
version changes the wire shape between builds, and the symptom is a client that
lists no tools with nothing logged anywhere.

**Serving results at all.** The server cannot answer until a statement can be
evaluated (§20, itself on a storage engine, §12). ⚠ There is no honest partial
step here. A server returning plausible rows from a stub is a worse artifact than
no server, because neither a caller nor a test can tell the difference.

**Rate limiting.** An agent is a new kind of caller: it can issue a read per token
it generates, and it does not get tired. ADR-015's read budget already exists and
already sheds, so the open question is not a new mechanism but which budget an
agent's calls count against — ⚠ if they are excluded as "not user traffic", a
model in a loop saturates the link while the node sheds the queries people are
waiting on.

**Observability.** ADR-012's vocabulary is CLOSED, so a per-call event needs its
kind declared there first, with its reader named and the operator question it
settles. The question worth answering is which tool an agent called and what it
was refused for — a refusal an operator cannot see is a loop nobody can diagnose.

**And the thing that has to be measured rather than reasoned about.** Rule 7 of
ADR-013 says a tool's description is the only documentation its caller will ever
have, and every refusal is written into it on that basis. Whether a model actually
calls these tools correctly from those descriptions alone is unknown, and the
failure mode is a caller that never reports being confused — it just calls the
wrong thing, or gives up. That needs a real model against a real corpus, and until
it exists the description rules are a reasoned guess.

### §26 — Nothing mounts the projection, lists an entity, or serves a byte of it

**Source:** ADR-014 (`docs/adr/ADR-014-filesystem-projection.md`), Out of Scope;
and `ADR-014-filesystem-projection/tasks/T2-mount.md`.

ADR-014 decides what a path MEANS, what it refuses, and what `stat` says about
time. Three things stand between that and a working mount.

**A FUSE library.** None is chosen. ⚠ This is not a dependency bump: the library's
supported platforms become the mount's supported platforms, and that belongs in
the record for whoever chooses. The binding also has to be checked against rule 3
— several report a read-only filesystem at the WRITE callback by default, which
would undo "refused at open" without changing a line of the grammar, and the
symptom is a program that buffers happily and loses its data at `close(2)`.

**Enumeration, which the language does not have.** The query language reads a
NAMED entity; it cannot list them. So `/e` has no query behind it and the root
directory is not implementable. ⚠ A mount that answered by inventing entries would
be indistinguishable from a real listing to every caller — including a backup that
would then record the invention as truth. Enumeration is §20's to add, and it is a
real language decision rather than a gap: an unbounded listing over a planetary
key space is not a thing a directory read can return.

**Serving bytes at all**, which needs the evaluator (§20) and a storage engine
(§12).

**And the measurement rule 4 rests on.** ADR-014 says `mtime` is the datom's
transaction time so that incremental tools work. That is reasoned, not observed.
The check is two `rsync -n` passes with no writes between them, confirming the
second copies nothing — the failure it guards against is invisible except at
scale, where it makes every backup a full one.

**One thing already decided and worth not re-opening:** an entity whose name
begins with a dot is an ORDINARY entity. Nothing under `/e` is interpreted, which
is what makes a control file impossible there — and refusing such a name would
make that entity unreachable through the mount, which is a worse failure than a
name `ls` hides by convention.

### §27 — The index is built and its rebuild is proven; ranking is open

**Source:** ADR-021 (`docs/adr/ADR-021-search-and-facets.md`), Out of Scope; and
`ADR-021-search-and-facets/tasks/T2-index-and-grammar.md`.

⚠ **THE HEADING WAS STALE AND IS CORRECTED.** It read *"Nothing builds a search
index, ranks a result, or decides what is indexed"*. Three of those four are done.

**The index itself.** ~~A read model over the datom log, fed by ADR-010's
subscription. It needs the storage engine (§12) before there is a log to
project.~~ **Built:** `search.Index` holds sealed postings, `search.Builder` is a
`subscribe.Sink` fed by ADR-010's subscription with a high-water mark for
idempotence, and `search.TermsOf` is the ONE place that decides what is indexed —
so the write path and a rebuild cannot disagree about it.

★ **And the constraint this section said was "worth nothing unproven" IS proven:**
`TestRebuildFromTheLogMatchesIncremental` builds an index incrementally, rebuilds
it by walking the log, and requires the same answers. That property is what makes
losing the index a performance event rather than a data-loss event.

⚠ Note what "reproduce it exactly" has to mean, because the obvious reading is
wrong: a posting is SEALED under its subject's key, and sealing is nondeterministic
by design — a deterministic seal would make two identical postings byte-identical
and therefore linkable. So the rebuilt index cannot be compared byte-for-byte, and
the property is that it ANSWERS the same.

**Confirming candidates against the datoms** (§20). ~~This is the rule that decays
quietest.~~ **Built:** `search.Confirm` re-checks every candidate against the
datoms at the snapshot, reducing through `ports.Carried` so a RETRACTED attribute
cannot confirm a fact that was withdrawn. ⚠ That retraction bug was real and a
mutant found it — the first version scanned raw visible datoms.

**Ranking.** Still open, and still for the reason given: it cannot be chosen
without a corpus to choose it against, and the choice must record which corpus and
on what date. `search.Rank` scores by IDF today, which is a placeholder rather
than a decision. ⚠ It also interacts with ADR-021's central cost: every candidate
costs a decrypt, so a ranker that needs to score thousands of them may be
unaffordable at that price. Measure the decrypt cost first.

**The `SEARCH` grammar.** ~~When it lands it must appear in
`docs/QUERY-LANGUAGE.md`.~~ **Landed**, and it is documented there — the
documentation-coverage gate would fail otherwise, which is the gate working.

**Analysis: stemming, stop words, language detection.** The analyzer is
deliberately the simplest testable thing — lower-case and split. Anything more
bakes in a language, and choosing one without a corpus is a preference.

**Which attributes are indexed, and who decides.** ⚠ This is where the residual
disclosure lives. ADR-021 confines the leak from the SUBJECT to the TERM, and
does not remove it: a dictionary is shared, and a sufficiently rare term
approximates an identifier. Not indexing high-cardinality identifiers is advice
today; it should become a rule with a named owner, because "do not index the
sensitive fields" as a policy fails silently and is discovered when the wrong
thing turns up in an index.

**And the cost nobody has measured.** A sealed posting cannot be scanned as
cheaply as a plaintext one. That is the price of erasure reaching the index, it
is accepted deliberately, and it is entirely unquantified.

### §28 — Writes reach a disk; routing and replication are open

**Source:** ADR-022 (`docs/adr/ADR-022-write-statements.md`), Out of Scope; and
both its task files.

`ASSERT` and `RETRACT` parse and run, and `cmd/sdev1-ql` shows the whole loop
working. Everything after that is open.

**Durability** (§12). ~~The session holds datoms in a map and loses them on exit.~~
**Answered by ADR-026 and ADR-027**: a session opened with a `*leafstore.Store`
records every datom through it, rehydrates on open, and `cmd/sdev1-ql --dir`
writes to a leaf on a disk that outlives the process. A session opened WITHOUT one
still holds datoms in memory, which is what the tests use.

⚠ **Still open:** everything past one leaf. Nothing routes a write to the leaf
that should hold it, and nothing replicates one — §18 and §19.

**Several attributes in one statement.** `ASSERT planet-7 mass = 1, radius = 2`
does not parse. It stays inside ADR-003's one-entity boundary and is purely a
grammar question, deferred only to keep ADR-022 about time rather than syntax.

**A write tool on the agent surface** (§25). ADR-013 already refuses `update` and
`delete` at registration; now there is finally something for that refusal to
point at, and the tool it should offer instead exists.

⚠ **And the constraint that must survive contact with the real engine:** the
session must not become the specification. It builds only on packages the records
govern and adds no rule of its own, so the engine has to agree with the RECORDS.
The failure here is slow and looks like progress — somebody adds a convenience to
the session, the engine copies it, and a rule nobody wrote down is what runs.

### §29 — Links are written, walked and read inbound; the depth default is open

**Source:** ADR-023 (`docs/adr/ADR-023-links-and-traversal.md`), Out of Scope; and
`ADR-023-links-and-traversal/tasks/T2-links-in-the-language.md`.

A reference is a typed value and `link.Walk` resolves one correctly. Nothing lets
a caller say either in the language.

**A reference literal.** `ASSERT` needs a way to state that a value IS a link,
distinct from a quoted string. ⚠ Whatever form is chosen must be unambiguous for
arbitrary content — a marker character makes any value legitimately starting with
it into an accidental edge, which is the same class of mistake as inferring from
shape.

**A traversal statement.** `TRAVERSE … DEPTH n` with ONE time clause for the
whole walk. ⚠ There must be no per-hop qualifier, and this is the one to hold the
line on: a shape query has a per-leg clause and it is genuinely right there, so
the symmetry is tempting. Here it would let a caller ASK for a tree assembled from
several instants — turning ADR-023's central defect from something implementable
into something documented.

**Inbound edges.** ~~"What points at this" is a different index, not a different
walk, and it interacts with §27's index work rather than with the traversal.~~
**Answered by ADR-035** (`docs/adr/ADR-035-inbound-read.md`): `READ ->a FROM [e]`
reads the entities that point at `e`, with `WHERE`, `LIMIT` and `OFFSET`.

★ The framing above was right and its conclusion was half wrong. It IS a
different index — but the MEANING does not wait for one. ADR-035 defines
membership as a property of the datoms and confirms every candidate against them,
so a scan is a correct implementation and the index §27 will build is an
optimisation that cannot change an answer.

⚠ **What is still open here** is the reach, not the meaning: a referrer is a
separate entity and lands on its own leaf, so a cluster-wide inbound read needs
the routing §18 defers. `leafstore.Store.Referrers` answers for ONE leaf.

**And what T1 could not prove.** The walk takes a `Resolver` and stores nothing,
so nothing yet demonstrates that a real storage layer passes ONE snapshot rather
than taking a fresh read per hop. The rule is easy to hold in a pure function and
easy to lose behind a cache (§12).

**A depth default, if the language should have one.** ADR-023 requires a bound
and deliberately does not say what a sensible one is. Choosing without a real
hierarchy to measure would be a constant nobody wrote down.

## Closed

Entries move here when the deferral is honoured, naming the record that closed it.

None yet.
