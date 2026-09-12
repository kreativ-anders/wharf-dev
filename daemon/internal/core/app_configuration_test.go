package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/manuel-steinberg/wharf/daemon/internal/php"
	"github.com/manuel-steinberg/wharf/daemon/internal/runtime"
	"github.com/manuel-steinberg/wharf/daemon/internal/supervisor"
)

// features/app-configuration.feature — "A running project shows what serves it"
func TestARunningProjectShowsWhatServesIt(t *testing.T) {
	h := newHarness(t, func(o *Options) {
		// Only a build with its CLI beside it reports a full version.
		for _, v := range []string{"8.1", "8.2", "8.3"} {
			stubBinary(t, filepath.Join(o.Root.PHPBin(v), php.CLIName()))
		}
		full := map[string]string{"8.1": "8.1.29", "8.2": "8.2.27", "8.3": "8.3.14"}
		o.Detector.Probe = func(_ context.Context, bin string) (string, error) {
			return full[filepath.Base(filepath.Dir(bin))], nil
		}
		o.WebDetector.Probe = func(_ context.Context, bin string) (string, error) {
			if strings.Contains(bin, "nginx") {
				return "1.27.3", nil
			}
			return "2.4.62", nil
		}
	})
	h.mustAdd("my-kirby-site")
	h.mustAdd("legacy-app")
	v81 := "8.1"
	if _, err := h.d.UpdateSettings(h.ctx(), "legacy-app", Settings{PHP: &v81}); err != nil {
		t.Fatalf("set PHP override: %v", err)
	}
	for _, name := range []string{"my-kirby-site", "legacy-app"} {
		if err := h.d.StartProject(h.ctx(), name); err != nil {
			t.Fatal(err)
		}
	}

	// Then its row shows "nginx 1.27.3 · PHP 8.3.14"
	p := h.project("my-kirby-site")
	if p.Webserver != "nginx" || p.WebserverVersion != "1.27.3" || p.PHPFullVersion != "8.3.14" {
		t.Fatalf("served by %s %q, PHP %q; want nginx 1.27.3, PHP 8.3.14",
			p.Webserver, p.WebserverVersion, p.PHPFullVersion)
	}

	// And a project with a PHP override shows the version of its own PHP build
	if got := h.project("legacy-app").PHPFullVersion; got != "8.1.29" {
		t.Fatalf("overridden project reports PHP %q, want 8.1.29", got)
	}
}

// features/app-configuration.feature — "Overriding the PHP version for one
// project"
func TestOverridingPHPVersionForOneProject(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mustAdd("other-site")
	if got := h.d.Config().Services.PHP.Version; got != "8.3" {
		t.Fatalf("global PHP default = %q, want 8.3", got)
	}
	// Both started: the webserver serves started projects only.
	for _, name := range []string{"my-kirby-site", "other-site"} {
		if err := h.d.StartProject(h.ctx(), name); err != nil {
			t.Fatal(err)
		}
	}

	v81 := "8.1"
	if _, err := h.d.UpdateSettings(h.ctx(), "my-kirby-site", Settings{PHP: &v81}); err != nil {
		t.Fatalf("set PHP override: %v", err)
	}

	// Then requests to "my-kirby-site" are served by the "8.1" PHP binary
	cfg := h.d.Config()
	conf := h.readGenerated("nginx.conf")
	want81 := fmt.Sprintf("fastcgi_pass 127.0.0.1:%d;", runtime.PHPPort(cfg, "8.1"))
	want83 := fmt.Sprintf("fastcgi_pass 127.0.0.1:%d;", runtime.PHPPort(cfg, "8.3"))

	mine := vhostBlock(t, conf, "my-kirby-site.localhost")
	if !strings.Contains(mine, want81) {
		t.Fatalf("my-kirby-site is not routed to PHP 8.1:\n%s", mine)
	}
	if !h.sup.Running(runtime.PHPServiceID("8.1")) {
		t.Fatal("the PHP 8.1 backend was not started")
	}

	// And all other projects continue to use PHP "8.3"
	other := vhostBlock(t, conf, "other-site.localhost")
	if !strings.Contains(other, want83) {
		t.Fatalf("other-site is not routed to PHP 8.3:\n%s", other)
	}
	if got := h.project("other-site").PHPVersion; got != "8.3" {
		t.Fatalf("other-site PHP = %q, want 8.3", got)
	}
}

