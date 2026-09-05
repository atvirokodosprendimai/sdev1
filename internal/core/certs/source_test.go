package certs_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/certs"
)

// materialFor is the paths of an issued bundle.
func materialFor(dir string) certs.Material {
	return certs.Material{
		CertFile: filepath.Join(dir, certs.LeafCert),
		KeyFile:  filepath.Join(dir, certs.LeafKey),
		CAFile:   filepath.Join(dir, certs.AuthorityCert),
	}
}

// listen starts a TLS listener over a source and echoes the peer's common name
// back, so a caller can see WHICH certificate the server presented.
func listen(t *testing.T, s *certs.Source) string {
	t.Helper()

	l, err := tls.Listen("tcp", "127.0.0.1:0", s.Server())
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				secure, ok := conn.(*tls.Conn)
				if !ok {
					return
				}
				_ = secure.SetDeadline(time.Now().Add(5 * time.Second))
				if err := secure.Handshake(); err != nil {
					return
				}
				_, _ = secure.Write([]byte("ok"))
			}()
		}
	}()
	return l.Addr().String()
}

// dialed returns the common name the SERVER presented, or an error.
//
// ★ The name is what proves which file was read: a stale certificate would still
// verify and still complete a handshake, so "it connected" says nothing about
// whether the replacement was picked up.
func dialed(address string, client certs.Material) (string, error) {
	source, err := certs.NewSource(client)
	if err != nil {
		return "", err
	}
	conn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: 5 * time.Second}, "tcp", address, source.Client())
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()

	if err := conn.Handshake(); err != nil {
		return "", err
	}

	// ⚠ READ, do not stop at the handshake. TLS 1.3 finishes the CLIENT's side
	// before the server has validated the client's certificate, so a rejected
	// caller sees a successful Handshake and learns the truth on its first read.
	// A test that stopped here would report every refusal as a success — and
	// this one did, until the authority-rotation test caught it.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var ack [2]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil {
		return "", fmt.Errorf("the server did not serve us: %w", err)
	}

	chains := conn.ConnectionState().VerifiedChains
	if len(chains) == 0 || len(chains[0]) == 0 {
		return "", errors.New("no verified chain")
	}
	return chains[0][0].Subject.CommonName, nil
}

// TestReplacedMaterialIsPickedUpWithoutARestart is ADR-047 rule 4.
func TestReplacedMaterialIsPickedUpWithoutARestart(t *testing.T) {
	a := mint(t, "cluster-ca")

	serverDir := t.TempDir()
	if _, err := certs.Issue(a, serverDir, certs.Subject{
		Name: "node-before", Hosts: []string{"127.0.0.1", "localhost"},
	}); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	source, err := certs.NewSource(materialFor(serverDir))
	if err != nil {
		t.Fatalf("NewSource: %v", err)
	}
	address := listen(t, source)

	clientDir := t.TempDir()
	if _, err := certs.Issue(a, clientDir, certs.Subject{Name: "caller"}); err != nil {
		t.Fatalf("Issue(caller): %v", err)
	}
	client := materialFor(clientDir)

	if got, err := dialed(address, client); err != nil {
		t.Fatalf("the first dial: %v", err)
	} else if got != "node-before" {
		t.Fatalf("the server presented %q, want node-before", got)
	}

	// ★ Replace the certificate IN PLACE, with one carrying a different name.
	// The listener is never touched.
	replacement := t.TempDir()
	if _, err := certs.Issue(a, replacement, certs.Subject{
		Name: "node-after", Hosts: []string{"127.0.0.1", "localhost"},
	}); err != nil {
		t.Fatalf("Issue(replacement): %v", err)
	}
	copyOver(t, replacement, serverDir, certs.LeafCert, certs.LeafKey)

	got, err := dialed(address, client)
	if err != nil {
		t.Fatalf("the dial after replacement: %v", err)
	}
	if got != "node-after" {
		t.Errorf("the server still presents %q after its certificate was replaced.\n"+
			"Material loaded once at construction means rotating requires a restart, and an "+
			"expiry nobody watched takes the cluster down at a moment nothing chose.", got)
	}
}

// TestAFailedReloadKeepsTheLastGoodMaterial is ADR-047 rule 5.
//
// ⚠ The assertion is that the connection STILL WORKS. "A reload returned an
// error" would pass for an implementation that returned the error and dropped the
// certificate — which is a node that stops serving because somebody's scp was
// interrupted.
func TestAFailedReloadKeepsTheLastGoodMaterial(t *testing.T) {
	a := mint(t, "cluster-ca")

	serverDir := t.TempDir()
	if _, err := certs.Issue(a, serverDir, certs.Subject{
		Name: "node-a", Hosts: []string{"127.0.0.1", "localhost"},
	}); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	source, err := certs.NewSource(materialFor(serverDir))
	if err != nil {
		t.Fatalf("NewSource: %v", err)
	}
	address := listen(t, source)

	clientDir := t.TempDir()
	if _, err := certs.Issue(a, clientDir, certs.Subject{Name: "caller"}); err != nil {
		t.Fatalf("Issue(caller): %v", err)
	}
	client := materialFor(clientDir)

	if _, err := dialed(address, client); err != nil {
		t.Fatalf("the first dial: %v", err)
	}

	// A half-written copy. An operator replacing a file is not guaranteed to
	// have used an atomic rename, and this is what a reader sees if they did not.
	if err := os.WriteFile(filepath.Join(serverDir, certs.LeafCert),
		[]byte("-----BEGIN CERTIFICATE-----\ntruncated"), 0o644); err != nil {
		t.Fatalf("corrupting the certificate: %v", err)
	}

	got, err := dialed(address, client)
	if err != nil {
		t.Fatalf("the server stopped serving after a corrupt file appeared beside it: %v.\n"+
			"The file on disk is the thing in doubt; the material already in hand has been "+
			"working. Failing closed here turns a routine rotation into a fleet outage.", err)
	}
	if got != "node-a" {
		t.Errorf("the server presented %q, want the last good node-a", got)
	}

	// ★ And the failure is REPORTED, so an operator can find out that what is on
	// disk is not what is being served. A rotation that silently did not take is
	// indistinguishable from one that did, until the certificate expires.
	if source.LastReloadError() == nil {
		t.Error("the failed reload was not reported, so nothing can tell an operator " +
			"that the material in use is older than the files beside it")
	}
}

