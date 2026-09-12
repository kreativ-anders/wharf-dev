package core

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/project"
)

// starterKit serves a zip shaped like GitHub's starterkit archive: everything
// inside one top-level folder that must be stripped.
func starterKit(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"starterkit-main/index.php":              "<?php require 'kirby/bootstrap.php';",
		"starterkit-main/site/config/config.php": "<?php return [];",
		"starterkit-main/content/home/home.txt":  "Title: Home",
		"starterkit-main/kirby/bootstrap.php":    "<?php",
	}
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// features/quick-app-php.feature — "Scaffolding a new Kirby project"
func TestScaffoldingANewKirbyProject(t *testing.T) {
	h := newHarness(t)

	body := starterKit(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	tpl, ok := project.TemplateByID("kirby")
	if !ok {
		t.Fatal(`no "Kirby" quick-app template is registered`)
	}
	h.d.scaf.Fetcher = project.HTTPFetcher{}
	h.d.scaf.URLOverrides = map[string]string{"kirby": srv.URL}

	p, err := h.d.Scaffold(h.ctx(), tpl.ID, "my-kirby-site", "")
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	// Then a folder "www/my-kirby-site" is created
	dir := h.root.ProjectDir("my-kirby-site")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("www/my-kirby-site was not created: %v", err)
	}

	// And the Kirby starter kit is fetched into that folder
	// (the archive's top-level folder is stripped, so index.php sits at the root)
	for _, want := range []string{"index.php", "site/config/config.php", "content/home/home.txt"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(want))); err != nil {
			t.Fatalf("starter kit file %s missing: %v", want, err)
		}
	}

	// And the project appears in the GUI's project list
	found := h.project("my-kirby-site")
	if found.Name != "my-kirby-site" {
		t.Fatalf("project list does not contain the new project: %+v", h.d.State().Projects)
	}
	// And it is reachable at "http://my-kirby-site.localhost"
	if p.URL != "http://my-kirby-site.localhost" {
		t.Fatalf("URL = %q, want http://my-kirby-site.localhost", p.URL)
	}
	// Scaffolding starts the project, so it is at least on its way up.
	if err := waitFor(func() bool {
		switch h.project("my-kirby-site").State {
		case "starting", "running":
			return true
		}
		return false
	}); err != nil {
		t.Fatalf("project state = %q, want starting or running", h.project("my-kirby-site").State)
	}
}

// features/quick-app-php.feature — "A typed name becomes a project name"
func TestATypedNameBecomesAProjectName(t *testing.T) {
	h := newHarness(t)
	h.d.scaf.Fetcher = offlineFetcher{}

	// And a name sent to the daemon without the GUI gets the same rewrite
	p, err := h.d.Scaffold(h.ctx(), "empty", "Müller & Söhne", "")
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if p.Name != "mueller-soehne" {
		t.Fatalf("name = %q, want mueller-soehne", p.Name)
	}
	if _, err := os.Stat(h.root.ProjectDir("mueller-soehne")); err != nil {
		t.Fatalf("www/mueller-soehne was not created: %v", err)
	}
	if p.URL != "http://mueller-soehne.localhost" {
		t.Fatalf("URL = %q, want http://mueller-soehne.localhost", p.URL)
	}

	// And a name with no letter or digit in it is refused, asking for at least one
	_, err = h.d.Scaffold(h.ctx(), "empty", "!!!", "")
	if err == nil || !strings.Contains(err.Error(), "letter or digit") {
		t.Fatalf("error = %v, want it to ask for a letter or digit", err)
	}
	entries, err := os.ReadDir(h.root.WWW())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("www/ holds %v, want only mueller-soehne", entries)
	}
}

