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

### §4 — Clock skew between nodes is unbounded and unpoliced

**Source:** ADR-002 (`docs/adr/ADR-002-transaction-identity.md`), Out of Scope.

A hybrid logical clock never moves backwards, but it does move *forward* to match
the fastest clock it hears from. A node whose wall clock jumps hours ahead drags
every timestamp it touches with it, permanently — the cluster cannot come back,
because monotonicity is the property that forbids it.

Nothing currently bounds this. What a decision here must answer: the maximum skew
a node may exhibit before its messages are rejected, how that skew is measured
without trusting the misbehaving node's own reading, and whether a node past the
bound is refused, evicted, or merely alarmed.

Until it is decided, the cluster's timestamp quality is set by its worst clock.

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

### §6 — The topology map is not versioned, so historical placement is unresolvable

**Source:** ADR-001 T3 (`docs/adr/ADR-001-address-space/tasks/T3-placement.md`),
Stop Condition. Raised 2026-09-04.

Placement is currently a function of `(leaf, current map)`. But a segment written
a year ago was placed under *that year's* map, and finding it requires resolving
against the map as it stood then — so placement is really a function of
`(leaf, map version)`, and a segment header must record the version it was placed
under.

This changes `placement.Resolve`'s signature, so it must be settled **before**
callers exist rather than after. It also makes the topology map itself a
time-varying record, which means ADR-002's ordering machinery is what versions it.

What a decision here must answer: whether map versions are ordered by `TxID`, how
an old map is retained and for how long, and what happens to a segment whose
placement map has been retired.

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

### §8 — No real domain has been tested against the one-entity transaction boundary

**Source:** ADR-003 (`docs/adr/ADR-003-transaction-boundary.md`), Out of Scope
and its stated falsifier.

ADR-003 confines a transaction to one entity, and that single constraint is what
removes the need for distributed commit from the entire system. Its falsifier is
correspondingly large: the decision fails if a legitimate domain operation cannot
be expressed within one entity.

Nothing has tested it. The refusal is implemented and proven to fire, but no real
domain has been modelled against it, so the question of whether the boundary is
liveable is open rather than answered.

What a decision here must answer: model at least one non-trivial domain against
the boundary, and for any operation that resists it, decide between expressing it
as several transactions plus a compensating one, widening the boundary (which
pulls in distributed commit and reopens ADR-003's central choice), or declaring
the operation out of scope for this engine.

⚠ Widening later is additive and therefore cheap; narrowing later is not. The
cost of leaving this open is bounded, and the cost of guessing wrong in the
permissive direction is not.

### §10 — Nothing decides what a cluster does with leaves below the durability floor

**Source:** ADR-004 (`docs/adr/ADR-004-durability-policy.md`), Out of Scope.

ADR-004 refuses writes to a leaf holding fewer than `MinSize` durable copies.
It does not decide what happens next, and "the write is refused" is only half an
answer: the leaf is still readable, still degraded, and still degrading.

What a decision here must answer: whether the cluster re-replicates
automatically or waits for an operator, how a leaf below the floor is surfaced
(an alarm, a status, a refusal message naming the shortfall), whether such a leaf
is evicted from the read path, and how an operator distinguishes "briefly
degraded during a restart" from "genuinely short of copies", since the two look
identical for the first few seconds and demand opposite responses.

### §11 — Tenant identifiers have no allocation, reuse or authorization story

**Source:** ADR-016 (`docs/adr/ADR-016-tenant-prefix.md`), Out of Scope and its
Follow-up.

ADR-016 makes a tenant the leading bytes of a key and therefore a contiguous
subtree. It does not decide who assigns those bytes.

Two things are genuinely open. **Allocation and reuse:** a reused identifier
inherits the previous tenant's subtree, including anything marked but not yet
swept and anything still present in a coded stripe — so reuse is a data-exposure
question rather than a bookkeeping one, and the safe answer may be that
identifiers are never reused. **Authorization:** a tenant boundary is usually
wanted in order to enforce something, and nothing here enforces anything yet.

⚠ One design constraint is already known and should survive into whatever record
closes this: a query `AS OF` a past instant must be authorized against the
CURRENT grant set, never the grants in force at that instant. The symmetry is
tempting — the data is historical, so why not the permissions — and it is a leak:
revoking access today would otherwise leave the revoked party able to read last
year. Grants are naturally datoms in a reserved system tenant, which makes "who
had access at time T" answerable and makes revocation a retraction.

### §12 — Nothing writes a segment to a disk, or finds a block inside one

**Source:** ADR-005 (`docs/adr/ADR-005-segment-format.md`), Out of Scope; and
both its task files, whose Out of Scope defers anything that opens a file here.

`internal/core/segment` decides what a block IS and refuses to touch a
filesystem. That was deliberate — the format has to be right before any byte
reaches a disk, and keeping the package to byte slices is what makes every
property testable with no storage engine. It leaves three things undecided.

**The writer.** Blocks must be packed into a segment, the segment made durable,
and the moment it becomes readable defined. A segment is immutable once sealed,
so "sealed" is a state transition somebody has to own, and ADR-004 already
attaches a different durability policy to each side of it.

**The block index.** A segment header records how many blocks it holds and each
block header is fixed-width, so a reader can stride — but striding a segment to
find one subject is a linear scan. What maps a subject to a block offset, where
that map lives, and whether it is in the segment or beside it, are open. This is
the question the layered-index discussion was circling and it should be answered
with it rather than separately.

**The on-disk layout.** Roughly four megabytes per block and a nested directory
path were both discussed as the shape. Neither is decided, and the reclaim
argument — many small units droppable whole rather than one file to rewrite —
constrains it more than the numbers do.

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

### §13 — It is undecided whether one compression block may mix subjects

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

The answer is likely "a block holds one subject's datoms, and a segment holds
many blocks", paying compression ratio for the other three. It is written down
rather than assumed because the format permits either and the cost of learning
it late is a rewrite of every stored byte.

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

## Closed

Entries move here when the deferral is honoured, naming the record that closed it.

None yet.
