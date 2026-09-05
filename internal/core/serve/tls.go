package serve

import (
	"crypto/tls"
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/certs"
)

var (
	// ErrNoTLS reports a certificate, key or authority pool that was not
	// declared.
	//
	// ⚠ There is no unencrypted mode and no default. A transport that can be
	// configured without TLS has a state in which it silently is not, and that
	// state looks exactly like "not finished configuring yet".
	ErrNoTLS = errors.New("serve: a certificate, a key and a certificate authority must all be declared")

	// ErrNoPrincipal reports a peer whose certificate names nobody.
	//
	// ⚠ An anonymous caller is not a caller with no permissions — it is a caller
	// whose identity cannot be checked, so the grant lookup would be against the
	// empty string. Refused here rather than turned into a principal.
	ErrNoPrincipal = errors.New("serve: the peer certificate carries no principal")
)

// TLSConfig is what one end needs to prove who it is and to check the other.
//
// ⚠ IT HAS NO USABLE ZERO VALUE, and every field is required in both directions.
// A server presents a certificate and verifies the client's; a client does
// exactly the same. There is no read-only or anonymous path that skips half of
// it.
type TLSConfig struct {
	// CertFile and KeyFile are this end's own identity. For a server the
	// certificate's name is what a client verifies; for a client the Common Name
	// is the PRINCIPAL the grant set is read for.
	CertFile string
	KeyFile  string

	// CAFile is the authority that signs this cluster's certificates.
	//
	// ★ Declared, never the host's trust store. See [TLSConfig.pool].
	CAFile string
}

// Server builds the listener's configuration.
//
// ⚠ Every value set here has a ZERO that is permissive, which is why they are set
// explicitly rather than left alone:
//
//   - ClientAuth's zero is [tls.NoClientCert] — no client certificate is asked
//     for at all, so every caller is anonymous.
//   - ClientCAs' zero is nil, which does NOT mean "trust nothing". It means the
//     host's root store, so any of the public authorities installed on the
//     machine could mint a peer for this cluster.
//   - MinVersion's zero is whatever the Go release considers old-but-acceptable,
//     which is a version nobody chose.
//
// ★ [tls.VerifyClientCertIfGiven] is the trap among the ClientAuth constants: it
// reads as "verify client certificates", it passes a casual review, and a caller
// that presents none is admitted.
func (c TLSConfig) Server() (*tls.Config, error) {
	source, err := c.Source()
	if err != nil {
		return nil, err
	}
	return source.Server(), nil
}

// Client builds the dialler's configuration.
//
// ⚠ It reads the material as it is NOW, so a caller that wants rotation must
// call it per dial rather than once. [Client.dial] does. See
// [github.com/atvirokodosprendimai/sdev1/internal/core/certs.Source.Client] for
// why `RootCAs` leaves no other option.
//
// ⚠ `InsecureSkipVerify` is not set and nothing exposes it. A test-only escape
// hatch is a production escape hatch with a comment on it, and the tests here
// need none.
func (c TLSConfig) Client() (*tls.Config, error) {
	source, err := c.Source()
	if err != nil {
		return nil, err
	}
	return source.Client(), nil
}

// Source turns declared paths into material that re-reads itself.
//
// ★ ADR-047 moved the loading here. What ADR-046 did once at construction now
// happens per connection, so rotating a certificate is replacing a file — and a
// failed reload keeps the last good material rather than stopping the node.
func (c TLSConfig) Source() (*certs.Source, error) {
	if c.CertFile == "" || c.KeyFile == "" || c.CAFile == "" {
		return nil, fmt.Errorf("%w: cert %q, key %q, ca %q",
			ErrNoTLS, c.CertFile, c.KeyFile, c.CAFile)
	}
	source, err := certs.NewSource(certs.Material{
		CertFile: c.CertFile, KeyFile: c.KeyFile, CAFile: c.CAFile,
	})
	if err != nil {
		// ⚠ ErrNoTLS stays the sentinel a caller matches on for "the material is
		// unusable", so ADR-046's construction refusals keep their meaning. An
		// expired certificate is reported as itself, because ErrExpired says
		// something ErrNoTLS does not.
		if errors.Is(err, certs.ErrExpired) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrNoTLS, err)
	}
	return source, nil
}

// PrincipalOf is the name the grant set is read for.
//
// ★★ IT READS [tls.ConnectionState.VerifiedChains], NEVER `PeerCertificates`.
// The two look interchangeable and are not: `PeerCertificates` is what the peer
// SENT, and it is populated whether or not verification succeeded or was even
// attempted. `VerifiedChains` is what was PROVED — it is empty unless a chain was
// actually built to a declared authority. Reading the first would turn an
// unverified claim into an identity, which is the whole failure this function
// exists at a single point to prevent.
//
// ⚠ The certificate answers WHO and never WHAT. No capability, scope or role is
// read from it — see ADR-046: a certificate is valid until it expires, so
// authority carried in one cannot be revoked, and a retraction would succeed
// while changing nothing.
func PrincipalOf(state *tls.ConnectionState) (string, error) {
	if state == nil || len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
		return "", fmt.Errorf("%w: no verified chain", ErrNoPrincipal)
	}
	name := state.VerifiedChains[0][0].Subject.CommonName
	if name == "" {
		return "", fmt.Errorf("%w: the verified leaf has an empty common name", ErrNoPrincipal)
	}
	return name, nil
}
