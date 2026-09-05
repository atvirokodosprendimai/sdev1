# Task ADR-047-T1: A command mints the authority and issues the certificates

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `certs.Authority`, `certs.Mint`, `certs.Load`, `certs.Issue`, `certs.Subject`, `certs.Issued`, `certs.FormatSerial`, `certs.ErrAuthorityExists`, `certs.ErrNoSubject`, `certs.ErrNoAuthority`, `cmd/sdev1-ca`
**Consumes:** `crypto/x509`, `crypto/ed25519` from the standard library; `serve.PrincipalOf` from ADR-046, to cross-check what the transport reads
**Data dependency:** hermetic — every test mints into `t.TempDir()`
**Proof map:** v1
**Rests-on:** `an existing authority key being refused rather than overwritten`, `an issued certificate carrying the principal as its common name`, `the authority's private key staying out of what a node is given`

## Goal

Let an operator make the certificates ADR-046 requires, without running a PKI and
without ever putting the signing key on a node.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/certs/certs.go` | add | `Authority`, `Mint`, `Load`, `Issue`, and the PEM files they write. |
| `internal/core/certs/certs_test.go` | add | The tests below. |
| `internal/core/certs/doc.go` | add | Why issuance is a command and not an endpoint. |
| `cmd/sdev1-ca/main.go` | add | `mint` and `issue`, flags via `urfave/cli/v3` as the other commands do. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestAnExistingAuthorityIsNeverOverwritten`, `TestAnIssuedCertificateNamesItsPrincipal`, `TestAnIssuedCertificateVerifiesAgainstItsAuthorityAndNoOther`, `TestASubjectIsRequired`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] `Mint(dir, name, life)` writes `ca.pem` and `ca-key.pem`. ⚠It refuses when `ca-key.pem` already exists, with `ErrAuthorityExists` — a second run must never silently invalidate every certificate ever issued. [proof: mutation]
3. [S3] `Load(dir)` reads an authority back, so `issue` can run against one minted a year ago. [proof: mutation]
4. [S4] `Issue(a, dir, subject)` signs a leaf whose Common Name is the principal, with client and server auth usage and the SANs a node needs. ⚠An empty subject is `ErrNoSubject`: a certificate naming nobody is refused at the point it would be created, not later at the point it fails to authenticate. [proof: mutation]
5. [S5] ★The issued bundle is `cert.pem`, `key.pem` and a COPY of `ca.pem` — and never the authority's private key. A node is given exactly what it needs to prove itself and check its peers, and nothing that would let it mint a peer. [proof: mutation]
6. [S6] Record each issued serial where the operator can read it, so T3's denial has something to name. [proof: acceptance]
7. [S7] Add `cmd/sdev1-ca` with `mint` and `issue`. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/certs/... -race -run 'TestAnExistingAuthorityIsNeverOverwritten|TestAnIssuedCertificateNamesItsPrincipal|TestAnIssuedCertificateVerifiesAgainstItsAuthorityAndNoOther|TestASubjectIsRequired|TestAnIssuedCertificateIsUsableRightAway|TestTheCommandIssuesWhatTheTransportAccepts' -count=1 2>&1 | tee /tmp/adr047-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr047-t1a.out \
  && go build -o /dev/null ./cmd/sdev1-ca \
  && go test ./internal/core/certs/... ./internal/core/serve/... -race -count=1 2>&1 | tee /tmp/adr047-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr047-t1b.out
```

`serve` is in the second command because what this task issues has to be
acceptable to the thing ADR-046 built; a certificate that mints cleanly and fails
a handshake is not an issued certificate.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAnExistingAuthorityIsNeverOverwritten` | `internal/core/certs/certs_test.go` | A second `Mint` into a directory holding a CA key is `ErrAuthorityExists`, and the key on disk is BYTE-IDENTICAL afterwards. ⚠ The byte comparison is the assertion — an error return with the file already replaced would pass a weaker check and still have invalidated every certificate ever issued | — | S2 |
| `TestAnIssuedCertificateNamesItsPrincipal` | `internal/core/certs/certs_test.go` | The leaf's Subject Common Name is the subject given, which is the principal ADR-046 reads. ★ Asserted through `serve.PrincipalOf` against a real verified chain, not by re-parsing the field this task wrote | — | S4 |
| `TestAnIssuedCertificateVerifiesAgainstItsAuthorityAndNoOther` | `internal/core/certs/certs_test.go` | A leaf chains to its own CA and NOT to a second one minted alongside it. ⚠ The second authority is what makes this a test: with one, a leaf that chained to nothing would still pass a "does it verify" check that verified against an empty pool | — | S4, S5 |
| `TestASubjectIsRequired` | `internal/core/certs/certs_test.go` | An empty subject is `ErrNoSubject`. ★ Refused where it is created — ADR-046 refuses a nameless certificate at the handshake, and by then somebody has deployed it | — | S4 |
| `TestAnIssuedCertificateIsUsableRightAway` | `internal/core/certs/certs_test.go` | A freshly issued certificate verifies against a clock thirty minutes behind. ⚠ `NotBefore` set to "now" is not yet valid on a peer whose clock lags, and the failure reads as an untrusted certificate rather than as a clock problem — so the backdating is deliberate and is asserted | — | S4 |
| `TestTheCommandIssuesWhatTheTransportAccepts` | `internal/core/certs/command_test.go` | **Rung 4.** Builds `cmd/sdev1-ca`, RUNS `mint` twice and `issue` once, and hands the result to the transport. ★ A `go build` proves only that `main` compiles — the verb names, the flag names and the wiring are invisible to it. The second `mint` is the important half: a command that overwrote an authority would print this test's own success and destroy a cluster | — | S2, S6, S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests, minting into `t.TempDir()`. |
| 2 — something selects it | `Issue` is the only path that signs a leaf; `Mint` the only one that makes an authority. |
| 3 — the caller can discover it | Three named sentinels, and `cmd/sdev1-ca --help` names both verbs. |
| 4 — it is used | ⚠ `TestTheCommandIssuesWhatTheTransportAccepts` BUILDS AND RUNS `cmd/sdev1-ca`, then verifies what it produced against the transport's own `PrincipalOf` and a real chain. A build step would have proved only that `main` compiles. T2 and T3 then take their fixtures from `certs.Mint`/`Issue`. |

