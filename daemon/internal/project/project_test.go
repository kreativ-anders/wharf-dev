package project

import (
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

// features/project-folders.feature — "The proposed name comes from the folder name"
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
