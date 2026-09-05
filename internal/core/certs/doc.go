// Package certs makes the certificates the transport requires, keeps them
// current, and says which of them have stopped being believable.
//
// ADR-046 made a certificate the caller's identity and consumed certificates it
// could not produce. This package produces them.
//
// # Issuance is a command, and that is a proof rather than a preference
//
// ★★ AN ISSUANCE ENDPOINT CANNOT EXIST HERE. Every request to this system is
// authenticated by a client certificate (ADR-046 rule 1), so an endpoint that
// issued certificates would have to authenticate a requester that does not have
// one yet. Every version of that either weakens the transport for one path or
// introduces a bootstrap token — which is the second identity ADR-046 rejected on
// the way in, because two identities can disagree and the interesting question
// then becomes which one was checked.
//
// ⚠ This is not a gap to apologise for. The first credential arrives out of band
// in every cluster PKI there has ever been, by definition.
//
// So `cmd/sdev1-ca` is a command, run wherever the authority's key is kept, which
// is NOT a node. ⚠ [Issue] hands a node its own certificate, its own key and a
// COPY of the authority's certificate — never the authority's key. A node that
// could mint peers would turn one compromise into all of them.
//
// # Rotation is replacing a file
//
// [Source] re-reads certificate material PER CONNECTION, so a replaced file is
// picked up with no restart, no signal and no watcher. ★ The connection is the
// event, and there is no other moment at which the material matters.
//
// ⚠ A FAILED RELOAD KEEPS THE LAST GOOD MATERIAL. Failing closed feels like the
// secure choice and it turns a routine rotation into a fleet outage: the file on
// disk is the thing in doubt, while the material already in hand has been working.
// A half-written copy or a typo must be a log line, not an outage.
//
// ⚠ An already-expired certificate is refused at LOAD ([ErrExpired]). That is a
// configuration error rather than a transient, and installing one makes every
// handshake fail with an error naming the PEER — which points the diagnosis at
// the wrong machine entirely.
//
// # Revocation is a datom, and it is checked on every request
//
// ★ Half of revocation already worked. ADR-033 made AUTHORITY revocable — a grant
// is a datom, revoking is retracting — and ADR-046 made that reach a caller
// mid-connection. A principal whose grants are gone can connect and read nothing.
//
// What was left is the STOLEN KEY: its holder still completes handshakes, still
// costs a node resources, and is re-admitted the moment anyone grants that
// principal name again for a good reason. [DenyDatom] addresses exactly that
// residual, and it is much smaller than a CRL.
//
// ⚠ A CRL or an OCSP responder is the conventional answer and the wrong shape
// here: a second distribution system with its own freshness problem, in a system
// whose reserved tenant already carries bitemporal, retractable, auditable
// statements. OCSP additionally puts a synchronous network dependency inside
// every handshake, so a responder outage becomes a cluster outage.
//
// ⚠ DENY BY SERIAL, NEVER BY PRINCIPAL. A leaked key is ONE certificate. Denying
// the name punishes the legitimate holder, blocks their reissuance, and says
// "this person may no longer read" — which retracting a grant already says,
// better.
//
// ⚠⚠ AND THE DENIAL IS CHECKED ON EVERY REQUEST. The obvious place is the
// handshake: cheaper, once per connection, refuses before anything else runs. It
// also silently undoes ADR-046, whose rule 8 made connections POOLED and
// long-lived — a handshake-only check leaves a denied certificate reading over a
// connection it opened moments before, for as long as the pool holds it.
//
// ⚠ A denial's validity ends at the CERTIFICATE'S OWN EXPIRY. Swept earlier it
// silently re-admits the certificate; kept forever it fills the reserved tenant
// with facts about certificates nobody can present. The expiry is in the
// certificate, so the rule is derivable rather than guessed.
//
// # What this does not do
//
// ⚠ No automatic renewal and nothing watches an expiry date — an operator runs
// the command and copies files. ⚠ The authority's private key is written
// unencrypted; protecting it is the operator's. ⚠ And a denial reaches only the
// nodes that hold it, because nothing replicates the reserved tenant yet
// (docs/adr/BACKLOG.md §19) — a partial revocation is worse than it sounds,
// because it looks complete from the node that has it.
//
// See docs/adr/ADR-047-certificate-lifecycle.md.
package certs
