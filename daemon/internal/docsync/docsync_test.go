package docsync

import (
	"encoding/json"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// repo is the repository root, relative to this package.
const repo = "../../.."

func read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repo, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// CLAUDE.md §1: "a stale map is worse than none". Every file in the places
// the map covers must at least be named in it.
func TestFileDirectoryNamesEveryFile(t *testing.T) {
	claude := read(t, "CLAUDE.md")
	var missing []string
	need := func(rel, name string) {
		if !strings.Contains(claude, name) {
			missing = append(missing, rel)
		}
	}

	for _, dir := range []string{".", "dev", "docs", "features", "tool", "packaging/macos", "packaging/linux", "packaging/windows", ".github/workflows", "gui/test"} {
		entries, err := os.ReadDir(filepath.Join(repo, dir))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			need(filepath.ToSlash(filepath.Join(dir, e.Name())), e.Name())
		}
	}
	for _, dir := range []string{"daemon/cmd", "daemon/internal"} {
		entries, err := os.ReadDir(filepath.Join(repo, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() {
				need(dir+"/"+e.Name(), e.Name()+"/")
			}
		}
	}
	gui := filepath.Join(repo, "gui", "lib")
	err := filepath.WalkDir(gui, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".dart") {
			return err
		}
		rel, _ := filepath.Rel(repo, path)
		need(filepath.ToSlash(rel), d.Name())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, rel := range missing {
		t.Errorf("%s is not in the CLAUDE.md file directory (§2).\n"+
			"    Add it in the same commit that added the file — or, if it was renamed, fix the entry.", rel)
	}
}

// CLAUDE.md §2: docs/index.html's FAQ is "mirrored in its JSON-LD". Search
// engines read the copy, people read the page; a question in only one of
// them is a page that says two different things.
func TestFAQIsMirroredInJSONLD(t *testing.T) {
	page := read(t, "docs/index.html")

	visible := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?s)<summary>(.*?)</summary>`).FindAllStringSubmatch(page, -1) {
		visible[plain(m[1])] = true
	}
	if len(visible) == 0 {
		t.Fatal("no <summary> in docs/index.html — has the FAQ markup changed? Update this test with it.")
	}

	block := regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`).FindStringSubmatch(page)
	if block == nil {
		t.Fatal("no JSON-LD block in docs/index.html")
	}
	var ld any
	if err := json.Unmarshal([]byte(block[1]), &ld); err != nil {
		t.Fatalf("docs/index.html JSON-LD does not parse: %v", err)
	}
	structured := map[string]bool{}
	collectQuestions(ld, structured)

	for _, q := range sorted(visible) {
		if !structured[q] {
			t.Errorf("FAQ question %q is on the page but not in its JSON-LD FAQPage.", q)
		}
	}
	for _, q := range sorted(structured) {
		if !visible[q] {
			t.Errorf("JSON-LD question %q is not on the page — search results would show an answer the page does not give.", q)
		}
	}
}

func collectQuestions(v any, into map[string]bool) {
	switch v := v.(type) {
	case map[string]any:
		if v["@type"] == "Question" {
			if name, ok := v["name"].(string); ok {
				into[plain(name)] = true
			}
		}
		for _, child := range v {
			collectQuestions(child, into)
		}
	case []any:
		for _, child := range v {
			collectQuestions(child, into)
		}
	}
}