// features/app-configuration.feature — "Enabling SSL for a single project"
func TestEnablingSSLForASingleProject(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mustAdd("other-site")
	if h.project("my-kirby-site").SSL {
		t.Fatal("project starts with ssl enabled, want ssl: false")
	}
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}

	on := true
	updated, err := h.d.UpdateSettings(h.ctx(), "my-kirby-site", Settings{SSL: &on})
	if err != nil {
		t.Fatalf("enable ssl: %v", err)
	}

	// Then a local certificate is generated for "my-kirby-site.localhost" via mkcert
	if _, ok := h.certs.Issued["my-kirby-site.localhost"]; !ok {
		t.Fatalf("no certificate issued; issued = %v", h.certs.Issued)
	}
	if _, err := os.Stat(runtime.CertPath(h.root, "my-kirby-site")); err != nil {
		t.Fatalf("certificate file missing: %v", err)
	}

	// And "my-kirby-site" becomes reachable at "https://my-kirby-site.localhost"
	if updated.URL != "https://my-kirby-site.localhost" {
		t.Fatalf("URL = %q, want https://my-kirby-site.localhost", updated.URL)
	}
	conf := h.readGenerated("nginx.conf")
	if !strings.Contains(conf, "listen 443 ssl;") {
		t.Fatalf("nginx config has no TLS listener:\n%s", conf)
	}

	// And other projects' SSL settings are unaffected
	other := h.project("other-site")
	if other.SSL {
		t.Fatal("other-site gained SSL")
	}
	if strings.HasPrefix(other.URL, "https://") {
		t.Fatalf("other-site URL = %q, want http", other.URL)
	}
	if _, ok := h.certs.Issued["other-site.localhost"]; ok {
		t.Fatal("a certificate was issued for other-site")
	}
}

// features/app-configuration.feature — "Removing an override reverts to the
// global default"
func TestRemovingAnOverrideRevertsToGlobalDefault(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")

	apache := "apache"
	if _, err := h.d.UpdateSettings(h.ctx(), "my-kirby-site", Settings{Webserver: &apache}); err != nil {
		t.Fatal(err)
	}
	if got := h.project("my-kirby-site").Webserver; got != "apache" {
		t.Fatalf("webserver = %q, want apache", got)
	}

	cleared := ""
	updated, err := h.d.UpdateSettings(h.ctx(), "my-kirby-site", Settings{Webserver: &cleared})
	if err != nil {
		t.Fatalf("clear override: %v", err)
	}

	// Then "my-kirby-site" is served by whichever webserver is globally active
	if updated.Webserver != h.d.Config().Services.Webserver.Active {
		t.Fatalf("webserver = %q, want the global %q", updated.Webserver, h.d.Config().Services.Webserver.Active)
	}
	if updated.WebserverOverride != nil {
		t.Fatalf("override still set to %q", *updated.WebserverOverride)
	}

	// And the "webserver_override" key is removed from the project's config entry
	raw, err := os.ReadFile(h.root.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "webserver_override") {
		t.Fatalf("webserver_override key still present in wharf.json:\n%s", raw)
	}

	var onDisk struct {
		Projects []map[string]any `json:"projects"`
	}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if _, present := onDisk.Projects[0]["webserver_override"]; present {
		t.Fatal("webserver_override survived as a null")
	}
}

// vhostBlock returns the generated server block for a hostname, so an
// assertion about one project cannot accidentally match another's block.
func vhostBlock(t *testing.T, conf, hostname string) string {
	t.Helper()
	idx := strings.Index(conf, "server_name "+hostname+";")
	if idx < 0 {
		t.Fatalf("no server block for %s in:\n%s", hostname, conf)
	}
	start := strings.LastIndex(conf[:idx], "server {")
	if start < 0 {
		t.Fatalf("malformed server block for %s", hostname)
	}
	depth := 0
	for i := start; i < len(conf); i++ {
		switch conf[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return conf[start : i+1]
			}
		}
	}
	t.Fatalf("unterminated server block for %s", hostname)
	return ""
}

// countStarts reports how often a supervisor ID has been started.
func (h *harness) countStarts(id string) int {
	n := 0
	for _, s := range h.runner.StartedIDs() {
		if s == id {
			n++
		}
	}
	return n
}

// features/app-configuration.feature — "Custom webserver directives for one
// project"
func TestCustomWebserverDirectivesForOneProject(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mustAdd("other-site")

	path, err := h.d.CustomConfig(h.ctx(), "my-kirby-site", "nginx")
	if err != nil {
		t.Fatalf("create custom config: %v", err)
	}

	// Then "config/vhosts/my-kirby-site.nginx.conf" is created with a
	// commented starting point
	if want := filepath.Join(h.root.Dir, "config", "vhosts", "my-kirby-site.nginx.conf"); path != want {
		t.Fatalf("path = %s, want %s", path, want)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if !strings.HasPrefix(line, "#") {
			t.Fatalf("the starting point has a live directive %q", line)
		}
	}
	if cc := h.project("my-kirby-site").CustomConfigs; !slices.ContainsFunc(cc, func(c CustomConfig) bool {
		return c.Webserver == "nginx" && c.Exists && c.Active
	}) {
		t.Fatalf("snapshot does not show the file: %+v", cc)
	}

	// And its contents are included in "my-kirby-site"'s nginx server block
	for _, name := range []string{"my-kirby-site", "other-site"} {
		if err := h.d.StartProject(h.ctx(), name); err != nil {
			t.Fatal(err)
		}
	}
	conf := h.readGenerated("nginx.conf")
	include := `include "` + filepath.ToSlash(path) + `";`
	if !strings.Contains(vhostBlock(t, conf, "my-kirby-site.localhost"), include) {
		t.Fatalf("custom config not included:\n%s", conf)
	}
	// And no other project's server block includes it
	if strings.Contains(vhostBlock(t, conf, "other-site.localhost"), "include \""+filepath.ToSlash(h.root.VhostDir())) {
		t.Fatal("other-site includes a custom config")
	}

	// Asking again returns the same file and leaves the user's edits alone.
	os.WriteFile(path, []byte("client_max_body_size 64m;\n"), 0o644)
	if again, _ := h.d.CustomConfig(h.ctx(), "my-kirby-site", "nginx"); again != path {
		t.Fatalf("second call returned %s", again)
	}
	if body, _ := os.ReadFile(path); string(body) != "client_max_body_size 64m;\n" {
		t.Fatal("an existing custom config was overwritten")
	}
}

