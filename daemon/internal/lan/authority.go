package lan

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Lifetimes. The authority is installed by hand on a phone, so it lasts a
// year: one forgotten there stops vouching for anything on its own. A
// certificate it issues is re-issued freely, so it lasts a month.
const (
	AuthorityLifetime   = 365 * 24 * time.Hour
	CertificateLifetime = 30 * 24 * time.Hour
	// renewBefore is how long before the end a certificate is re-issued, so
	// one never runs out while it is served.
	renewBefore = 7 * 24 * time.Hour
	// authorityRenewBefore replaces the authority before a phone's copy
	// could lapse in the middle of a certificate's month.
	authorityRenewBefore = CertificateLifetime
)

// CAFileName is the authority's certificate as a phone downloads it: DER,
// which iOS and Android both install from a download.
const CAFileName = "wharf-network-ca.crt"

// privateRanges are the only addresses the authority may sign for: the
// private IPv4 ranges (RFC 1918) and IPv6 unique local addresses (RFC 4193).
var privateRanges = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"}

// noName is the one DNS name, e-mail domain and URI domain the authority may
// sign for. "invalid" is reserved and never resolves (RFC 6761), so in effect
// it may sign for no name at all.
const noName = "invalid"

// Authority is Wharf's network certificate authority: a certificate
// authority of its own, separate from mkcert's, for the projects shared on
// the network (sharing.feature). mkcert's authority is trusted by this
// machine for every name; this one is installed on a phone, so it is
// constrained to private addresses — its key, were it ever copied, could not
// vouch for a real website.
type Authority struct {
	// Dir is where it keeps its files; see layout.Root.NetworkCADir.
	Dir string
	// Now is the clock; tests move it.
	Now func() time.Time
	// Host names the machine in the authority's name, so a phone that has
	// several lists which is which.
	Host string

	mu sync.Mutex
}

// Info describes the authority as the GUI shows it.
type Info struct {
	// Fingerprint is the SHA-256 of its certificate, as the phone shows it
	// before installing: pairs of hex digits, separated by spaces.
	Fingerprint string    `json:"fingerprint"`
	Expires     time.Time `json:"expires"`
	// File is the certificate a phone downloads, for "Show file".
	File string `json:"file"`
}

func (a *Authority) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Authority) caCert() string    { return filepath.Join(a.Dir, "ca.pem") }
func (a *Authority) caKey() string     { return filepath.Join(a.Dir, "ca-key.pem") }
func (a *Authority) leafCert() string  { return filepath.Join(a.Dir, "address.pem") }
func (a *Authority) leafKey() string   { return filepath.Join(a.Dir, "address-key.pem") }
func (a *Authority) PublicDir() string { return filepath.Join(a.Dir, "public") }
func (a *Authority) PublicFile() string {
	return filepath.Join(a.PublicDir(), CAFileName)
}

// Info describes the authority, if it exists and is still in use.
func (a *Authority) Info() (Info, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cert, _, err := a.load()
	if err != nil || !a.current(cert) {
		return Info{}, false
	}
	return info(cert, a.PublicFile()), true
}

// Ensure creates the authority unless one exists that is still in use.
func (a *Authority) Ensure() (Info, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cert, _, err := a.ensure()
	if err != nil {
		return Info{}, err
	}
	return info(cert, a.PublicFile()), nil
}

// Certificate returns the certificate for addr and its key, issuing a new
// one when there is none for addr yet, it runs out soon, or the authority
// behind it was replaced. The phone trusts every one of them through the
// authority, so a new address needs nothing new on the phone.
func (a *Authority) Certificate(addr netip.Addr) (certFile, keyFile string, err error) {
	if !Signable(addr) {
		return "", "", fmt.Errorf("%s is not a private network address; Wharf shares projects on a local network only", addr)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	ca, caKey, err := a.ensure()
	if err != nil {
		return "", "", err
	}
	if a.leafFits(ca, addr) {
		return a.leafCert(), a.leafKey(), nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	now := a.now()
	notAfter := now.Add(CertificateLifetime)
	if notAfter.After(ca.NotAfter) {
		notAfter = ca.NotAfter
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		// INFO: No common name. A verifier may read one that looks like a
		// host name as a DNS name, which the authority may not sign for; the
		// address is in the subject alternative name, where it belongs.
		Subject:     pkix.Name{Organization: []string{"Wharf"}},
		NotBefore:   now.Add(-time.Hour),
		NotAfter:    notAfter,
		IPAddresses: []net.IP{addr.AsSlice()},
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return "", "", err
	}
	if err := writeKey(a.leafKey(), key); err != nil {
		return "", "", err
	}
	if err := writePEM(a.leafCert(), "CERTIFICATE", der, 0o644); err != nil {
		return "", "", err
	}
	return a.leafCert(), a.leafKey(), nil
}

// Current reports whether the certificate for addr can be served as it is:
// issued by the authority in use, for addr, and not about to run out.
func (a *Authority) Current(addr netip.Addr) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	ca, _, err := a.load()
	return err == nil && a.current(ca) && a.leafFits(ca, addr)
}

// Replace deletes the authority, its key and what it issued. The next
// Ensure creates a new one; every copy of the old one on a phone is left
// with no key that could ever sign for it again (sharing.feature,
// "Replacing the network certificate authority").
func (a *Authority) Replace() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return os.RemoveAll(a.Dir)
}

