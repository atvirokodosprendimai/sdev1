# Task ADR-045-T2: A server that serves or redirects, and refuses a write by name

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** L
**Owner:** unassigned
**Produces:** `serve.Server`, `serve.NewServer`, `serve.Options`, `serve.Server.Serve`, `serve.Server.Addr`, `serve.Server.Close`, `serve.ErrWriteNotServed`, `serve.ErrNoTimeout`, `cmd/sdev1-serve`
**Consumes:** `wire.Request`, framing (T1); `wire.Response` from ADR-043; `routing.Table`/`Route`/`Redirect` from ADR-008; `addr.Descend` from ADR-001; `leafstore.Store` from ADR-026; `eval.Read` from ADR-027; `ql.Parse` from ADR-011/034
**Data dependency:** hermetic — every test binds `127.0.0.1:0` and reads its own temporary leaf
**Proof map:** v1
**Rests-on:** `a node computing a redirect from a key it does not hold`, `a write over the wire being refused by name`, `a server without declared timeouts being refused`

## Goal

Put a leaf behind a socket, and make the node that does NOT hold a key still able
to say where it went.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/serve/doc.go` | add | Why the server resolves the key itself, and why it serves no writes. |
| `internal/core/serve/server.go` | add | `Server`, `Options`, the accept loop and the serve-or-redirect decision. |
| `internal/core/serve/server_test.go` | add | The tests below. |
| `cmd/sdev1-serve/main.go` | add | A real listening process, flags via `urfave/cli/v3` as the rest of the commands do. |
| `internal/core/eval/eval.go` | modify | ⚠ **Not in the original plan.** `Row` carried `Valid` and `IsReference` nowhere, and encoding an answer without them would have meant fabricating both. See the note below. |
| `internal/core/eval/inbound.go` | modify | The same two fields, at the inbound read's row-building site. |

⚠ **A row was a lossy projection, and the loss was invisible until one left the
process.** `eval.Row` carried `Entity`, `Attribute`, `Value` and `TxID` — HALF the
bitemporal coordinate, and none of the reference flag. Every consumer so far
printed a value locally, so nothing had asked. `datom.Encode` writes both validity
endpoints in full and says why: *"leaving it implicit is how a fact acquires an
end at the epoch with nothing about it looking unusual"* — so serving a row as a
datom without `Valid` would have put exactly that fabricated fact on the wire.
`IsReference` is the same class: `"star-1"` as a name and `"star-1"` as a link are
the same six bytes, and dropping the flag turns every edge into data in transit.

`Assert` needs no field and was not added: a projection holds what an entity
CURRENTLY carries, so a retracted attribute is absent rather than
present-and-false, and `true` at the encode site is derived rather than assumed.

★ This is a change to ADR-027's type made by a record about the transport, which
is worth naming rather than burying: the wire did not introduce the defect, it
was the first caller that could SEE it.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestANodeRedirectsForAKeyItDoesNotHold`, `TestAWriteOverTheWireIsRefused`, `TestAServerNeedsDeclaredTimeouts`, `TestAReadIsServedFromARealLeaf`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Refuse `NewServer` without positive read and write timeouts and a positive frame bound, with `ErrNoTimeout`. ⚠A connection with no deadline is a goroutine a stranger can pin forever. [proof: mutation]
3. [S3] Accept a connection, set both deadlines, read ONE framed request, and close after the response. ★Rule 7: one exchange per connection has a failure model with nothing in it — no half-consumed stream, no correlation identifiers, nothing to reconcile after a drop. [proof: mutation]
4. [S4] ★Resolve the request's KEY against this node's OWN routing table by descending it to a leaf, then decide: serve if this node holds that leaf, redirect otherwise. ⚠This is the step that a leaf-named request makes impossible. [proof: mutation]
5. [S5] Serve a read by parsing the statement and running `eval.Read` against the leaf store, returning a `wire.Answer` whose payload is an encoded datom run. [proof: mutation]
6. [S6] Refuse a write statement with `ErrWriteNotServed`, as a `wire.Refusal` naming the reason. ⚠Not a redirect and not an answer: ADR-043's three outcomes exist so "I will not" is distinct from "not here" and from "here it is". [proof: mutation]
7. [S7] Add `cmd/sdev1-serve` so the server is a process an operator can start, with the leaf directory, address, timeouts and frame bound as flags. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/serve/... -race -run 'TestANodeRedirectsForAKeyItDoesNotHold|TestAWriteOverTheWireIsRefused|TestAServerNeedsDeclaredTimeouts|TestAReadIsServedFromARealLeaf' -count=1 2>&1 | tee /tmp/adr045-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr045-t2a.out \
  && go build -o /dev/null ./cmd/sdev1-serve \
  && go test ./internal/core/serve/... ./internal/core/wire/... ./internal/core/routing/... ./internal/core/leafstore/... -race -count=1 2>&1 | tee /tmp/adr045-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr045-t2b.out
