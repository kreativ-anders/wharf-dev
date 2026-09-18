package core

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/certs"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/elevate"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/layout"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/php"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/shellpath"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/supervisor"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/webserver"
)

// testNow fixes the clock so PHP support statuses — which depend on the date —
// do not change the meaning of a test as time passes.
var testNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// harness is a daemon with every outside-world collaborator faked: no real
// processes, no real ports, no real mkcert, no password prompt. What is left
// under test is the behaviour the feature files describe.
type harness struct {
	t      *testing.T
	d      *Daemon
	root   layout.Root
	runner *supervisor.FakeRunner
	ports  *supervisor.FakePorts
	el     *elevate.Fake
	certs  *certs.Fake
	sup    *supervisor.Supervisor
	php    *fakeInstaller
	web    *fakeWebInstaller
	shell  *shellpath.Fake
	opts   Options
}

// stubWebservers stages nginx and Apache as Wharf's own copies. Apache is
// only usable with its modules beside it, so one module is staged too.
func stubWebservers(t *testing.T, root layout.Root) {
	t.Helper()
	stubBinary(t, exeName(filepath.Join(root.Bin(), "nginx", "nginx")))
	stubBinary(t, exeName(filepath.Join(root.Bin(), "apache", "httpd")))
	stubBinary(t, filepath.Join(root.Bin(), "apache", "modules", "mod_proxy_fcgi.so"))
}

// testWebDetector sees only what a test staged under bin/, never the
// webservers of the machine running the suite.
func testWebDetector() *webserver.Detector {
	return &webserver.Detector{
		Candidates: map[string][]string{},
		Probe:      func(context.Context, string) (string, error) { return "", nil },
	}
}

// fakeWebInstaller plays the part of a webserver install: by default a
// download that stages the binary.
type fakeWebInstaller struct {
	plan map[string]webserver.Plan
	fn   func(ctx context.Context, name, dest string) error
}

func (f *fakeWebInstaller) Plan(name string) webserver.Plan {
	if p, ok := f.plan[name]; ok {
		return p
	}
	return webserver.Plan{Installable: true, Via: "download"}
}

func (f *fakeWebInstaller) Install(ctx context.Context, name, dest string) error {
	if f.fn != nil {
		return f.fn(ctx, name, dest)
	}
	if name == webserver.Apache {
		stubFile(filepath.Join(dest, "modules", "mod_proxy_fcgi.so"))
		return stubFile(exeName(filepath.Join(dest, "bin", "httpd")))
	}
	return stubFile(exeName(filepath.Join(dest, "nginx")))
}

func stubFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755)
}

// fakeInstaller plays the part of the PHP download: by default it writes a
// stub build, as a successful download would.
type fakeInstaller struct {
	fn func(ctx context.Context, version, dest string) (string, error)
	// latest and latestErr answer a lookup of the newest releases.
	latest    map[string]string
	latestErr error
}

func (f *fakeInstaller) Latest(context.Context) (map[string]string, error) {
	return f.latest, f.latestErr
}

