package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/certs"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/ipc"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/runtime"
)

// mkcertWorld is the outside world of an SSL setup: a download server
// offering a stand-in mkcert, and a certificate authority folder that the
// stand-in fills in the way the real one does.
type mkcertWorld struct {
	h      *harness
	caRoot string
	// served counts downloads of the binary.
	served int
}

// newMkcertWorld builds a harness whose Issuer is the real mkcert setup code,
// pointed at a local server. The stand-in binary is a shell script, so this
// runs where the suite runs: macOS and Linux.
func newMkcertWorld(t *testing.T, offline bool) *mkcertWorld {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("the stand-in mkcert is a shell script")
	}
	// INFO: mkcert is neither in bin/mkcert/ nor on the PATH — the script below
	// uses absolute paths so it still runs.
	t.Setenv("PATH", "")

	w := &mkcertWorld{caRoot: filepath.Join(t.TempDir(), "ca")}
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
  -CAROOT) echo '%[1]s' ;;
  -install)
    /bin/mkdir -p '%[1]s'
    [ -f '%[1]s/rootCA.pem' ] || echo "owner=$TRUST_STORES" > '%[1]s/rootCA.pem'
    [ "$TRUST_STORES" = system ] && echo trusted > '%[1]s/trusted'
    ;;
  -cert-file) echo cert > "$2"; echo key > "$4" ;;
