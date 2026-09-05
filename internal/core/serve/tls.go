package serve

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
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
	certificate, pool, err := c.load()
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{certificate},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// Client builds the dialler's configuration.
//
// ⚠ `InsecureSkipVerify` is not set and nothing exposes it. See the package
// comment: a test-only escape hatch is a production escape hatch with a comment
// on it, and the tests here need none.
func (c TLSConfig) Client() (*tls.Config, error) {
	certificate, pool, err := c.load()
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{certificate},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// load reads this end's key pair and the declared authority.
func (c TLSConfig) load() (tls.Certificate, *x509.CertPool, error) {
	if c.CertFile == "" || c.KeyFile == "" || c.CAFile == "" {
		return tls.Certificate{}, nil, fmt.Errorf("%w: cert %q, key %q, ca %q",
			ErrNoTLS, c.CertFile, c.KeyFile, c.CAFile)
	}

	certificate, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("serve: loading the key pair: %w", err)
	}
	pool, err := c.pool()
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	return certificate, pool, nil
}

// pool reads the declared authority into a pool of exactly one thing.
//
// ★ It starts from [x509.NewCertPool] and never from [x509.SystemCertPool]. A
// node's peers are its own cluster, so the set of authorities that may mint one
// is a set the operator chose — and starting from the system store makes that set
// "every CA trusted by this machine", which is not a decision anyone would write
// down but is exactly what the convenient constructor gives.
func (c TLSConfig) pool() (*x509.CertPool, error) {
	pem, err := os.ReadFile(c.CAFile)
	if err != nil {
		return nil, fmt.Errorf("serve: reading the certificate authority: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%w: %s holds no certificate", ErrNoTLS, c.CAFile)
	}
	return pool, nil
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