func (f *fakeInstaller) Install(ctx context.Context, version, dest string) (string, error) {
	if f.fn != nil {
		return f.fn(ctx, version, dest)
	}
	for _, name := range []string{php.CLIName(), php.FastCGIName(), exeName("php-cgi")} {
		if err := os.WriteFile(filepath.Join(dest, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			return "", err
		}
	}
	return version + ".0", nil
}

// newHarness builds the daemon under test. Options can be adjusted before it
// is constructed, for the few tests that swap a fake for something real.
func newHarness(t *testing.T, adjust ...func(*Options)) *harness {
	t.Helper()
	dir := t.TempDir()
	root := layout.Root{Dir: dir}
	if err := root.Ensure(); err != nil {
		t.Fatal(err)
	}
	// INFO: The resolver refuses to build a spec for a binary that is not there, so
	// the vendored layout is stubbed out.
	stubWebservers(t, root)
	for _, v := range []string{"8.1", "8.2", "8.3"} {
		stubBinary(t, filepath.Join(root.PHPBin(v), php.FastCGIName()))
		stubBinary(t, exeName(filepath.Join(root.PHPBin(v), "php-cgi")))
	}

	ports := supervisor.NewFakePorts()
	runner := supervisor.NewFakeRunner(ports)
	sup := supervisor.New(runner, ports)
	sup.StartTimeout = 2 * time.Second
	sup.StopTimeout = 2 * time.Second
	sup.Poll = time.Millisecond

	el := elevate.NewFake()
	ca := certs.NewFake()

	store, err := config.Load(root.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	// INFO: The daemon's first-run setup detects the stubbed 8.1/8.2/8.3 builds and
	// selects the newest supported one, so the harness does not preset them.

	installer := &fakeInstaller{}
	webFake := &fakeWebInstaller{}
	// WARNING: Never the real one: it would write the shell startup files of
	// whoever runs the suite.
	shell := shellpath.NewFake()
	opts := Options{
		Root:  root,
		Store: store,
		// WARNING: Detection must see only what the test staged: no probing of the
		// machine the suite happens to run on.
		Detector: &php.Detector{
			VendorDir:  filepath.Join(root.Bin(), "php"),
			Candidates: []string{},
			Probe:      func(context.Context, string) (string, error) { return "", nil },
		},
		Now: func() time.Time { return testNow },

		Supervisor:   sup,
		Elevator:     el,
		Certs:        ca,
		PHPInstaller: installer,
		WebDetector:  testWebDetector(),
		WebInstaller: webFake,
		ShellPath:    shell,
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, fn := range adjust {
		fn(&opts)
	}
	d, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
	})

	return &harness{t: t, d: d, root: root, runner: runner, ports: ports, el: el, certs: ca, sup: sup, php: installer, web: webFake, shell: shell, opts: opts}
}

func stubBinary(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// exeName names a stubbed binary the way the daemon looks for it on this OS:
// Windows knows an executable by its .exe suffix, not by a mode bit.
func exeName(path string) string {
	if goruntime.GOOS == "windows" {
		return path + ".exe"
	}
	return path
}

// mkProject creates a folder under www/, which is all it takes to make a
// project exist.
func (h *harness) mkProject(name string) {
	h.t.Helper()
	if err := os.MkdirAll(h.root.ProjectDir(name), 0o755); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) ctx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	h.t.Cleanup(cancel)
	return ctx
}

func (h *harness) mustAdd(name string) Project {
	h.t.Helper()
	h.mkProject(name)
	p, err := h.d.AddProject(h.ctx(), name)
	if err != nil {
		h.t.Fatalf("add project %s: %v", name, err)
	}
	return p
}

func (h *harness) project(name string) Project {
	h.t.Helper()
	for _, p := range h.d.State().Projects {
		if p.Name == name {
			return p
		}
	}
	h.t.Fatalf("project %q not in state", name)
	return Project{}
}

var includeRe = regexp.MustCompile(`(?m)^\s*(?:include|Include) "([^"]+)";?$`)

// readGenerated returns a generated server config by file name, with the
// per-project files it includes inlined — the configuration the webserver
// actually sees.
func (h *harness) readGenerated(name string) string {
	h.t.Helper()
	gen := filepath.ToSlash(filepath.Join(h.root.Data(), "gen"))
	body, err := os.ReadFile(filepath.Join(h.root.Data(), "gen", name))
	if err != nil {
		h.t.Fatalf("read generated config %s: %v", name, err)
	}
	return includeRe.ReplaceAllStringFunc(string(body), func(line string) string {
		path := includeRe.FindStringSubmatch(line)[1]
		if !strings.HasPrefix(path, gen) || strings.HasSuffix(path, "fastcgi_params") || strings.HasSuffix(path, "mime.types") {
			return line
		}
		included, err := os.ReadFile(filepath.FromSlash(path))
		if err != nil {
			h.t.Fatalf("read included config %s: %v", path, err)
		}
		return string(included)
	})
}

// shortSocket returns a socket path well inside the ~104-byte sun_path limit.
// Go's t.TempDir() paths on macOS are long enough to exceed it on their own.
func shortSocket(t *testing.T) string {
	t.Helper()
	// INFO: Windows serves IPC over TCP, where the path's length does not matter.
	base := "/tmp"
	if goruntime.GOOS == "windows" {
		base = ""
	}
	dir, err := os.MkdirTemp(base, "wh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "w.sock")
}
