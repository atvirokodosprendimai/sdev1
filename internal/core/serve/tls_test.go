package serve_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/authz"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/leafstore"
	"github.com/atvirokodosprendimai/sdev1/internal/core/routing"
	"github.com/atvirokodosprendimai/sdev1/internal/core/serve"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// authority is a certificate authority and somewhere to write the files that
// prove membership of it.
//
// ⚠ EVERY TEST HERE NEEDS TWO OF THESE. With one, both peers are signed by the
// only trust anchor in the fixture, and an implementation that verified nothing
// at all would pass every assertion.
type authority struct {
	dir  string
	cert *x509.Certificate
	key  ed25519.PrivateKey
	file string // the CA's own PEM, for the pool
}

// sharedCA is the authority every ordinary test in this package uses, minted
// once for the whole binary.
//
// ★ Shared so that ADR-045's existing helpers keep their signatures: a test that
// does not care about certificates should not have to mention them. A test that
// DOES care mints its own, and that is how the second authority arrives.
var sharedCA *authority

// sharedGrants is the grant leaf every ordinary test's node reads.
//
// ★ It grants read on this package's tenant to the two principals the shared
// helpers issue certificates for. A test that is ABOUT authorization builds its
// own leaf instead, so that granting and revoking are its own to control.
var sharedGrants *leafstore.Store

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sdev1-ca")
	if err != nil {
		panic("serve_test: making a CA directory: " + err.Error())
	}
	sharedCA, err = mintAuthority(dir, "sdev1-test-ca")
	if err != nil {
		panic("serve_test: minting the shared CA: " + err.Error())
	}
	if sharedGrants, err = mintGrants(dir); err != nil {
		panic("serve_test: seeding the shared grants: " + err.Error())
	}

	code := m.Run()
	_ = sharedGrants.Close()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// grantsDir writes a fresh grant leaf on disk and returns its directory, for the
// binary test — which starts a separate PROCESS and so cannot share the
// in-process store.
func grantsDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	store, err := mintGrants(dir)
	if err != nil {
		t.Fatalf("seeding grants: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("closing the grant leaf: %v", err)
	}
	return filepath.Join(dir, "grants")
}

// mintGrants seeds a grant leaf permitting this package's ordinary principals to
// read this package's tenant.
func mintGrants(dir string) (*leafstore.Store, error) {
	store, err := leafstore.Open(filepath.Join(dir, "grants"), authz.SystemTenant.TenantSubtree())
	if err != nil {
		return nil, fmt.Errorf("opening the grant leaf: %w", err)
	}

	ctx := context.Background()
	var seq uint32
	for _, principal := range []string{"test-reader", "reader-2", "node", "friend"} {
		seq++
		id := tx.TxID{HLC: hlc.Timestamp{Wall: 1}, Seq: seq}
		d, err := authz.GrantDatom(principal, tenant(), authz.Read, id, 1)
		if err != nil {
			return nil, fmt.Errorf("granting %q: %w", principal, err)
		}
		if err := store.Append(ctx, d); err != nil {
			return nil, fmt.Errorf("appending a grant for %q: %w", principal, err)
		}
	}
	if err := store.Seal(ctx); err != nil {
		return nil, fmt.Errorf("sealing the grant leaf: %w", err)
	}
	return store, nil
}

// newAuthority mints a self-signed CA into its own directory.
func newAuthority(t *testing.T, name string) *authority {
	t.Helper()

	ca, err := mintAuthority(t.TempDir(), name)
	if err != nil {
		t.Fatalf("minting authority %q: %v", name, err)
	}
	return ca
}

// mintAuthority is newAuthority without a *testing.T, so TestMain can call it.
func mintAuthority(dir, name string) (*authority, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating a CA key: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, priv)
	if err != nil {
		return nil, fmt.Errorf("signing the CA: %w", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parsing the CA: %w", err)
	}

	ca := &authority{dir: dir, cert: parsed, key: priv}
	ca.file = filepath.Join(dir, "ca.pem")
	if err := writeFile(ca.file, "CERTIFICATE", der); err != nil {
		return nil, err
	}
	return ca, nil
}

// issue mints a leaf certificate whose Common Name is the principal.
//
// ★ The CN is the whole payload. No capability, scope or role is placed in the
// certificate — see ADR-046: a certificate is valid until it expires, so
// authority carried in one could not be revoked.
func (a *authority) issue(t *testing.T, principal string) serve.TLSConfig {
	t.Helper()

	conf, err := a.issueInto(t.TempDir(), principal)
	if err != nil {
		t.Fatalf("issuing for %q: %v", principal, err)
	}
	return conf
}