```

The build of `cmd/sdev1-serve` is in the fence because a server nobody can start
is a library; `-o /dev/null` because a binary left in the repository root was
committed once already and `.gitignore` carries the scar.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestANodeRedirectsForAKeyItDoesNotHold` | `internal/core/serve/server_test.go` | A server holding leaf A, asked for a key that descends to leaf B, answers a `wire.Redirect` carrying B's route and epoch — **computed from the key it was given**. ★ It is the falsifier's server half: the node has no prior knowledge of the client's belief, only the key | — | S4 |
| `TestAWriteOverTheWireIsRefused` | `internal/core/serve/server_test.go` | `ASSERT …` over the wire is a `wire.Refusal` naming `ErrWriteNotServed` — not a redirect, not an empty answer. ⚠ An empty ANSWER is the dangerous shape: a client would read "no rows" and believe the write landed | — | S6 |
| `TestAServerNeedsDeclaredTimeouts` | `internal/core/serve/server_test.go` | Zero or negative read timeout, write timeout, or frame bound is `ErrNoTimeout` at construction | — | S2 |
| `TestAReadIsServedFromARealLeaf` | `internal/core/serve/server_test.go` | A datom appended to a real sealed leaf comes back over a real loopback socket as a `wire.Answer` decoding to that datom. ★ End to end over a socket, not a fake | — | S3, S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests, over `127.0.0.1:0` listeners and temporary leaves. |
| 2 — something selects it | The accept loop is the only entry, and every response leaves through `wire.Encode`. |
| 3 — the caller can discover it | Two named sentinels, and `Options` has no usable zero value. |
| 4 — it is used | `cmd/sdev1-serve` is a process an operator starts, and T3's client talks to it. |

## Invariants

- The server resolves the key against its own table; the client's belief is never an input.
- A write is refused by name, never answered.
- Both deadlines are set on every connection.
- One request per connection.

## Mutation Log

- 2026-09-05 · cc86d37* · mutant killed · exit 1 · `internal/core/serve/server.go` · Answer a key this node does not hold with a refusal instead of a redirect. ★ This is not an arbitrary break — it is EXACTLY what a leaf-named request would force, since a node handed a leaf it does not recognise has nothing to compute a redirect from and nothing to send but an error. If the test survives it, ADR-008 rule 4 is unproven and the whole reason a request carries a key rather than a leaf goes unchecked. · acceptance-sha256:6659ef32fe5061070af8593bfed18006bf43cd5aa993e2bca4f91fea1abfeb20 · covers:a node computing a redirect from a key it does not hold
- 2026-09-05 · cc86d37* · mutant killed · exit 1 · `internal/core/serve/server.go` · Answer a write with an EMPTY ANSWER instead of a named refusal — the exact shape ADR-043 built three outcomes to make inexpressible by accident. A client reads zero rows and concludes the write ran and matched nothing, so nothing anywhere reports a failure. The write is still not performed either way, which is why only a test asserting the RESPONSE SHAPE can tell the two apart. · acceptance-sha256:6659ef32fe5061070af8593bfed18006bf43cd5aa993e2bca4f91fea1abfeb20 · covers:a write over the wire being refused by name
- 2026-09-05 · cc86d37* · mutant killed · exit 1 · `internal/core/serve/server.go` · Fill in sensible defaults instead of refusing — the most natural "helpful" change anyone would make here, and one that reviews well because every connection is still bounded. What it destroys is the distinction between an operator who chose these numbers and one who never configured the node, and the server that results looks identical from outside. Only a construction-time refusal makes the omission visible to the caller. · acceptance-sha256:6659ef32fe5061070af8593bfed18006bf43cd5aa993e2bca4f91fea1abfeb20 · covers:a server without declared timeouts being refused

## Risks

- ⚠ **An empty ANSWER is the wrong refusal shape for a write.** A client reads zero rows and concludes the write succeeded and matched nothing. The refusal must be `wire.Refusal`, which ADR-043 made a distinct outcome precisely so this is not expressible by accident.
- ⚠ **A redirect must be computed from the KEY.** A test where the server already knows which leaf the client wanted proves nothing — the fixture must give the server only the request.
- ⚠ **Deadlines must be set per connection, not once on the listener.** A listener deadline bounds accept, not the conversation, and the goroutine a stranger pins is the one after accept.
- ⚠ **`t.TempDir` plus a sealed leaf is the honest fixture.** A server over an in-memory reader would not exercise the path an operator runs.
- Nothing authenticates. Recorded on the parent record as a consequence; this task must not invent a caller identity to fill the gap.

## Stop Condition

Stop and ask before serving a write. There is no leader, so it would be unfenced
(ADR-009) and committed at a durability nobody has (ADR-020), and the refusal is
the only honest answer until `BACKLOG.md` §19 exists.

## Out of Scope

- The client (deferred: T3)
- Authentication (deferred: `docs/adr/BACKLOG.md` §18)
- Shedding (deferred: `docs/adr/BACKLOG.md` §22 — there is no queue)
- Pooling or multiplexing (deferred: `docs/adr/BACKLOG.md` §16)

## Verification Log
- 2026-09-05 · cc86d37* · exit 0 · `set -o pipefail …` · acceptance-sha256:6659ef32fe5061070af8593bfed18006bf43cd5aa993e2bca4f91fea1abfeb20 · ms:5140
- 2026-09-05 · cc86d37* · exit 0 · `set -o pipefail …` · acceptance-sha256:6659ef32fe5061070af8593bfed18006bf43cd5aa993e2bca4f91fea1abfeb20 · ms:5183
- 2026-09-05 · cc86d37* · exit 0 · `set -o pipefail …` · acceptance-sha256:6659ef32fe5061070af8593bfed18006bf43cd5aa993e2bca4f91fea1abfeb20 · ms:5134
- 2026-09-05 · cc86d37* · exit 0 · `set -o pipefail …` · acceptance-sha256:6659ef32fe5061070af8593bfed18006bf43cd5aa993e2bca4f91fea1abfeb20 · ms:5060