// features/quick-app-php.feature — "Scaffolding fails without network access"
func TestScaffoldingFailsWithoutNetworkAccess(t *testing.T) {
	h := newHarness(t)
	h.d.scaf.Fetcher = offlineFetcher{}

	_, err := h.d.Scaffold(h.ctx(), "kirby", "my-kirby-site", "")

	// Then the GUI reports that the template could not be fetched
	if err == nil {
		t.Fatal("scaffolding offline should fail")
	}
	if !errors.Is(err, project.ErrOffline) {
		t.Fatalf("error = %v, want it to report a fetch failure", err)
	}

	// And no partial project folder is left behind
	if _, statErr := os.Stat(h.root.ProjectDir("my-kirby-site")); !os.IsNotExist(statErr) {
		t.Fatalf("a partial project folder was left behind: %v", statErr)
	}
	entries, err2 := os.ReadDir(h.root.WWW())
	if err2 != nil {
		t.Fatal(err2)
	}
	if len(entries) != 0 {
		t.Fatalf("www/ is not clean after the failure: %v", entries)
	}
	if _, ok := h.d.Config().Project("my-kirby-site"); ok {
		t.Fatal("a failed scaffold registered a project anyway")
	}
}

// features/quick-app-php.feature — the WordPress and Laravel templates are
// tagged @roadmap and must not be offered in v1.
func TestOnlyTheKirbyTemplateIsRegisteredInV1(t *testing.T) {
	h := newHarness(t)
	got := map[string]bool{}
	for _, tpl := range h.d.Templates() {
		got[tpl.ID] = true
	}
	if !got["kirby"] {
		t.Fatal("the Kirby template is missing")
	}
	for _, roadmap := range []string{"wordpress", "laravel"} {
		if got[roadmap] {
			t.Fatalf("roadmap template %q is offered in v1", roadmap)
		}
	}
}

// features/quick-app-php.feature — "Choosing the webserver while creating a
// project"
func TestChoosingTheWebserverWhileCreatingAProject(t *testing.T) {
	h := newHarness(t)

	p, err := h.d.Scaffold(h.ctx(), "empty", "legacy-app", "apache")
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	// Then "legacy-app" is created with "webserver_override: apache"
	entry, _ := h.d.Config().Project("legacy-app")
	if entry.WebserverOverride == nil || *entry.WebserverOverride != "apache" {
		t.Fatalf("override = %v, want apache", entry.WebserverOverride)
	}
	// And its settings open, with its apache config one click away: the
	// snapshot marks apache's file as the one in use, which is the entry the
	// GUI puts first.
	if p.Webserver != "apache" {
		t.Fatalf("webserver = %q, want apache", p.Webserver)
	}
	inUse := ""
	for _, c := range p.CustomConfigs {
		if c.Active {
			inUse = c.Webserver
		}
	}
	if inUse != "apache" {
		t.Fatalf("custom config in use = %q, want apache", inUse)
	}

	// And a project created with the webserver left at "Default" has no override
	if _, err := h.d.Scaffold(h.ctx(), "empty", "plain", ""); err != nil {
		t.Fatal(err)
	}
	if plain, _ := h.d.Config().Project("plain"); plain.WebserverOverride != nil {
		t.Fatalf("a default project was pinned to %q", *plain.WebserverOverride)
	}

	// A webserver Wharf does not know is refused before anything is created.
	if _, err := h.d.Scaffold(h.ctx(), "empty", "odd", "lighttpd"); err == nil {
		t.Fatal("an unknown webserver was accepted")
	}
	if _, err := os.Stat(h.root.ProjectDir("odd")); !os.IsNotExist(err) {
		t.Fatal("a folder was created for a refused project")
	}
}

// features/quick-app-php.feature — "Creating an empty project"
func TestCreatingAnEmptyProject(t *testing.T) {
	h := newHarness(t)
	// And nothing is downloaded: any fetch would fail here.
	h.d.scaf.Fetcher = offlineFetcher{}

	if _, err := h.d.Scaffold(h.ctx(), "empty", "blank", ""); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	// Then a folder "www/blank" is created with an "index.php" in it
	entries, err := os.ReadDir(h.root.ProjectDir("blank"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "index.php" {
		t.Fatalf("www/blank holds %v, want only index.php", entries)
	}
	body, _ := os.ReadFile(filepath.Join(h.root.ProjectDir("blank"), "index.php"))
	if !strings.HasPrefix(string(body), "<?php") {
		t.Fatalf("index.php is not PHP:\n%s", body)
	}
}

type offlineFetcher struct{}

func (offlineFetcher) Fetch(context.Context, string, string) error {
	return fmt.Errorf("%w: dial tcp: no such host", project.ErrOffline)
}
