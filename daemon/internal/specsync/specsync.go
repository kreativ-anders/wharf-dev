// Package specsync keeps the Gherkin specs in features/ and the code that
// implements them from drifting apart.
//
// The rule: every scenario tagged @v1 must be claimed by at least one Go test,
// and every claim must name a scenario that still exists. A test claims a
// scenario with a comment directly above it:
//
//	// features/service-management.feature — "Switching the active webserver"
//	func TestSwitchingTheActiveWebserver(t *testing.T) { … }
//
// TestSpecCoverage in this package enforces both directions, so renaming a
// scenario, deleting one, or adding one without an implementation all fail the
// build rather than quietly rotting.
package specsync

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Tags a scenario may carry.
const (
	TagV1      = "@v1"
	TagRoadmap = "@roadmap"
)

// Scenario is one Gherkin scenario.
type Scenario struct {
	Feature string // file name, e.g. "service-management.feature"
	Title   string // text after "Scenario:"
	Tags    []string
	Line    int
}

// Key is how a scenario is referenced from a test claim.
func (s Scenario) Key() string { return s.Feature + " — " + s.Title }

// Has reports whether the scenario carries a tag, including tags inherited
// from its Feature.
func (s Scenario) Has(tag string) bool {
	for _, t := range s.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// Claim is a test's reference to a scenario.
type Claim struct {
	Feature string
	Title   string
	File    string
	Line    int
	Test    string
}

func (c Claim) Key() string { return c.Feature + " — " + c.Title }

var (
	tagLineRe   = regexp.MustCompile(`(^|\s)(@[a-zA-Z0-9_-]+)`)
	scenarioRe  = regexp.MustCompile(`^\s*Scenario(?: Outline)?:\s*(.+?)\s*$`)
	featureRe   = regexp.MustCompile(`^\s*Feature:`)
	claimOpenRe = regexp.MustCompile(`^\s*//\s*features/([a-z0-9-]+\.feature)\s*[—-]\s*"(.*)$`)
	commentRe   = regexp.MustCompile(`^\s*//\s?(.*)$`)
	funcRe      = regexp.MustCompile(`^func (Test[A-Za-z0-9_]*)\(`)
	// The GUI implements these specs too, so Dart tests may claim scenarios.
	dartTestRe  = regexp.MustCompile(`^\s*(?:testWidgets|test)\(\s*'([^']*)`)
	featureFile = regexp.MustCompile(`\.feature$`)
)

// ParseFeatures reads every .feature file in dir. Tags written above the
// Feature keyword apply to every scenario in that file, which is how
// roadmap-services.feature marks all of its scenarios at once.
func ParseFeatures(dir string) ([]Scenario, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read features dir: %w", err)
	}

	var out []Scenario
	for _, e := range entries {
		if e.IsDir() || !featureFile.MatchString(e.Name()) {
			continue
		}
		scenarios, err := parseFeatureFile(filepath.Join(dir, e.Name()), e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, scenarios...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out, nil
}

func parseFeatureFile(path, name string) ([]Scenario, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		out         []Scenario
		pending     []string // tags seen since the last keyword
		featureTags []string
		sawFeature  bool
		lineNo      int
		scanner     = bufio.NewScanner(f)
	)
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "@") {
			for _, m := range tagLineRe.FindAllStringSubmatch(trimmed, -1) {
				pending = append(pending, m[2])
			}
			continue
		}
		if featureRe.MatchString(line) && !sawFeature {
			sawFeature = true
			featureTags = pending
			pending = nil
			continue
		}
		if m := scenarioRe.FindStringSubmatch(line); m != nil {
			tags := append(append([]string{}, featureTags...), pending...)
			pending = nil
			out = append(out, Scenario{Feature: name, Title: m[1], Tags: tags, Line: lineNo})
			continue
		}
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			// Any other content ends a tag block that was not consumed.
			if !strings.HasPrefix(trimmed, "|") && !strings.HasPrefix(trimmed, "\"\"\"") {
				pending = nil
			}
		}
	}
	return out, scanner.Err()
}

// ParseClaims walks a source tree and collects every scenario claim written
// above a test — a Go test function or a Dart test() / testWidgets() call.
func ParseClaims(root string) ([]Claim, error) {
	var out []Claim
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", ".git", "build", ".dart_tool", "ephemeral", "Pods":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") && !strings.HasSuffix(path, "_test.dart") {
			return nil
		}
		claims, err := parseClaimsInFile(path)
		if err != nil {
			return err
		}
		out = append(out, claims...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out, nil
}

