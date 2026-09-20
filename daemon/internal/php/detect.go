package php

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Install is a PHP installation found on this machine.
type Install struct {
	// Version is the minor version the tool tracks, e.g. "8.3".
	Version string `json:"version"`
	// FullVersion is what the binary reported, e.g. "8.3.14".
	FullVersion string `json:"full_version"`
	// Dir holds the binaries.
	Dir string `json:"dir"`
	// CLI is the php binary; FastCGI is php-fpm, or php-cgi on Windows.
	CLI     string `json:"cli"`
	FastCGI string `json:"fastcgi,omitempty"`
	// Source is "vendored" for a build under bin/php/, "system" otherwise.
	Source string `json:"source"`
	// Status is the support status at detection time.
	Status Status `json:"status"`
}

// Servable reports whether this install can serve requests. A CLI-only build
// has no FastCGI binary and can be listed but not selected.
func (i Install) Servable() bool { return i.FastCGI != "" }

// FastCGIName is the FastCGI binary this OS ships. php-fpm does not exist on
// Windows, where php-cgi speaks the same protocol over TCP.
func FastCGIName() string {
	if runtime.GOOS == "windows" {
		return "php-cgi.exe"
	}
	return "php-fpm"
}

// CLIName is the interpreter binary's name on this OS.
func CLIName() string {
	if runtime.GOOS == "windows" {
		return "php.exe"
	}
	return "php"
}

// Detector finds PHP installations. Both fields exist so detection can be
// tested without invoking real binaries.
type Detector struct {
	// VendorDir is bin/php, whose subfolders are versions.
	VendorDir string
	// Candidates overrides where system installs are looked for.
	Candidates []string
	// Probe reports a binary's full version. Defaults to running `php -v`.
	Probe func(ctx context.Context, bin string) (string, error)
	// Now fixes the clock for support-status calculation.
	Now func() time.Time
	// Hidden reports whether the user hid a system install's folder. It is
	// passed over before the versions found are merged, so another copy of
	// the same version can take its place. Builds under VendorDir are never
	// hidden: they are Wharf's own, and removed instead.
	Hidden func(dir string) bool
	// PathDir is Wharf's bin/path. Its php only runs another install, so it
	// is never adopted as one of its own, however PATH finds it
	// (php-terminal.feature, "Wharf's own folder is not adopted as another PHP").
	PathDir string
}

// NewDetector returns a Detector for the given bin/php directory.
func NewDetector(vendorDir string) *Detector {
	return &Detector{VendorDir: vendorDir}
}

// Detect returns every PHP installation found, newest version first. Versions
// found in more than one place are reported once, preferring a vendored build
// over a system one so the tool's own copy always wins.
func (d *Detector) Detect(ctx context.Context) []Install {
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}

	found := map[string]Install{}
	add := func(in Install) {
		if in.Version == "" {
			return
		}
		existing, ok := found[in.Version]
		if ok && (existing.Source == "vendored" || !in.Servable()) {
			return
		}
		in.Status = StatusAt(in.Version, now())
		found[in.Version] = in
	}

	for _, in := range d.vendored(ctx) {
		add(in)
	}
	for _, in := range d.system(ctx) {
		add(in)
	}

	out := make([]Install, 0, len(found))
	for _, in := range found {
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool { return Less(out[j].Version, out[i].Version) })
	return out
}

// vendored scans bin/php/<version>/. The folder name is authoritative: it is
// what the rest of the daemon uses to address the version.
func (d *Detector) vendored(ctx context.Context) []Install {
	if d.VendorDir == "" {
		return nil
	}
	entries, err := os.ReadDir(d.VendorDir)
	if err != nil {
		return nil
	}
	var out []Install
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(d.VendorDir, e.Name())
		in := Install{Version: Minor(e.Name()), Dir: dir, Source: "vendored"}
		if p := existing(filepath.Join(dir, CLIName())); p != "" {
			in.CLI = p
			if full, err := d.probe(ctx, p); err == nil && full != "" {
				in.FullVersion = full
				in.Version = Minor(full)
			}
		}
		in.FastCGI = existing(filepath.Join(dir, FastCGIName()))
		if in.CLI == "" && in.FastCGI == "" {
			continue
		}
		out = append(out, in)
	}
	return out
}