## Mutation Log

- 2026-09-05 · 297751f* · mutant killed · exit 1 · `internal/core/certs/certs.go` · Notice the existing authority, keep going, and report nothing until the work is done — the shape a guard takes when it is added after the fact rather than before. ⚠ The caller still gets no authority back and the directory still ends up holding a key, so any test that only reads the RETURNED ERROR passes. What is destroyed is the file that was already there, and the outage does not appear here: it appears later, on every node at once, as certificates that no longer verify. Only comparing the key bytes before and after can tell a refusal from a refusal-after-the-damage. · acceptance-sha256:90dc58abe1ac7b54cc3bcf53b4b0d2a3f5b2f68105a4759aacfc5c567a05bf7f · covers:an existing authority key being refused rather than overwritten
- 2026-09-05 · 297751f* · mutant killed · exit 1 · `internal/core/certs/certs.go` · Put the subject in the Organization field instead of the Common Name. ★ The certificate is still perfectly valid, still chains, still completes a handshake, and still carries the name a human reading `openssl x509 -text` would find — so every test about issuance, verification and chaining passes untouched. What it destroys is the ONE field ADR-046 reads as the principal: every caller becomes nameless, and the transport refuses them all with an error about an empty common name that says nothing about where the name went. · acceptance-sha256:90dc58abe1ac7b54cc3bcf53b4b0d2a3f5b2f68105a4759aacfc5c567a05bf7f · covers:an issued certificate carrying the principal as its common name
- 2026-09-05 · 297751f* · mutant killed · exit 1 · `internal/core/certs/certs.go` · Copy the whole authority directory into the bundle "so a node has everything it needs" — which it now does, including the ability to mint its own peers. ★ Nothing fails: the certificate is identical, every handshake works, every chain verifies, and the bundle is more self-contained than before. It is the change someone makes to fix a support ticket about a missing file. What it costs is the entire blast radius of a node compromise: one machine can now issue a certificate for any principal in the cluster, and no test that checks certificates would ever notice. · acceptance-sha256:90dc58abe1ac7b54cc3bcf53b4b0d2a3f5b2f68105a4759aacfc5c567a05bf7f · covers:the authority's private key staying out of what a node is given

## Invariants

- An existing authority key is never overwritten.
- A leaf's Common Name is its principal.
- What a node is given never includes the authority's private key.
- A nameless subject is refused at issuance.

## Risks

- ⚠ **Refusing to overwrite is not the same as not overwriting.** Write the guard before the file, and assert the bytes on disk are unchanged — an implementation that truncates and then errors passes any check that only reads the error.
- ⚠ **One authority in the fixture proves nothing about chaining.** A leaf verified against a pool containing its own CA proves the CA is in the pool. Mint a second and assert it does NOT verify.
- ⚠ **`Issue` must not return or write the CA private key**, however convenient a single bundle would be. A node that holds it can mint peers, and compromising one node then compromises the cluster.
- ⚠ **An Ed25519 key is not a decision to relitigate per call.** No curve flag, no size flag: an option here is an option an operator can get wrong, and every wrong answer looks identical until it is attacked.
- Serials must be unpredictable enough not to collide across issuances; a counter in a file is a second piece of state to keep and a random 128-bit serial is not.

## Stop Condition

Stop and ask before adding an online issuance path, a bootstrap token, or
anything that lets a node request its own certificate. ADR-047 rule 1 is that
authenticating such a request needs a certificate, so every version of it either
weakens the transport for one path or invents the second identity ADR-046
rejected.

## Out of Scope

- Rotation (deferred: T2)
- Revocation (deferred: T3)
- Automatic renewal (deferred: `docs/adr/BACKLOG.md` §18)
- Encrypting the CA key at rest (deferred: `docs/adr/BACKLOG.md` §18)
- Intermediate CAs (permanent: boundary: ADR-047 — one CA and one leaf is the shape this cluster needs)

## Verification Log
- 2026-09-05 · 297751f* · exit 0 · `set -o pipefail …` · acceptance-sha256:90dc58abe1ac7b54cc3bcf53b4b0d2a3f5b2f68105a4759aacfc5c567a05bf7f · ms:7750
- 2026-09-05 · 297751f* · exit 0 · `set -o pipefail …` · acceptance-sha256:90dc58abe1ac7b54cc3bcf53b4b0d2a3f5b2f68105a4759aacfc5c567a05bf7f · ms:7567
- 2026-09-05 · 297751f* · exit 0 · `set -o pipefail …` · acceptance-sha256:90dc58abe1ac7b54cc3bcf53b4b0d2a3f5b2f68105a4759aacfc5c567a05bf7f · ms:7751
- 2026-09-05 · 297751f* · exit 0 · `set -o pipefail …` · acceptance-sha256:90dc58abe1ac7b54cc3bcf53b4b0d2a3f5b2f68105a4759aacfc5c567a05bf7f · ms:7772
