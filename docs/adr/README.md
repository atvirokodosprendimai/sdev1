# sdev1 — Decision Records

sdev1 is a planetary-scale structural data engine written in Go. Facts are stored
as EAVT datoms on two independent time axes, written through a CQRS command path
onto an append-only event log, and served from read models with their own shapes.
The key space is addressed as a trie of 256-way fan-out. Storage is a binary
segment format; sealed segments are erasure-coded; live segments are replicated
whole under per-shard leader election.

This directory is the authoritative record of the decisions that shape it. A
decision that is not written here is not a decision; it is a thing somebody
happened to do.

## How to read this corpus

- Every record is a single decision with its rejected alternatives. The rejected
  alternative is load-bearing: without it a decision reads as arbitrary
  preference and the next reader reopens it.
- `Status` is one of `Proposed`, `Accepted`, `Superseded by ADR-NNN`, `Withdrawn`.
  Only `Accepted` records govern.
- `Governs:` names the paths a record is authoritative over. Run
  `adr-context <path>` to find which decisions own the file you are about to edit,
  and — equally important — which approaches for that file were already killed.
- Implementation lives in `ADR-NNN-<slug>/tasks/`. Task files are the source of
  truth; each `tasks/README.md` is a derived index.
- `BACKLOG.md` carries deferred work. Every `(deferred: …)` disposition in a
  record has a matching entry there, written in the same commit.

## Status of the corpus

Nine records are **Accepted and implemented**: ADR-001 through ADR-006, ADR-016,
ADR-017 and ADR-019 — the last with one of its two tasks done, which its task
index states rather than implies. Their `Governs:` paths resolve, their tasks carry tool-written
acceptance and mutation evidence, and the packages they name exist and pass
under the race detector. The remaining records describe intent, and their
`Governs:` paths will resolve to nothing until their tasks land — which is
expected at this stage rather than drift.

## The records

Ordered by dependency rather than by number where the two differ. ADR-001 through
ADR-004 are foundational: each decides something that cannot be changed once data
exists, because the choice is encoded in the data itself.

