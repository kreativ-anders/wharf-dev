package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// outsideFolder creates a folder with one file in it, somewhere that is not
// the harness's www/.
func outsideFolder(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(append([]string{t.TempDir()}, parts...)...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php echo 'hi';"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// features/project-folders.feature — "Adding a folder from anywhere with the
// folder picker"
func TestAddingAFolderFromAnywhere(t *testing.T) {
	h := newHarness(t)
	dir := outsideFolder(t, "Code", "Client Site")

	p, err := h.d.AddFolder(h.ctx(), dir)
	if err != nil {
		t.Fatalf("add folder: %v", err)
	}

	// Then a project "client-site" is registered
	if p.Name != "client-site" {
		t.Fatalf("name = %q, want client-site", p.Name)
	}
	// And its files stay where they are
	if _, err := os.Stat(filepath.Join(dir, "index.php")); err != nil {
		t.Fatalf("the folder's files moved: %v", err)
	}
	if _, err := os.Stat(h.root.ProjectDir("client-site")); !os.IsNotExist(err) {
		t.Fatal("a folder was created in www/")
	}
	// And the project's config entry records the folder's location as "path"
	entry, _ := h.d.Config().Project("client-site")
	if entry.Path != dir {
		t.Fatalf("path = %q, want %q", entry.Path, dir)
	}
	if !p.Linked || p.Dir != dir {
		t.Fatalf("snapshot dir = %q linked = %v, want %q linked", p.Dir, p.Linked, dir)
	}
	// And its document root is that folder
	if err := h.d.StartProject(h.ctx(), "client-site"); err != nil {
		t.Fatal(err)
	}
	block := vhostBlock(t, h.readGenerated("nginx.conf"), "client-site.localhost")
	if !strings.Contains(block, `root "`+filepath.ToSlash(dir)+`";`) {
		t.Fatalf("document root is not the added folder:\n%s", block)
	}

	// And a folder named "Müller & Söhne" is registered as "mueller-soehne"
	umlauts, err := h.d.AddFolder(h.ctx(), outsideFolder(t, "Code", "Müller & Söhne"))
	if err != nil {
		t.Fatalf("add folder: %v", err)
	}
	if umlauts.Name != "mueller-soehne" {
		t.Fatalf("name = %q, want mueller-soehne", umlauts.Name)
	}
}

// features/project-folders.feature — "Choosing a folder inside www/ registers
// it by name"
func TestChoosingAFolderInsideWWWRegistersItByName(t *testing.T) {
	h := newHarness(t)
	h.mkProject("my-kirby-site")

	p, err := h.d.AddFolder(h.ctx(), h.root.ProjectDir("my-kirby-site"))
	if err != nil {
		t.Fatalf("add folder: %v", err)
	}
	if p.Name != "my-kirby-site" || p.Linked {
		t.Fatalf("got %q linked=%v, want my-kirby-site from www/", p.Name, p.Linked)
	}
	// And no "path" key is written
	raw, err := os.ReadFile(h.root.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"path"`) {
		t.Fatalf("a path key was written for a folder in www/:\n%s", raw)
	}
}

// features/project-folders.feature — "A folder whose name is already taken"
func TestAFolderWhoseNameIsAlreadyTaken(t *testing.T) {
	h := newHarness(t)
	if _, err := h.d.AddFolder(h.ctx(), outsideFolder(t, "one", "client-site")); err != nil {
		t.Fatal(err)
	}

	_, err := h.d.AddFolder(h.ctx(), outsideFolder(t, "two", "client-site"))

	// Then the GUI reports that the name is taken
	var c *ConflictError
	if !errors.As(err, &c) {
		t.Fatalf("err = %v, want a conflict", err)
	}
	if !strings.Contains(err.Error(), "client-site") {
		t.Fatalf("the error does not name the project: %v", err)
	}
	// And nothing is registered
	if n := len(h.d.Config().Projects); n != 1 {
		t.Fatalf("%d projects registered, want 1", n)
	}

	// A folder in www/ with the same name would share its hostname too.
	h.mkProject("taken")
	if _, err := h.d.AddFolder(h.ctx(), outsideFolder(t, "three", "Taken")); !errors.As(err, &c) {
		t.Fatalf("err = %v, want a conflict with www/taken", err)
	}
}

// features/project-folders.feature — "Removing a project added from elsewhere
// leaves its folder alone"
func TestRemovingALinkedProjectLeavesItsFolderAlone(t *testing.T) {
	h := newHarness(t)
	dir := outsideFolder(t, "Code", "Client Site")
	if _, err := h.d.AddFolder(h.ctx(), dir); err != nil {
		t.Fatal(err)
	}

	if err := h.d.RemoveProject(h.ctx(), "client-site"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "index.php")); err != nil {
		t.Fatalf("the folder's files were touched: %v", err)
	}
	if _, ok := h.d.Config().Project("client-site"); ok {
		t.Fatal("project still registered")
	}
}
