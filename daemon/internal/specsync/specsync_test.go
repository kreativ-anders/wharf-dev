package specsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// featuresDir is the Gherkin spec directory, relative to this package.
const featuresDir = "../../../features"

// sourceDirs are the trees that implement those specs: the Go daemon and the
// Flutter GUI. Both may claim scenarios.
var sourceDirs = []string{"../..", "../../../gui/test"}

// TestSpecCoverage is the sync rule itself: the specs in features/ and the
// tests in daemon/ must agree, in both directions. See CLAUDE.md.
func TestSpecCoverage(t *testing.T) {
	scenarios, err := ParseFeatures(featuresDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) == 0 {
		t.Fatalf("no scenarios parsed from %s — has the spec directory moved?", featuresDir)
	}
	var claims []Claim
	for _, dir := range sourceDirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue // INFO: the GUI may not be checked out
		}
		found, err := ParseClaims(dir)
		if err != nil {
			t.Fatal(err)
		}
		claims = append(claims, found...)
	}

	rep := Check(scenarios, claims)

	for _, s := range rep.Uncovered {
		t.Errorf("%s:%d: @v1 scenario %q has no test.\n"+
			"    Implement it and claim it with a comment above the test:\n"+
			"      // features/%s — %q\n"+
			"    Or re-tag the scenario @roadmap if it is not part of v1.",
			filepath.Join(featuresDir, s.Feature), s.Line, s.Title, s.Feature, s.Title)
	}
	for _, c := range rep.Unknown {
		t.Errorf("%s:%d: %s claims scenario %q in %s, which does not exist.\n"+
			"    The scenario was renamed or deleted — update the claim to match features/%s.",
			c.File, c.Line, c.Test, c.Title, c.Feature, c.Feature)
	}
	for _, c := range rep.Roadmap {
		t.Errorf("%s:%d: %s claims %q, which is tagged @roadmap.\n"+
			"    Roadmap scenarios are out of v1 scope (dev/architecture.md §2).\n"+
			"    If it has genuinely been implemented, re-tag the scenario @v1 first.",
			c.File, c.Line, c.Test, c.Title)
	}

	if t.Failed() || testing.Verbose() {
		t.Log(Matrix(scenarios, rep))
	}
}

// The parser is what the rule rests on, so its own behaviour is pinned down.
func TestParseFeaturesReadsTagsAndScenarios(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "example.feature"), `@v1
Feature: Example
  As a developer

  Background:
    Given something

  Scenario: First one
    When a thing happens
    Then it works

  @roadmap
  Scenario: Later one
    Then it also works
`)
	write(t, filepath.Join(dir, "all-roadmap.feature"), `@roadmap
Feature: Everything here is later

  Scenario: Not in v1
    Then nothing happens
`)

	got, err := ParseFeatures(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("parsed %d scenarios, want 3: %+v", len(got), got)
	}

	first := find(t, got, "First one")
	if !first.Has(TagV1) || first.Has(TagRoadmap) {
		t.Fatalf("scenario tags = %v, want the feature's @v1 only", first.Tags)
	}

	later := find(t, got, "Later one")
	if !later.Has(TagRoadmap) {
		t.Fatalf("a scenario-level @roadmap tag was lost: %v", later.Tags)
	}
	if !later.Has(TagV1) {
		t.Fatalf("feature-level tags should still apply: %v", later.Tags)
	}

	// INFO: A feature-level @roadmap covers every scenario in the file.
	inherited := find(t, got, "Not in v1")
	if !inherited.Has(TagRoadmap) {
		t.Fatalf("feature-level @roadmap was not inherited: %v", inherited.Tags)
	}
}

func TestParseClaimsReadsDartTestsToo(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "gui_test.dart"), "import 'x';\n\n"+
		claimLine("settings.feature", "Settings screen hides roadmap services in v1")+
		"  testWidgets('settings show only Webserver and PHP runtime', (tester) async {});\n")

	got, err := ParseClaims(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("claims = %+v, want one", got)
	}
	if got[0].Test != "settings show only Webserver and PHP runtime" {
		t.Fatalf("test name = %q", got[0].Test)
	}
}

func TestParseClaimsRequiresTheCommentDirectlyAboveTheTest(t *testing.T) {
	dir := t.TempDir()
	// WARNING: The claim lines are assembled rather than written literally: this file
	// is itself scanned by TestSpecCoverage, and a literal claim here would
	// be read as a real one.
	write(t, filepath.Join(dir, "x_test.go"), "package x\n\n"+
		claimLine("pretty-urls.feature", "Elevation is declined")+
		"func TestElevationDeclined(t *testing.T) {}\n\n"+
		claimLine("pretty-urls.feature", "Detached claim")+
		"\nfunc TestSomethingElse(t *testing.T) {}\n")
	got, err := ParseClaims(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("claims = %+v, want only the one directly above a test", got)
	}
	if got[0].Test != "TestElevationDeclined" || got[0].Title != "Elevation is declined" {
		t.Fatalf("claim = %+v", got[0])
	}
}

func TestCheckReportsBothDirections(t *testing.T) {
	scenarios := []Scenario{
		{Feature: "a.feature", Title: "Implemented", Tags: []string{TagV1}},
		{Feature: "a.feature", Title: "Forgotten", Tags: []string{TagV1}},
		{Feature: "a.feature", Title: "Later", Tags: []string{TagV1, TagRoadmap}},
	}
	claims := []Claim{
		{Feature: "a.feature", Title: "Implemented", Test: "TestImplemented"},
		{Feature: "a.feature", Title: "Renamed away", Test: "TestStale"},
		{Feature: "a.feature", Title: "Later", Test: "TestTooEarly"},
	}

	rep := Check(scenarios, claims)
	if rep.OK() {
		t.Fatal("Check reported agreement despite three problems")
	}
	if len(rep.Uncovered) != 1 || rep.Uncovered[0].Title != "Forgotten" {
		t.Fatalf("uncovered = %+v", rep.Uncovered)
	}
	if len(rep.Unknown) != 1 || rep.Unknown[0].Test != "TestStale" {
		t.Fatalf("unknown = %+v", rep.Unknown)
	}
	if len(rep.Roadmap) != 1 || rep.Roadmap[0].Test != "TestTooEarly" {
		t.Fatalf("roadmap = %+v", rep.Roadmap)
	}

	good := Check(scenarios[:1], claims[:1])
	if !good.OK() {
		t.Fatalf("a matching pair was reported as out of sync: %+v", good)
	}
}

func TestParseClaimsAcceptsAWrappedTitle(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "y_test.go"), "package y\n\n"+
		"// features/service-management.feature — \"Per-project override takes\n"+
		"// precedence over the global default\"\n"+
		"func TestOverride(t *testing.T) {}\n")

	got, err := ParseClaims(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("claims = %+v, want one", got)
	}
	if got[0].Title != "Per-project override takes precedence over the global default" {
		t.Fatalf("wrapped title = %q", got[0].Title)
	}
}

// claimLine renders a claim comment the way a test file carries one.
func claimLine(feature, title string) string {
	return "// features/" + feature + " — \"" + title + "\"\n"
}

func find(t *testing.T, in []Scenario, title string) Scenario {
	t.Helper()
	for _, s := range in {
		if s.Title == title {
			return s
		}
	}
	t.Fatalf("no scenario %q in %+v", title, in)
	return Scenario{}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimLeft(body, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}