// system looks where PHP is normally installed on each OS, plus whatever is on
// PATH. Adopting an existing install is the difference between a tool that
// works on first launch and one that demands a download first.
func (d *Detector) system(ctx context.Context) []Install {
	candidates := d.Candidates
	if candidates == nil {
		candidates = systemCandidates()
	}

	seen := map[string]bool{}
	var out []Install
	for _, cli := range candidates {
		dir := filepath.Dir(cli)
		// WARNING: Before the seen check: the link resolves to the install it
		// runs, which must still be found under its own folder.
		if d.PathDir != "" && filepath.Clean(dir) == filepath.Clean(d.PathDir) {
			continue
		}
		if otherPathBin(dir) {
			continue
		}
		resolved, err := filepath.EvalSymlinks(cli)
		if err != nil {
			resolved = cli
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true

		if d.Hidden != nil && d.Hidden(dir) {
			continue
		}
		full, err := d.probe(ctx, cli)
		if err != nil || full == "" {
			continue
		}
		out = append(out, Install{
			Version:     Minor(full),
			FullVersion: full,
			Dir:         dir,
			CLI:         cli,
			FastCGI:     findFastCGI(dir, Minor(full)),
			Source:      "system",
		})
	}
	return out
}

// otherPathBin reports whether dir is the bin/path of some other Wharf
// folder: a "bin" holding both "path" and the "php" that folder's link points
// into. PathDir above covers this folder's own; the terminal PATH names
// whichever Wharf switched "Use in terminal" on last, which need not be this
// one (php-terminal.feature, "Only the last Wharf folder switched on is on
// the terminal PATH").
//
// WARNING: Adopted, such a folder is listed as a PHP found on the machine
// that holds no php-fpm, so it can never be selected — a row the user can
// only stare at, while the version is offered for download beside it.
func otherPathBin(dir string) bool {
	if filepath.Base(dir) != "path" || filepath.Base(filepath.Dir(dir)) != "bin" {
		return false
	}
	info, err := os.Stat(filepath.Join(filepath.Dir(dir), "php"))
	return err == nil && info.IsDir()
}

// systemCandidates lists plausible php binaries for this OS. Globs keep the
// list short: one pattern covers every Homebrew or distro version.
func systemCandidates() []string {
	var patterns []string
	switch runtime.GOOS {
	case "windows":
		patterns = []string{
			`C:\php\php.exe`, `C:\php*\php.exe`, `C:\tools\php*\php.exe`,
			`C:\xampp\php\php.exe`, `C:\laragon\bin\php\*\php.exe`,
		}
	case "darwin":
		patterns = []string{
			"/opt/homebrew/opt/php@*/bin/php", "/opt/homebrew/opt/php/bin/php",
			"/usr/local/opt/php@*/bin/php", "/usr/local/opt/php/bin/php",
			"/opt/homebrew/bin/php8*", "/usr/local/bin/php8*",
			"/usr/bin/php",
		}
	default:
		patterns = []string{
			"/usr/bin/php", "/usr/bin/php8*", "/usr/local/bin/php8*",
			"/usr/lib/php*/bin/php", "/opt/php*/bin/php",
			"/home/linuxbrew/.linuxbrew/opt/php@*/bin/php",
		}
	}

	var out []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, m := range matches {
			if isExecutableFile(m) {
				out = append(out, m)
			}
		}
	}
	// INFO: Whatever `php` resolves to on PATH, which may be none of the above.
	if p, err := exec.LookPath(CLIName()); err == nil {
		out = append(out, p)
	}
	return out
}

// findFastCGI looks for the FastCGI binary beside the interpreter, then in the
// sbin directory Homebrew and some distros use for php-fpm.
func findFastCGI(binDir, version string) string {
	name := FastCGIName()
	candidates := []string{
		filepath.Join(binDir, name),
		filepath.Join(binDir, name+"-"+version),
		filepath.Join(filepath.Dir(binDir), "sbin", name),
		filepath.Join(binDir, "php-fpm"+version),
	}
	for _, c := range candidates {
		if p := existing(c); p != "" {
			return p
		}
	}
	return ""
}

var versionRe = regexp.MustCompile(`PHP (\d+\.\d+\.\d+)`)

// probe asks a binary for its version.
func (d *Detector) probe(ctx context.Context, bin string) (string, error) {
	if d.Probe != nil {
		return d.Probe(ctx, bin)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(probeCtx, bin, "-v").Output()
	if err != nil {
		return "", err
	}
	m := versionRe.FindStringSubmatch(string(out))
	if m == nil {
		return "", nil
	}
	return m[1], nil
}

func existing(path string) string {
	if isExecutableFile(path) {
		return path
	}
	return ""
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Ext(path), ".exe")
	}
	return info.Mode().Perm()&0o111 != 0
}