// TestAnExpiredCertificateIsRefusedAtLoad is ADR-047 rule 6.
func TestAnExpiredCertificateIsRefusedAtLoad(t *testing.T) {
	a := mint(t, "cluster-ca")

	dir := t.TempDir()
	if _, err := certs.Issue(a, dir, certs.Subject{Name: "node-a"}); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// ⚠ `Issue` cannot make an expired certificate — a non-positive life takes
	// the default, deliberately, so nobody issues one by mistyping a flag. So the
	// fixture writes one by hand. ★ That is the honest shape: the only way to get
	// an expired certificate is to not use the issuer, which is exactly the
	// situation this refusal exists for.
	expire(t, dir)

	if _, err := certs.NewSource(materialFor(dir)); !errors.Is(err, certs.ErrExpired) {
		t.Errorf("NewSource with an expired certificate = %v, want ErrExpired.\n"+
			"Installing one makes every handshake fail with an error naming the PEER, "+
			"which points the diagnosis at the wrong machine.", err)
	}
}

// TestTheAuthorityPoolRotatesToo is ADR-047 rule 4 applied to trust.
//
// ★ This is what makes a CA changeover possible at all: both authorities must be
// trusted through the overlap, and a pool loaded once could never hold the second.
func TestTheAuthorityPoolRotatesToo(t *testing.T) {
	ours := mint(t, "cluster-ca")
	theirs := mint(t, "second-ca")

	serverDir := t.TempDir()
	if _, err := certs.Issue(ours, serverDir, certs.Subject{
		Name: "node-a", Hosts: []string{"127.0.0.1", "localhost"},
	}); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	source, err := certs.NewSource(materialFor(serverDir))
	if err != nil {
		t.Fatalf("NewSource: %v", err)
	}
	address := listen(t, source)

	// A caller from the SECOND authority, which the server does not yet trust.
	// It has to trust the server, so its own CA file names the first authority.
	strangerDir := t.TempDir()
	if _, err := certs.Issue(theirs, strangerDir, certs.Subject{Name: "stranger"}); err != nil {
		t.Fatalf("Issue(stranger): %v", err)
	}
	stranger := materialFor(strangerDir)
	stranger.CAFile = filepath.Join(serverDir, certs.AuthorityCert)

	if _, err := dialed(address, stranger); err == nil {
		t.Fatal("a caller from an undeclared authority was accepted before it was added")
	}

	// ★ Append the second authority to the pool file. No restart.
	first, err := os.ReadFile(filepath.Join(ours.Dir, certs.AuthorityCert))
	if err != nil {
		t.Fatalf("reading the first authority: %v", err)
	}
	second, err := os.ReadFile(filepath.Join(theirs.Dir, certs.AuthorityCert))
	if err != nil {
		t.Fatalf("reading the second authority: %v", err)
	}
	if err := os.WriteFile(filepath.Join(serverDir, certs.AuthorityCert),
		append(first, second...), 0o644); err != nil {
		t.Fatalf("appending the second authority: %v", err)
	}

	if _, err := dialed(address, stranger); err != nil {
		t.Errorf("a caller from the newly trusted authority was still refused: %v.\n"+
			"A pool loaded once can never grow, so retiring a CA would mean restarting "+
			"every node at the same moment.", err)
	}

	// And the first authority still works, which is what an overlap means.
	callerDir := t.TempDir()
	if _, err := certs.Issue(ours, callerDir, certs.Subject{Name: "caller"}); err != nil {
		t.Fatalf("Issue(caller): %v", err)
	}
	if _, err := dialed(address, materialFor(callerDir)); err != nil {
		t.Errorf("the original authority stopped working when the second was added: %v", err)
	}
}

// expire replaces the leaf in dir with a self-signed one that expired yesterday.
//
// ⚠ Self-signed, because what is being tested is the EXPIRY check at load and
// that runs before anything about the chain. Signing it properly would need the
// authority's key, which this package deliberately does not hand out.
func expire(t *testing.T, dir string) {
	t.Helper()

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "long-gone"},
		NotBefore:    time.Now().Add(-72 * time.Hour),
		NotAfter:     time.Now().Add(-24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatalf("signing an expired certificate: %v", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatalf("marshalling the key: %v", err)
	}

	write := func(name, kind string, body []byte) {
		out := pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: body})
		if err := os.WriteFile(filepath.Join(dir, name), out, 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	write(certs.LeafCert, "CERTIFICATE", der)
	write(certs.LeafKey, "PRIVATE KEY", pkcs8)
}

// copyOver replaces named files in dst with the ones in src.
func copyOver(t *testing.T, src, dst string, names ...string) {
	t.Helper()

	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dst, name), body, 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
}
