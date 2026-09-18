package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/ipc"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/php"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/runtime"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/supervisor"
)

// features/app-configuration.feature — "A running project shows what serves it"
func TestARunningProjectShowsWhatServesIt(t *testing.T) {
	h := newHarness(t, func(o *Options) {
		// INFO: Only a build with its CLI beside it reports a full version.
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

	// INFO: Then its row shows "nginx 1.27.3 · PHP 8.3.14"
	p := h.project("my-kirby-site")
	if p.Webserver != "nginx" || p.WebserverVersion != "1.27.3" || p.PHPFullVersion != "8.3.14" {
		t.Fatalf("served by %s %q, PHP %q; want nginx 1.27.3, PHP 8.3.14",
			p.Webserver, p.WebserverVersion, p.PHPFullVersion)
	}

	// INFO: And a project with a PHP override shows the version of its own PHP build
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
	// INFO: Both started: the webserver serves started projects only.
	for _, name := range []string{"my-kirby-site", "other-site"} {
		if err := h.d.StartProject(h.ctx(), name); err != nil {
			t.Fatal(err)
		}
	}

	v81 := "8.1"
	if _, err := h.d.UpdateSettings(h.ctx(), "my-kirby-site", Settings{PHP: &v81}); err != nil {
		t.Fatalf("set PHP override: %v", err)
	}

	// INFO: Then requests to "my-kirby-site" are served by the "8.1" PHP binary
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

	// INFO: And all other projects continue to use PHP "8.3"
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

	// INFO: Then a local certificate is generated for "my-kirby-site.localhost" via mkcert
	if _, ok := h.certs.Issued["my-kirby-site.localhost"]; !ok {
		t.Fatalf("no certificate issued; issued = %v", h.certs.Issued)
	}
	if _, err := os.Stat(runtime.CertPath(h.root, "my-kirby-site")); err != nil {
		t.Fatalf("certificate file missing: %v", err)
	}

	// INFO: And "my-kirby-site" becomes reachable at "https://my-kirby-site.localhost"
	if updated.URL != "https://my-kirby-site.localhost" {
		t.Fatalf("URL = %q, want https://my-kirby-site.localhost", updated.URL)
	}
	conf := h.readGenerated("nginx.conf")
	if !strings.Contains(conf, "listen 443 ssl;") {
		t.Fatalf("nginx config has no TLS listener:\n%s", conf)
	}

	// INFO: And other projects' SSL settings are unaffected
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

	// INFO: Then "my-kirby-site" is served by whichever webserver is globally active
	if updated.Webserver != h.d.Config().Services.Webserver.Active {
		t.Fatalf("webserver = %q, want the global %q", updated.Webserver, h.d.Config().Services.Webserver.Active)
	}
	if updated.WebserverOverride != nil {
		t.Fatalf("override still set to %q", *updated.WebserverOverride)
	}

	// INFO: And the "webserver_override" key is removed from the project's config entry
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

// saveCustomConfig saves a project's custom config for the webserver serving
// it, starting from the rules the editor opens with and adding a line inside
// the server block.
func (h *harness) saveCustomConfig(name, line string) string {
	h.t.Helper()
	rules, err := h.d.ReadCustomConfig(name)
	if err != nil {
		h.t.Fatal(err)
	}
	body := rules.Content
	if !rules.Exists {
		anchor := "  root \"{{root}}\";\n"
		if rules.Webserver == "apache" {
			anchor = "  DocumentRoot \"{{root}}\"\n"
		}
		if !strings.Contains(body, anchor) {
			h.t.Fatalf("no %q to add to in:\n%s", anchor, body)
		}
		body = strings.Replace(body, anchor, anchor+"  "+line+"\n", 1)
	}
	if err := h.d.SaveCustomConfig(h.ctx(), name, rules.Webserver, body); err != nil {
		h.t.Fatalf("save custom config: %v", err)
	}
	return h.root.CustomConfig(name, rules.Webserver)
}

// features/app-configuration.feature — "A project's custom config starts from
// its config template"
func TestAProjectsCustomConfigStartsFromItsConfigTemplate(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mustAdd("other-site")
	h.pickTemplate("my-kirby-site", "kirby")

	// INFO: Then the editor opens on the Kirby template's nginx server block, its
	// placeholders included
	rules, err := h.d.ReadCustomConfig("my-kirby-site")
	if err != nil {
		t.Fatal(err)
	}
	if rules.Webserver != "nginx" || rules.Exists {
		t.Fatalf("rules = %s, exists %v; want nginx's starting point", rules.Webserver, rules.Exists)
	}
	for _, want := range []string{
		"started from the Kirby config template", "{{listen}}", "{{ssl}}", "server_name {{server_name}};",
		"rewrite ^/(content|site|kirby)/(.*)$ /error last;",
		// INFO: And a warning says Wharf fills in each {{…}} and a fixed value breaks
		// the project
		"Keep every {{…}}", `A fixed "listen 80;"`,
	} {
		if !strings.Contains(rules.Content, want) {
			t.Fatalf("starting point lacks %q:\n%s", want, rules.Content)
		}
	}
	if strings.Contains(rules.Content, "built into Wharf") {
		t.Fatalf("the template's own header came along:\n%s", rules.Content)
	}

	// INFO: And saving writes "config/vhosts/my-kirby-site.nginx.conf"
	path := h.saveCustomConfig("my-kirby-site", "client_max_body_size 64m;")
	if want := filepath.Join(h.root.Dir, "config", "vhosts", "my-kirby-site.nginx.conf"); path != want {
		t.Fatalf("path = %s, want %s", path, want)
	}
	if c := h.project("my-kirby-site").CustomConfig; !c.Exists || c.Webserver != "nginx" || c.Path != path {
		t.Fatalf("snapshot = %+v", c)
	}
	if again, _ := h.d.ReadCustomConfig("my-kirby-site"); !again.Exists || !strings.Contains(again.Content, "client_max_body_size 64m;") {
		t.Fatalf("reading it again = %+v", again)
	}

	// INFO: And "my-kirby-site" is served with that file instead of its config
	// template: a Kirby rule taken out of the file is gone from its block.
	body, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(body), "  rewrite /\\.(?!well-known/) /error last;\n", "", 1)), 0o644)
	for _, name := range []string{"my-kirby-site", "other-site"} {
		if err := h.d.StartProject(h.ctx(), name); err != nil {
			t.Fatal(err)
		}
	}
	conf := h.readGenerated("nginx.conf")
	block := vhostBlock(t, conf, "my-kirby-site.localhost")
	if !strings.Contains(block, "client_max_body_size 64m;") || strings.Contains(block, "well-known") {
		t.Fatalf("my-kirby-site is not served with its custom config:\n%s", block)
	}
	if strings.Contains(conf, "{{") {
		t.Fatalf("a placeholder is left:\n%s", conf)
	}
	// INFO: And no other project uses it
	if strings.Contains(vhostBlock(t, conf, "other-site.localhost"), "client_max_body_size 64m;") {
		t.Fatal("other-site got my-kirby-site's custom config")
	}
}

