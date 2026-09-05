# Task ADR-046-T1: Both ends authenticate, and the certificate carries the principal

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `serve.TLSConfig`, `serve.TLSConfig.Server`, `serve.TLSConfig.Client`, `serve.PrincipalOf`, `serve.ErrNoTLS`, `serve.ErrNoPrincipal`
**Consumes:** `serve.Options`, `serve.ClientOptions` from ADR-045; `crypto/tls`, `crypto/x509` from the standard library
**Data dependency:** hermetic — the tests mint their own CAs and leaf certificates in memory
**Proof map:** v1
**Rests-on:** `a peer certificate from an undeclared authority being refused`, `a client certificate being required rather than merely requested`, `the principal coming from the certificate rather than from anything the caller sends`

## Goal

Make the connection itself prove who the peer is, so that ADR-033's `principal`
argument has an honest source, and make every fail-open default in `crypto/tls`
unreachable.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/serve/tls.go` | add | `TLSConfig`, the two `*tls.Config` builders, `PrincipalOf`. |
| `internal/core/serve/tls_test.go` | add | The tests below, over two CAs. |
| `internal/core/serve/server.go` | modify | `Options.TLS`; wrap the listener; carry the principal off the connection. |
| `internal/core/serve/client.go` | modify | `ClientOptions.TLS`; dial with `tls.Dialer`. |
| `internal/core/serve/doc.go` | modify | Why the certificate names only WHO. |
| `cmd/sdev1-serve/main.go` | modify | `--cert`, `--key`, `--ca`, all required. |
| `internal/core/serve/server_test.go` | modify | Existing tests gain a TLS fixture; there is no unauthenticated path left to test through. |
| `internal/core/serve/client_test.go` | modify | Same. |
| `internal/core/serve/binary_test.go` | modify | The two started processes get certificate flags. |

⚠ **The four existing test files all change, and that is the point rather than
collateral.** ADR-045's tests dial in the clear. If any of them still passed
unchanged after this task, there would be an unauthenticated path left open.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestAnUntrustedCertificateIsRefused`, `TestAClientCertificateIsRequired`, `TestThePrincipalIsTheCertificateSubject`, `TestTheTLSConfigRefusesItsFailOpenDefaults`, `TestDeclaredTLSIsRequired`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `TLSConfig` — certificate, key, CA pool — and refuse any of the three absent with `ErrNoTLS`. ⚠At construction, not at the first handshake: a node that starts and then cannot accept anything reports its misconfiguration to whoever was unlucky enough to connect. [proof: mutation]
3. [S3] Build the server config: `ClientAuth: tls.RequireAndVerifyClientCert`, `ClientCAs` the declared pool, `MinVersion: tls.VersionTLS13`. ★All three are non-zero values whose zero is permissive — that is the whole of this step. [proof: mutation]
4. [S4] Build the client config: `RootCAs` the declared pool, `Certificates` the client's own, `MinVersion: tls.VersionTLS13`. ⚠`InsecureSkipVerify` is never set, and no option exposes it. [proof: mutation]
5. [S5] Implement `PrincipalOf(*tls.ConnectionState) (string, error)`: the first verified chain's leaf Subject Common Name, `ErrNoPrincipal` when empty or when no verified chain is present. ★It reads `VerifiedChains`, never `PeerCertificates` — the latter is what the peer SENT, the former what was PROVED. [proof: mutation]
6. [S6] Wrap the server's listener with `tls.NewListener` and the client's dial with `tls.Dialer`, and update `cmd/sdev1-serve` with three required flags. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/serve/... -race -run 'TestAnUntrustedCertificateIsRefused|TestAClientCertificateIsRequired|TestThePrincipalIsTheCertificateSubject|TestTheTLSConfigRefusesItsFailOpenDefaults|TestDeclaredTLSIsRequired' -count=1 2>&1 | tee /tmp/adr046-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr046-t1a.out \
  && go build -o /dev/null ./cmd/sdev1-serve \
  && go test ./internal/core/serve/... ./internal/core/wire/... ./internal/core/routing/... -race -count=1 2>&1 | tee /tmp/adr046-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr046-t1b.out
