package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manuel-steinberg/wharf/daemon/internal/ipc"
	"github.com/manuel-steinberg/wharf/daemon/internal/webserver"
)

// withoutWebserver removes Wharf's own copy of a webserver before the daemon
// starts, as on a machine where it was never installed.
func withoutWebserver(names ...string) func(*Options) {
	return func(o *Options) {
		for _, n := range names {
			os.RemoveAll(filepath.Join(o.Root.Bin(), n))
		}
	}
}

// features/webserver-install.feature — "First start adopts a webserver
// already on the machine"
func TestFirstStartAdoptsAWebserverAlreadyOnTheMachine(t *testing.T) {
	// Apache where macOS keeps it: the binary in sbin, its modules in
	// libexec/apache2.
	machine := t.TempDir()
	httpd := filepath.Join(machine, "usr", "sbin", "httpd")
	stubBinary(t, httpd)
	stubBinary(t, filepath.Join(machine, "usr", "libexec", "apache2", "mod_proxy_fcgi.so"))

	h := newHarness(t, withoutWebserver("nginx", "apache"), func(o *Options) {
		o.WebDetector.Candidates = map[string][]string{webserver.Apache: {httpd}}
	})

	// Then Apache is used where it is, without being copied
	apache := h.server("apache")
	if !apache.Installed || apache.Binary != httpd || apache.Source != "system" {
		t.Fatalf("apache = %+v, want the system copy at %s", apache, httpd)
	}
	if _, err := os.Stat(filepath.Join(h.root.Bin(), "apache")); !os.IsNotExist(err) {
		t.Fatal("apache was copied into bin/")
	}
	// And Apache is selected as the default webserver
	if got := h.d.Config().Services.Webserver.Active; got != "apache" {
		t.Fatalf("default webserver = %q, want apache", got)
	}
	h.mustAdd("my-kirby-site")
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatalf("start on the adopted apache: %v", err)
	}
	if conf := h.readGenerated("apache.conf"); !strings.Contains(conf,
		filepath.ToSlash(filepath.Join(machine, "usr", "libexec", "apache2", "mod_proxy_fcgi.so"))) {
		t.Fatalf("the adopted apache's modules are not loaded:\n%s", conf)
	}
}

// features/webserver-install.feature — "Installing nginx"
func TestInstallingNginx(t *testing.T) {
	h := newHarness(t, withoutWebserver("nginx"))
	if h.server("nginx").Installed {
		t.Fatal("nginx reported as installed before the install")
	}

	inside := make(chan bool)
	release := make(chan struct{})
	h.web.fn = func(ctx context.Context, name, dest string) error {
		inside <- h.server(name).Installing
		<-release
		return stubFile(filepath.Join(dest, "nginx"))
	}
	done := make(chan error)
	go func() { done <- h.d.InstallWebserver(h.ctx(), "nginx") }()

	// And Settings shows it as installing until it is done
	if !<-inside {
		t.Fatal("nginx not shown as installing mid-way")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("install: %v", err)
	}
	// Then the newest nginx for this OS and CPU is installed
	nginx := h.server("nginx")
	if !nginx.Installed || nginx.Installing || nginx.Binary != filepath.Join(h.root.Bin(), "nginx", "nginx") {
		t.Fatalf("nginx = %+v, want installed into bin/nginx", nginx)
	}
	// And every project can be served by nginx without further setup
	h.mustAdd("my-kirby-site")
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatalf("start on the installed nginx: %v", err)
	}
}

// features/webserver-install.feature — "A webserver install fails"
func TestAWebserverInstallFails(t *testing.T) {
	h := newHarness(t, withoutWebserver("nginx"))
	h.web.fn = func(_ context.Context, _, dest string) error {
		stubFile(filepath.Join(dest, "nginx.part"))
		return webserver.ErrFetch
	}

	err := h.d.InstallWebserver(h.ctx(), "nginx")

	// Then the GUI reports that it could not be fetched
	var ipcErr *ipc.Error
	if !errors.Is(err, webserver.ErrFetch) || !asIPC(asIPCError(err), &ipcErr) || ipcErr.Code != ipc.CodeOffline {
		t.Fatalf("err = %v, want an offline error", err)
	}
	// And no partial "bin/nginx" folder is left behind
	if _, err := os.Stat(filepath.Join(h.root.Bin(), "nginx")); !os.IsNotExist(err) {
		t.Fatal("bin/nginx exists after a failed install")
	}
	entries, _ := os.ReadDir(h.root.Bin())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".install") {
			t.Fatalf("staging folder %s left behind", e.Name())
		}
	}
}

// features/webserver-install.feature — "A webserver Wharf cannot install says
// how to get it"
func TestAWebserverWharfCannotInstallSaysHowToGetIt(t *testing.T) {
	h := newHarness(t, withoutWebserver("apache"))
	hint := "Install Apache with your package manager — sudo apt install apache2"
	h.web.plan = map[string]webserver.Plan{webserver.Apache: {Hint: hint}}

	apache := h.server("apache")
	// Then Apache offers no "Install" action
	if apache.Install.Installable {
		t.Fatal("apache offered as installable")
	}
	// And the GUI says how to install it instead
	if apache.Install.Hint != hint {
		t.Fatalf("hint = %q, want %q", apache.Install.Hint, hint)
	}
	var invalid *InvalidError
	if err := h.d.InstallWebserver(h.ctx(), "apache"); !errors.As(err, &invalid) || !strings.Contains(err.Error(), "apt install") {
		t.Fatalf("err = %v, want the hint as an invalid request", err)
	}
}

// features/webserver-install.feature — "One webserver, one config file per
// project"
func TestOneWebserverOneConfigFilePerProject(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mustAdd("other-site")

	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	if err := h.d.StartProject(h.ctx(), "other-site"); err != nil {
		t.Fatal(err)
	}

	// Then one nginx process serves both projects
	running := 0
	for _, st := range h.sup.Statuses() {
		if st.State == "running" && !strings.HasPrefix(st.ID, "php:") {
			running++
		}
	}
	if running != 1 {
		t.Fatalf("%d webserver processes running, want 1", running)
	}
	// And each project's server block is generated into its own file under
	// "data/gen/nginx/"
	gen := filepath.Join(h.root.Data(), "gen")
	for _, name := range []string{"my-kirby-site", "other-site"} {
		body, err := os.ReadFile(filepath.Join(gen, "nginx", name+".conf"))
		if err != nil {
			t.Fatalf("no config file for %s: %v", name, err)
		}
		if !strings.Contains(string(body), "server_name "+name+".wharf;") || strings.Count(string(body), "server_name") != 1 {
			t.Fatalf("%s's file is not exactly its own server block:\n%s", name, body)
		}
	}
	// And the main nginx config includes exactly those files
	main, _ := os.ReadFile(filepath.Join(gen, "nginx.conf"))
	if strings.Contains(string(main), "server_name") {
		t.Fatal("the main config holds a server block itself")
	}
	includes := includeRe.FindAllStringSubmatch(string(main), -1)
	var projects []string
	for _, m := range includes {
		if strings.Contains(m[1], "/gen/nginx/") {
			projects = append(projects, filepath.Base(m[1]))
		}
	}
	if strings.Join(projects, ",") != "my-kirby-site.conf,other-site.conf" {
		t.Fatalf("main config includes %v", projects)
	}
}