// features/app-configuration.feature — "A custom config belongs to the
// webserver serving the project"
func TestACustomConfigBelongsToTheWebserverServingTheProject(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.pickTemplate("my-kirby-site", "kirby")
	nginxFile := h.saveCustomConfig("my-kirby-site", "client_max_body_size 64m;")

	// INFO: Then its settings offer its nginx custom config and no Apache one
	if c := h.project("my-kirby-site").CustomConfig; c.Webserver != "nginx" || !c.Exists {
		t.Fatalf("snapshot = %+v, want the nginx file", c)
	}
	rules, _ := h.d.ReadCustomConfig("my-kirby-site")
	err := h.d.SaveCustomConfig(h.ctx(), "my-kirby-site", "apache", rules.Content)
	var ipcErr *ipc.Error
	if !errors.As(asIPCError(err), &ipcErr) || ipcErr.Code != ipc.CodeConflict {
		t.Fatalf("saving an apache config for an nginx project: %v", err)
	}
	if _, err := os.Stat(h.root.CustomConfig("my-kirby-site", "apache")); !os.IsNotExist(err) {
		t.Fatal("an apache config was written")
	}

	// INFO: When "my-kirby-site" is switched to apache
	apache := "apache"
	if _, err := h.d.UpdateSettings(h.ctx(), "my-kirby-site", Settings{Webserver: &apache}); err != nil {
		t.Fatal(err)
	}
	// INFO: Then "Customize…" starts from its config template's Apache rules
	if c := h.project("my-kirby-site").CustomConfig; c.Webserver != "apache" || c.Exists {
		t.Fatalf("snapshot = %+v, want apache's, not yet created", c)
	}
	rules, err = h.d.ReadCustomConfig("my-kirby-site")
	if err != nil || rules.Webserver != "apache" || rules.Exists || !strings.Contains(rules.Content, "<VirtualHost {{listen}}>") {
		t.Fatalf("rules = %+v, %v", rules, err)
	}

	// INFO: And the nginx file is kept, and used again once nginx serves it
	if _, err := os.Stat(nginxFile); err != nil {
		t.Fatalf("the nginx file is gone: %v", err)
	}
	cleared := ""
	if _, err := h.d.UpdateSettings(h.ctx(), "my-kirby-site", Settings{Webserver: &cleared}); err != nil {
		t.Fatal(err)
	}
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(vhostBlock(t, h.readGenerated("nginx.conf"), "my-kirby-site.localhost"), "client_max_body_size 64m;") {
		t.Fatal("the nginx file is not used once nginx serves the project again")
	}
}

