package certs

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	// ErrExpired reports certificate material whose leaf has already expired.
	//
	// ⚠ Refused at LOAD rather than installed. It is a configuration error, not a
	// transient, and a node serving an expired certificate makes every handshake
	// fail with an error naming the PEER — which points whoever is debugging at
	// the wrong machine entirely.
	ErrExpired = errors.New("certs: the certificate has already expired")

	// ErrNoMaterial reports a source built with a path missing.
	ErrNoMaterial = errors.New("certs: a certificate, a key and an authority must all be named")
)

// Material is what one end presents and what it trusts.
type Material struct {
	CertFile string
	KeyFile  string
	CAFile   string
}

// loaded is one successful read of the files.
type loaded struct {
	pair tls.Certificate
	pool *x509.CertPool
	// leaf is parsed once so an expiry can be checked without re-parsing.
	leaf *x509.Certificate
}

// Source is certificate material that is RE-READ PER CONNECTION.
//
// ★ Rotation is therefore replacing a file: no restart, no signal handler, no
// watcher goroutine. The connection is the event, and there is no other moment at
// which the material matters.
//
// ⚠⚠ A FAILED RELOAD KEEPS THE LAST GOOD MATERIAL. Failing closed feels like the
// secure choice and it turns a routine rotation into a fleet outage: the file on
// disk is the thing in doubt, while what is already in hand has been working. A
// half-written copy, a truncated scp or a typo must be a log line rather than an
// outage — and an operator replacing a file is not guaranteed to have used an
// atomic rename.
//
// It is safe for concurrent use.
type Source struct {
	material Material

	mu   sync.RWMutex
	good *loaded
	// lastErr is the most recent reload failure, kept so an operator can be told
	// that the material on disk is not the material in use.
	lastErr error
}

// NewSource loads once and refuses a misconfiguration at construction.
//
// ★ Refusing here rather than at the first connection is the same discipline
// ADR-046 applies to timeouts and to the frame bound: a node that starts and then
// cannot serve reports its misconfiguration to whoever was unlucky enough to
// connect first.
func NewSource(m Material) (*Source, error) {
	if m.CertFile == "" || m.KeyFile == "" || m.CAFile == "" {
		return nil, fmt.Errorf("%w: cert %q, key %q, ca %q",
			ErrNoMaterial, m.CertFile, m.KeyFile, m.CAFile)
	}

	s := &Source{material: m}
	first, err := s.read()
	if err != nil {
		return nil, err
	}
	s.good = first
	return s, nil
}

// read loads the files and refuses material that is already expired.
func (s *Source) read() (*loaded, error) {
	pair, err := tls.LoadX509KeyPair(s.material.CertFile, s.material.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("certs: loading the key pair: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("certs: parsing the certificate: %w", err)
	}
	// ⚠ Refused, not installed. See [ErrExpired].
	if now := time.Now(); now.After(leaf.NotAfter) {
		return nil, fmt.Errorf("%w: %s expired at %s",
			ErrExpired, s.material.CertFile, leaf.NotAfter.UTC().Format(time.RFC3339))
	}

	pem, err := os.ReadFile(s.material.CAFile)
	if err != nil {
		return nil, fmt.Errorf("certs: reading the certificate authority: %w", err)
	}
	// ★ A pool of exactly what was declared, never seeded from the host trust
	// store — nil there means every public CA on the machine may mint a peer.
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("certs: %s holds no certificate", s.material.CAFile)
	}
	return &loaded{pair: pair, pool: pool, leaf: leaf}, nil
}

// current re-reads, and falls back to the last good material.
//
// ⚠ The fallback is the whole point and the error is still recorded, so an
// operator can find out that what is on disk is not what is being served.
func (s *Source) current() *loaded {
	next, err := s.read()

	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.lastErr = err
		return s.good
	}
	s.lastErr = nil
	s.good = next
	return next
}

// LastReloadError is the most recent failed reload, or nil.
//
// ★ Ordinary observability, and the only way to notice that a node is serving
// material older than the files beside it. A rotation that silently did not take
// is indistinguishable from one that did, until the certificate expires.
func (s *Source) LastReloadError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastErr
}

// NotAfter is when the material in use expires.
func (s *Source) NotAfter() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.good.leaf.NotAfter
}

// Server builds a listener configuration that re-reads per connection.
//
// ⚠ Every fail-open default in [crypto/tls] is a ZERO VALUE, which is why four
// things are set explicitly here. `ClientAuth`'s zero asks for no certificate at
// all; a nil `ClientCAs` means the host trust store rather than "trust nothing";
// an unset `MinVersion` is a version nobody chose. See ADR-046.
func (s *Source) Server() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		// ★ Called once per connection, which is what makes the CA pool rotate
		// too — and rotating the pool is what allows a CA changeover at all,
		// because both authorities must be trusted through the overlap.
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			now := s.current()
			return &tls.Config{
				Certificates: []tls.Certificate{now.pair},
				ClientCAs:    now.pool,
				ClientAuth:   tls.RequireAndVerifyClientCert,
				MinVersion:   tls.VersionTLS13,
			}, nil
		},
	}
}

// Client builds a dialler configuration from the material as it is NOW.
//
// ⚠ CALL IT PER DIAL, not once per client. `tls.Config.RootCAs` has no
// per-connection callback the way `ClientCAs` effectively does through
// `GetConfigForClient`, so a client's TRUST cannot rotate inside one config —
// the config itself has to be rebuilt. Returning a fresh one every time is the
// only shape that makes both halves rotate, and building it once is the mistake
// this comment exists to prevent.
//
// ⚠ `InsecureSkipVerify` is never set and nothing exposes it, including for
// tests: a test-only escape hatch is a production escape hatch with a comment on
// it.
func (s *Source) Client() *tls.Config {
	now := s.current()
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      now.pool,
		Certificates: []tls.Certificate{now.pair},
	}
}
