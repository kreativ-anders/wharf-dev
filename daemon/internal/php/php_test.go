package php

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func at(s string) time.Time { return date(s) }

func TestStatusAtTracksTheSupportTimeline(t *testing.T) {
	cases := []struct {
		version string
		when    string
		want    Status
	}{
		{"8.3", "2023-06-01", StatusUnreleased},
		{"8.3", "2024-06-01", StatusActive},
		{"8.3", "2026-06-01", StatusSecurity},
		{"8.3", "2028-06-01", StatusEndOfLife},
		{"8.1", "2026-06-01", StatusEndOfLife},
		{"7.4", "2026-06-01", StatusUnknown},
	}
	for _, c := range cases {
		if got := StatusAt(c.version, at(c.when)); got != c.want {
			t.Errorf("StatusAt(%s, %s) = %s, want %s", c.version, c.when, got, c.want)
		}
	}
}

func TestRecommendedIsTheNewestActivelySupportedRelease(t *testing.T) {
	// INFO: Between 8.4's release and 8.5's, 8.4 is the newest in active support.
	if got := Recommended(at("2025-06-01")); got != "8.4" {
		t.Errorf("Recommended(2025-06-01) = %s, want 8.4", got)
	}
	// INFO: Once 8.5 is out, it takes over.
	if got := Recommended(at("2026-03-01")); got != "8.5" {
		t.Errorf("Recommended(2026-03-01) = %s, want 8.5", got)
	}
	// INFO: An unreleased version is never recommended, however new the table is.
	if got := Recommended(at("2024-01-01")); got != "8.3" {
		t.Errorf("Recommended(2024-01-01) = %s, want 8.3", got)
	}
}

func TestPreferredChoosesFromWhatIsInstalled(t *testing.T) {
	now := at("2026-03-01") // INFO: 8.4 and 8.5 active; 8.2 and 8.3 security; 8.1 EOL

	if got := Preferred([]string{"8.1", "8.3", "8.4"}, now); got != "8.4" {
		t.Errorf("Preferred = %s, want the actively supported 8.4", got)
	}
	// INFO: With nothing active installed, the newest still getting security fixes.
	if got := Preferred([]string{"8.1", "8.2", "8.3"}, now); got != "8.3" {
		t.Errorf("Preferred = %s, want 8.3", got)
	}
	// INFO: Only end-of-life builds: still better than nothing, and the GUI says so.
	if got := Preferred([]string{"8.1"}, now); got != "8.1" {
		t.Errorf("Preferred = %s, want 8.1", got)
	}
	if got := Preferred(nil, now); got != "" {
		t.Errorf("Preferred(nothing) = %q, want empty so the caller falls back", got)
	}
}

func TestVersionsSortNumerically(t *testing.T) {
	got := []string{"8.9", "8.10", "8.2"}
	Sort(got)
	want := []string{"8.2", "8.9", "8.10"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Sort = %v, want %v", got, want)
		}
	}
}

func TestMinor(t *testing.T) {
	for in, want := range map[string]string{"8.3.14": "8.3", "8.3": "8.3", "8": "8", " 8.4.1 ": "8.4"} {
		if got := Minor(in); got != want {
			t.Errorf("Minor(%q) = %q, want %q", in, got, want)
		}
	}
}

// The release table is the one thing here that ages; a self-inconsistent entry
// would silently distort every recommendation.
func TestReleaseTableIsConsistent(t *testing.T) {
	for i, r := range Releases {
		if !r.Released.Before(r.ActiveUntil) || !r.ActiveUntil.Before(r.SecurityUntil) {
			t.Errorf("%s: dates out of order: released %s, active until %s, security until %s",
				r.Version, r.Released, r.ActiveUntil, r.SecurityUntil)
		}
		if i > 0 && !Less(Releases[i-1].Version, r.Version) {
			t.Errorf("Releases is not ordered oldest first at %s", r.Version)
		}
	}
	if _, ok := Known(MinimumForKirby); !ok {
		t.Errorf("MinimumForKirby %s is not in the release table", MinimumForKirby)
	}
}