// features/app-configuration.feature — "A custom config keeps Wharf's
// placeholders"
func TestACustomConfigKeepsWharfsPlaceholders(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mustAdd("other-site")
	for _, name := range []string{"my-kirby-site", "other-site"} {
		if err := h.d.StartProject(h.ctx(), name); err != nil {
			t.Fatal(err)
		}
	}

	// INFO: When the user saves a custom config with a fixed port in place of
	// {{listen}}
	rules, _ := h.d.ReadCustomConfig("my-kirby-site")
	fixed := strings.Replace(rules.Content, "  {{listen}}\n", "  listen 8080;\n", 1)
	err := h.d.SaveCustomConfig(h.ctx(), "my-kirby-site", "nginx", fixed)

	// INFO: Then the save is refused, naming each placeholder that is missing
	var ipcErr *ipc.Error
	if !errors.As(asIPCError(err), &ipcErr) || ipcErr.Code != ipc.CodeBadRequest ||
		!strings.Contains(err.Error(), "{{listen}}") || strings.Contains(err.Error(), "{{ssl}}") {
		t.Fatalf("saving without {{listen}}: %v", err)
	}
	path := h.root.CustomConfig("my-kirby-site", "nginx")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the refused rules were saved")
	}

	// INFO: And a file edited in another editor without them keeps
	// "my-kirby-site" from starting, naming the file
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(fixed), 0o644); err != nil {
		t.Fatal(err)
	}
	h.d.ApplyCustomConfigs(h.ctx())
	p := h.project("my-kirby-site")
	if p.State != string(supervisor.StateFailed) || !strings.Contains(p.Error, filepath.ToSlash(path)) ||
		!strings.Contains(p.Error, "{{listen}}") {
		t.Fatalf("my-kirby-site = %q %q", p.State, p.Error)
	}
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err == nil || !strings.Contains(err.Error(), "{{listen}}") {
		t.Fatalf("starting it again: %v", err)
	}

	// INFO: And every other project keeps being served
	if !h.sup.Running(runtimeWebserverID) || h.project("other-site").State != string(supervisor.StateRunning) {
		t.Fatal("the front door went down with it")
	}
	conf := h.readGenerated("nginx.conf")
	if strings.Contains(conf, "listen 8080;") || strings.Contains(conf, "server_name my-kirby-site.localhost;") {
		t.Fatalf("the broken rules reached the front door:\n%s", conf)
	}
}