// features/app-configuration.feature — "Each webserver keeps its own custom
// config"
func TestEachWebserverKeepsItsOwnCustomConfig(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	nginxFile, _ := h.d.CustomConfig(h.ctx(), "my-kirby-site", "nginx")
	apacheFile, _ := h.d.CustomConfig(h.ctx(), "my-kirby-site", "apache")

	apache := "apache"
	if _, err := h.d.UpdateSettings(h.ctx(), "my-kirby-site", Settings{Webserver: &apache}); err != nil {
		t.Fatal(err)
	}
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}

	// Then only the apache config is included
	conf := h.readGenerated("project-my-kirby-site-apache.conf")
	if !strings.Contains(conf, `Include "`+filepath.ToSlash(apacheFile)+`"`) {
		t.Fatalf("apache config not included:\n%s", conf)
	}
	if strings.Contains(conf, filepath.ToSlash(nginxFile)) {
		t.Fatal("the nginx file is included in apache's config")
	}

	// And switching it back to "nginx" includes only the nginx config
	cleared := ""
	if _, err := h.d.UpdateSettings(h.ctx(), "my-kirby-site", Settings{Webserver: &cleared}); err != nil {
		t.Fatal(err)
	}
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	block := vhostBlock(t, h.readGenerated("nginx.conf"), "my-kirby-site.localhost")
	if !strings.Contains(block, filepath.ToSlash(nginxFile)) || strings.Contains(block, filepath.ToSlash(apacheFile)) {
		t.Fatalf("want only the nginx file included:\n%s", block)
	}
}

// features/app-configuration.feature — "Saving a custom config applies it"
func TestSavingACustomConfigAppliesIt(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	path, err := h.d.CustomConfig(h.ctx(), "my-kirby-site", "nginx")
	if err != nil {
		t.Fatal(err)
	}
	before := h.countStarts(runtimeWebserverID)

	// Nothing changed since the file was created: no restart.
	h.d.ApplyCustomConfigs(h.ctx())
	if got := h.countStarts(runtimeWebserverID); got != before {
		t.Fatalf("an unchanged file restarted the webserver (%d → %d)", before, got)
	}

	// When the user saves a change to that file
	if err := os.WriteFile(path, []byte("client_max_body_size 64m;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	os.Chtimes(path, later, later)
	h.d.ApplyCustomConfigs(h.ctx())

	// Then the webserver serving "my-kirby-site" is restarted with the change
	if got := h.countStarts(runtimeWebserverID); got != before+1 {
		t.Fatalf("webserver started %d times after the save, want %d", got, before+1)
	}
	if !h.sup.Running(runtimeWebserverID) {
		t.Fatal("webserver not running after the restart")
	}
}

// features/app-configuration.feature — "A custom config the webserver
// refuses names the problem"
func TestACustomConfigTheWebserverRefusesNamesTheProblem(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	path, err := h.d.CustomConfig(h.ctx(), "my-kirby-site", "nginx")
	if err != nil {
		t.Fatal(err)
	}

	// When the user saves a change that nginx refuses to start with
	complaint := `"server" directive is not allowed here in ` + path + `:17`
	h.runner.Refuse[runtimeWebserverID] = "2026/09/11 10:13:20 [emerg] 71176#0: " + complaint + "\n"
	os.WriteFile(path, []byte("server {\n  listen 8080;\n}\n"), 0o644)
	later := time.Now().Add(2 * time.Second)
	os.Chtimes(path, later, later)

	// A minute is far longer than the test may take: only an early exit
	// can end the wait in time.
	h.sup.StartTimeout = time.Minute
	began := time.Now()
	h.d.ApplyCustomConfigs(h.ctx())

	// Then "my-kirby-site" shows nginx's own error, naming the file and line
	p := h.project("my-kirby-site")
	if p.State != string(supervisor.StateFailed) || !strings.Contains(p.Error, complaint) {
		t.Fatalf("project = %q %q, want failed with %q", p.State, p.Error, complaint)
	}
	// And the error appears as soon as nginx exits, not after a timeout
	if waited := time.Since(began); waited > 5*time.Second {
		t.Fatalf("the error took %s", waited)
	}
}