func TestDetectFindsVendoredBuilds(t *testing.T) {
	root := t.TempDir()
	vendor := filepath.Join(root, "php")
	stub(t, filepath.Join(vendor, "8.3", FastCGIName()))
	stub(t, filepath.Join(vendor, "8.3", CLIName()))
	stub(t, filepath.Join(vendor, "8.1", FastCGIName()))
	// INFO: A folder with nothing usable in it is not an installation.
	if err := os.MkdirAll(filepath.Join(vendor, "8.0"), 0o755); err != nil {
		t.Fatal(err)
	}

	d := &Detector{
		VendorDir:  vendor,
		Candidates: []string{},
		Probe:      func(context.Context, string) (string, error) { return "8.3.14", nil },
		Now:        func() time.Time { return at("2026-03-01") },
	}
	got := d.Detect(context.Background())

	if len(got) != 2 {
		t.Fatalf("detected %+v, want 8.3 and 8.1", got)
	}
	// INFO: Newest first, so the picker's default sits at the top.
	if got[0].Version != "8.3" || got[1].Version != "8.1" {
		t.Fatalf("order = %s, %s; want newest first", got[0].Version, got[1].Version)
	}
	if got[0].FullVersion != "8.3.14" {
		t.Errorf("full version = %q, want the probed 8.3.14", got[0].FullVersion)
	}
	if got[0].Source != "vendored" || !got[0].Servable() {
		t.Errorf("install = %+v, want a servable vendored build", got[0])
	}
	if got[0].Status != StatusSecurity {
		t.Errorf("status = %s, want security in March 2026", got[0].Status)
	}
}

func TestDetectAdoptsSystemInstalls(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	cli := filepath.Join(bin, CLIName())
	stub(t, cli)
	stub(t, filepath.Join(bin, FastCGIName()))

	d := &Detector{
		VendorDir:  filepath.Join(dir, "none"),
		Candidates: []string{cli},
		Probe:      func(context.Context, string) (string, error) { return "8.4.3", nil },
		Now:        func() time.Time { return at("2026-03-01") },
	}
	got := d.Detect(context.Background())
	if len(got) != 1 {
		t.Fatalf("detected %+v, want one system install", got)
	}
	if got[0].Source != "system" || got[0].Version != "8.4" || !got[0].Servable() {
		t.Fatalf("install = %+v", got[0])
	}
	if got[0].Status != StatusActive {
		t.Errorf("status = %s, want active", got[0].Status)
	}
}

// A vendored build and a system build of the same version are one entry, and
// the tool's own copy wins.
func TestVendoredBuildsBeatSystemOnes(t *testing.T) {
	dir := t.TempDir()
	vendor := filepath.Join(dir, "vendor", "php")
	stub(t, filepath.Join(vendor, "8.4", FastCGIName()))

	sysBin := filepath.Join(dir, "usr", "bin")
	sysCLI := filepath.Join(sysBin, CLIName())
	stub(t, sysCLI)
	stub(t, filepath.Join(sysBin, FastCGIName()))

	d := &Detector{
		VendorDir:  vendor,
		Candidates: []string{sysCLI},
		Probe:      func(context.Context, string) (string, error) { return "8.4.3", nil },
		Now:        func() time.Time { return at("2026-03-01") },
	}
	got := d.Detect(context.Background())
	if len(got) != 1 {
		t.Fatalf("detected %+v, want one entry for 8.4", got)
	}
	if got[0].Source != "vendored" {
		t.Fatalf("source = %s, want the vendored build to win", got[0].Source)
	}
}

// A CLI-only install is reported but cannot serve: nginx needs FastCGI.
func TestCLIOnlyInstallIsNotServable(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "bin", CLIName())
	stub(t, cli)

	d := &Detector{
		VendorDir:  filepath.Join(dir, "none"),
		Candidates: []string{cli},
		Probe:      func(context.Context, string) (string, error) { return "8.4.3", nil },
		Now:        func() time.Time { return at("2026-03-01") },
	}
	got := d.Detect(context.Background())
	if len(got) != 1 || got[0].Servable() {
		t.Fatalf("install = %+v, want a listed but unservable entry", got)
	}
}

func stub(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0o755)
	if runtime.GOOS == "windows" {
		mode = 0o644
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
}
