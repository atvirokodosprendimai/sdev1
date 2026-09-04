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

The repository is greenfield. At commit `2db614d` it held `README.md`, `LICENSE`
and `.gitignore` and no Go source. Every record below therefore describes intent
rather than code that exists, and `Governs:` paths will resolve to nothing until
the corresponding tasks land. That is expected at this stage and is not drift.

## The records

Ordered by dependency rather than by number where the two differ. ADR-001 through
ADR-004 are foundational: each decides something that cannot be changed once data
exists, because the choice is encoded in the data itself.

| ADR | Decision | Status | Why it cannot wait |
|-----|----------|--------|--------------------|
| [ADR-001](ADR-001-address-space.md) | The key space is a 256-way trie of static fan-out and dynamic depth | Proposed | Fixing 256 as a shard *count* caps the system at 256 machines. The addressing model is encoded in every key. |
| ADR-002 | Transaction identity is a hybrid-logical-clock triple; the two temporal axes are queried independently | Planned | `T` is written into every datom. A per-shard counter makes cross-shard time travel unimplementable, and that is discovered only after data exists. |
| ADR-003 | Consistency is linearizable per entity and snapshot-isolated across entities; the entity is the transaction boundary | Planned | Determines whether distributed commit is needed at all. Retrofitting cross-entity transactions is a rewrite. |
| ADR-004 | Durability is a per-tier policy over declared failure domains | Planned | Replication factor, erasure scheme and failure domain are written into segment headers. |
| ADR-005 | The binary segment format | Planned | Every stored byte. Versioned from the first release. |
| ADR-006 | Erasure coding applies to sealed segments only; the scheme lives in the segment header | Planned | A scheme held only in configuration renders existing stripes unreadable when it changes. |
| ADR-007 | Physical erasure is crypto-shredding, not log rewriting | Planned | Requires per-entity key separation in the format from the first version. |
| ADR-008 | Placement is deterministic and computed by the client from a topology map | Planned | Decides whether the placement service sits in the write path. |
| ADR-009 | Replication and leader election are multi-raft with coalesced heartbeats and fencing epochs | Planned | Fencing epochs are written into the log. |
| ADR-010 | Retention, backup and purge are one fan-out with per-sink acknowledgement | Planned | A sink that is silently not wired lets a restore resurrect purged data. |
| ADR-011 | One query language, combining a SQL-shaped relational surface, Oracle-style flashback clauses, and a graph traversal operator | Planned | The public contract. Time is a clause that composes with every query rather than a family of temporal verbs — that orthogonality is what keeps a combined language small. It must implement ADR-002's defaults table exactly, since its time qualifiers are where the two-axis defect recurs. |
| ADR-012 | Observability is a cluster-wide event stream with a server-rendered console | Planned | Determines what every component is obliged to emit. |
| ADR-013 | An MCP server exposing the engine as a knowledge graph for agents, built on the official `modelcontextprotocol/go-sdk` | Planned | A public contract whose tool descriptions are the only documentation its callers read. It exposes ADR-011's evaluator rather than becoming a second query surface. The SDK choice overrides the team's `effective-go` convention, on M's instruction of 2026-09-04. |
| ADR-014 | A FUSE filesystem projection of the store | Planned | A public contract. Bitemporal storage makes it a natively snapshotting filesystem — an entity is a directory, its attributes are files, and a time qualifier is a snapshot path — and per-attribute history falls out of EAVT rather than being built. Like ADR-013 it is a projection over ADR-011's evaluator, not a further query surface. |

Records ADR-002 onward are named here so the shape of the corpus is visible, and
so a session picking one up knows what its neighbours already claim. Naming them
is not accepting them; only a record with `Status: Accepted` governs anything.

## Conventions

- **Vocabulary.** This project describes itself at planetary scale, over the level
  labels `universe, planet, datacenter, rack, server, disk`. Records, documentation, README text and
  code comments use that vocabulary and no other. Analogies used while designing
  do not enter written artifacts.
- **Numbers carry a date and a subject.** Write "measured 2026-09-04 against a
  single-node checkout", never a bare figure. An undated measurement is
  unfalsifiable the moment the data changes.
- **A threshold is valid for a configuration, never in the abstract.** Say which
  cluster shape, corpus or traffic a criterion holds for.