esac
exit 0
`, w.caRoot)
	sum := sha256.Sum256([]byte(script))

	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if offline {
			http.Error(rw, "offline", http.StatusBadGateway)
			return
		}
		w.served++
		fmt.Fprint(rw, script)
	}))
	t.Cleanup(srv.Close)

	w.h = newHarness(t, func(o *Options) {
		m := certs.New(o.Root, o.Elevator)
		m.BaseURL = srv.URL
		m.Checksums = map[string]string{goruntime.GOOS + "/" + goruntime.GOARCH: hex.EncodeToString(sum[:])}
		m.TrustCheck = func(caRoot string) bool {
			_, err := os.Stat(filepath.Join(caRoot, "trusted"))
			return err == nil
		}
		o.Certs = m
	})
	// INFO: An approved prompt runs the program, as the real adapters do.
	w.h.el.OnRun = func(program string, args, env []string) error {
		cmd := exec.Command(program, args...)
		cmd.Env = env
		return cmd.Run()
	}
	return w
}

func enableSSL(h *harness, name string) (Project, error) {
	on := true
	return h.d.UpdateSettings(h.ctx(), name, Settings{SSL: &on})
}

// features/local-ssl.feature — "Enabling SSL when mkcert is not installed"
func TestEnablingSSLWhenMkcertIsNotInstalled(t *testing.T) {
	w := newMkcertWorld(t, false)
	h := w.h
	h.mustAdd("my-kirby-site")
	if h.d.State().SSL.Installed {
		t.Fatal("mkcert reported as installed before setup")
	}

	if _, err := enableSSL(h, "my-kirby-site"); err != nil {
		t.Fatalf("enable ssl: %v", err)
	}

	// INFO: Then mkcert is downloaded into "bin/mkcert/" and its checksum verified
	if w.served != 1 {
		t.Fatalf("mkcert downloaded %d times, want 1", w.served)
	}
	if _, err := os.Stat(filepath.Join(h.root.MkcertBin(), "mkcert")); err != nil {
		t.Fatalf("mkcert not in bin/mkcert/: %v", err)
	}
	// INFO: And a certificate for "my-kirby-site.localhost" is issued
	if _, err := os.Stat(runtime.CertPath(h.root, "my-kirby-site")); err != nil {
		t.Fatalf("no certificate issued: %v", err)
	}
	if !h.project("my-kirby-site").SSL {
		t.Fatal("SSL is not on")
	}
}

// A download that does not match the pinned checksum is never run.
func TestAnMkcertWithTheWrongChecksumIsRejected(t *testing.T) {
	w := newMkcertWorld(t, false)
	m := w.h.d.certs.(*certs.Mkcert)
	m.Checksums = map[string]string{goruntime.GOOS + "/" + goruntime.GOARCH: strings.Repeat("0", 64)}
	w.h.mustAdd("my-kirby-site")

	_, err := enableSSL(w.h, "my-kirby-site")
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v, want a checksum mismatch", err)
	}
	if _, err := os.Stat(filepath.Join(w.h.root.MkcertBin(), "mkcert")); !os.IsNotExist(err) {
		t.Fatal("the mismatched binary was kept")
	}
}

// features/local-ssl.feature — "Trusting the local certificate authority"
func TestTrustingTheLocalCertificateAuthority(t *testing.T) {
	w := newMkcertWorld(t, false)
	h := w.h
	h.mustAdd("my-kirby-site")
	h.mustAdd("other-site")

	if _, err := enableSSL(h, "my-kirby-site"); err != nil {
		t.Fatal(err)
	}

	// INFO: Then an elevation prompt asks to trust the local certificate authority
	if h.el.RunCount() != 1 {
		t.Fatalf("%d elevated runs, want 1", h.el.RunCount())
	}
	if run := h.el.Runs[0]; !slices.Contains(run, "-install") {
		t.Fatalf("elevated run = %v, want mkcert -install", run)
	}
	// INFO: The authority itself was created unprivileged, so it belongs to the user.
	owner, _ := os.ReadFile(filepath.Join(w.caRoot, "rootCA.pem"))
	if strings.TrimSpace(string(owner)) != "owner=nss" {
		t.Fatalf("authority created by %q, want the unprivileged nss-only run", owner)
	}
	// INFO: And, once approved, browsers accept the project's certificate
	if !h.d.State().SSL.Trusted {
		t.Fatal("authority not reported as trusted")
	}

	// INFO: Once trusted, no project asks again.
	if _, err := enableSSL(h, "other-site"); err != nil {
		t.Fatal(err)
	}
	if h.el.RunCount() != 1 {
		t.Fatalf("a second project prompted again: %d runs", h.el.RunCount())
	}
}

// features/local-ssl.feature — "Trust is declined"
func TestTrustIsDeclined(t *testing.T) {
	w := newMkcertWorld(t, false)
	h := w.h
	h.mustAdd("my-kirby-site")
	h.el.Decline = true

	updated, err := enableSSL(h, "my-kirby-site")

	// INFO: Then SSL is still enabled for "my-kirby-site"
	if err != nil {
		t.Fatalf("a declined trust prompt failed the change: %v", err)
	}
	if !updated.SSL || !strings.HasPrefix(updated.URL, "https://") {
		t.Fatalf("ssl = %v url = %q, want SSL on", updated.SSL, updated.URL)
	}
	// INFO: And the GUI says browsers will warn until the authority is trusted
	st := h.d.State().SSL
	if !st.Installed || st.Trusted {
		t.Fatalf("ssl status = %+v, want installed but not trusted", st)
	}
	// INFO: And Settings offers to ask again
	h.el.Decline = false
	if err := h.d.SetupSSL(h.ctx()); err != nil {
		t.Fatalf("asking again: %v", err)
	}
	if !h.d.State().SSL.Trusted {
		t.Fatal("still untrusted after approving the second prompt")
	}
}

// features/local-ssl.feature — "mkcert cannot be downloaded"
func TestMkcertCannotBeDownloaded(t *testing.T) {
	w := newMkcertWorld(t, true)
	h := w.h
	h.mustAdd("my-kirby-site")

	_, err := enableSSL(h, "my-kirby-site")

	// INFO: Then SSL stays off for "my-kirby-site"
	if h.project("my-kirby-site").SSL {
		t.Fatal("SSL turned on without mkcert")
	}
	// INFO: And the GUI reports that mkcert could not be fetched
	if !errors.Is(err, certs.ErrFetch) {
		t.Fatalf("err = %v, want ErrFetch", err)
	}
	var ipcErr *ipc.Error
	if !asIPC(asIPCError(err), &ipcErr) || ipcErr.Code != ipc.CodeOffline {
		t.Fatalf("ipc error = %v, want code %s", asIPCError(err), ipc.CodeOffline)
	}
}

// features/local-ssl.feature — "HTTPS for a project on the other webserver
// ends at the front door"
func TestHTTPSForAProjectOnTheOtherWebserverEndsAtTheFrontDoor(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mustAdd("legacy-app")
	apache := "apache"
	if _, err := h.d.UpdateSettings(h.ctx(), "legacy-app", Settings{Webserver: &apache}); err != nil {
		t.Fatal(err)
	}
	if err := h.d.StartProject(h.ctx(), "legacy-app"); err != nil {
		t.Fatal(err)
	}

	updated, err := enableSSL(h, "legacy-app")
	if err != nil {
		t.Fatal(err)
	}

	// INFO: Then the global nginx serves "legacy-app.localhost" on port 443 with its
	// certificate
	front := h.readGenerated("nginx.conf")
	cert := filepath.ToSlash(runtime.CertPath(h.root, "legacy-app"))
	if !strings.Contains(front, "listen 443 ssl;") || !strings.Contains(front, cert) {
		t.Fatalf("nginx does not terminate HTTPS for legacy-app:\n%s", front)
	}
	// INFO: And forwards each request to "legacy-app"'s own apache, which tells PHP
	// it was HTTPS on port 443
	if !strings.Contains(front, "proxy_set_header X-Forwarded-Proto $scheme;") {
		t.Fatalf("nginx does not forward the scheme:\n%s", front)
	}
	own := h.readGenerated("project-legacy-app-apache.conf")
	if strings.Contains(own, "SSLEngine") || strings.Contains(own, "Listen 443") || strings.Contains(own, ":443>") {
		t.Fatalf("legacy-app's own apache handles TLS itself:\n%s", own)
	}
	if !strings.Contains(own, `ProxyFCGISetEnvIf "%{HTTP:X-Forwarded-Proto} == 'https'" HTTPS "on"`) {
		t.Fatalf("PHP is not told the request was HTTPS:\n%s", own)
	}
	// INFO: And its URL is "https://legacy-app.localhost"
	if updated.URL != "https://legacy-app.localhost" {
		t.Fatalf("URL = %q, want https://legacy-app.localhost", updated.URL)
	}
}