func parseClaimsInFile(path string) ([]Claim, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		out     []Claim
		pending []Claim
		// A claim's title may wrap onto the following comment lines, so that
		// a long scenario name does not force an unreadable source line.
		openClaim *Claim
		openTitle string
		lineNo    int
		scanner   = bufio.NewScanner(f)
	)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	closeClaim := func() {
		if openClaim == nil {
			return
		}
		openClaim.Title = strings.Join(strings.Fields(openTitle), " ")
		pending = append(pending, *openClaim)
		openClaim, openTitle = nil, ""
	}

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()

		// Continuing a wrapped claim title.
		if openClaim != nil {
			m := commentRe.FindStringSubmatch(line)
			if m == nil {
				openClaim, openTitle = nil, "" // an unterminated claim is not a claim
				continue
			}
			rest := m[1]
			if idx := strings.Index(rest, `"`); idx >= 0 {
				openTitle += " " + rest[:idx]
				closeClaim()
			} else {
				openTitle += " " + rest
			}
			continue
		}

		if m := claimOpenRe.FindStringSubmatch(line); m != nil {
			c := Claim{Feature: m[1], File: path, Line: lineNo}
			rest := m[2]
			if idx := strings.Index(rest, `"`); idx >= 0 {
				c.Title = strings.Join(strings.Fields(rest[:idx]), " ")
				pending = append(pending, c)
			} else {
				openClaim, openTitle = &c, rest
			}
			continue
		}
		if name, ok := testName(line); ok {
			for _, c := range pending {
				c.Test = name
				out = append(out, c)
			}
			pending = nil
			continue
		}
		if strings.TrimSpace(line) == "" {
			// A blank line separates a comment block from what follows, so a
			// claim must sit directly above its test.
			pending = nil
		}
	}
	return out, scanner.Err()
}

// testName returns the name of the test a line declares, in either language.
func testName(line string) (string, bool) {
	if m := funcRe.FindStringSubmatch(line); m != nil {
		return m[1], true
	}
	if m := dartTestRe.FindStringSubmatch(line); m != nil {
		return m[1], true
	}
	return "", false
}

// Report is the outcome of comparing specs against claims.
type Report struct {
	Uncovered []Scenario // @v1 scenarios no test claims
	Unknown   []Claim    // claims naming a scenario that no longer exists
	Roadmap   []Claim    // claims on @roadmap scenarios, which v1 must not implement
	Covered   map[string][]string
}

// OK reports whether the specs and the code agree.
func (r Report) OK() bool {
	return len(r.Uncovered) == 0 && len(r.Unknown) == 0 && len(r.Roadmap) == 0
}

// Check compares parsed scenarios with parsed claims.
func Check(scenarios []Scenario, claims []Claim) Report {
	byKey := map[string]Scenario{}
	for _, s := range scenarios {
		byKey[s.Key()] = s
	}

	rep := Report{Covered: map[string][]string{}}
	for _, c := range claims {
		s, ok := byKey[c.Key()]
		if !ok {
			rep.Unknown = append(rep.Unknown, c)
			continue
		}
		if s.Has(TagRoadmap) {
			rep.Roadmap = append(rep.Roadmap, c)
			continue
		}
		rep.Covered[c.Key()] = append(rep.Covered[c.Key()], c.Test)
	}
	for _, s := range scenarios {
		if !s.Has(TagV1) || s.Has(TagRoadmap) {
			continue
		}
		if len(rep.Covered[s.Key()]) == 0 {
			rep.Uncovered = append(rep.Uncovered, s)
		}
	}
	return rep
}

// Matrix renders the coverage table printed by `make spec`.
func Matrix(scenarios []Scenario, rep Report) string {
	var sb strings.Builder
	feature := ""
	for _, s := range scenarios {
		if s.Feature != feature {
			feature = s.Feature
			fmt.Fprintf(&sb, "\n%s\n", feature)
		}
		switch {
		case s.Has(TagRoadmap):
			fmt.Fprintf(&sb, "  ○ %-56s roadmap\n", s.Title)
		case len(rep.Covered[s.Key()]) > 0:
			fmt.Fprintf(&sb, "  ✓ %-56s %s\n", s.Title, strings.Join(rep.Covered[s.Key()], ", "))
		default:
			fmt.Fprintf(&sb, "  ✗ %-56s NO TEST\n", s.Title)
		}
	}
	return sb.String()
}
