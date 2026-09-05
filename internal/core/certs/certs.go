package certs

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

var (
	// ErrAuthorityExists reports a mint into a directory that already holds one.
	//
	// ⚠ Refused rather than replaced, and this is the sharpest edge in the
	// package. Overwriting an authority key invalidates every certificate ever
	// issued from it — and the failure does not arrive here. It arrives later,
	// somewhere else, as a fleet of handshake errors that look like a network
	// problem.
	ErrAuthorityExists = errors.New("certs: an authority already exists here and will not be overwritten")

	// ErrNoSubject reports a certificate that would name nobody.
	//
	// ★ Refused where it is CREATED. ADR-046 refuses a nameless certificate at
	// the handshake, which is correct and far too late: by then somebody has
	// deployed it and is debugging a peer that will not connect.
	ErrNoSubject = errors.New("certs: a certificate must name a subject")

	// ErrNoAuthority reports a directory holding no authority to load.
	ErrNoAuthority = errors.New("certs: no authority in that directory")
)

// File names, fixed rather than configurable.
//
// ★ An operator who can rename these gains nothing and can mismatch them across
// machines, which fails as a handshake error naming the wrong thing.
const (
	AuthorityCert = "ca.pem"
	AuthorityKey  = "ca-key.pem"
	LeafCert      = "cert.pem"
	LeafKey       = "key.pem"
	SerialLog     = "issued.log"
)

// Lifetimes are conventions rather than decisions this package defends.
const (
	// DefaultAuthorityLife is how long a minted authority is valid.
	DefaultAuthorityLife = 365 * 24 * time.Hour
	// DefaultLeafLife is how long an issued certificate is valid.
	//
	// ⚠ Ninety days is short enough to make rotation a practised operation
	// rather than a yearly emergency, and long enough that nothing here needs to
	// automate it. Nothing watches this date; see the package comment.
	DefaultLeafLife = 90 * 24 * time.Hour
)

// Authority is a certificate authority: the certificate everyone trusts, and the
// key that must never leave the machine this runs on.
type Authority struct {
	// Dir is where the authority's own files live.
	Dir string

	cert *x509.Certificate
	key  ed25519.PrivateKey
}

// Certificate is the authority's public certificate, for a caller that wants to
// inspect it.
func (a *Authority) Certificate() *x509.Certificate { return a.cert }

// Mint creates a new authority.
//
// ⚠ It REFUSES when one already exists, and the guard runs before anything is
// written. See [ErrAuthorityExists].
//
// ★ Ed25519, with no curve or size option. Every knob here is a knob an operator
// can set wrong, and every wrong setting looks identical to a right one until
// somebody attacks it.
func Mint(dir, name string, life time.Duration) (*Authority, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: an authority needs a name", ErrNoSubject)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("certs: making %s: %w", dir, err)
	}

	// ⚠ BEFORE anything is created. An implementation that generated a key,
	// wrote it, and then noticed would have already done the damage.
	keyPath := filepath.Join(dir, AuthorityKey)
	if _, err := os.Stat(keyPath); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrAuthorityExists, keyPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("certs: checking %s: %w", keyPath, err)
	}

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("certs: generating an authority key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: name},
		// ⚠ Backdated by an hour so a node whose clock is slightly behind does
		// not reject a certificate minted moments ago. ADR-042 bounds skew for
		// transaction identifiers; nothing bounds it for a wall clock here.
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(life),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		// One CA, one leaf. Depth is an operator's decision to bring, not this
		// package's to invent.
		MaxPathLen:     0,
		MaxPathLenZero: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		return nil, fmt.Errorf("certs: signing the authority: %w", err)
	}
	if err := writePEM(filepath.Join(dir, AuthorityCert), "CERTIFICATE", der, 0o644); err != nil {
		return nil, err
	}

	pkcs8, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return nil, fmt.Errorf("certs: marshalling the authority key: %w", err)
	}
	// ⚠ 0o600. It is unencrypted — see the package comment — so the file mode is
	// the only protection this package provides.
	if err := writePEM(keyPath, "PRIVATE KEY", pkcs8, 0o600); err != nil {
		return nil, err
	}

	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("certs: parsing the authority: %w", err)
	}
	return &Authority{Dir: dir, cert: parsed, key: private}, nil
}

// Load reads an authority back, so `issue` can run against one minted long ago.
func Load(dir string) (*Authority, error) {
	certPEM, err := os.ReadFile(filepath.Join(dir, AuthorityCert))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrNoAuthority, dir, err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, AuthorityKey))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrNoAuthority, dir, err)
	}

	certBlock, _ := pem.Decode(certPEM)
	keyBlock, _ := pem.Decode(keyPEM)
	if certBlock == nil || keyBlock == nil {
		return nil, fmt.Errorf("%w: %s holds no PEM", ErrNoAuthority, dir)
	}

	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("certs: parsing the authority certificate: %w", err)
	}
	any, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("certs: parsing the authority key: %w", err)
	}
	key, ok := any.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%w: the authority key is %T, not ed25519", ErrNoAuthority, any)
	}
	return &Authority{Dir: dir, cert: cert, key: key}, nil
}