// issueInto is issue without a *testing.T, so a fixture outside a test can call
// it.
func (a *authority) issueInto(dir, principal string) (serve.TLSConfig, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return serve.TLSConfig{}, fmt.Errorf("generating a key for %q: %w", principal, err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: principal},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth,
		},
		// Both peers dial 127.0.0.1, so the server half needs it as a SAN.
		DNSNames:    []string{"localhost"},
		IPAddresses: localhost(),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.cert, pub, a.key)
	if err != nil {
		return serve.TLSConfig{}, fmt.Errorf("signing %q: %w", principal, err)
	}

	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	if err := writeFile(certFile, "CERTIFICATE", der); err != nil {
		return serve.TLSConfig{}, err
	}

	pkcs8, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return serve.TLSConfig{}, fmt.Errorf("marshalling the key for %q: %w", principal, err)
	}
	if err := writeFile(keyFile, "PRIVATE KEY", pkcs8); err != nil {
		return serve.TLSConfig{}, err
	}
	return serve.TLSConfig{CertFile: certFile, KeyFile: keyFile, CAFile: a.file}, nil
}

// localhost is what both peers dial, so it has to be in the certificate.
func localhost() []net.IP { return []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback} }

func writeFile(path, kind string, der []byte) error {
	body := pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// TestAnUntrustedCertificateIsRefused is ADR-046 rule 2.
//
// ⚠ The second authority is what makes this a test. Both peers are well-formed
// and both hold a valid chain to SOMETHING; only the declared pool separates
// them, so an implementation that skipped verification entirely fails here and
// nowhere else.
func TestAnUntrustedCertificateIsRefused(t *testing.T) {
	ours := newAuthority(t, "cluster-ca")
	theirs := newAuthority(t, "some-other-ca")

	key := addr.KeyOf(tenant(), "planet-7")
	held, err := addr.Descend(key, leafDepth)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}
	address := nodeWithTLS(t, held, routing.NewTable(), ours.issue(t, "node-a"))

	// A client whose certificate is signed by an authority this node never
	// declared, and which trusts that authority rather than ours.
	stranger := theirs.issue(t, "intruder")
	c := clientWithTLS(t, routing.Route{Prefix: held, NextHops: []string{address}, Epoch: 1}, 0, stranger)

	if _, err := c.Read(key, "READ name FROM planet-7", registered+1); err == nil {
		t.Fatal("a certificate from an undeclared authority was accepted.\n" +
			"Nil ClientCAs does not mean 'trust nothing' — it means the host's root store, " +
			"so every public CA on the machine could mint a peer for this cluster.")
	}

	// ★ And the same client, re-issued by the DECLARED authority, works — so the
	// refusal above is about the authority and not about something else broken.
	ok := ours.issue(t, "friend")
	good := clientWithTLS(t, routing.Route{Prefix: held, NextHops: []string{address}, Epoch: 1}, 0, ok)
	if _, err := good.Read(key, "READ name FROM planet-7", registered+1); err != nil {
		t.Fatalf("a certificate from the declared authority was refused: %v", err)
	}
}

// TestAClientCertificateIsRequired is the RequireAndVerify half of rule 1.
//
// ★ `tls.VerifyClientCertIfGiven` is the trap: it reads as "verify client
// certificates", it passes review, and a caller presenting none is admitted.
func TestAClientCertificateIsRequired(t *testing.T) {
	ca := newAuthority(t, "cluster-ca")

	key := addr.KeyOf(tenant(), "planet-7")
	held, err := addr.Descend(key, leafDepth)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}
	address := nodeWithTLS(t, held, routing.NewTable(), ca.issue(t, "node-a"))

	// A raw TLS dial that verifies the server and presents NOTHING of its own.
	pool := x509.NewCertPool()
	pem, err := os.ReadFile(ca.file)
	if err != nil {
		t.Fatalf("reading the CA: %v", err)
	}
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatal("the CA file holds no certificate")
	}

	conn, err := tls.Dial("tcp", address, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13})
	if err == nil {
		// ⚠ TLS 1.3 finishes the handshake lazily, so the dial may succeed and
		// the refusal arrive on the first read. Either is a pass; silence is not.
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var buf [1]byte
		_, err = conn.Read(buf[:])
		_ = conn.Close()
	}
	if err == nil {
		t.Fatal("a client presenting no certificate was served.\n" +
			"ClientAuth's zero value is tls.NoClientCert — every caller anonymous.")
	}
}