| ADR | Decision | Status | Why it cannot wait |
|-----|----------|--------|--------------------|
| [ADR-001](ADR-001-address-space.md) | The key space is a 256-way trie of static fan-out and dynamic depth | **Accepted** | Fixing 256 as a shard *count* caps the system at 256 machines. The addressing model is encoded in every key. |
| [ADR-002](ADR-002-transaction-identity.md) | Transaction identity is a hybrid-logical-clock triple; the two temporal axes are queried independently | **Accepted** | `T` is written into every datom. A per-shard counter makes cross-shard time travel unimplementable, and that is discovered only after data exists. |
| [ADR-003](ADR-003-transaction-boundary.md) | Consistency is linearizable per entity and snapshot-isolated across entities; the entity is the transaction boundary | **Accepted** | Determines whether distributed commit is needed at all. Retrofitting cross-entity transactions is a rewrite. |
| [ADR-004](ADR-004-durability-policy.md) | Durability is a per-tier policy over a declared failure domain, with a refusal floor | **Accepted** | Two knobs rather than one: a target copy count, and a floor below which writes are REFUSED — so a degraded cluster stops accepting data instead of taking it at a durability nobody has. The floor is at least 2 and cannot be constructed lower. ⚠Two is a DURABILITY floor and not a CONSENSUS floor: two voting members give a quorum of two, so a bare pair is less available than a single node, and the minimum viable live shape is two data replicas plus a witness. A policy the topology could never satisfy is refused at load rather than discovered during a repair. |
| [ADR-005](ADR-005-segment-format.md) | The binary segment format: per-block codec, checksum, and the compress → encrypt → erasure-code order | **Accepted** | Every stored byte, versioned from the first release. The block header records its own codec and cipher, because a block is only readable by something that knows how it was written — configuration alone would make a settings change reinterpret existing data. The pipeline order is fixed rather than chosen: encrypting first destroys compressibility, coding first compresses parity. Also owns whether one compression block may mix subjects, since compressing before encrypting leaks through size. |
| [ADR-006](ADR-006-erasure-coding.md) | Erasure-code a block into a stripe that records its own scheme, and checksum every fragment | **Accepted** | Sealed data survives `m` fragment losses at `(k+m)/k` storage cost instead of 3×. The scheme travels with the stripe, so changing the configured one is safe at any time — a scheme held only in configuration would render every existing stripe unreadable the moment it changed. ⚠A code with `m` parity fragments corrects `m` fragments known to be MISSING but only `⌊m/2⌋` that are present and WRONG, because locating a fault costs as much redundancy as repairing it. Every fragment therefore carries a checksum and is verified before decoding, which turns every error into an erasure and removes the case where reconstruction returns wrong bytes reporting success. |
| ADR-007 | Physical erasure is crypto-shredding, not log rewriting | Planned | Requires per-entity key separation in the format from the first version. |
| ADR-008 | Routing and discovery: aggregated prefix routes through frontdoor and border nodes, not a full map in every client | Planned | Decides how much a client must know to reach a key, and keeps a metadata service off the request path without making every client hold the cluster. A trie prefix is a routable prefix, so a border node advertises a whole subtree as one route and its table is bounded by fan-out and live depth rather than by data volume. Also owns the redirect that makes a stale route cache self-healing rather than a correctness bug. |
| ADR-009 | Replication and leader election are multi-raft with coalesced heartbeats and fencing epochs | Planned | Fencing epochs are written into the log. |
| ADR-010 | Subscription, retention and erasure: mark, shred and sweep at three timescales | Planned | A subscription is one primitive with three consumers — streaming backup, read models, and the console. Purge is a fan-out with per-sink acknowledgement, not a delete: a sink silently unwired lets a restore resurrect what an operator believes is gone. ⚠Mark, shred and sweep are three mechanisms and not synonyms: marking makes a subject invisible immediately, shredding makes it unreadable everywhere by destroying a key, and sweeping reclaims space eventually and is bounded by the retention horizon. Only shredding is erasure; a sweep reaches neither backups nor coded stripes. |
| ADR-011 | One query language: a SQL-shaped relational surface, flashback time clauses, a graph traversal operator, shape matching with required and optional legs, and storage-policy clauses | Planned | The public contract. Time is a clause composing with every term rather than a family of temporal verbs, which keeps a combined language small and makes a per-leg time qualifier free. Storage policy (compression codec, and later the coding scheme) is likewise a clause on a write, setting the policy for NEW data only — every block records how it was written. Three things must be pinned or the language is untestable: it implements ADR-002's defaults table exactly; shape similarity is a stated metric with a stated threshold; and an optional leg matching nothing yields an unbound value rather than dropping the row. |
| ADR-012 | Observability is a cluster-wide event stream with a server-rendered console | Planned | Determines what every component is obliged to emit. |
| ADR-013 | An MCP server exposing the engine as a knowledge graph for agents, built on the official `modelcontextprotocol/go-sdk` | Planned | A public contract whose tool descriptions are the only documentation its callers read. It exposes ADR-011's evaluator rather than becoming a second query surface. The SDK choice deliberately departs from the team-wide Go convention, decided 2026-09-04. |
| ADR-014 | A FUSE filesystem projection of the store | Planned | A public contract. Bitemporal storage makes it a natively snapshotting filesystem — an entity is a directory, its attributes are files, and a time qualifier is a snapshot path — and per-attribute history falls out of EAVT rather than being built. Like ADR-013 it is a projection over ADR-011's evaluator, not a further query surface. |
| ADR-015 | Admission control: per-node counters, a declared traffic ceiling, and read shedding by subscription withdrawal | Planned | Determines what every node measures and what it does when it saturates. Shedding is subscription management rather than client routing — a loaded replica withdraws and work stops arriving. Read and write budgets are separate, because a leaf has one writer and a read burst must never be able to stop its writes. |
| [ADR-016](ADR-016-tenant-prefix.md) | The tenant is the leading bytes of the key, so a tenant owns a contiguous subtree | **Accepted** | ⚠**Amends ADR-001's key format, which was already implemented.** A tenant owning a contiguous subtree makes tenant deletion a subtree drop, data residency a placement rule, and per-tenant durability, retention, compression and shred-key namespaces all subtree policy — while a hot tenant is absorbed by the depth mechanism that already exists. Taken now because the repository holds no data: one edit today, a full re-ingest later. Identity, roles and grants remain open (`BACKLOG.md` §11), including the trap that a query `AS OF` a past instant must be authorized by TODAY's grants and never that instant's. |

