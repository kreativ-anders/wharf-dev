package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestLoadCreatesADefaultConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "wharf.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Get()
	if cfg.Services.Webserver.Active != "nginx" {
		t.Fatalf("default webserver = %q", cfg.Services.Webserver.Active)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file was not created: %v", err)
	}
}

// The config file is the tool's whole persistent state and is meant to be
// hand-edited, so its shape must stay the one documented in architecture §6.
func TestWrittenShapeMatchesTheDocumentedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wharf.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(func(c *Config) error {
		c.SetProject(Project{Name: "my-kirby-site", SSL: true, Port: 8080})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]any
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatal(err)
	}
	if len(shape) != 2 || shape["services"] == nil || shape["projects"] == nil {
		t.Fatalf("top-level keys = %v, want services and projects only", shape)
	}
	if strings.Contains(string(raw), "null") {
		t.Fatalf("config contains nulls, which a person should not have to read:\n%s", raw)
	}
}

func TestClearedOverridesLeaveNoKeyBehind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wharf.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	apache := "apache"
	if _, err := s.Update(func(c *Config) error {
		c.SetProject(Project{Name: "legacy-app", WebserverOverride: &apache})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(func(c *Config) error {
		p, _ := c.Project("legacy-app")
		p.WebserverOverride = nil
		c.SetProject(p)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "webserver_override") {
		t.Fatalf("cleared override still written:\n%s", raw)
	}
}

func TestUpdateIsAtomicOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wharf.json")
	s, _ := Load(path)
	before := s.Get()

	_, err := s.Update(func(c *Config) error {
		c.Services.Webserver.Active = "apache"
		return errSentinel
	})
	if err != errSentinel {
		t.Fatalf("error = %v, want it passed through", err)
	}
	if s.Get().Services.Webserver.Active != before.Services.Webserver.Active {
		t.Fatal("a failed update changed the in-memory config")
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), `"active": "apache"`) {
		t.Fatalf("a failed update was persisted:\n%s", raw)
	}
}

func TestGetReturnsACopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wharf.json")
	s, _ := Load(path)
	got := s.Get()
	got.Services.Webserver.Active = "mutated"
	got.Services.Webserver.Available[0] = "mutated"
	if s.Get().Services.Webserver.Active == "mutated" {
		t.Fatal("Get exposed the live config")
	}
	if s.Get().Services.Webserver.Available[0] == "mutated" {
		t.Fatal("Get shared the available-services slice")
	}
}

// clone copies field by field, so a new key it does not know is silently
// lost on every update. Every field below is set; one clone forgot is caught.
func TestCloneKeepsEveryField(t *testing.T) {
	override := "apache"
	version := "8.2"
	full := &Config{
		Appearance: "dark",
		Services: Services{
			Webserver: Webserver{Active: "apache", Available: []string{"apache", "nginx"}},
			PHP: PHP{
				Version:   "8.3",
				Available: []string{"8.2", "8.3"},
				Paths:     map[string]string{"8.2": "/opt/php82/bin"},
				Hidden:    []string{"/opt/php81/bin"},
				Terminal:  true,
			},
		},
		Projects: []Project{{Name: "site", WebserverOverride: &override, PHPVersion: &version, SSL: true, Port: 8081, Path: "/src/site", Template: "kirby"}},
	}
	for _, v := range []reflect.Value{reflect.ValueOf(full.Services.PHP), reflect.ValueOf(full.Services.Webserver), reflect.ValueOf(full.Projects[0])} {
		for i := range v.NumField() {
			if v.Field(i).IsZero() {
				t.Fatalf("the fixture leaves %s.%s unset; set it so clone is checked for it", v.Type().Name(), v.Type().Field(i).Name)
			}
		}
	}
	if got := full.clone(); !reflect.DeepEqual(got, full) {
		t.Fatalf("clone = %+v\nwant %+v", got, full)
	}
}

func TestNormaliseRepairsAHandEditedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wharf.json")
	// INFO: A user edits the file to select a webserver they did not list.
	body := `{"services":{"webserver":{"active":"apache","available":["nginx"]},"php":{"version":"8.2","available":[]}},"projects":[{"name":"site","webserver_override":"  "}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Get()
	if !slices.Contains(cfg.Services.Webserver.Available, "apache") {
		t.Fatalf("active webserver missing from available: %v", cfg.Services.Webserver.Available)
	}
	if !slices.Contains(cfg.Services.PHP.Available, "8.2") {
		t.Fatalf("php available = %v, want it to include the selected version", cfg.Services.PHP.Available)
	}
	if p, _ := cfg.Project("site"); p.WebserverOverride != nil {
		t.Fatalf("blank override was kept as %q", *p.WebserverOverride)
	}
}

func TestResolutionPrefersOverrides(t *testing.T) {
	cfg := Default()
	cfg.Services.Webserver.Active = "nginx"
	cfg.Services.PHP.Version = "8.3"
	apache, v81 := "apache", "8.1"
	plain := Project{Name: "plain"}
	fancy := Project{Name: "fancy", WebserverOverride: &apache, PHPVersion: &v81}

	if got := cfg.WebserverFor(plain); got != "nginx" {
		t.Fatalf("plain webserver = %q", got)
	}
	if got := cfg.WebserverFor(fancy); got != "apache" {
		t.Fatalf("overridden webserver = %q", got)
	}
	if got := cfg.PHPVersionFor(plain); got != "8.3" {
		t.Fatalf("plain php = %q", got)
	}
	if got := cfg.PHPVersionFor(fancy); got != "8.1" {
		t.Fatalf("overridden php = %q", got)
	}
}

// A name becomes a folder in www/, files in config/vhosts/ and data/certs/,
// and a server_name line. A hand-edited one that could leave those folders
// or end a directive is refused with the fix — on load and on reload — and
// the daemon's own writes are held to the same rule.
func TestAProjectThatCannotBeServedSafelyIsRefused(t *testing.T) {
	for _, entry := range []string{
		`{"name":"../../escape"}`,
		`{"name":"my site"}`,
		`{"name":"a;b"}`,
		`{"name":"   "}`,
		`{"name":"quoted","path":"/tmp/a\"b"}`,
	} {
		path := filepath.Join(t.TempDir(), "wharf.json")
		body := `{"services":{"webserver":{"active":"nginx","available":["nginx"]},` +
			`"php":{"version":"8.3","available":["8.3"]}},"projects":[` + entry + `]}`
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "wharf.json") {
			t.Errorf("Load accepted %s (err = %v), want a refusal naming the file", entry, err)
		}
	}

	path := filepath.Join(t.TempDir(), "wharf.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(func(c *Config) error {
		c.SetProject(Project{Name: "../escape"})
		return nil
	}); err == nil {
		t.Fatal("Update wrote a project name that leaves www/")
	}
	// INFO: What a hand-edit may still use, though the GUI would rewrite it.
	if _, err := s.Update(func(c *Config) error {
		c.SetProject(Project{Name: "Client_Site.v2", Path: "/Users/you/Code/Client Site"})
		return nil
	}); err != nil {
		t.Fatalf("a usable hand-written name was refused: %v", err)
	}

	if err := os.WriteFile(path, []byte(`{"projects":[{"name":"../escape"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reload(); err == nil {
		t.Fatal("Reload accepted a project name that leaves www/")
	}
	if _, ok := s.Get().Project("Client_Site.v2"); !ok {
		t.Fatal("a refused reload replaced the config in use")
	}
}

var errSentinel = sentinel{}

type sentinel struct{}

func (sentinel) Error() string { return "refused" }
