// Package certs issues local TLS certificates with mkcert, which is already
// cross-platform and is therefore used unmodified (dev/architecture.md §4).
//
// The user never installs or configures mkcert (local-ssl.feature): Setup
// downloads a pinned release into bin/mkcert/, creates the local certificate
// authority as the user, and asks once for administrator rights to trust it.
package certs

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/download"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/elevate"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/layout"
)

// ErrMkcertMissing reports that mkcert is neither vendored nor on PATH.
var ErrMkcertMissing = errors.New("mkcert is not installed — SSL cannot be enabled")

// ErrFetch reports that mkcert could not be downloaded
// (local-ssl.feature, "mkcert cannot be downloaded").
var ErrFetch = errors.New("mkcert could not be fetched — check your internet connection")

// Status is what the GUI needs to know about SSL as a whole.
type Status struct {
	// Installed reports that mkcert is present, vendored or on PATH.
	Installed bool `json:"installed"`
	// Trusted reports that the system trusts mkcert's local certificate
	// authority, so browsers accept the certificates without a warning.
	Trusted bool `json:"trusted"`
	// CARoot is where the certificate authority's files live.
	CARoot string `json:"ca_root,omitempty"`
}

// Issuer creates certificates for hostnames.
type Issuer interface {
	Issue(ctx context.Context, hostname, certFile, keyFile string) error
	Revoke(certFile, keyFile string) error
	// Status is cheap: it is read for every state snapshot.
	Status() Status
	// Setup makes SSL work: mkcert installed, authority created and trusted.
	// A declined trust prompt returns elevate.ErrDeclined; certificates can
	// still be issued, browsers just warn about them.
	Setup(ctx context.Context) error
}

// Version is the mkcert release Wharf downloads. It is pinned, and so are
// the checksums below: this binary installs a root certificate, so it is
// never taken on trust from whatever "latest" happens to be.
const Version = "v1.4.4"

// Checksums are the SHA-256 of each release asset, keyed by GOOS/GOARCH.
var Checksums = map[string]string{
	"darwin/amd64":  "a32dfab51f1845d51e810db8e47dcf0e6b51ae3422426514bf5a2b8302e97d4e",
	"darwin/arm64":  "c8af0df44bce04359794dad8ea28d750437411d632748049d08644ffb66a60c6",
	"linux/amd64":   "6d31c65b03972c6dc4a14ab429f2928300518b26503f58723e532d1b0a3bbb52",
	"linux/arm64":   "b98f2cc69fd9147fe4d405d859c57504571adec0d3611c3eefd04107c7ac00d0",
	"windows/amd64": "d2660b50a9ed59eada480750561c96abc2ed4c9a38c6a24d93e30e0977631398",
	"windows/arm64": "793747256c562622d40127c8080df26add2fb44c50906ce9db63b42a5280582e",
}

// Mkcert shells out to the mkcert binary.
type Mkcert struct {
	Root     layout.Root
	Elevator elevate.Elevator

	// Client, BaseURL and Checksums locate and verify the download; tests
	// point them at a local server.
	Client    *http.Client
	BaseURL   string
	Checksums map[string]string
	// TrustCheck reports whether the authority in caRoot is trusted. The
	// default asks the system, exactly as mkcert itself does.
	TrustCheck func(caRoot string) bool

	mu     sync.Mutex
	status *Status
}

// New returns an Issuer for the given root.
func New(root layout.Root, el elevate.Elevator) *Mkcert {
	return &Mkcert{
		Root:      root,
		Elevator:  el,
		BaseURL:   "https://github.com/FiloSottile/mkcert/releases/download",
		Checksums: Checksums,
	}
}

func exeName() string {
	if runtime.GOOS == "windows" {
		return "mkcert.exe"
	}
	return "mkcert"
}

func (m *Mkcert) vendored() string { return filepath.Join(m.Root.MkcertBin(), exeName()) }

// binary prefers the vendored copy under bin/, falling back to PATH so a
// developer who already has mkcert installed is not made to re-download it.
func (m *Mkcert) binary() (string, error) {
	if _, err := os.Stat(m.vendored()); err == nil {
		return m.vendored(), nil
	}
	p, err := exec.LookPath(exeName())
	if err != nil {
		return "", ErrMkcertMissing
	}
	return p, nil
}

// Issue writes a certificate and key for hostname.
func (m *Mkcert) Issue(ctx context.Context, hostname, certFile, keyFile string) error {
	bin, err := m.binary()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(certFile), 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, "-cert-file", certFile, "-key-file", keyFile, hostname)
	cmd.Dir = m.Root.CertDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mkcert failed for %s: %w: %s", hostname, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Revoke deletes a project's certificate files. mkcert has no revocation of
// its own; removing the files is what "SSL off" means here.
func (m *Mkcert) Revoke(certFile, keyFile string) error {
	for _, p := range []string{certFile, keyFile} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// Status returns the cached status, computing it once. It is refreshed by
// Setup — the only thing in Wharf that changes it.
func (m *Mkcert) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status == nil {
		st := m.probe(context.Background())
		m.status = &st
	}
	return *m.status
}

func (m *Mkcert) refresh(ctx context.Context) Status {
	st := m.probe(ctx)
	m.mu.Lock()
	m.status = &st
	m.mu.Unlock()
	return st
}