| [ADR-017](ADR-017-lock-free-read-path.md) | The read path takes no locks: immutable sealed segments, a published watermark over the live tail, and snapshot reads bounded by a transaction identifier | **Accepted** | Foundational, and it constrains every data structure the storage engine may use. Readers and writers must never block each other, which append-only storage plus a transaction-bounded visibility rule already gives for sealed data — a concurrent write appends at a higher identifier and is simply not visible. The live tail is the one mutable part and is the whole decision: a reader acquire-loads a watermark the writer release-stores only after an entry is completely written, so a partially written entry is unreachable rather than guarded. Sealing publishes by swapping an immutable manifest, so nobody observes a half-sealed segment. Writer-versus-writer on one leaf stays serialized through that leaf's single writer, which is a different question and ADR-009's. |
| ADR-018 | Read-ahead is an explicit, budgeted hint that fetches the nearest `k` fragments and streams from memory | Planned |
| [ADR-019](ADR-019-chaos-and-the-failure-catalogue.md) | Inject faults from a seed, and keep a written catalogue of every failure that does not recover | **Accepted** (T1 done, T2 pending) | Every record here states how its mechanism fails and recovers, and until now none had been broken on purpose to find out. `FAILURES.md` is the deliverable: what this system does NOT survive, which is the half an operator needs and the half nobody writes down. Three dispositions and no fourth, because a fourth is how "we are looking into it" enters a catalogue and stops anything being countable — and "unrecoverable by design" is a correct answer, since losing more fragments than a stripe has parity destroys the block and a system that returned something anyway would be inventing. Every schedule is a pure function of one seed, because an unreproducible failure is a report rather than a bug. ⚠The composed-cluster half is `pending` on a node binary, and its hardest requirement is that the 8GB test budget must not MANUFACTURE findings: a container the kernel's out-of-memory killer stops looks exactly like the node crash being injected. | Determines what a sequential read of a large blob costs the cluster. Reading one block of a coded stripe otherwise means `k` fragment fetches across `k` failure domains, so a naive projection turns streaming into per-block fan-out. Three things make it a decision rather than a cache: a prefetch is a HINT and correctness never depends on it; it is BUDGETED, because "load every part into memory" is right for a 40MB blob and fatal for a 4TB one; and it fetches the nearest `k` rather than all `k+m`, hedging to further fragments only on a straggler — fetching every fragment wastes `m/k` of the bandwidth on every healthy read. It is admission-controlled by ADR-015, because one open otherwise amplifies into load on every server holding the blob. |

Records ADR-002 onward are named here so the shape of the corpus is visible, and
so a session picking one up knows what its neighbours already claim. Naming them
is not accepting them; only a record with `Status: Accepted` governs anything.

## Conventions
- **Write how it works, how it fails, and how it recovers.** A record that
  explains only the happy path has documented the easy half. Every mechanism
  states what happens when it breaks and what an operator does about it.
- **No transcripts.** Requirements are stated as requirements, in the record's
  own words. Conversations, prompts and quoted instructions do not appear in
  README text, documentation or decision records — a reader a year from now
  needs the requirement, not the exchange that produced it.
- **Describe mechanisms, do not attribute them.** Name a technique where the
  name is the thing — Raft, Reed-Solomon, a hybrid logical clock, a nested set —
  and name a system where it is a deliberate featureset target. Do not write
  "this is taken from" or "the way X does it": a reader needs to know how this
  system behaves, and a comparison tells them how a different one behaves.

- **Vocabulary.** This project describes itself at planetary scale, over the level
  labels `universe, planet, datacenter, rack, server, disk`. Records, documentation, README text and
  code comments use that vocabulary and no other. Analogies used while designing
  do not enter written artifacts.
- **Numbers carry a date and a subject.** Write "measured 2026-09-04 against a
  single-node checkout", never a bare figure. An undated measurement is
  unfalsifiable the moment the data changes.
- **A threshold is valid for a configuration, never in the abstract.** Say which
  cluster shape, corpus or traffic a criterion holds for.
