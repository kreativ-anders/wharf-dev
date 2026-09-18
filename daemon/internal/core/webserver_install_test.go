package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/ipc"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/webserver"
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
	// INFO: Apache where macOS keeps it: the binary in sbin, its modules in
	// libexec/apache2.
	machine := t.TempDir()
	httpd := exeName(filepath.Join(machine, "usr", "sbin", "httpd"))
	stubBinary(t, httpd)
	stubBinary(t, filepath.Join(machine, "usr", "libexec", "apache2", "mod_proxy_fcgi.so"))

	h := newHarness(t, withoutWebserver("nginx", "apache"), func(o *Options) {
		o.WebDetector.Candidates = map[string][]string{webserver.Apache: {httpd}}
	})

	// INFO: Then Apache is used where it is, without being copied
	apache := h.server("apache")
	if !apache.Installed || apache.Binary != httpd || apache.Source != "system" {
		t.Fatalf("apache = %+v, want the system copy at %s", apache, httpd)
	}
	if _, err := os.Stat(filepath.Join(h.root.Bin(), "apache")); !os.IsNotExist(err) {
		t.Fatal("apache was copied into bin/")
	}
	// INFO: And Apache is selected as the default webserver
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
		return stubFile(exeName(filepath.Join(dest, "nginx")))
	}
	done := make(chan error)
	go func() { done <- h.d.InstallWebserver(h.ctx(), "nginx") }()

	// INFO: And Settings shows it as installing until it is done
	if !<-inside {
		t.Fatal("nginx not shown as installing mid-way")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("install: %v", err)
	}
	// INFO: Then the newest nginx for this OS and CPU is installed
	nginx := h.server("nginx")
	if !nginx.Installed || nginx.Installing || nginx.Binary != exeName(filepath.Join(h.root.Bin(), "nginx", "nginx")) {
		t.Fatalf("nginx = %+v, want installed into bin/nginx", nginx)
	}
	// INFO: And every project can be served by nginx without further setup
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
	// INFO: And no partial "bin/nginx" folder is left behind
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
	// INFO: Then Apache offers no "Install" action
	if apache.Install.Installable {
		t.Fatal("apache offered as installable")
	}
	// INFO: And the GUI says how to install it instead
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

	// INFO: Then one nginx process serves both projects
	running := 0
	for _, st := range h.sup.Statuses() {
		if st.State == "running" && !strings.HasPrefix(st.ID, "php:") {
			running++
		}
	}
	if running != 1 {
		t.Fatalf("%d webserver processes running, want 1", running)
	}
	// INFO: And each project's server block is generated into its own file under
	// "data/gen/nginx/"
	gen := filepath.Join(h.root.Data(), "gen")
	for _, name := range []string{"my-kirby-site", "other-site"} {
		body, err := os.ReadFile(filepath.Join(gen, "nginx", name+".conf"))
		if err != nil {
			t.Fatalf("no config file for %s: %v", name, err)
		}
		if !strings.Contains(string(body), "server_name "+name+".localhost;") || strings.Count(string(body), "server_name") != 1 {
			t.Fatalf("%s's file is not exactly its own server block:\n%s", name, body)
		}
	}
	// INFO: And the main nginx config includes exactly those files
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

// features/webserver-install.feature — "A Kirby project needs no custom
// webserver config". What nginx and Apache then do with a real Starterkit is
// checked by hand against real binaries; this pins the rules that make it so.
func TestAKirbyProjectNeedsNoWebserverConfiguration(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	kirby := "kirby"
	if _, err := h.d.UpdateSettings(h.ctx(), "my-kirby-site", Settings{Template: &kirby}); err != nil {
		t.Fatal(err)
	}
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}

	// INFO: nginx has no .htaccess, so the generated block carries the Kirby
	// config template's rules.
	block := vhostBlock(t, h.readGenerated("nginx.conf"), "my-kirby-site.localhost")
	for _, rule := range []string{
		// INFO: Then its pages, the Panel and its media are served
		`try_files $uri $uri/ /index.php$is_args$args;`,
		// INFO: And "content/", "site/", "kirby/" and dot-files are never served
		// as files
		"rewrite ^/(content|site|kirby)/(.*)$ /error last;",
		"rewrite /\\.(?!well-known/) /error last;",
		// INFO: Files at the top level — composer.json, README.md — go to Kirby too.
		"rewrite ^/(?!app\\.webmanifest)[^/]+$ /index.php last;",
	} {
		if !strings.Contains(block, rule) {
			t.Fatalf("nginx block lacks %q:\n%s", rule, block)
		}
	}
	// INFO: And no custom config is needed for any of it
	if strings.Contains(block, filepath.ToSlash(h.root.VhostDir())) {
		t.Fatal("the rules came from a custom config")
	}

	// INFO: Apache reads the Starterkit's own .htaccess, and denies the folders
	// even where mod_rewrite is missing. The .htaccess needs the modules its
	// directives come from, where the install has them.
	modules := filepath.Join(h.root.Bin(), "apache", "modules")
	for _, m := range []string{"rewrite", "headers", "setenvif"} {
		stubBinary(t, filepath.Join(modules, "mod_"+m+".so"))
	}
	if err := h.d.SetWebserver(h.ctx(), "apache"); err != nil {
		t.Fatal(err)
	}
	conf := h.readGenerated("apache.conf")
	for _, want := range []string{"AllowOverride All", "Require all denied", "rewrite_module", "headers_module", "setenvif_module"} {
		if !strings.Contains(conf, want) {
			t.Fatalf("apache config lacks %q:\n%s", want, conf)
		}
	}
	// INFO: And PHP is handed the script's path as the OS spells it, a Windows
	// drive letter included: Apache appends the path to the handler URL, and
	// "C:/..." without the slash runs into the port ("DNS lookup failure for:
	// 127.0.0.1:9003c:").
	if !regexp.MustCompile(`SetHandler "proxy:fcgi://127\.0\.0\.1:\d+/"`).MatchString(conf) ||
		!strings.Contains(conf, `ProxyFCGISetEnvIf "reqenv('SCRIPT_FILENAME') =~ m|^proxy:fcgi://[^/]+/([A-Za-z]:)?(/.*)|" SCRIPT_FILENAME "$1$2"`) {
		t.Fatalf("apache does not hand PHP the script's own path:\n%s", conf)
	}
}
