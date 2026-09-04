# Task ADR-001-T4: An operator command that shows a key's descent and placement

**Depends-on:** T1, T2, T3
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the `sdev1-addr` command
**Consumes:** `addr.Descend()` (T1); `topology.Load()` (T2); `placement.Resolve()` (T3)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the exit code`, `the printed leaf identifier`, `the built binary actually running rather than only compiling`, `the internal regression suites still passing`, `the JSON and text forms agreeing`

## Goal

Give an operator a command that answers "where does this entity live, and why",
by printing the byte-by-byte descent and the resolved server set for a key against
a topology file.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/sdev1-addr/main.go` | add | The command: flags, the descent, the resolved set, the output. |
| `cmd/sdev1-addr/main_test.go` | add | The tests below, exercising the command's own entry point. |
| `go.mod` | edit | Adds `urfave/cli/v3` for flag handling, per the house convention. |

This task is the reachability rung for everything ADR-001 builds: it is the one
place all three packages are actually called, and it is the first externally
observable slice of the record — an operator can inspect the addressing model
before any storage exists.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestCommandPrintsDescent`, `TestCommandResolvesToTargets`, `TestCommandExitsNonZeroOnBadTopology`, `TestCommandRequiresItsFlags`. Run the Acceptance fence and confirm it is red. [proof: acceptance]
2. [S2] Add `urfave/cli/v3` to `go.mod` and wire the command with `--topology <path>` and `--entity <id>` flags. [proof: acceptance]
3. [S3] Load the topology map, hash the entity, descend to the map's declared depth, and resolve.
4. [S4] Print one line per level showing the byte consumed and the child selected, then the resolved server set in preference order.
5. [S5] Exit non-zero with a readable message when the topology file is missing, unreadable, or carries an unknown version — an operator diagnostic must not exit 0 on a bad input.
6. [S6] Add `--json` for machine-readable output, so the command is usable from a script and from the console of ADR-012. [proof: human: an operator runs the command both ways and confirms the JSON carries the same leaf and server set as the text form]

## Acceptance

```bash
set -o pipefail
go test ./cmd/sdev1-addr/... -run 'TestCommand' -count=1 2>&1 | tee /tmp/adr001-t4.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr001-t4.out \
  && go test ./internal/... -count=1 \
  && go build -o /tmp/sdev1-addr ./cmd/sdev1-addr \
  && /tmp/sdev1-addr --topology testdata/topology/minimal.json --entity demo-entity
```

The new unit runs alone first; the internal suites follow as regression; then the
command is actually built and run, because a command that compiles and is never
executed is this pipeline's most common shipped defect. The final invocation
exits non-zero if the wiring is wrong even when every unit test passes.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestCommandPrintsDescent` | `cmd/sdev1-addr/main_test.go` | One hop per level, each naming the byte of the key consumed and the child it selects, and exactly as many hops as the map's declared depth | — | S3, S4 |
| `TestCommandResolvesToTargets` | `cmd/sdev1-addr/main_test.go` | The printed targets are the ones placement resolves, at the map's deepest level — the assertion that goes red if the call site is deleted | — | S3, S4 |
| `TestCommandExitsNonZeroOnBadTopology` | `cmd/sdev1-addr/main_test.go` | A missing file and an unknown format version each exit non-zero with a readable message | — | S5 |
| `TestCommandJSONMatchesTextOutput` | `cmd/sdev1-addr/main_test.go` | `--json` reports the same leaf and server set as the text form, so the two cannot drift | — | S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five unit tests above, three of them with subtests over the failure paths. |
| 2 — something selects it | `main()` is the selector, and the Acceptance fence *builds and runs the binary* rather than only testing the package — deleting the `placement.Resolve` call makes `TestCommandResolvesToTargets` red and the final invocation wrong. |
| 3 — the caller can discover it | `--help` text listing both flags; `TestCommandExitsNonZeroOnBadTopology` covers the failure path an operator meets first. |
| 4 — it is used | Nothing measures this yet. The command is a diagnostic; usage would be observable only once the console of ADR-012 exists. |

## Mutation Log

- 2026-09-04 · 489b1cf* · mutant killed · exit 1 · `cmd/sdev1-addr/main.go` · the command must actually call placement and print what it returns; dropping the assignment leaves the targets section empty while everything still compiles, and TestCommandResolvesToTargets must go red · acceptance-sha256:198b6f43d9baa0a9d994fa81db00e5c523353150bfa3040bbe37e44a8fcd92bb · covers:the printed leaf identifier
- 2026-09-04 · 489b1cf* · mutant killed · exit 1 · `cmd/sdev1-addr/main.go` · an operator diagnostic that exits 0 on a broken topology is worse than none; TestCommandExitsNonZeroOnBadTopology must go red · acceptance-sha256:198b6f43d9baa0a9d994fa81db00e5c523353150bfa3040bbe37e44a8fcd92bb · covers:the exit code

## Invariants

- The command performs no writes. It reads a topology file and prints; it never contacts a server and never mutates state.
- Text and JSON output report the same leaf and the same server set, in the same order.
- A bad input exits non-zero. An operator diagnostic that exits 0 on a broken topology is worse than no diagnostic.

## Risks

- Adding a dependency (`urfave/cli/v3`) to a module that had none. It is the house convention for command-line flags, so the alternative is hand-rolling flag parsing in every future command.
- The command's output format is not a stability promise at this stage, and nothing yet says so to an operator who might script against it. `--json` exists partly so that a later stability promise has somewhere to land.

## Stop Condition

Stop and ask if the descent output needs to show the *containment path* (which
datacenter, which rack) rather than only the byte and child index. That is more
useful to an operator and requires `placement` to return more structure, which is
T3's stated Stop Condition — the two must be decided together, not separately.

## Out of Scope

- Contacting a server to confirm the leaf is actually there — the command is a pure computation over a file, and confirming would need a client that does not exist yet.
- Any subcommand beyond the descent — the command grows when there is something else worth inspecting.

## Verification Log
- 2026-09-04 · 489b1cf* · exit 0 · `set -o pipefail …` · acceptance-sha256:198b6f43d9baa0a9d994fa81db00e5c523353150bfa3040bbe37e44a8fcd92bb · ms:2108
- 2026-09-04 · 489b1cf* · exit 0 · `set -o pipefail …` · acceptance-sha256:198b6f43d9baa0a9d994fa81db00e5c523353150bfa3040bbe37e44a8fcd92bb · ms:2347
- 2026-09-04 · 489b1cf* · exit 0 · `set -o pipefail …` · acceptance-sha256:198b6f43d9baa0a9d994fa81db00e5c523353150bfa3040bbe37e44a8fcd92bb · ms:2083
