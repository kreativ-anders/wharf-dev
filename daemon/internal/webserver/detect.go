// Package webserver finds and installs nginx and Apache (webserver-install
// .feature). One copy of each serves every project; which copy that is — one
// Wharf installed under bin/, one from a package manager, one the OS ships —
// is decided here and nowhere else.
package webserver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// The webservers Wharf knows how to run.
const (
	Nginx  = "nginx"
	Apache = "apache"
)

// Install is a webserver found on this machine.
type Install struct {
	Name   string `json:"name"`
	Binary string `json:"binary"`
	// Version is what the binary reported, e.g. "1.31.5".
	Version string `json:"version,omitempty"`
	// Source is "wharf" for a copy under bin/, "homebrew", or "system".
	Source string `json:"source"`
	// Modules is where Apache's loadable modules are. Every distribution
	// puts them somewhere else, and the generated config must name them.
	Modules string `json:"modules,omitempty"`
}

// Detector finds webservers. The fields exist so detection can be tested
// without the binaries of the machine running the suite.
type Detector struct {
	// BinDir is the root's bin/, where Wharf's own copies live.
	BinDir string
	// Candidates overrides where installs are looked for, per webserver.
	// Wharf's own copy under BinDir is always looked for first.
	Candidates map[string][]string
	// Probe reports a binary's version. Defaults to running it with -v.
	Probe func(ctx context.Context, bin string) (string, error)
	// GOOS picks the candidate list and executable suffix.
	GOOS string
}

func (d *Detector) goos() string {
	if d.GOOS != "" {
		return d.GOOS
	}
	return runtime.GOOS
}

func (d *Detector) exe(name string) string {
	if d.goos() == "windows" {
		return name + ".exe"
	}
	return name
}

// Vendored is where Wharf's own copy of a webserver lives. Apache keeps its
// upstream layout — bin/httpd beside modules/ — so a downloaded build works
// unmodified.
func (d *Detector) Vendored(name string) string {
	if name == Apache {
		return filepath.Join(d.BinDir, "apache", "bin", d.exe("httpd"))
	}
	return filepath.Join(d.BinDir, "nginx", d.exe("nginx"))
}

// Detect returns the preferred install of each webserver found: Wharf's own
// copy first, then a package manager's, then the one the OS ships.
func (d *Detector) Detect(ctx context.Context) map[string]Install {
	out := map[string]Install{}
	for _, name := range []string{Nginx, Apache} {
		for _, bin := range d.candidates(name) {
			if in, ok := d.inspect(ctx, name, bin); ok {
				out[name] = in
				break
			}
		}
	}
	return out
}

func (d *Detector) candidates(name string) []string {
	list := []string{d.Vendored(name)}
	if name == Apache {
		// The layout used before Wharf installed Apache itself.
		list = append(list, filepath.Join(d.BinDir, "apache", d.exe("httpd")))
	}
	if d.Candidates != nil {
		return append(list, d.Candidates[name]...)
	}
	list = append(list, systemCandidates(d.goos(), name)...)
	lookup := map[string][]string{Nginx: {"nginx"}, Apache: {"httpd", "apache2"}}[name]
	for _, n := range lookup {
		if p, err := exec.LookPath(d.exe(n)); err == nil {
			list = append(list, p)
		}
	}
	return list
}

// systemCandidates lists where each OS and package manager put the binaries.
func systemCandidates(goos, name string) []string {
	switch goos + "/" + name {
	case "darwin/nginx":
		return []string{"/opt/homebrew/bin/nginx", "/usr/local/bin/nginx", "/opt/local/sbin/nginx"}
	case "darwin/apache":
		// macOS ships Apache; a Homebrew one is newer and preferred.
		return []string{"/opt/homebrew/opt/httpd/bin/httpd", "/usr/local/opt/httpd/bin/httpd", "/usr/sbin/httpd"}
	case "linux/nginx":
		return []string{"/usr/sbin/nginx", "/usr/local/sbin/nginx", "/usr/local/nginx/sbin/nginx",
			"/home/linuxbrew/.linuxbrew/bin/nginx"}
	case "linux/apache":
		return []string{"/usr/sbin/apache2", "/usr/sbin/httpd", "/usr/local/apache2/bin/httpd"}
	case "windows/nginx":
		return []string{`C:\nginx\nginx.exe`}
	case "windows/apache":
		return []string{`C:\Apache24\bin\httpd.exe`}
	}
	return nil
}

func (d *Detector) inspect(ctx context.Context, name, bin string) (Install, bool) {
	if !isExecutable(bin, d.goos()) {
		return Install{}, false
	}
	in := Install{Name: name, Binary: bin, Source: d.source(bin)}
	if name == Apache {
		// An Apache without mod_proxy_fcgi cannot reach PHP; it is not an
		// install Wharf can use.
		in.Modules = ModulesDir(bin)
		if in.Modules == "" {
			return Install{}, false
		}
	}
	in.Version, _ = d.probe(ctx, bin)
	return in, true
}

func (d *Detector) source(bin string) string {
	if rel, err := filepath.Rel(d.BinDir, bin); err == nil && !strings.HasPrefix(rel, "..") {
		return "wharf"
	}
	for _, marker := range []string{"/homebrew/", "/usr/local/opt/", "/usr/local/Cellar/", "linuxbrew"} {
		if strings.Contains(filepath.ToSlash(bin), marker) {
			return "homebrew"
		}
	}
	// /usr/local/bin/nginx on an Intel Mac is Homebrew's too.
	if resolved, err := filepath.EvalSymlinks(bin); err == nil && strings.Contains(resolved, "/Cellar/") {
		return "homebrew"
	}
	return "system"
}

// ModulesDir finds Apache's modules folder for a binary: beside it in the
// upstream layout, or wherever the OS's packaging put it.
func ModulesDir(bin string) string {
	dir := filepath.Dir(bin)
	candidates := []string{
		filepath.Join(dir, "..", "modules"),
		filepath.Join(dir, "modules"),
		filepath.Join(dir, "..", "lib", "httpd", "modules"),
		filepath.Join(dir, "..", "libexec", "apache2"),
		filepath.Join(dir, "..", "lib", "apache2", "modules"),
		filepath.Join(dir, "..", "lib64", "httpd", "modules"),
		"/usr/libexec/apache2",
		"/usr/lib/apache2/modules",
		"/usr/lib64/httpd/modules",
		"/usr/lib/httpd/modules",
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "mod_proxy_fcgi.so")); err == nil {
			return filepath.Clean(c)
		}
	}
	return ""
}

var versionRe = regexp.MustCompile(`(?:nginx|Apache)/(\d+\.\d+\.\d+)`)

func (d *Detector) probe(ctx context.Context, bin string) (string, error) {
	if d.Probe != nil {
		return d.Probe(ctx, bin)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// nginx prints its version on stderr, Apache on stdout.
	out, err := exec.CommandContext(ctx, bin, "-v").CombinedOutput()
	if m := versionRe.FindStringSubmatch(string(out)); m != nil {
		return m[1], nil
	}
	return "", err
}

func isExecutable(path, goos string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if goos == "windows" {
		return strings.EqualFold(filepath.Ext(path), ".exe")
	}
	return info.Mode().Perm()&0o111 != 0
}