// features/app-configuration.feature — "Removing a custom config returns to
// the config template"
func TestRemovingACustomConfigReturnsToTheConfigTemplate(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.pickTemplate("my-kirby-site", "kirby")
	path := h.saveCustomConfig("my-kirby-site", "client_max_body_size 64m;")
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}

	// INFO: When the user chooses "Use config template" in the editor and confirms
	if err := h.d.DeleteCustomConfig(h.ctx(), "my-kirby-site", "nginx"); err != nil {
		t.Fatal(err)
	}

	// INFO: Then "config/vhosts/my-kirby-site.nginx.conf" is deleted
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the file is still there: %v", err)
	}
	if h.project("my-kirby-site").CustomConfig.Exists {
		t.Fatal("the snapshot still shows the file")
	}
	// INFO: And "my-kirby-site" is served with its config template again
	block := vhostBlock(t, h.readGenerated("nginx.conf"), "my-kirby-site.localhost")
	if strings.Contains(block, "client_max_body_size 64m;") || !strings.Contains(block, "rewrite ^/(content|site|kirby)/(.*)$ /error last;") {
		t.Fatalf("not served with the Kirby template again:\n%s", block)
	}
}

// features/app-configuration.feature — "Saving a custom config applies it"
func TestSavingACustomConfigAppliesIt(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	path := h.saveCustomConfig("my-kirby-site", "client_max_body_size 64m;")
	before := h.countStarts(runtimeWebserverID)

	// INFO: Nothing changed since the file was saved: no restart.
	h.d.ApplyCustomConfigs(h.ctx())
	if got := h.countStarts(runtimeWebserverID); got != before {
		t.Fatalf("an unchanged file restarted the webserver (%d → %d)", before, got)
	}

	// INFO: When the user saves a change to that file
	body, _ := os.ReadFile(path)
	changed := strings.Replace(string(body), "client_max_body_size 64m;", "client_max_body_size 128m;", 1)
	if err := os.WriteFile(path, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	os.Chtimes(path, later, later)
	h.d.ApplyCustomConfigs(h.ctx())

	// INFO: Then the webserver serving "my-kirby-site" is restarted with the change
	if got := h.countStarts(runtimeWebserverID); got != before+1 {
		t.Fatalf("webserver started %d times after the save, want %d", got, before+1)
	}
	if !h.sup.Running(runtimeWebserverID) {
		t.Fatal("webserver not running after the restart")
	}
	if !strings.Contains(h.readGenerated("nginx.conf"), "client_max_body_size 128m;") {
		t.Fatal("the restarted webserver does not have the change")
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
	path := h.saveCustomConfig("my-kirby-site", "client_max_body_size 64m;")

	// INFO: When the user saves a change that nginx refuses to start with
	complaint := `unknown directive "client_max_body" in ` + path + `:17`
	h.runner.Refuse[runtimeWebserverID] = "2026/09/11 10:13:20 [emerg] 71176#0: " + complaint + "\n"
	body, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(body), "client_max_body_size", "client_max_body", 1)), 0o644)
	later := time.Now().Add(2 * time.Second)
	os.Chtimes(path, later, later)

	// INFO: A minute is far longer than the test may take: only an early exit
	// can end the wait in time.
	h.sup.StartTimeout = time.Minute
	began := time.Now()
	h.d.ApplyCustomConfigs(h.ctx())

	// INFO: Then "my-kirby-site" shows nginx's own error, naming the file and line
	p := h.project("my-kirby-site")
	if p.State != string(supervisor.StateFailed) || !strings.Contains(p.Error, complaint) {
		t.Fatalf("project = %q %q, want failed with %q", p.State, p.Error, complaint)
	}
	// INFO: And the error appears as soon as nginx exits, not after a timeout
	if waited := time.Since(began); waited > 5*time.Second {
		t.Fatalf("the error took %s", waited)
	}
}
