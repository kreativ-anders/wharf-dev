package lan

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"
)

var start = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// clock is a test clock that moves only when told to.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newAuthority(t *testing.T) (*Authority, *clock) {
	t.Helper()
	c := &clock{t: start}
	return &Authority{Dir: filepath.Join(t.TempDir(), "network-ca"), Now: c.now, Host: "macbook"}, c
}

// verify checks a certificate the way a phone that trusts only root does,
// name constraints included.
func verify(t *testing.T, root *x509.Certificate, leaf *x509.Certificate, at time.Time, host string) error {
	t.Helper()
	roots := x509.NewCertPool()
	roots.AddCert(root)
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:       roots,
		CurrentTime: at,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return err
	}
	return leaf.VerifyHostname(host)
}

func mustCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	cert, err := readCert(path)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// phoneCopy is the authority as a phone installs it: the downloaded file.
func phoneCopy(t *testing.T, a *Authority) *x509.Certificate {
	t.Helper()
	der, err := os.ReadFile(a.PublicFile())
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// forge signs a certificate with the authority's own key, as someone who
// copied the key could.
func forge(t *testing.T, a *Authority, tmpl *x509.Certificate) *x509.Certificate {
	t.Helper()
	ca, caKey, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl.SerialNumber = serial()
	tmpl.NotBefore, tmpl.NotAfter = start.Add(-time.Hour), start.Add(24*time.Hour)
	tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// features/sharing.feature — "The network certificate authority cannot vouch
// for real websites"
func TestTheNetworkCertificateAuthorityCannotVouchForRealWebsites(t *testing.T) {
	a, _ := newAuthority(t)
	if _, err := a.Ensure(); err != nil {
		t.Fatal(err)
	}
	root := phoneCopy(t, a)

	// INFO: Then it may sign certificates for private network addresses only,
	// never for a domain name — even signed with its own key.
	certFile, _, err := a.Certificate(netip.MustParseAddr("192.168.1.23"))
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(t, root, mustCert(t, certFile), start, "192.168.1.23"); err != nil {
		t.Fatalf("a phone rejects the certificate for this machine's address: %v", err)
	}
	for _, forged := range []struct {
		host string
		tmpl *x509.Certificate
	}{
		{"example.com", &x509.Certificate{Subject: pkix.Name{CommonName: "example.com"}, DNSNames: []string{"example.com"}}},
		{"bank.localhost", &x509.Certificate{DNSNames: []string{"bank.localhost"}}},
		{"8.8.8.8", &x509.Certificate{IPAddresses: []net.IP{net.ParseIP("8.8.8.8")}}},
	} {
		if err := verify(t, root, forge(t, a, forged.tmpl), start, forged.host); err == nil {
			t.Errorf("a certificate for %s signed with the authority's key is accepted", forged.host)
		}
	}
	if _, _, err := a.Certificate(netip.MustParseAddr("203.0.113.7")); err == nil {
		t.Error("Wharf issued a certificate for a public address")
	}

	// INFO: And it expires after one year
	if got := root.NotAfter.Sub(start); got > AuthorityLifetime || got < AuthorityLifetime-time.Hour {
		t.Errorf("the authority lasts %v, not a year", got)
	}
	if !root.IsCA || root.MaxPathLen != 0 || !root.MaxPathLenZero {
		t.Errorf("the authority may delegate: IsCA %v, MaxPathLen %d", root.IsCA, root.MaxPathLen)
	}

	// INFO: And its private key stays in the Wharf folder, readable by the user
	// alone — and the folder that is served holds the certificate alone.
	if goruntime.GOOS != "windows" {
		for path, want := range map[string]os.FileMode{a.caKey(): 0o600, a.leafKey(): 0o600} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != want {
				t.Errorf("%s has mode %v, want %v", filepath.Base(path), got, want)
			}
		}
	}
	entries, err := os.ReadDir(a.PublicDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != CAFileName {
		t.Errorf("the served folder holds %v, not the certificate alone", entries)
	}
	if raw, _ := os.ReadFile(a.PublicFile()); len(raw) == 0 || pemHasKey(raw) {
		t.Error("the served certificate is empty or holds a key")
	}
}

func pemHasKey(raw []byte) bool {
	for {
		var block *pem.Block
		block, raw = pem.Decode(raw)
		if block == nil {
			return false
		}
		if block.Type != "CERTIFICATE" {
			return true
		}
	}
}

// features/sharing.feature — "A new address needs no new certificate on the
// phone"
func TestANewAddressNeedsNoNewCertificateOnThePhone(t *testing.T) {
	a, _ := newAuthority(t)
	first, _, err := a.Certificate(netip.MustParseAddr("192.168.1.23"))
	if err != nil {
		t.Fatal(err)
	}
	firstCert := mustCert(t, first)
	root := phoneCopy(t, a)
	before, _ := a.Info()

	// INFO: When this machine's address changes, Then Wharf issues a certificate
	// for it from the same authority, And a phone that trusts the authority
	// trusts the new certificate without installing anything.
	second, _, err := a.Certificate(netip.MustParseAddr("192.168.1.42"))
	if err != nil {
		t.Fatal(err)
	}
	secondCert := mustCert(t, second)
	if secondCert.SerialNumber.Cmp(firstCert.SerialNumber) == 0 {
		t.Fatal("no new certificate was issued for the new address")
	}
	if err := verify(t, root, secondCert, start, "192.168.1.42"); err != nil {
		t.Fatalf("the phone's copy of the authority does not vouch for the new address: %v", err)
	}
	if after, _ := a.Info(); after.Fingerprint != before.Fingerprint {
		t.Fatal("a new address replaced the authority, so the phone would need a new one")
	}
}

func TestACertificateIsReissuedOnlyWhenNeeded(t *testing.T) {
	a, c := newAuthority(t)
	addr := netip.MustParseAddr("10.0.0.5")
	file, _, err := a.Certificate(addr)
	if err != nil {
		t.Fatal(err)
	}
	first := mustCert(t, file)

	// INFO: The same address, a day later: the same certificate.
	c.t = start.Add(24 * time.Hour)
	if _, _, err := a.Certificate(addr); err != nil {
		t.Fatal(err)
	}
	if mustCert(t, file).SerialNumber.Cmp(first.SerialNumber) != 0 {
		t.Error("an unchanged address got a new certificate")
	}

	// INFO: A week before it runs out: a new one, from the same authority.
	before, _ := a.Info()
	c.t = first.NotAfter.Add(-6 * 24 * time.Hour)
	if _, _, err := a.Certificate(addr); err != nil {
		t.Fatal(err)
	}
	renewed := mustCert(t, file)
	if renewed.SerialNumber.Cmp(first.SerialNumber) == 0 {
		t.Error("a certificate about to run out was served again")
	}
	if after, _ := a.Info(); after.Fingerprint != before.Fingerprint {
		t.Error("renewing a certificate replaced the authority")
	}

	// INFO: A month before the authority runs out, it is replaced, so no
	// certificate outlives the copy on the phone.
	c.t = start.Add(AuthorityLifetime - 20*24*time.Hour)
	if _, ok := a.Info(); ok {
		t.Error("an authority about to run out is still offered to a phone")
	}
	if _, _, err := a.Certificate(addr); err != nil {
		t.Fatal(err)
	}
	if after, _ := a.Info(); after.Fingerprint == before.Fingerprint {
		t.Error("an authority about to run out was kept")
	}
}

// features/sharing.feature — "Replacing the network certificate authority"
func TestReplacingTheNetworkCertificateAuthorityDeletesItsKey(t *testing.T) {
	a, _ := newAuthority(t)
	file, _, err := a.Certificate(netip.MustParseAddr("192.168.1.23"))
	if err != nil {
		t.Fatal(err)
	}
	old := phoneCopy(t, a)
	oldKey := a.caKey()

	if err := a.Replace(); err != nil {
		t.Fatal(err)
	}
	// INFO: Then the old one's private key is deleted
	if _, err := os.Stat(oldKey); !os.IsNotExist(err) {
		t.Fatalf("the old authority's key is still there: %v", err)
	}
	// INFO: And the copy on the phone can no longer vouch for any certificate
	// Wharf serves.
	file, _, err = a.Certificate(netip.MustParseAddr("192.168.1.23"))
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(t, old, mustCert(t, file), start, "192.168.1.23"); err == nil {
		t.Fatal("the old authority still vouches for the certificate Wharf serves")
	}
	if err := verify(t, phoneCopy(t, a), mustCert(t, file), start, "192.168.1.23"); err != nil {
		t.Fatalf("the new authority does not vouch for its own certificate: %v", err)
	}
}

func TestSignableAddressesArePrivate(t *testing.T) {
	for addr, want := range map[string]bool{
		"192.168.1.23": true, "10.1.2.3": true, "172.20.0.1": true, "fd00::1": true,
		"8.8.8.8": false, "127.0.0.1": false, "169.254.1.1": false, "100.64.0.1": false,
	} {
		if got := Signable(netip.MustParseAddr(addr)); got != want {
			t.Errorf("Signable(%s) = %v, want %v", addr, got, want)
		}
	}
}
