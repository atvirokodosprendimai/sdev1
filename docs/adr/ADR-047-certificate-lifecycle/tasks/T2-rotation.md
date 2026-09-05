# Task ADR-047-T2: A replaced certificate is picked up without a restart

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `certs.Source`, `certs.NewSource`, `certs.Source.Server`, `certs.Source.Client`, `certs.ErrExpired`
**Consumes:** `certs.Mint`, `certs.Issue` (T1); `serve.TLSConfig` from ADR-046
**Data dependency:** hermetic — certificates minted into `t.TempDir()` and replaced in place
**Proof map:** v1
**Rests-on:** `replaced material being picked up without restarting the process`, `the last good material surviving a failed reload`, `an already-expired certificate being refused at load`

## Goal

Make rotation a file copy, and make a botched one a log line rather than an
outage.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/certs/source.go` | add | `Source`: material re-read per connection, with the last good kept. |
| `internal/core/certs/source_test.go` | add | The tests below. |
| `internal/core/serve/tls.go` | modify | `TLSConfig` builds its configs from a `Source` rather than from a one-time load. |
| `internal/core/certs/doc.go` | modify | Why a failed reload keeps the last good material. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestReplacedMaterialIsPickedUpWithoutARestart`, `TestAFailedReloadKeepsTheLastGoodMaterial`, `TestAnExpiredCertificateIsRefusedAtLoad`, `TestTheAuthorityPoolRotatesToo`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] `NewSource(TLSConfig)` loads once so a misconfiguration is refused at construction, exactly as ADR-046 refuses one. [proof: mutation]
3. [S3] Re-read per connection: `tls.Config.GetConfigForClient` on the server, `GetClientCertificate` on the client. ★No signal handler and no watcher — the connection is the event, and there are no others that matter. [proof: mutation]
4. [S4] ⚠Keep the LAST GOOD material when a reload fails and report the failure. A half-written file, a truncated copy or a typo must not stop a node that has valid material in hand. [proof: mutation]
5. [S5] ⚠Refuse an already-expired certificate at load with `ErrExpired`. That is a configuration error, and installing it makes every handshake fail with an error naming the PEER — which points the diagnosis at the wrong machine. [proof: mutation]
6. [S6] Rotate the authority pool as well as the key pair, so adding a new CA before retiring the old one is possible without a restart. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/certs/... -race -run 'TestReplacedMaterialIsPickedUpWithoutARestart|TestAFailedReloadKeepsTheLastGoodMaterial|TestAnExpiredCertificateIsRefusedAtLoad|TestTheAuthorityPoolRotatesToo' -count=1 2>&1 | tee /tmp/adr047-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr047-t2a.out \
  && go test ./internal/core/certs/... ./internal/core/serve/... -race -count=1 2>&1 | tee /tmp/adr047-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr047-t2b.out
```

⚠ `-race` matters: a `Source` is read by every accepting goroutine and written by
whichever one notices a changed file.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestReplacedMaterialIsPickedUpWithoutARestart` | `internal/core/certs/source_test.go` | A running server is reached, its certificate is replaced on disk with one carrying a DIFFERENT common name, and the next connection sees the new name. ★ Asserted through a real handshake against a real listener that was never restarted — the name is what proves the new file was read, since a stale one would still verify | — | S3 |
| `TestAFailedReloadKeepsTheLastGoodMaterial` | `internal/core/certs/source_test.go` | The certificate file is replaced with garbage and the server KEEPS SERVING with the previous material. ⚠ The assertion is that the connection still works — "an error was returned" would pass for an implementation that returned the error and dropped the certificate | — | S4 |
| `TestAnExpiredCertificateIsRefusedAtLoad` | `internal/core/certs/source_test.go` | Material whose leaf expired yesterday is `ErrExpired` at `NewSource`, before anything binds | — | S5 |
| `TestTheAuthorityPoolRotatesToo` | `internal/core/certs/source_test.go` | A client signed by a SECOND authority is refused, the second authority is appended to the pool file, and the same client is then accepted — with no restart. ★ This is what makes a CA changeover possible at all: both authorities are trusted for as long as the overlap needs | — | S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests, over real listeners and real file replacement. |
| 2 — something selects it | `serve.TLSConfig` builds every config through a `Source`; there is no other load path left. |
| 3 — the caller can discover it | `ErrExpired` joins ADR-046's `ErrNoTLS`, and `NewSource` refuses at construction. |
| 4 — it is used | Every `serve.NewServer` and `NewClient` goes through it, including the two-process binary test. |

## Mutation Log

## Invariants

- Material is re-read per connection.
- A failed reload never removes working material.
- An expired certificate is refused at load.
- The authority pool rotates with the key pair.

## Risks

- ⚠ **"Reports an error" is not "keeps serving".** The failed-reload test must assert a working connection AFTER the file is corrupted, not that a reload function returned something. This is the mutant most likely to survive a lazy test.
- ⚠ **Reading the file on every connection is a syscall per handshake.** Acceptable here — a handshake already costs asymmetric crypto — and worth stating rather than discovering. A modification-time check is an optimisation for §16 to justify, not for this task to assume.
- ⚠ **A `Source` is shared mutable state read by every accept.** It needs a mutex, and the tests need `-race`; ADR-046's pool is the precedent and this is the second one.
- ⚠ **Replacing a certificate is not atomic unless the operator renames.** A reader can see a half-written file, which is exactly the case S4 exists for — so the test should corrupt rather than assume the operator was careful.
- ⚠ **An expired certificate that was ALREADY LOADED is not this task's problem to fix.** Rule 5 refuses one at load; a certificate that expires while a node runs will fail its next handshake, and nothing here can conjure a valid one. Watching for that is §18's follow-up, and pretending otherwise would be worse than the gap.

## Stop Condition

Stop and ask before adding a filesystem watcher, a signal handler, or a
background refresh goroutine. Each is a second mechanism to get right, and the
connection is already an event that arrives exactly when the material is needed.

## Out of Scope

- Revocation (deferred: T3)
- Automatic renewal, and alerting before an expiry (deferred: `docs/adr/BACKLOG.md` §18)
- Caching the file between connections (deferred: `docs/adr/BACKLOG.md` §16 — it is an optimisation and it needs the measurement that record owns)
- A signal handler or watcher (permanent: boundary: ADR-047 rule 4 — the connection is the event)

## Verification Log
