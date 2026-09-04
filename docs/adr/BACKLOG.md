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

### §15 — Nothing decides when the live tail is sealed, or how an index over it is published

**Source:** ADR-017 (`docs/adr/ADR-017-lock-free-read-path.md`), Out of Scope; and
both its task files.

ADR-017 makes the live tail readable without locks and ADR-005 makes a sealed
segment immutable. Neither says how one becomes the other, and two open
questions sit in that gap.

**When sealing happens.** A size threshold, an age, a transaction count, or an
operator's instruction — each gives a different tail length, and the tail's
length is what a reader walks. Sealing too rarely makes reads linear in ingest
rate; too often makes tiny segments and pays ADR-006's per-stripe overhead on
almost nothing. It also interacts with ADR-004's tiers: sealing is the moment a
leaf's data moves from a replicated policy to a coded one, so it is a durability
transition and not only a layout one.

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

### §17 — The keystore has no home, no rotation and no caching story

**Source:** ADR-007 (`docs/adr/ADR-007-crypto-shredding.md`), Out of Scope and
its Follow-ups; and both its task files.

ADR-007 makes erasure the destruction of a per-subject key and puts that key in a
mutable keystore, deliberately separate from the immutable ciphertext. It does
not say where the keystore actually lives, and three questions follow.

**Persistence.** The shipped implementation is `MemoryKeystore`, and its name is
the warning: every key is lost on restart, which erases everything. That is safe
in the wrong direction and unusable in production. Whatever replaces it inherits
an unusual requirement — it must be genuinely DELETABLE, since a store that only
tombstones has not destroyed anything, and a log-structured store that keeps old
versions would quietly defeat the whole record.

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

### §18 — There is no transport, and nothing distributes a route

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

### §20 — Nothing evaluates a query, plans one, or computes a similarity

**Source:** ADR-011 (`docs/adr/ADR-011-query-language.md`), Out of Scope; and both
its task files.

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

### §21 — Nothing exports, samples, retains or watches the event stream

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

**Watching, which is the one that matters.** ADR-010 leaves a purge INCOMPLETE
when a sink has not acknowledged, and deliberately does not escalate — it defers
that here. ADR-012 can now EXPRESS it as a declared event with a named reader.
Nothing yet looks.

⚠ That is exactly the failure ADR-012 is about, one level up: a declared thing
whose reader exists on paper and never runs. Whatever closes this must make the
watching real rather than declaring a watcher, and the honest test is whether an
incomplete purge from a month ago would actually reach a person.

### §22 — Nothing decides what happens when every replica sheds, or which reads matter more

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

**Which reads matter more.** A repair read and a user query compete for the same
budget, and shedding treats them alike. That is wrong in both directions: shedding
the repair prolongs the degradation that is causing the load, and shedding the
user query is what the mechanism is for. ⚠ And §16 is the reason this is not
merely a preference — a degraded read costs `k` fragment fetches, so the reads a
repair is trying to make unnecessary are also the expensive ones.

Whatever closes this must not grow a second budget dimension per class; ADR-015
refuses a third budget kind deliberately, and priority within a budget is a
different mechanism from a budget per priority.

### §23 — Nothing bounds how long acknowledged data stays unflushed

**Source:** ADR-020 (`docs/adr/ADR-020-commit-point.md`), Out of Scope and its
Consequences; and both its task files.

ADR-020 acknowledges a write once N memory replicas in distinct power domains
hold it, and flushes afterwards. That is deliberate and it is the whole
performance argument. It leaves an exposure window nothing measures or bounds.

**How long.** Between acknowledgement and flush, the data survives independent
failures and not correlated ones. The size of that window is how long a block
takes to fill and be flushed — which depends on write rate, block size and the
sealing trigger (§15), none of which is decided.

**What bounds it.** A time bound ("flush at least every N seconds") and a size
bound ("flush at least every N bytes") answer different questions, and a quiet
tenant needs the first while a busy one needs the second. Whatever closes this
probably needs both, and the pair is a decision rather than two constants.

**What reports it.** ADR-020's `Pending` counts entries written and not yet
committed. Nothing counts entries COMMITTED and not yet flushed, which is the
actual exposure — and that number is what an operator wants during a power
event, not afterwards.

⚠ The trap is stating the window as an average. The number that matters is the
worst case at the moment the power goes, which correlates with load — so the
exposure is largest exactly when a correlated failure is most likely, and an
average hides that completely.

### §24 — Nothing caches a prefetched block, evicts one, or decides when to prefetch at all

**Source:** ADR-018 (`docs/adr/ADR-018-read-ahead.md`), Out of Scope; and both its
task files.

ADR-018 decides WHICH fragments a read should ask for and whether it may. Three
things past that are open, and they interact.

**The cache.** Fetched blocks have to live somewhere and be found again. ⚠ The
constraint ADR-018 imposes must survive: a read must still work with every
prefetched block evicted. If it stops working, the prefetch has become
load-bearing and the failure appears only under memory pressure — which is
precisely when eviction happens, so the bug and its trigger arrive together.

**Eviction.** Least-recently-used is the obvious answer and it is wrong for a
sequential scan, which evicts exactly what it is about to read. A scan-resistant
policy is a real decision and it needs a workload to choose against.

**When to prefetch at all.** Prefetching a random-access read is pure waste: it
pulls `k` fragments per block for blocks nobody will ask for, and it spends the
read budget doing it. Detecting sequentiality is the usual answer and it is a
heuristic — so it will be wrong sometimes, and what it costs when wrong is the
part worth deciding rather than the detection itself.

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

## Closed

Entries move here when the deferral is honoured, naming the record that closed it.

None yet.