// TestThePrincipalIsTheCertificateSubject is rule 1's identity half.
func TestThePrincipalIsTheCertificateSubject(t *testing.T) {
	ca := newAuthority(t, "cluster-ca")

	// ⚠ Nothing verified means no principal — not an empty one, and not whatever
	// the peer happened to send.
	if _, err := serve.PrincipalOf(nil); !errors.Is(err, serve.ErrNoPrincipal) {
		t.Errorf("PrincipalOf(nil) = %v, want ErrNoPrincipal", err)
	}
	if _, err := serve.PrincipalOf(&tls.ConnectionState{}); !errors.Is(err, serve.ErrNoPrincipal) {
		t.Errorf("PrincipalOf(no verified chain) = %v, want ErrNoPrincipal", err)
	}

	// ★★ A state carrying a peer certificate that was NEVER VERIFIED yields no
	// principal. This is the whole reason the function reads VerifiedChains:
	// PeerCertificates is what the peer SENT, and reading it would turn an
	// unverified claim into an identity.
	claimed := &x509.Certificate{Subject: pkix.Name{CommonName: "impostor"}}
	unverified := &tls.ConnectionState{PeerCertificates: []*x509.Certificate{claimed}}
	if got, err := serve.PrincipalOf(unverified); !errors.Is(err, serve.ErrNoPrincipal) {
		t.Errorf("an unverified peer certificate produced principal %q (err %v).\n"+
			"PeerCertificates is populated whether or not verification happened.", got, err)
	}

	// A verified chain yields its leaf's common name.
	verified := &tls.ConnectionState{
		VerifiedChains: [][]*x509.Certificate{{
			{Subject: pkix.Name{CommonName: "reader-1"}}, ca.cert,
		}},
	}
	got, err := serve.PrincipalOf(verified)
	if err != nil {
		t.Fatalf("PrincipalOf(verified) = %v", err)
	}
	if got != "reader-1" {
		t.Errorf("principal = %q, want reader-1", got)
	}

	// An empty common name is refused rather than becoming an anonymous caller.
	anonymous := &tls.ConnectionState{
		VerifiedChains: [][]*x509.Certificate{{{Subject: pkix.Name{}}}},
	}
	if _, err := serve.PrincipalOf(anonymous); !errors.Is(err, serve.ErrNoPrincipal) {
		t.Errorf("an empty common name = %v, want ErrNoPrincipal", err)
	}

	// ★ And the same refusal on the SERVED PATH, over a real socket: a
	// certificate this cluster's own authority signed, carrying no name. The
	// chain verifies, so `RequireAndVerifyClientCert` is satisfied and the
	// handshake completes — only the empty-name check stands between it and being
	// served.
	key := addr.KeyOf(tenant(), "planet-7")
	held, err := addr.Descend(key, leafDepth)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}
	address := nodeWithTLS(t, held, routing.NewTable(), sharedCA.issue(t, "node"))

	route := routing.Route{Prefix: held, NextHops: []string{address}, Epoch: 1}

	nameless := clientWithTLS(t, route, 0, sharedCA.issue(t, ""))
	if _, err := nameless.Read(key, "READ name FROM planet-7", registered+1); err == nil {
		t.Error("a certificate carrying no principal was served.\n" +
			"A verified chain naming nobody is not an anonymous caller — it is a caller " +
			"whose grant set would be looked up under the empty string.")
	}

	// ⚠ The positive control. Without it the assertion above passes whenever the
	// read fails for ANY reason — a broken node, a bad fixture, a port nobody is
	// listening on — and it would keep passing after the empty-name check was
	// deleted.
	named := clientWithTLS(t, route, 0, sharedCA.issue(t, "reader-2"))
	if _, err := named.Read(key, "READ name FROM planet-7", registered+1); err != nil {
		t.Fatalf("a named certificate was refused by the same node: %v.\n"+
			"The refusal above therefore proves nothing about the name.", err)
	}
}

// TestTheTLSConfigRefusesItsFailOpenDefaults asserts the ABSENCE of four silent
// defaults, directly on the built configuration.
//
// ★ Every value checked here has a zero that means "permissive", so a working
// handshake proves nothing about them: a config with all four left alone still
// completes a handshake happily, with anonymous callers and the host's root
// store.
// effective resolves the configuration a handshake will ACTUALLY use.
//
// ⚠ ADR-047 moved the certificate and the authority pool behind
// `GetConfigForClient`, so that rotation reaches both. That means the outer
// config legitimately has a nil `ClientCAs` and a zero `ClientAuth` — and those
// are the exact values that mean "fail open" when there is no callback. Asserting
// on the outer config would therefore have started passing for the wrong reason
// the moment the callback was removed.
//
// ★ So the test asks the config what it would do, rather than what it holds.
func effective(t *testing.T, c *tls.Config) *tls.Config {
	t.Helper()

	if c.GetConfigForClient == nil {
		return c
	}
	got, err := c.GetConfigForClient(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("GetConfigForClient: %v", err)
	}
	if got == nil {
		t.Fatal("GetConfigForClient returned nil, so the outer config's zero values apply — " +
			"which is anonymous callers and the host root store")
	}
	return got
}

