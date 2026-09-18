package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/php"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/runtime"
)

// features/settings.feature — "Changing the global active webserver"
func TestSettingsWritesTheWebserverChangeToConfig(t *testing.T) {
	h := newHarness(t)

	if err := h.d.SetWebserver(h.ctx(), "apache"); err != nil {
		t.Fatalf("set webserver: %v", err)
	}

	// INFO: Then the change is written to config/wharf.json
	raw, err := os.ReadFile(h.root.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	var onDisk config.Config
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("wharf.json is not valid JSON: %v", err)
	}
	if onDisk.Services.Webserver.Active != "apache" {
		t.Fatalf("services.webserver.active = %q, want apache", onDisk.Services.Webserver.Active)
	}
	// INFO: It must stay hand-editable: indented, one flat file, no stray nulls.
	if !strings.Contains(string(raw), "\n  \"services\"") {
		t.Fatalf("wharf.json is not written for humans to read:\n%s", raw)
	}
}

// features/settings.feature — "Adding a PHP version"
func TestAddingAPHPVersion(t *testing.T) {
	h := newHarness(t)
	// INFO: Given PHP "8.1" and "8.3" are offered, and 8.2 is present but not yet
	if _, err := h.d.store.Update(func(c *config.Config) error {
		c.Services.PHP.Available = []string{"8.1", "8.3"}
		c.Services.PHP.Version = "8.3"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.mustAdd("my-kirby-site")

	// INFO: When the user adds PHP "8.2" via Settings → "Add runtime version"
	if err := h.d.AddPHPVersion(h.ctx(), "8.2"); err != nil {
		t.Fatalf("add PHP version: %v", err)
	}

	// INFO: Then "8.2" is registered along with where it was found. A vendored build
	// needs no recorded location — the config stays as short as
	// dev/architecture.md §6 shows it.
	if _, err := os.Stat(filepath.Join(h.root.PHPBin("8.2"), php.FastCGIName())); err != nil {
		t.Fatalf("bin/php/8.2 is not usable: %v", err)
	}
	if path, adopted := h.d.Config().PHPPath("8.2"); adopted {
		t.Fatalf("a vendored build recorded a location %q", path)
	}

	// INFO: And "8.2" becomes selectable as a project-level override
	if !contains(h.d.State().Services.PHP.Available, "8.2") {
		t.Fatalf("8.2 is not offered: %v", h.d.State().Services.PHP.Available)
	}
	v82 := "8.2"
	p, err := h.d.UpdateSettings(h.ctx(), "my-kirby-site", Settings{PHP: &v82})
	if err != nil {
		t.Fatalf("select 8.2 as an override: %v", err)
	}
	if p.PHPVersion != "8.2" {
		t.Fatalf("project PHP = %q, want 8.2", p.PHPVersion)
	}
}

// A version found somewhere else on the machine is used where it is, and its
// location is written down so the daemon can find it again after a restart.
func TestAddingAPHPVersionAdoptsASystemInstall(t *testing.T) {
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	f := newFirstRun(t, now, "8.3")

	// INFO: A version appears on the machine that first-run detection did not see.
	binDir := filepath.Join(f.root.Dir, "machine", "php8.4", "bin")
	stubBinary(t, filepath.Join(binDir, php.CLIName()))
	stubBinary(t, filepath.Join(binDir, php.FastCGIName()))
	f.d.php.Candidates = append(f.d.php.Candidates, filepath.Join(binDir, php.CLIName()))
	probe := f.d.php.Probe
	f.d.php.Probe = func(ctx context.Context, bin string) (string, error) {
		if strings.Contains(bin, "php8.4") {
			return "8.4.1", nil
		}
		return probe(ctx, bin)
	}

	if err := f.d.AddPHPVersion(f.ctx(), "8.4"); err != nil {
		t.Fatalf("adopt system install: %v", err)
	}

	path, adopted := f.d.Config().PHPPath("8.4")
	if !adopted {
		t.Fatal("the adopted version recorded no location")
	}
	if path != binDir {
		t.Fatalf("recorded location = %q, want %q", path, binDir)
	}
	if !contains(f.d.State().Services.PHP.Available, "8.4") {
		t.Fatal("8.4 is not selectable")
	}
	// INFO: Nothing was copied into the tool's own folder.
	if _, err := os.Stat(f.root.PHPBin("8.4")); !os.IsNotExist(err) {
		t.Fatalf("bin/php/8.4 was created: %v", err)
	}
}

func TestAddingAPHPVersionThatIsNotInstalled(t *testing.T) {
	h := newHarness(t)
	err := h.d.AddPHPVersion(h.ctx(), "7.4")
	if err == nil {
		t.Fatal("registering a version with no binary should fail")
	}
	// INFO: The message must say where the build belongs, not just that exec failed.
	if !strings.Contains(err.Error(), filepath.Join("bin", "php", "7.4")) {
		t.Fatalf("error = %q, want it to name the expected path", err)
	}
	if contains(h.d.State().Services.PHP.Available, "7.4") {
		t.Fatal("an uninstalled version was offered anyway")
	}
}

// features/settings.feature — "Settings screen hides roadmap services in v1"
func TestSettingsExposesOnlyWebserverAndPHP(t *testing.T) {
	h := newHarness(t)

	raw, err := json.Marshal(h.d.State().Services)
	if err != nil {
		t.Fatal(err)
	}
	var services map[string]json.RawMessage
	if err := json.Unmarshal(raw, &services); err != nil {
		t.Fatal(err)
	}

	// INFO: Then only "Webserver" and "PHP runtime" sections are visible
	if len(services) != 2 || services["webserver"] == nil || services["php"] == nil {
		t.Fatalf("services surface = %v, want exactly webserver and php", keys(services))
	}

	// INFO: And no "MySQL/PostgreSQL" or "Mailpit" section is shown
	for _, roadmap := range []string{"database", "mysql", "postgres", "mail", "mailpit", "node", "go", "python"} {
		if _, present := services[roadmap]; present {
			t.Fatalf("roadmap service %q is surfaced in v1", roadmap)
		}
	}

	// INFO: The config file must not carry roadmap keys either (architecture §6).
	cfgRaw, err := os.ReadFile(h.root.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	for _, roadmap := range []string{"database", "mailpit", "runtimes"} {
		if strings.Contains(string(cfgRaw), roadmap) {
			t.Fatalf("wharf.json contains roadmap key %q:\n%s", roadmap, cfgRaw)
		}
	}
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

func (h *harness) server(name string) Server {
	h.t.Helper()
	for _, s := range h.d.State().Services.Webserver.Servers {
		if s.Name == name {
			return s
		}
	}
	h.t.Fatalf("no webserver %q in state", name)
	return Server{}
}

// features/settings.feature — "Webserver status while nothing is running"
func TestWebserverStatusWhileNothingIsRunning(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mustAdd("legacy-app")
	apache := "apache"
	if _, err := h.d.UpdateSettings(h.ctx(), "legacy-app", Settings{Webserver: &apache}); err != nil {
		t.Fatal(err)
	}

	// INFO: Given no project is running, the process state is "stopped" — the GUI
	// words that as "starts with the first project" (widgets_test.dart).
	if st := h.d.State().Services.Webserver.State; st != "stopped" {
		t.Fatalf("state = %q, want stopped", st)
	}
	// INFO: And each webserver lists the projects it serves
	if got := h.server("nginx").Projects; !slices.Equal(got, []string{"my-kirby-site"}) {
		t.Fatalf("nginx serves %v, want [my-kirby-site]", got)
	}
	if got := h.server("apache").Projects; !slices.Equal(got, []string{"legacy-app"}) {
		t.Fatalf("apache serves %v, want [legacy-app]", got)
	}
}

// features/settings.feature — "A webserver that is not installed says where
// it belongs"
func TestAWebserverThatIsNotInstalledSaysWhereItBelongs(t *testing.T) {
	h := newHarness(t)
	if err := os.RemoveAll(filepath.Join(h.root.Bin(), "apache")); err != nil {
		t.Fatal(err)
	}
	h.d.RefreshWebservers(h.ctx())
	binary := exeName(filepath.Join(h.root.Bin(), "apache", "bin", "httpd"))

	apache := h.server("apache")
	if apache.Installed {
		t.Fatal("apache reported as installed")
	}
	if apache.Binary != binary {
		t.Fatalf("binary = %s, want %s", apache.Binary, binary)
	}
	if !h.server("nginx").Installed {
		t.Fatal("nginx reported as missing")
	}
}

// features/settings.feature — "Choosing light or dark appearance"
func TestChoosingLightOrDarkAppearance(t *testing.T) {
	h := newHarness(t)
	if got := h.d.State().Appearance; got != "system" {
		t.Fatalf("appearance = %q, want system", got)
	}

	if err := h.d.SetAppearance("dark"); err != nil {
		t.Fatal(err)
	}
	// INFO: Then the window switches to the dark theme at once — it renders the
	// snapshot, which now says so (widgets_test.dart covers the switch).
	if got := h.d.State().Appearance; got != "dark" {
		t.Fatalf("appearance = %q, want dark", got)
	}
	// INFO: And "appearance": "dark" is written to config/wharf.json
	raw, _ := os.ReadFile(h.root.ConfigFile())
	if !strings.Contains(string(raw), `"appearance": "dark"`) {
		t.Fatalf("wharf.json does not record it:\n%s", raw)
	}
	// INFO: And choosing "System" again removes the key
	if err := h.d.SetAppearance("system"); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(h.root.ConfigFile())
	if strings.Contains(string(raw), "appearance") {
		t.Fatalf("appearance key survived choosing system:\n%s", raw)
	}
	var invalid *InvalidError
	if err := h.d.SetAppearance("sepia"); !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want invalid", err)
	}
}

// features/settings.feature — "General shows the version, and no update check
// yet"
func TestGeneralShowsTheVersion(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Version = "v1.2.3" })

	// INFO: Then the version of the running Wharf is shown, as the daemon reports it
	if got := h.d.State().Version; got != "v1.2.3" {
		t.Fatalf("version = %q, want v1.2.3", got)
	}
	// INFO: Under the key the GUI reads (gui/lib/models/state.dart).
	raw, err := json.Marshal(h.d.State())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"version":"v1.2.3"`) {
		t.Fatalf("snapshot does not publish the version:\n%s", raw)
	}
}

// features/settings.feature — "Resetting Wharf"
func TestResettingWharf(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mkProject("dropped-in")
	elsewhere := filepath.Join(t.TempDir(), "client-site")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := h.d.AddFolder(h.ctx(), elsewhere); err != nil {
		t.Fatal(err)
	}
	h.saveCustomConfig("my-kirby-site", "client_max_body_size 64m;")
	cert := runtime.CertPath(h.root, "my-kirby-site")
	if err := os.WriteFile(cert, []byte("cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.d.SetAppearance("dark"); err != nil {
		t.Fatal(err)
	}
	phpIni, err := h.d.PHPSettings(h.ctx())
	if err != nil {
		t.Fatal(err)
	}
	// INFO: Anything else put in config/ goes as well.
	stray := filepath.Join(h.root.Config(), "notes.txt")
	if err := os.WriteFile(stray, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	serviceLog := filepath.Join(h.root.LogDir(), "nginx-access.log")
	daemonLog := h.root.DaemonLog()
	for _, log := range []string{serviceLog, daemonLog} {
		if err := os.WriteFile(log, []byte("a line\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.d.Reset(h.ctx(), false); err != nil {
		t.Fatalf("reset: %v", err)
	}

	// INFO: Then every service is stopped
	if h.sup.AnyRunning() {
		t.Fatal("something still runs after the reset")
	}
	// INFO: And every project is unregistered, and its folder is left where it is
	for _, keep := range []string{h.root.ProjectDir("my-kirby-site"), h.root.ProjectDir("dropped-in"), elsewhere} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("%s was deleted: %v", keep, err)
		}
	}
	if n := len(h.d.Config().Projects); n != 0 {
		t.Fatalf("%d projects still registered", n)
	}
	// INFO: And everything in "config/" is deleted: settings, custom webserver
	// configs and PHP settings
	for _, gone := range []string{h.root.CustomConfig("my-kirby-site", "nginx"), phpIni, stray} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s survived the reset", gone)
		}
	}
	// INFO: And project certificates, generated configs and service logs are deleted
	for _, gone := range []string{
		cert,
		filepath.Join(h.root.Data(), "gen", "nginx.conf"),
		serviceLog,
		h.root.ProjectLogDir("my-kirby-site"),
	} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s survived the reset", gone)
		}
	}
	// INFO: The daemon's own log stays: it is still writing to it.
	if _, err := os.Stat(daemonLog); err != nil {
		t.Fatalf("the daemon's log was deleted: %v", err)
	}
	// INFO: And wharf.json is back to what a first start writes
	if _, err := os.Stat(h.root.ConfigFile()); err != nil {
		t.Fatalf("wharf.json was not written again: %v", err)
	}
	st := h.d.State()
	if st.Appearance != "system" {
		t.Fatalf("appearance = %q, want system", st.Appearance)
	}
	if st.Services.PHP.Version == "" || st.Services.Webserver.Active == "" {
		t.Fatalf("first-start defaults missing: %+v", st.Services)
	}
	// INFO: And downloaded PHP versions and webservers are kept
	for _, keep := range []string{filepath.Join(h.root.PHPBin("8.3"), php.FastCGIName()), exeName(filepath.Join(h.root.Bin(), "nginx", "nginx"))} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("%s was deleted: %v", keep, err)
		}
	}
	// INFO: And no elevation prompt is shown
	if n := h.el.RunCount(); n != 0 {
		t.Fatalf("%d elevation prompts were raised", n)
	}
}

// features/settings.feature — "Resetting Wharf and deleting the projects in
// www/"
func TestResettingWharfAndDeletingTheProjectsInWWW(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mkProject("dropped-in")
	elsewhere := filepath.Join(t.TempDir(), "client-site")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := h.d.AddFolder(h.ctx(), elsewhere); err != nil {
		t.Fatal(err)
	}

	if err := h.d.Reset(h.ctx(), true); err != nil {
		t.Fatalf("reset: %v", err)
	}

	// INFO: Then every folder in "www/" is deleted
	if entries, _ := os.ReadDir(h.root.WWW()); len(entries) != 0 {
		t.Fatalf("www/ still holds %v", entries)
	}
	// INFO: And a folder added from elsewhere is still left where it is
	if _, err := os.Stat(elsewhere); err != nil {
		t.Fatalf("a folder outside www/ was deleted: %v", err)
	}
}