// plain is text as a reader sees it: tags out, entities in, spaces collapsed.
func plain(s string) string {
	s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, "")
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Settings shows the version wharfd was stamped with (features/settings.feature,
// "General shows the version, and no update check yet"). A build that forgets
// the stamp reports "dev" — which the Windows build did until the stamp was
// added to its CMake target.
func TestEveryDaemonBuildIsStamped(t *testing.T) {
	builds := regexp.MustCompile(`\bbuild\b.*\./cmd/wharfd\b`)
	for _, rel := range []string{"Makefile", "gui/windows/runner/CMakeLists.txt", "gui/linux/CMakeLists.txt", ".github/workflows/release.yml"} {
		b, err := os.ReadFile(filepath.Join(repo, rel))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if builds.MatchString(line) && !strings.Contains(line, "main.version") && !strings.Contains(line, "LDFLAGS") {
				t.Errorf("%s:%d builds wharfd without stamping its version:\n    %s\n"+
					"    Pass -ldflags \"-X main.version=…\" from tool/version.sh (dev/releasing.md).",
					rel, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// The release workflow names scripts and files it only touches at release
// time; a rename would otherwise surface on the day of a release.
func TestReleaseWorkflowPathsExist(t *testing.T) {
	workflow := read(t, ".github/workflows/release.yml")
	// INFO: Not preceded by a slash or a word character: /dev/null is not dev/.
	pathRe := regexp.MustCompile(`(?:^|[^/\w.$])((?:tool|daemon|gui|packaging|dev)/[A-Za-z0-9_./-]*[A-Za-z0-9_-])`)
	seen := map[string]bool{}
	for _, m := range pathRe.FindAllStringSubmatch(workflow, -1) {
		p := m[1]
		// INFO: Build output, not repository content.
		if seen[p] || strings.HasPrefix(p, "gui/build/") {
			continue
		}
		seen[p] = true
		if _, err := os.Stat(filepath.Join(repo, p)); err != nil {
			t.Errorf(".github/workflows/release.yml names %s, which does not exist.", p)
		}
	}
	if !seen["tool/version.sh"] {
		t.Error("the release workflow no longer uses tool/version.sh — the version would have two sources.")
	}
}

// tool/version.sh is the one place a version is computed; its arithmetic and
// its refusals are pinned down here, against a throwaway pubspec.
func TestVersionScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tool/version.sh runs under bash; the release workflow runs it on Linux")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	script, _ := filepath.Abs(filepath.Join(repo, "tool", "version.sh"))
	pubspec := filepath.Join(t.TempDir(), "pubspec.yaml")
	const original = "name: wharf_gui\n# the version line below\nversion: 1.4.2+7\n\nflutter:\n"
	if err := os.WriteFile(pubspec, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		cmd := exec.Command(bash, append([]string{script}, args...)...)
		cmd.Env = append(os.Environ(), "PUBSPEC="+pubspec)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}

	for kind, want := range map[string]string{
		"patch":  "1.4.3+8",
		"minor":  "1.5.0+8",
		"major":  "2.0.0+8",
		"1.10.0": "1.10.0+8",
	} {
		if got, err := run("next", kind); err != nil || got != want {
			t.Errorf("next %s = %q (%v), want %q", kind, got, err, want)
		}
	}
	for _, kind := range []string{"1.4.2", "1.3.9", "01.5.0", "banana", ""} {
		if got, err := run("next", kind); err == nil {
			t.Errorf("next %q = %q, want a refusal", kind, got)
		}
	}

	if got, err := run("bump", "minor"); err != nil || got != "1.5.0+8" {
		t.Fatalf("bump minor = %q (%v)", got, err)
	}
	b, _ := os.ReadFile(pubspec)
	if want := strings.Replace(original, "1.4.2+7", "1.5.0+8", 1); string(b) != want {
		t.Errorf("bump changed more than the version line:\n%s", b)
	}

	if _, err := run("check-tag", "v1.5.0"); err != nil {
		t.Errorf("check-tag v1.5.0 refused for a pubspec at 1.5.0: %v", err)
	}
	if _, err := run("check-tag", "v1.4.2"); err == nil {
		t.Error("check-tag v1.4.2 accepted for a pubspec at 1.5.0")
	}
}

// What Settings will show for this checkout: a clean release tag gives the
// bare version, anything else says which commit it is.
func TestDescribeIsWellFormed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tool/version.sh runs under bash")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	out, err := exec.Command(bash, filepath.Join(repo, "tool", "version.sh"), "describe").CombinedOutput()
	if err != nil {
		t.Fatalf("version.sh describe: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if !regexp.MustCompile(`^\d+\.\d+\.\d+(\+[0-9a-f]{4,}(\.dirty)?)?$`).MatchString(got) {
		t.Errorf("version.sh describe = %q, want X.Y.Z or X.Y.Z+<commit>[.dirty]", got)
	}
}
