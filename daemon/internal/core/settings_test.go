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

	"github.com/manuel-steinberg/wharf/daemon/internal/config"
	"github.com/manuel-steinberg/wharf/daemon/internal/php"
)

// features/settings.feature — "Changing the global active webserver"
func TestSettingsWritesTheWebserverChangeToConfig(t *testing.T) {
	h := newHarness(t)

	if err := h.d.SetWebserver(h.ctx(), "apache"); err != nil {
		t.Fatalf("set webserver: %v", err)
	}

	// Then the change is written to config/wharf.json
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
	// It must stay hand-editable: indented, one flat file, no stray nulls.
	if !strings.Contains(string(raw), "\n  \"services\"") {
		t.Fatalf("wharf.json is not written for humans to read:\n%s", raw)
	}
}

// features/settings.feature — "Adding a PHP version"
func TestAddingAPHPVersion(t *testing.T) {
	h := newHarness(t)
	// Given PHP "8.1" and "8.3" are offered, and 8.2 is present but not yet
	if _, err := h.d.store.Update(func(c *config.Config) error {
		c.Services.PHP.Available = []string{"8.1", "8.3"}
		c.Services.PHP.Version = "8.3"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.mustAdd("my-kirby-site")

	// When the user adds PHP "8.2" via Settings → "Add runtime version"
	if err := h.d.AddPHPVersion(h.ctx(), "8.2"); err != nil {
		t.Fatalf("add PHP version: %v", err)
	}

	// Then "8.2" is registered along with where it was found. A vendored build
	// needs no recorded location — the config stays as short as
	// dev/architecture.md §6 shows it.
	if _, err := os.Stat(filepath.Join(h.root.PHPBin("8.2"), php.FastCGIName())); err != nil {
		t.Fatalf("bin/php/8.2 is not usable: %v", err)
	}
	if path, adopted := h.d.Config().PHPPath("8.2"); adopted {
		t.Fatalf("a vendored build recorded a location %q", path)
	}

	// And "8.2" becomes selectable as a project-level override
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

	// A version appears on the machine that first-run detection did not see.
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
	// Nothing was copied into the tool's own folder.
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
	// The message must say where the build belongs, not just that exec failed.
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

	// Then only "Webserver" and "PHP runtime" sections are visible
	if len(services) != 2 || services["webserver"] == nil || services["php"] == nil {
		t.Fatalf("services surface = %v, want exactly webserver and php", keys(services))
	}

	// And no "MySQL/PostgreSQL" or "Mailpit" section is shown
	for _, roadmap := range []string{"database", "mysql", "postgres", "mail", "mailpit", "node", "go", "python"} {
		if _, present := services[roadmap]; present {
			t.Fatalf("roadmap service %q is surfaced in v1", roadmap)
		}
	}

	// The config file must not carry roadmap keys either (architecture §6).
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

	// Given no project is running, the process state is "stopped" — the GUI
	// words that as "starts with the first project" (widgets_test.dart).
	if st := h.d.State().Services.Webserver.State; st != "stopped" {
		t.Fatalf("state = %q, want stopped", st)
	}
	// And each webserver lists the projects it serves
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
	binary := filepath.Join(h.root.Bin(), "apache", "bin", "httpd")

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
	// Then the window switches to the dark theme at once — it renders the
	// snapshot, which now says so (widgets_test.dart covers the switch).
	if got := h.d.State().Appearance; got != "dark" {
		t.Fatalf("appearance = %q, want dark", got)
	}
	// And "appearance": "dark" is written to config/wharf.json
	raw, _ := os.ReadFile(h.root.ConfigFile())
	if !strings.Contains(string(raw), `"appearance": "dark"`) {
		t.Fatalf("wharf.json does not record it:\n%s", raw)
	}
	// And choosing "System" again removes the key
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