// ensure loads the authority, creating a new one when none is in use.
// Callers hold mu.
func (a *Authority) ensure() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cert, key, err := a.load()
	if err == nil && a.current(cert) {
		return cert, key, nil
	}
	// INFO: What the old authority issued goes with it: nothing signed by a
	// deleted key is served again.
	if err := os.RemoveAll(a.Dir); err != nil {
		return nil, nil, err
	}
	return a.create()
}

// current reports whether cert may still be handed to a phone: it lasts
// longer than the certificates it issues.
func (a *Authority) current(cert *x509.Certificate) bool {
	return a.now().Add(authorityRenewBefore).Before(cert.NotAfter)
}

func (a *Authority) create() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	var permitted []*net.IPNet
	for _, cidr := range privateRanges {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, nil, err
		}
		permitted = append(permitted, n)
	}
	name := "Wharf network CA"
	if a.Host != "" {
		name += " (" + a.Host + ")"
	}
	now := a.now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: name, Organization: []string{"Wharf"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(AuthorityLifetime),
		IsCA:                  true,
		BasicConstraintsValid: true,
		// INFO: It signs certificates for a webserver and nothing else: no
		// authority below it, no other use.
		MaxPathLen:     0,
		MaxPathLenZero: true,
		KeyUsage:       x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		ExtKeyUsage:    []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		// WARNING: The name constraints are what make installing this on a
		// phone safe. Without them its key could vouch for any website.
		PermittedDNSDomainsCritical: true,
		PermittedIPRanges:           permitted,
		PermittedDNSDomains:         []string{noName},
		PermittedEmailAddresses:     []string{noName},
		PermittedURIDomains:         []string{noName},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	// WARNING: The key is 0600: it signs for every shared project. The folder
	// stays readable, because a webserver's workers — another user where it
	// was started as root — read public/ inside it, the only part ever served.
	if err := os.MkdirAll(a.Dir, 0o755); err != nil {
		return nil, nil, err
	}
	if err := writeKey(a.caKey(), key); err != nil {
		return nil, nil, err
	}
	if err := writePEM(a.caCert(), "CERTIFICATE", der, 0o644); err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(a.PublicDir(), 0o755); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(a.PublicFile(), der, 0o644); err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// load reads the authority's certificate and key.
func (a *Authority) load() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cert, err := readCert(a.caCert())
	if err != nil {
		return nil, nil, err
	}
	raw, err := os.ReadFile(a.caKey())
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, nil, errors.New("the network certificate authority's key is unreadable")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// leafFits reports whether the certificate on disk is for addr, issued by
// ca, and far enough from its end to be served.
func (a *Authority) leafFits(ca *x509.Certificate, addr netip.Addr) bool {
	cert, err := readCert(a.leafCert())
	if err != nil {
		return false
	}
	if _, err := os.Stat(a.leafKey()); err != nil {
		return false
	}
	if cert.CheckSignatureFrom(ca) != nil || !a.now().Add(renewBefore).Before(cert.NotAfter) {
		return false
	}
	return len(cert.IPAddresses) == 1 && cert.IPAddresses[0].Equal(addr.AsSlice())
}

func readCert(path string) (*x509.Certificate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%s holds no certificate", path)
	}
	return x509.ParseCertificate(block.Bytes)
}

func writeKey(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return writePEM(path, "EC PRIVATE KEY", der, 0o600)
}

// writePEM replaces a file through a rename, so a webserver starting
// meanwhile never reads half a certificate.
func writePEM(path, kind string, der []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pem-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := pem.Encode(tmp, &pem.Block{Type: kind, Bytes: der}); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		// WARNING: crypto/rand does not fail on any supported OS; a fixed
		// serial would only make two certificates look alike.
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}

func info(cert *x509.Certificate, file string) Info {
	sum := sha256.Sum256(cert.Raw)
	pairs := make([]string, len(sum))
	for i, b := range sum {
		pairs[i] = fmt.Sprintf("%02X", b)
	}
	return Info{Fingerprint: strings.Join(pairs, " "), Expires: cert.NotAfter, File: file}
}