```

The second command runs the WHOLE `serve` package, which is how ADR-045's four
existing tests are proved to have been converted rather than left dialling in the
clear.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAnUntrustedCertificateIsRefused` | `internal/core/serve/tls_test.go` | A client holding a well-formed certificate from a SECOND, undeclared CA cannot complete a handshake. ⚠ The second CA is what makes this a test at all — with one CA in the fixture, an implementation that skipped verification entirely would pass | — | S3, S4 |
| `TestAClientCertificateIsRequired` | `internal/core/serve/tls_test.go` | A client presenting NO certificate is refused. ★ This is the `RequireAndVerifyClientCert` vs `VerifyClientCertIfGiven` distinction, and the second is the spelling that reads as configured and serves anonymous callers | — | S3 |
| `TestThePrincipalIsTheCertificateSubject` | `internal/core/serve/tls_test.go` | `PrincipalOf` returns the CN of the verified leaf, and `ErrNoPrincipal` for an empty CN. ⚠ Asserted against `VerifiedChains`, so a state carrying a peer certificate that was never verified yields no principal | — | S5 |
| `TestTheTLSConfigRefusesItsFailOpenDefaults` | `internal/core/serve/tls_test.go` | Directly on the built `*tls.Config`: `MinVersion == VersionTLS13`, `ClientAuth == RequireAndVerifyClientCert`, `ClientCAs`/`RootCAs` non-nil, `InsecureSkipVerify` false. ★ Every one of these is a ZERO VALUE that means "permissive", so this asserts the absence of four silent defaults rather than the presence of a feature | — | S3, S4 |
| `TestDeclaredTLSIsRequired` | `internal/core/serve/tls_test.go` | A missing certificate, key or CA pool is `ErrNoTLS` at construction — for both `NewServer` and `NewClient` | — | S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests, over two in-memory CAs. |
| 2 — something selects it | The listener is wrapped, so there is no unencrypted accept path; the dialler is the only way `Client` reaches a node. |
| 3 — the caller can discover it | `TLSConfig` has no usable zero value and two named sentinels say so. |
| 4 — it is used | ⚠ `TestTheBinaryServesOverARealNetwork` (ADR-045 T2) starts two real processes and is converted to pass certificate flags — so the operator-facing path is exercised, not just the library. |

## Mutation Log

- 2026-09-05 · a2d28bc* · mutant survived · exit 0 · `internal/core/serve/tls.go` · Start the pool from the host trust store and ADD the declared authority to it, rather than building a pool of exactly one thing. ★ Every legitimate peer still connects — the declared CA is in there — so nothing an operator would notice changes, and the diff reads like a robustness improvement. What it silently adds is every public authority installed on the machine as an entity that may mint a peer for this cluster. Only a certificate from a DIFFERENT, undeclared CA can tell the two pools apart. · acceptance-sha256:51ac3de4d1c372adf0b2dc1f7dba2a98ef9cbb68801967b3610f57eeecaf06a0 · covers:a peer certificate from an undeclared authority being refused
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-05 · a2d28bc* · mutant killed · exit 1 · `internal/core/serve/tls.go` · Start the pool from the host trust store and ADD the declared authority to it. ★ Every legitimate peer still connects, so nothing an operator would notice changes, and the diff reads like a robustness improvement. What it silently adds is every public authority on the machine as an entity that may mint a peer for this cluster. ⚠ This mutant SURVIVED on the first attempt: no handshake test can see it, because a second CA minted in-process is not in the host store either. It is killed only by asserting the pool COMPOSITION. · acceptance-sha256:51ac3de4d1c372adf0b2dc1f7dba2a98ef9cbb68801967b3610f57eeecaf06a0 · covers:a peer certificate from an undeclared authority being refused
- 2026-09-05 · a2d28bc* · mutant killed · exit 1 · `internal/core/serve/tls.go` · The one-word change that reads as configured and is not. VerifyClientCertIfGiven still verifies every certificate that IS presented, so every legitimate peer behaves identically and any test using a real client passes untouched. A caller presenting nothing is simply admitted — and after T3 it would be admitted with an empty principal, whose grant set is whatever is filed under the empty string. It is the single most plausible way this record gets undone by someone being helpful. · acceptance-sha256:51ac3de4d1c372adf0b2dc1f7dba2a98ef9cbb68801967b3610f57eeecaf06a0 · covers:a client certificate being required rather than merely requested
- 2026-09-05 · a2d28bc* · mutant killed · exit 1 · `internal/core/serve/tls.go` · Read the principal from what the peer SENT instead of from what was PROVED. ★ The two fields carry the same certificate on every successful handshake, so every end-to-end test in this package passes unchanged — the identity is even correct. What is lost is the guarantee: PeerCertificates is populated whether or not a chain was built, so any code path that reaches this function without full verification turns an unverified claim into an identity. It is the difference between a name and an authenticated name, and only a state carrying an unverified certificate shows it. · acceptance-sha256:51ac3de4d1c372adf0b2dc1f7dba2a98ef9cbb68801967b3610f57eeecaf06a0 · covers:the principal coming from the certificate rather than from anything the caller sends

