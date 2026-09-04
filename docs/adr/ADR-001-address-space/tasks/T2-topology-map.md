# Task ADR-001-T2: The topology map as a nested-set interval tree over declared level labels

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `topology.Map`, `topology.Node`, `topology.ServerID`, `topology.Levels`, `topology.Distance()`, `topology.AncestorAtLevel()`, `topology.Load()`, the versioned map file format
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the format version field`, `the rejection of an unknown version`, `the level list being data rather than Go types`, `the interval containment relation`

## Goal

Declare the cluster's containment hierarchy as an ordered list of level labels
over a **nested-set interval tree**, so that ancestry, distance and
failure-domain membership are all integer range comparisons on a flat array
rather than pointer walks.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/topology/topology.go` | add | `Map`, `Node` carrying `(Lft, Rgt, LevelIdx, Name)`, `ServerID`, the ordered `Levels` list, the live-depth field. |
| `internal/core/topology/nested.go` | add | The nested-set numbering: assign `(Lft, Rgt)` by depth-first traversal on load, and the containment predicate `contains(a, b) = a.Lft < b.Lft && b.Rgt < a.Rgt`. |
| `internal/core/topology/distance.go` | add | `Distance(a, b)` and `AncestorAtLevel(s, levelIdx)` — both interval lookups, the two primitives locality and failure domains share. |
| `internal/core/topology/load.go` | add | `Load(io.Reader) (Map, error)`: parse the authored nested JSON, validate, then number the intervals. |
| `internal/core/topology/doc.go` | add | Package comment: levels are data, the authored form is nested and the resident form is intervals, and the map never carries per-object location. |
| `internal/core/topology/topology_test.go` | add | The tests below. |
| `testdata/topology/minimal.json` | add | A six-level map, one datacenter, two racks, three servers — enough for `Distance` to distinguish and for T3's rack spreading. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestLoadRejectsUnknownVersion`, `TestLoadRejectsDepthOutOfRange`, `TestLevelsAreDataNotTypes`, `TestLoadRejectsNodeAtUndeclaredLevel`, `TestIntervalsNestStrictly`, `TestDistanceIsCommonAncestorLevel`, `TestAncestorAtLevelIsIntervalLookup`, `TestAncestorAtLevelRefusesWhenLevelIsSkipped`, `TestNestedAndIntervalFormsRoundTrip`, `TestMapDeclaresNoObjectLocations`. Run the Acceptance fence and confirm it is red. [proof: acceptance]
2. [S2] Define `Levels []string` — the ordered label list, broadest first. The default map declares `universe, planet, datacenter, rack, server, disk`, but the list is **data**: a deployment may insert `row` or `pod`, or drop `universe`, with no change to this package.
3. [S3] Define `Node{ Name string; LevelIdx int; Lft, Rgt int; Weight int }` and `Map{ Version int; Depth uint8; Levels []string; Nodes []Node }`, with `Nodes` held flat and sorted by `Lft` so it is binary-searchable and contains no pointers.
4. [S4] Implement the nested-set numbering: a depth-first traversal of the authored tree assigning `Lft` on entry and `Rgt` on exit. Containment is then `a.Lft < b.Lft && b.Rgt < a.Rgt`, with no traversal at query time.
5. [S5] Implement `AncestorAtLevel(s ServerID, levelIdx int)` as the unique node at that level whose interval contains `s`, found by binary search rather than by walking parents. This is the primitive a durability rule uses to ask "which rack is this replica in".
6. [S6] Implement `Distance(a, b)` as the `LevelIdx` of the smallest interval containing both. Smaller is nearer. Two servers in one rack are nearer than two in one datacenter.
7. [S7] Implement `Load`: parse the authored nested JSON, refuse an unknown `Version`, a `Depth` outside 1..32, a node whose level label is absent from `Levels`, and a child whose level is not strictly deeper than its parent's — each with a named sentinel rather than a default — then number the intervals.
8. [S8] Write `testdata/topology/minimal.json` in the authored nested form, declaring all six default levels with two racks.
9. [S9] Write the package comment stating that levels are data, that the authored form is nested while the resident form is intervals with one codec between them, and that the map never carries per-object location. [proof: human: a reader confirms the comment states all three bounds rather than restating the types]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/topology/... -run 'TestLoad|TestMap|TestLevels|TestDistance|TestInterval|TestAncestor|TestNested' -count=1 2>&1 | tee /tmp/adr001-t2.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr001-t2.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestLoadRejectsUnknownVersion` | `internal/core/topology/topology_test.go` | A map written by a future release is refused, not partially read | — | S7 |
| `TestLoadRejectsDepthOutOfRange` | `internal/core/topology/topology_test.go` | Depth 0 and 33 are refused rather than clamped or defaulted | — | S7 |
| `TestLevelsAreDataNotTypes` | `internal/core/topology/topology_test.go` | A map declaring level labels the package has never heard of (`universe, region, pod, host, device`) loads and resolves — fails if anyone reintroduces hardcoded level types | — | S2, S3 |
| `TestLoadRejectsNodeAtUndeclaredLevel` | `internal/core/topology/topology_test.go` | An undeclared level label, and a child not strictly deeper than its parent, are both refused | — | S7 |
| `TestIntervalsNestStrictly` | `internal/core/topology/topology_test.go` | Every child's interval is strictly inside its parent's, and sibling intervals are disjoint — the property every later range comparison rests on | — | S4 |
| `TestDistanceIsCommonAncestorLevel` | `internal/core/topology/topology_test.go` | Same-rack is nearer than same-datacenter, which is nearer than same-planet; and `Distance` is symmetric. Exercises the two-rack fixture S8 writes | — | S6, S8 |
| `TestAncestorAtLevelIsIntervalLookup` | `internal/core/topology/topology_test.go` | Two servers in one rack share an ancestor at level `rack`; two in different racks do not — the assertion ADR-004's failure domains will rest on | — | S5 |
| `TestAncestorAtLevelRefusesWhenLevelIsSkipped` | `internal/core/topology/topology_test.go` | A node with NO ancestor at the requested level is refused rather than assigned the nearest earlier one. Added 2026-09-04 after a mutation dropping the right bound from `Node.Contains` SURVIVED: every server in the fixture sits inside a rack, so nothing discriminated | — | S5 |
| `TestNestedAndIntervalFormsRoundTrip` | `internal/core/topology/topology_test.go` | Property test over generated trees: authored-nested → intervals → reconstructed-nested is the identity. The falsifier for the two-representation defect | — | S4, S7 |
| `TestMapDeclaresNoObjectLocations` | `internal/core/topology/topology_test.go` | The decoded `Map` exposes no per-key or per-object field | — | S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The nine unit tests above. |
| 2 — something selects it | T3's `Resolve` takes a `topology.Map` and its `Nearest` calls `Distance`; ADR-004's durability rule will call `AncestorAtLevel`. The mutation proving both are read belongs to T3's fence, which builds against this package. |
| 3 — the caller can discover it | The authored JSON format is the declared interface. `testdata/topology/minimal.json` plus the package comment are what an operator reads; `TestNestedAndIntervalFormsRoundTrip` is the check that the documented form and the resident form cannot drift. |
| 4 — it is used | Nothing measures this yet. |

## Mutation Log

- 2026-09-04 · 90c3039* · mutant killed · exit 1 · `internal/core/topology/load.go` · a map written by a future release must be refused whole, never partially read; accepting any non-negative version must turn TestLoadRejectsUnknownVersion red · acceptance-sha256:02a1a68440ab7e93a54b03fd0df4f48f978079b85f8d8406b3db61b499507cdd · covers:the rejection of an unknown version
- 2026-09-04 · 90c3039* · mutant killed · exit 1 · `internal/core/topology/load.go` · levels are data, so a typo in a level label is a load-time error rather than a build error; dropping the check silently places nodes at level 0 and must turn TestLoadRejectsNodeAtUndeclaredLevel red · acceptance-sha256:02a1a68440ab7e93a54b03fd0df4f48f978079b85f8d8406b3db61b499507cdd · covers:the level list being data rather than Go types
- 2026-09-04 · 90c3039* · mutant survived · exit 0 · `internal/core/topology/topology.go` · containment is what every ancestry and failure-domain answer rests on; dropping the right bound makes unrelated later subtrees look contained and must turn TestAncestorAtLevelIsIntervalLookup red · acceptance-sha256:02a1a68440ab7e93a54b03fd0df4f48f978079b85f8d8406b3db61b499507cdd · covers:the interval containment relation
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-04 · 90c3039* · mutant killed · exit 1 · `internal/core/topology/topology.go` · second attempt: the first mutant SURVIVED because every server in the fixture sits inside a rack, so the right bound never discriminated. TestAncestorAtLevelRefusesWhenLevelIsSkipped adds a server with no rack, where dropping the bound returns a wrong rack instead of an error · acceptance-sha256:02a1a68440ab7e93a54b03fd0df4f48f978079b85f8d8406b3db61b499507cdd · covers:the interval containment relation

## Invariants

- Levels are **data**. The package hardcodes no level name, so `universe, planet, datacenter, rack, server, disk` is the default map's content and not the package's vocabulary.
- Intervals nest strictly: a child is inside its parent, siblings are disjoint. Every ancestry, distance and failure-domain answer is a comparison on these integers and never a traversal.
- `Nodes` is flat, pointer-free and sorted by `Lft`, so the resident map is binary-searchable and cheap to ship or map into memory.
- There is exactly **one codec** between the authored nested form and the resident interval form, and `TestNestedAndIntervalFormsRoundTrip` is what keeps the two from silently narrowing against each other.
- The map declares cluster shape only, never the location of an individual key, object or segment.
- An unknown format version is refused; a map is never partially applied.

## Risks

- Nested sets are cheap to query and expensive to mutate in place. This is safe **only because the map is never mutated in place** — it is republished as a whole new version. If anything ever tries to add one server by editing the resident structure, that assumption is broken and the renumbering cost becomes real. `TestIntervalsNestStrictly` catches an incorrect renumber; nothing catches the decision to attempt one, so it is stated here.
- Two representations of one structure is the exact shape of a defect the predecessor project shipped, where a value flowed through a log and its projection and a schema check exercised only one path. The round-trip property test is the deliberate answer, and it is a property test rather than a fixture because a hand-written fixture encodes what the author expected and so cannot falsify the expectation.
- Making levels data loses compile-time checking: a typo in a level label is a load-time error, not a build error. `TestLoadRejectsNodeAtUndeclaredLevel` converts that into a loud refusal rather than a silently misplaced node.
- `Weight` is declared here and read by nothing until T3.

## Stop Condition

Stop and ask if the map must be **versioned over time** rather than merely
carrying a format version — that is, if resolving a segment written last year needs
last year's map. That is a real requirement (`BACKLOG.md` §6), it changes what
`Load` returns, and it must be settled before callers exist. Nested-set numbering
makes this cheaper rather than harder: each published version is a self-contained
flat array, so retaining an old map costs storage and no complexity.

Stop also if spare servers must be declarable in this map rather than tracked
elsewhere (`BACKLOG.md` §7). A spare is a server holding no leaves, which this
format can express — whether it should is ADR-004's call.

## Out of Scope

- How the map reaches a client, and how a change to it is distributed — the parent record's Follow-up, owned by ADR-008.
- Any placement or preference rule that reads the map — that is T3.
- Which level a durability policy spreads across — that is ADR-004. This task only makes the question expressible, by giving levels names and giving nodes intervals.

## Verification Log
- 2026-09-04 · 90c3039* · exit 0 · `set -o pipefail …` · acceptance-sha256:02a1a68440ab7e93a54b03fd0df4f48f978079b85f8d8406b3db61b499507cdd · ms:478
- 2026-09-04 · 90c3039* · exit 0 · `set -o pipefail …` · acceptance-sha256:02a1a68440ab7e93a54b03fd0df4f48f978079b85f8d8406b3db61b499507cdd · ms:486
- 2026-09-04 · 90c3039* · exit 0 · `set -o pipefail …` · acceptance-sha256:02a1a68440ab7e93a54b03fd0df4f48f978079b85f8d8406b3db61b499507cdd · ms:447
- 2026-09-04 · 90c3039* · exit 0 · `set -o pipefail …` · acceptance-sha256:02a1a68440ab7e93a54b03fd0df4f48f978079b85f8d8406b3db61b499507cdd · ms:444
- 2026-09-04 · 90c3039* · exit 0 · `set -o pipefail …` · acceptance-sha256:02a1a68440ab7e93a54b03fd0df4f48f978079b85f8d8406b3db61b499507cdd · ms:445
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:02a1a68440ab7e93a54b03fd0df4f48f978079b85f8d8406b3db61b499507cdd · ms:489