func (m *Mkcert) probe(ctx context.Context) Status {
	bin, err := m.binary()
	if err != nil {
		return Status{}
	}
	st := Status{Installed: true}
	st.CARoot, _ = m.caRoot(ctx, bin)
	if st.CARoot != "" {
		st.Trusted = m.trusted(st.CARoot)
	}
	return st
}

func (m *Mkcert) caRoot(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-CAROOT").Output()
	if err != nil {
		return "", fmt.Errorf("mkcert -CAROOT: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (m *Mkcert) trusted(caRoot string) bool {
	if m.TrustCheck != nil {
		return m.TrustCheck(caRoot)
	}
	return systemTrusts(caRoot)
}

// systemTrusts verifies the authority's certificate against the system's
// roots — the same check mkcert uses to decide whether -install is needed.
func systemTrusts(caRoot string) bool {
	raw, err := os.ReadFile(filepath.Join(caRoot, "rootCA.pem"))
	if err != nil {
		return false
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	_, err = cert.Verify(x509.VerifyOptions{})
	return err == nil
}

// Setup installs, creates and trusts, skipping whatever is already done, so
// it is safe to call every time a project turns SSL on.
func (m *Mkcert) Setup(ctx context.Context) error {
	bin, err := m.binary()
	if errors.Is(err, ErrMkcertMissing) {
		bin, err = m.fetch(ctx)
	}
	if err != nil {
		return err
	}

	caRoot, err := m.caRoot(ctx, bin)
	if err != nil {
		return err
	}
	// The authority is created unprivileged, so its files belong to the user
	// and later certificates can be issued without a prompt. Limiting the
	// trust stores to NSS (Firefox's, per user) keeps mkcert from reaching
	// for sudo at this step.
	if _, err := os.Stat(filepath.Join(caRoot, "rootCA.pem")); err != nil {
		cmd := exec.CommandContext(ctx, bin, "-install")
		cmd.Env = append(os.Environ(), "CAROOT="+caRoot, "TRUST_STORES=nss")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("create the local certificate authority: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}

	if !m.trusted(caRoot) {
		err := m.Elevator.RequestElevatedRun(bin, []string{"-install"},
			[]string{"CAROOT=" + caRoot, "TRUST_STORES=system"})
		m.refresh(ctx)
		return err
	}
	m.refresh(ctx)
	return nil
}

// fetch downloads the pinned mkcert release into bin/mkcert/ and verifies it.
func (m *Mkcert) fetch(ctx context.Context) (string, error) {
	platform := runtime.GOOS + "/" + runtime.GOARCH
	sum, ok := m.Checksums[platform]
	if !ok {
		return "", fmt.Errorf("mkcert publishes no build for %s — install it yourself and put it on the PATH", platform)
	}
	asset := fmt.Sprintf("mkcert-%s-%s-%s", Version, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		asset += ".exe"
	}

	if err := os.MkdirAll(m.Root.MkcertBin(), 0o755); err != nil {
		return "", err
	}
	tmp := m.vendored() + ".download"
	defer os.Remove(tmp)
	_, err := download.File(ctx, m.Client, m.BaseURL+"/"+Version+"/"+asset, tmp, sum)
	if errors.Is(err, download.ErrUnreachable) {
		return "", fmt.Errorf("%w (%v)", ErrFetch, err)
	}
	if err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, m.vendored()); err != nil {
		return "", err
	}
	return m.vendored(), nil
}

// Fake is an Issuer for tests: it records issued hostnames and writes
// placeholder files so callers that stat them succeed.
type Fake struct {
	mu      sync.Mutex
	Issued  map[string]string
	FailErr error

	// St is what Status reports. NewFake starts installed and trusted, so
	// tests that are not about setup never trigger it.
	St Status
	// SetupErr makes Setup fail, as an offline download would.
	SetupErr error
	// DeclineTrust makes Setup install but leave the authority untrusted.
	DeclineTrust bool
	SetupCalls   int
}

func NewFake() *Fake {
	return &Fake{Issued: map[string]string{}, St: Status{Installed: true, Trusted: true}}
}

func (f *Fake) Issue(ctx context.Context, hostname, certFile, keyFile string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailErr != nil {
		return f.FailErr
	}
	if !f.St.Installed {
		return ErrMkcertMissing
	}
	if err := os.MkdirAll(filepath.Dir(certFile), 0o755); err != nil {
		return err
	}
	for _, p := range []string{certFile, keyFile} {
		if err := os.WriteFile(p, []byte("fake "+hostname), 0o600); err != nil {
			return err
		}
	}
	f.Issued[hostname] = certFile
	return nil
}

func (f *Fake) Revoke(certFile, keyFile string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.Issued, filepath.Base(certFile))
	for _, p := range []string{certFile, keyFile} {
		os.Remove(p)
	}
	return nil
}

func (f *Fake) Status() Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.St
}

func (f *Fake) Setup(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SetupCalls++
	if f.SetupErr != nil {
		return f.SetupErr
	}
	f.St.Installed = true
	if f.DeclineTrust {
		return elevate.ErrDeclined
	}
	f.St.Trusted = true
	return nil
}
