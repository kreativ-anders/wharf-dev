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

	"github.com/manuel-steinberg/wharf/daemon/internal/project"
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

	p, err := h.d.Scaffold(h.ctx(), tpl.ID, "my-kirby-site")
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

	// And a hosts entry for "my-kirby-site.wharf" is requested
	if !strings.Contains(h.hostsContent(), "my-kirby-site.wharf") {
		t.Fatalf("no hosts entry was requested:\n%s", h.hostsContent())
	}

	// And the project appears in the GUI's project list
	found := h.project("my-kirby-site")
	if found.Name != "my-kirby-site" {
		t.Fatalf("project list does not contain the new project: %+v", h.d.State().Projects)
	}
	if p.URL == "" {
		t.Fatal("scaffolded project has no URL to open")
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

// features/quick-app-php.feature — "Scaffolding fails without network access"
func TestScaffoldingFailsWithoutNetworkAccess(t *testing.T) {
	h := newHarness(t)
	h.d.scaf.Fetcher = offlineFetcher{}

	_, err := h.d.Scaffold(h.ctx(), "kirby", "my-kirby-site")

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

type offlineFetcher struct{}

func (offlineFetcher) Fetch(context.Context, string, string) error {
	return fmt.Errorf("%w: dial tcp: no such host", project.ErrOffline)
}
