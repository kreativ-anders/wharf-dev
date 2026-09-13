package project

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/layout"
)

func TestValidateName(t *testing.T) {
	ok := []string{"my-kirby-site", "site1", "a-b-c", "ab", "a", "7"}
	bad := []string{"", "-", "-leading", "trailing-", "Upper", "with space", "dots.here", "../escape", "sub/dir"}
	for _, name := range ok {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range bad {
		if err := ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) = nil, want an error", name)
		}
	}
}

// features/quick-app-php.feature — "A typed name becomes a project name"
func TestSlugRewritesTypedNames(t *testing.T) {
	// INFO: The GUI's field is tested against the same table
	// (gui/test/project_name_test.dart), so the two cannot drift apart.
	raw, err := os.ReadFile(filepath.Join("testdata", "slug.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases [][2]string
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		got := Slug(c[0])
		if got != c[1] {
			t.Errorf("Slug(%q) = %q, want %q", c[0], got, c[1])
		}
		if got != "" {
			if err := ValidateName(got); err != nil {
				t.Errorf("Slug(%q) = %q, which is not a valid name: %v", c[0], got, err)
			}
		}
	}
	// INFO: A hostname label ends at 63 characters, and never on a hyphen.
	if got, want := Slug(strings.Repeat("a", 62)+" b"), strings.Repeat("a", 62); got != want {
		t.Errorf("a long name was cut to %q, want %q", got, want)
	}
}

func TestDiscoverListsFoldersOnly(t *testing.T) {
	root := layout.Root{Dir: t.TempDir()}
	if err := root.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"beta", "alpha", ".hidden"} {
		if err := os.MkdirAll(root.ProjectDir(d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root.WWW(), "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "beta"}
	if len(got) != len(want) {
		t.Fatalf("Discover = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Discover = %v, want %v (sorted, no dotfiles, no files)", got, want)
		}
	}
}

func TestScaffoldStripsTheArchiveRootFolder(t *testing.T) {
	root := layout.Root{Dir: t.TempDir()}
	if err := root.Ensure(); err != nil {
		t.Fatal(err)
	}
	s := &Scaffolder{Root: root, Fetcher: zipFetcher(map[string]string{
		"starterkit-main/index.php":       "<?php",
		"starterkit-main/site/config.php": "<?php",
	})}

	if err := s.Create(context.Background(), Template{ID: "kirby", ZipURL: "https://example.invalid/kit.zip"}, "site"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root.ProjectDir("site"), "index.php")); err != nil {
		t.Fatalf("index.php is not at the project root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root.ProjectDir("site"), "starterkit-main")); err == nil {
		t.Fatal("the archive's wrapper folder was not stripped")
	}
}

func TestScaffoldRefusesPathTraversalInAnArchive(t *testing.T) {
	root := layout.Root{Dir: t.TempDir()}
	if err := root.Ensure(); err != nil {
		t.Fatal(err)
	}
	// WARNING: A zip that tries to write outside the project folder. Template archives
	// come off the internet, so this is untrusted input.
	s := &Scaffolder{Root: root, Fetcher: zipFetcher(map[string]string{
		"kit/ok.php":            "<?php",
		"kit/../../escaped.txt": "pwned",
	})}

	err := s.Create(context.Background(), Template{ID: "kirby", ZipURL: "https://example.invalid/kit.zip"}, "site")
	if err == nil {
		t.Fatal("an archive escaping its destination should be refused")
	}
	if _, statErr := os.Stat(filepath.Join(root.Dir, "escaped.txt")); statErr == nil {
		t.Fatal("a file was written outside the project folder")
	}
	if _, statErr := os.Stat(root.ProjectDir("site")); statErr == nil {
		t.Fatal("a partial project folder was left behind")
	}
}

func TestScaffoldRefusesAnExistingFolder(t *testing.T) {
	root := layout.Root{Dir: t.TempDir()}
	if err := root.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root.ProjectDir("site"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s(root).Create(context.Background(), Template{ID: "kirby", ZipURL: "https://example.invalid/kit.zip"}, "site"); err == nil {
		t.Fatal("scaffolding over an existing folder should be refused")
	}
}

func s(root layout.Root) *Scaffolder {
	return &Scaffolder{Root: root, Fetcher: zipFetcher(map[string]string{"kit/index.php": "<?php"})}
}

// zipFetcher unpacks an in-memory archive, standing in for the network.
type zipFetcher map[string]string

func (z zipFetcher) Fetch(_ context.Context, _ string, destDir string) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range z {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(body)); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		return err
	}
	return unzipStripRoot(zr, destDir)
}