// Subject is who a certificate is for.
type Subject struct {
	// Name becomes the Common Name, which ADR-046 reads as the PRINCIPAL. It is
	// the whole payload: no capability, scope or role is placed in a certificate.
	Name string

	// Hosts are the names and addresses a SERVER will be reached at. Empty for a
	// certificate that only ever dials.
	Hosts []string

	// Life defaults to [DefaultLeafLife].
	Life time.Duration
}

// Issued is what one issuance produced.
type Issued struct {
	// Dir holds the certificate, its key, and a copy of the authority's
	// certificate. ⚠ It never holds the authority's KEY.
	Dir string
	// Serial identifies this certificate, and is what [DenyDatom] names.
	Serial string
	// NotAfter is when it stops being valid, which is also how long a denial of
	// it must be retained.
	NotAfter time.Time
}

// Issue signs a leaf certificate for a subject.
//
// ★ What lands in `dir` is exactly what a node needs and nothing more: its
// certificate, its key, and a COPY of the authority's certificate so it can check
// its peers. ⚠ The authority's private key is never written here — a node holding
// it could mint peers, and one compromise would become all of them.
func Issue(a *Authority, dir string, s Subject) (Issued, error) {
	if s.Name == "" {
		return Issued{}, ErrNoSubject
	}
	if s.Life <= 0 {
		s.Life = DefaultLeafLife
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Issued{}, fmt.Errorf("certs: making %s: %w", dir, err)
	}

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Issued{}, fmt.Errorf("certs: generating a key for %q: %w", s.Name, err)
	}
	serial, err := newSerial()
	if err != nil {
		return Issued{}, err
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: s.Name},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(s.Life),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		// Both, because a node is a server to its callers and a client to its
		// peers, and issuing two certificates for one identity would mean two
		// serials to deny when one key leaks.
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth,
		},
	}
	for _, host := range s.Hosts {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
			continue
		}
		template.DNSNames = append(template.DNSNames, host)
	}

	der, err := x509.CreateCertificate(rand.Reader, template, a.cert, public, a.key)
	if err != nil {
		return Issued{}, fmt.Errorf("certs: signing %q: %w", s.Name, err)
	}
	if err := writePEM(filepath.Join(dir, LeafCert), "CERTIFICATE", der, 0o644); err != nil {
		return Issued{}, err
	}

	pkcs8, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return Issued{}, fmt.Errorf("certs: marshalling the key for %q: %w", s.Name, err)
	}
	if err := writePEM(filepath.Join(dir, LeafKey), "PRIVATE KEY", pkcs8, 0o600); err != nil {
		return Issued{}, err
	}

	// A copy of the authority's CERTIFICATE, so the bundle is self-contained.
	authorityPEM, err := os.ReadFile(filepath.Join(a.Dir, AuthorityCert))
	if err != nil {
		return Issued{}, fmt.Errorf("certs: reading the authority certificate: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, AuthorityCert), authorityPEM, 0o644); err != nil {
		return Issued{}, fmt.Errorf("certs: copying the authority certificate: %w", err)
	}

	issued := Issued{Dir: dir, Serial: FormatSerial(serial), NotAfter: template.NotAfter}

	// ★ Recorded where the operator can read it. A serial nobody wrote down
	// cannot be denied, so the denial in T3 would be unreachable in practice
	// without this.
	if err := record(a.Dir, s.Name, issued); err != nil {
		return Issued{}, err
	}
	return issued, nil
}

// FormatSerial renders a serial number canonically.
//
// ⚠ ONE SPELLING, used in both directions. `big.Int` renders differently
// depending on how it is asked, and a denial written as one spelling and read as
// another denies nothing at all — silently, because both sides look right.
func FormatSerial(n *big.Int) string { return fmt.Sprintf("%040x", n) }

// newSerial returns an unpredictable 128-bit serial.
//
// ★ Random rather than a counter: a counter is a second piece of state to keep
// consistent across every machine that ever issues, and it buys nothing here.
func newSerial() (*big.Int, error) {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("certs: generating a serial: %w", err)
	}
	return n, nil
}

// record appends one line to the authority's issuance log.
func record(dir, name string, i Issued) error {
	line := fmt.Sprintf("%s\t%s\t%s\n", i.Serial, name, i.NotAfter.UTC().Format(time.RFC3339))
	f, err := os.OpenFile(filepath.Join(dir, SerialLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("certs: opening the issuance log: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(line); err != nil {
		return fmt.Errorf("certs: recording a serial: %w", err)
	}
	return nil
}

func writePEM(path, kind string, der []byte, mode os.FileMode) error {
	body := pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})
	if err := os.WriteFile(path, body, mode); err != nil {
		return fmt.Errorf("certs: writing %s: %w", path, err)
	}
	return nil
}
