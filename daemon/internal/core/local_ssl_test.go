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

	"github.com/manuel-steinberg/wharf/daemon/internal/certs"
	"github.com/manuel-steinberg/wharf/daemon/internal/ipc"
	"github.com/manuel-steinberg/wharf/daemon/internal/runtime"
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
	// mkcert is neither in bin/mkcert/ nor on the PATH — the script below
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
	// An approved prompt runs the program, as the real adapters do.
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

	// Then mkcert is downloaded into "bin/mkcert/" and its checksum verified
	if w.served != 1 {
		t.Fatalf("mkcert downloaded %d times, want 1", w.served)
	}
	if _, err := os.Stat(filepath.Join(h.root.MkcertBin(), "mkcert")); err != nil {
		t.Fatalf("mkcert not in bin/mkcert/: %v", err)
	}
	// And a certificate for "my-kirby-site.wharf" is issued
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

	// Then an elevation prompt asks to trust the local certificate authority
	if h.el.RunCount() != 1 {
		t.Fatalf("%d elevated runs, want 1", h.el.RunCount())
	}
	if run := h.el.Runs[0]; !slices.Contains(run, "-install") {
		t.Fatalf("elevated run = %v, want mkcert -install", run)
	}
	// The authority itself was created unprivileged, so it belongs to the user.
	owner, _ := os.ReadFile(filepath.Join(w.caRoot, "rootCA.pem"))
	if strings.TrimSpace(string(owner)) != "owner=nss" {
		t.Fatalf("authority created by %q, want the unprivileged nss-only run", owner)
	}
	// And, once approved, browsers accept the project's certificate
	if !h.d.State().SSL.Trusted {
		t.Fatal("authority not reported as trusted")
	}

	// Once trusted, no project asks again.
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

	// Then SSL is still enabled for "my-kirby-site"
	if err != nil {
		t.Fatalf("a declined trust prompt failed the change: %v", err)
	}
	if !updated.SSL || !strings.HasPrefix(updated.URL, "https://") {
		t.Fatalf("ssl = %v url = %q, want SSL on", updated.SSL, updated.URL)
	}
	// And the GUI says browsers will warn until the authority is trusted
	st := h.d.State().SSL
	if !st.Installed || st.Trusted {
		t.Fatalf("ssl status = %+v, want installed but not trusted", st)
	}
	// And Settings offers to ask again
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

	// Then SSL stays off for "my-kirby-site"
	if h.project("my-kirby-site").SSL {
		t.Fatal("SSL turned on without mkcert")
	}
	// And the GUI reports that mkcert could not be fetched
	if !errors.Is(err, certs.ErrFetch) {
		t.Fatalf("err = %v, want ErrFetch", err)
	}
	var ipcErr *ipc.Error
	if !asIPC(asIPCError(err), &ipcErr) || ipcErr.Code != ipc.CodeOffline {
		t.Fatalf("ipc error = %v, want code %s", asIPCError(err), ipc.CodeOffline)
	}
}

// features/local-ssl.feature — "A project on its own webserver instance gets
// its own HTTPS port"
func TestAProjectOnItsOwnInstanceGetsItsOwnHTTPSPort(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mustAdd("legacy-app")
	if p := h.project("legacy-app"); p.Port != 8081 {
		t.Fatalf("legacy-app port = %d, want 8081", p.Port)
	}
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

	// Then "legacy-app" is served over HTTPS on port 8444
	conf := h.readGenerated("project-legacy-app-apache.conf")
	if !strings.Contains(conf, "Listen 8444") || !strings.Contains(conf, "<VirtualHost *:8444>") {
		t.Fatalf("no HTTPS listener on 8444:\n%s", conf)
	}
	// And its URL is "https://legacy-app.wharf:8444"
	if updated.URL != "https://legacy-app.wharf:8444" {
		t.Fatalf("URL = %q, want https://legacy-app.wharf:8444", updated.URL)
	}
	// And it does not compete with the global webserver for port 443
	if strings.Contains(conf, "Listen 443\n") || strings.Contains(conf, "*:443>") {
		t.Fatalf("the project instance binds 443:\n%s", conf)
	}
}