## Invariants

- A peer from an undeclared authority never completes a handshake.
- A client certificate is required, not requested.
- The principal comes from a VERIFIED chain.
- No option exposes `InsecureSkipVerify`.

## Risks

- ⚠ **One CA in the fixture proves nothing.** Both peers signed by the only trust anchor present means an implementation that verified nothing still passes. Mint two CAs and keep the second out of every pool.
- ⚠ **`PeerCertificates` is populated even when verification failed or was skipped.** Reading the principal from it would authenticate whatever the peer claimed. Use `VerifiedChains`, which is empty unless a chain was actually built.
- ⚠ **A handshake failure surfaces at different points on each side** — the client may see it on `Read` rather than on dial, because TLS 1.3 finishes the handshake lazily. Assert that the exchange FAILS, not that a specific call fails.
- ⚠ **`MinVersion` unset is TLS 1.0-era permissive on old Go and 1.2 on current.** Either way it is a value nobody chose; assert the constant.
- Converting the four existing test files is mechanical but wide. Do it in one pass so no test is left dialling in the clear by accident.

## Stop Condition

Stop and ask before adding any option that disables verification, including one
meant only for tests. ⚠ A test-only escape hatch is a production escape hatch with
a comment on it, and the fixture needs none — minting a CA in-process is fifteen
lines and gives a stronger test than skipping verification would.

## Out of Scope

- Authorization (deferred: T3)
- Pooling (deferred: T2)
- Certificate issuance, distribution and rotation (deferred: `docs/adr/BACKLOG.md` §18)
- Revocation, CRL and OCSP (deferred: `docs/adr/BACKLOG.md` §18)

## Verification Log
- 2026-09-05 · a2d28bc* · exit 0 · `set -o pipefail …` · acceptance-sha256:51ac3de4d1c372adf0b2dc1f7dba2a98ef9cbb68801967b3610f57eeecaf06a0 · ms:6833
- 2026-09-05 · a2d28bc* · exit 0 · `set -o pipefail …` · acceptance-sha256:51ac3de4d1c372adf0b2dc1f7dba2a98ef9cbb68801967b3610f57eeecaf06a0 · ms:6850
- 2026-09-05 · a2d28bc* · exit 0 · `set -o pipefail …` · acceptance-sha256:51ac3de4d1c372adf0b2dc1f7dba2a98ef9cbb68801967b3610f57eeecaf06a0 · ms:6867
- 2026-09-05 · a2d28bc* · exit 0 · `set -o pipefail …` · acceptance-sha256:51ac3de4d1c372adf0b2dc1f7dba2a98ef9cbb68801967b3610f57eeecaf06a0 · ms:6763
- 2026-09-05 · a2d28bc* · exit 0 · `set -o pipefail …` · acceptance-sha256:51ac3de4d1c372adf0b2dc1f7dba2a98ef9cbb68801967b3610f57eeecaf06a0 · ms:6647