func TestTheTLSConfigRefusesItsFailOpenDefaults(t *testing.T) {
	ca := newAuthority(t, "cluster-ca")
	conf := ca.issue(t, "node-a")

	built, err := conf.Server()
	if err != nil {
		t.Fatalf("Server: %v", err)
	}
	server := effective(t, built)
	if server.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("ClientAuth = %v, want RequireAndVerifyClientCert.\n"+
			"The zero is NoClientCert, and VerifyClientCertIfGiven is the spelling that "+
			"reads as configured and admits a caller presenting nothing.", server.ClientAuth)
	}
	if server.ClientCAs == nil {
		t.Error("ClientCAs is nil, which is the host root store — every public CA may mint a peer")
	}

	// ★★ THE POOL HOLDS EXACTLY THE DECLARED AUTHORITY AND NOTHING ELSE.
	//
	// ⚠ This assertion exists because a mutant SURVIVED without it. Building the
	// pool from x509.SystemCertPool and appending our CA passes every
	// handshake test in this file — the legitimate peer still connects, and the
	// "untrusted" peer in TestAnUntrustedCertificateIsRefused is signed by a CA
	// this test minted, which is not in the host store either. So that test
	// cannot tell the two pools apart, and the property is about COMPOSITION
	// rather than about any handshake. Assert the composition.
	only := x509.NewCertPool()
	caPEM, err := os.ReadFile(conf.CAFile)
	if err != nil {
		t.Fatalf("reading the CA: %v", err)
	}
	if !only.AppendCertsFromPEM(caPEM) {
		t.Fatal("the CA file holds no certificate")
	}
	if !server.ClientCAs.Equal(only) {
		t.Error("ClientCAs holds more than the declared authority.\n" +
			"Seeding from the host trust store lets every public CA on the machine mint a " +
			"peer for this cluster, and no handshake test can see it.")
	}
	if server.MinVersion != tls.VersionTLS13 {
		t.Errorf("server MinVersion = %#x, want TLS 1.3 (%#x)", server.MinVersion, tls.VersionTLS13)
	}
	if server.InsecureSkipVerify {
		t.Error("InsecureSkipVerify is set on the server config")
	}

	client, err := conf.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	if client.RootCAs == nil {
		t.Error("RootCAs is nil, which is the host root store")
	}
	if !client.RootCAs.Equal(only) {
		t.Error("RootCAs holds more than the declared authority")
	}
	if client.MinVersion != tls.VersionTLS13 {
		t.Errorf("client MinVersion = %#x, want TLS 1.3 (%#x)", client.MinVersion, tls.VersionTLS13)
	}
	if client.InsecureSkipVerify {
		t.Error("InsecureSkipVerify is set on the client config")
	}
	if len(client.Certificates) == 0 {
		t.Error("the client presents no certificate, so it can carry no principal")
	}
}

// TestDeclaredTLSIsRequired is rule 2's declaration half.
func TestDeclaredTLSIsRequired(t *testing.T) {
	ca := newAuthority(t, "cluster-ca")
	full := ca.issue(t, "node-a")

	for _, c := range []struct {
		name string
		conf serve.TLSConfig
	}{
		{"nothing at all", serve.TLSConfig{}},
		{"no certificate", serve.TLSConfig{KeyFile: full.KeyFile, CAFile: full.CAFile}},
		{"no key", serve.TLSConfig{CertFile: full.CertFile, CAFile: full.CAFile}},
		{"no authority", serve.TLSConfig{CertFile: full.CertFile, KeyFile: full.KeyFile}},
	} {
		if _, err := c.conf.Server(); !errors.Is(err, serve.ErrNoTLS) {
			t.Errorf("Server with %s = %v, want ErrNoTLS", c.name, err)
		}
		if _, err := c.conf.Client(); !errors.Is(err, serve.ErrNoTLS) {
			t.Errorf("Client with %s = %v, want ErrNoTLS", c.name, err)
		}

		// And it is refused where a caller sees it: at construction.
		srv, err := serve.NewServer(serve.Options{
			Addr: "127.0.0.1:0", Leaf: addr.LeafID{Depth: 1}, Store: emptyReader{},
			Table: routing.NewTable(), ReadTimeout: time.Second, WriteTimeout: time.Second,
			MaxFrame: 1 << 20, TLS: c.conf, Grants: sharedGrants,
		})
		if !errors.Is(err, serve.ErrNoTLS) {
			if srv != nil {
				_ = srv.Close()
			}
			t.Errorf("NewServer with %s = %v, want ErrNoTLS", c.name, err)
		}
	}
}
