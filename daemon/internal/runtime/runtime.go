// Package runtime turns config into concrete process specs: which binary
// serves which project, on which port, with which generated configuration.
//
// Which webserver binary to run is decided by internal/webserver; which PHP
// by the config (dev/architecture.md §4b). Nothing here shells out; it only
// resolves paths and renders config files, so it is fully testable without
// the binaries present.
package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/manuel-steinberg/wharf/daemon/internal/config"
	"github.com/manuel-steinberg/wharf/daemon/internal/layout"
	"github.com/manuel-steinberg/wharf/daemon/internal/php"
	"github.com/manuel-steinberg/wharf/daemon/internal/supervisor"
	"github.com/manuel-steinberg/wharf/daemon/internal/webserver"
)

// Ports the tool binds by convention. They are constants rather than config
// keys because a user who needs different ones is outside v1's scope
// (dev/design-principles.md §2: no settings surfaced that aren't needed).
const (
	HTTPPort  = 80
	HTTPSPort = 443
	// phpBasePort is the first FastCGI port; each installed PHP version gets
	// the next one.
	phpBasePort = 9000
	// projectBasePort is the first port handed to a project served by its own
	// webserver instance via a per-project override.
	projectBasePort = 8080
	// projectSSLOffset puts a project instance's HTTPS listener beside its
	// HTTP one — 8080 becomes 8443 — so it never competes with the global
	// webserver for 443 (local-ssl.feature, "A project on its own webserver
	// instance gets its own HTTPS port").
	projectSSLOffset = 363
)

// ProjectSSLPort is the HTTPS port of a project served by its own instance.
func ProjectSSLPort(p config.Project) int { return p.Port + projectSSLOffset }

// ProjectDir is where a project's files are: its recorded path if it was
// added from outside www/, its folder in www/ otherwise
// (project-folders.feature).
func ProjectDir(root layout.Root, p config.Project) string {
	if p.Path != "" {
		return p.Path
	}
	return root.ProjectDir(p.Name)
}

// Service IDs used with the supervisor. They are stable strings because the
// GUI subscribes to state changes by ID.
const (
	WebserverID = "webserver"
)

// PHPServiceID is the supervisor ID of the FastCGI backend for a version.
func PHPServiceID(version string) string { return "php:" + version }

// ProjectServiceID is the supervisor ID of a project's own webserver instance,
// used only when the project overrides the global webserver.
func ProjectServiceID(name string) string { return "project:" + name }

// Resolver builds process specs for a root folder.
type Resolver struct {
	Root layout.Root
	// Installs reports the webservers found on this machine. The daemon
	// caches detection, so this is a lookup, not a scan.
	Installs func() map[string]webserver.Install
}

// New returns a Resolver for the given root.
func New(root layout.Root) *Resolver { return &Resolver{Root: root} }

// MissingBinaryError reports a service whose vendored binary is absent. The
// GUI turns this into "PHP 8.2 is not installed" rather than an exec failure.
type MissingBinaryError struct {
	Service string
	Path    string
}

func (e *MissingBinaryError) Error() string {
	return fmt.Sprintf("%s is not installed (expected %s)", e.Service, e.Path)
}

// exe appends the Windows executable suffix. This and the php-fpm/php-cgi
// choice below are the only OS differences in this package.
func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// WebserverPath is where Wharf's own copy of a webserver goes, whether or
// not it is there.
func (r *Resolver) WebserverPath(name string) (string, error) {
	if name != webserver.Nginx && name != webserver.Apache {
		return "", fmt.Errorf("unknown webserver %q", name)
	}
	return (&webserver.Detector{BinDir: r.Root.Bin()}).Vendored(name), nil
}

// Webserver resolves the install that runs a webserver: whichever detection
// preferred, wherever it is.
func (r *Resolver) Webserver(name string) (webserver.Install, error) {
	path, err := r.WebserverPath(name)
	if err != nil {
		return webserver.Install{}, err
	}
	if r.Installs != nil {
		if in, ok := r.Installs()[name]; ok {
			return in, nil
		}
	}
	return webserver.Install{}, &MissingBinaryError{Service: name, Path: path}
}

// PHPBinary resolves the FastCGI backend for a PHP version: a build the tool
// adopted from the machine if the config records a path for it, otherwise the
// vendored one under bin/php/<version>.
//
// Unix builds ship php-fpm; Windows ships php-cgi, which speaks the same
// protocol over TCP.
func (r *Resolver) PHPBinary(cfg *config.Config, version string) (string, error) {
	dir := r.Root.PHPBin(version)
	if adopted, ok := cfg.PHPPath(version); ok {
		dir = adopted
	}
	p := filepath.Join(dir, php.FastCGIName())
	if _, err := os.Stat(p); err != nil {
		return "", &MissingBinaryError{Service: "PHP " + version, Path: p}
	}
	return p, nil
}

// PHPPort is the FastCGI port for a version, derived from its position in the
// installed list so that it is stable for as long as that list is.
func PHPPort(cfg *config.Config, version string) int {
	for i, v := range cfg.Services.PHP.Available {
		if v == version {
			return phpBasePort + i
		}
	}
	return phpBasePort
}

// NextProjectPort returns a port not yet claimed by another project. Ports are
// persisted per project so the fallback URL stays stable across restarts
// (pretty-urls.feature, "Elevation is declined").
func NextProjectPort(cfg *config.Config) int {
	taken := map[int]bool{}
	for _, p := range cfg.Projects {
		if p.Port > 0 {
			taken[p.Port] = true
			taken[ProjectSSLPort(p)] = true
		}
	}
	for port := projectBasePort; port < projectBasePort+1000; port++ {
		if !taken[port] {
			return port
		}
	}
	return 0
}

// PHPSpec builds the FastCGI backend process for one PHP version.
func (r *Resolver) PHPSpec(cfg *config.Config, version string) (supervisor.Spec, error) {
	bin, err := r.PHPBinary(cfg, version)
	if err != nil {
		return supervisor.Spec{}, err
	}
	port := PHPPort(cfg, version)

	confPath := filepath.Join(r.genDir(), fmt.Sprintf("php-fpm-%s.conf", version))
	if err := writeFile(confPath, renderPHPFPMConf(version, port, r.Root)); err != nil {
		return supervisor.Spec{}, err
	}

	spec := supervisor.Spec{
		ID:      PHPServiceID(version),
		Label:   "PHP " + version,
		Path:    bin,
		Port:    port,
		Dir:     r.Root.Dir,
		LogPath: filepath.Join(r.Root.LogDir(), fmt.Sprintf("php-%s.log", version)),
	}
	if runtime.GOOS == "windows" {
		// php-cgi has no config file of its own; it is told where to listen,
		// and where its extension DLLs are — relative to the binary, which
		// php.ini cannot express.
		spec.Args = []string{
			"-b", fmt.Sprintf("127.0.0.1:%d", port),
			"-d", "extension_dir=" + filepath.Join(filepath.Dir(bin), "ext"),
		}
	} else {
		spec.Args = []string{"--nodaemonize", "--fpm-config", confPath}
	}
	return spec, nil
}

// WebserverSpec builds the globally active webserver, serving every project
// that does not override it.
func (r *Resolver) WebserverSpec(cfg *config.Config) (supervisor.Spec, error) {
	name := cfg.Services.Webserver.Active
	in, err := r.Webserver(name)
	if err != nil {
		return supervisor.Spec{}, err
	}

	var projects []config.Project
	for _, p := range cfg.Projects {
		if p.WebserverOverride == nil {
			projects = append(projects, p)
		}
	}

	confPath := filepath.Join(r.genDir(), name+".conf")
	conf, err := r.renderWebserverConf(cfg, in, "global-"+name, projects, HTTPPort, HTTPSPort)
	if err != nil {
		return supervisor.Spec{}, err
	}
	if err := r.write(confPath, conf, in); err != nil {
		return supervisor.Spec{}, err
	}

	return supervisor.Spec{
		ID:      WebserverID,
		Label:   name,
		Path:    in.Binary,
		Args:    webserverArgs(name, confPath, r.Root),
		Dir:     r.Root.Dir,
		Port:    HTTPPort,
		LogPath: filepath.Join(r.Root.LogDir(), name+".log"),
	}, nil
}

// ProjectSpec builds a dedicated webserver instance for a project that
// overrides the global webserver, on its own port so the global instance is
// unaffected (service-management.feature).
func (r *Resolver) ProjectSpec(cfg *config.Config, p config.Project) (supervisor.Spec, error) {
	if p.WebserverOverride == nil {
		return supervisor.Spec{}, fmt.Errorf("project %q has no webserver override", p.Name)
	}
	name := *p.WebserverOverride
	in, err := r.Webserver(name)
	if err != nil {
		return supervisor.Spec{}, err
	}
	if p.Port == 0 {
		return supervisor.Spec{}, fmt.Errorf("project %q has no port assigned", p.Name)
	}

	confPath := filepath.Join(r.genDir(), "project-"+p.Name+"-"+name+".conf")
	conf, err := r.renderWebserverConf(cfg, in, "project-"+p.Name, []config.Project{p}, p.Port, ProjectSSLPort(p))
	if err != nil {
		return supervisor.Spec{}, err
	}
	if err := r.write(confPath, conf, in); err != nil {
		return supervisor.Spec{}, err
	}

	return supervisor.Spec{
		ID:      ProjectServiceID(p.Name),
		Label:   fmt.Sprintf("%s (%s)", p.Name, name),
		Path:    in.Binary,
		Args:    webserverArgs(name, confPath, r.Root),
		Dir:     r.Root.Dir,
		Port:    p.Port,
		LogPath: filepath.Join(r.Root.LogDir(), "project-"+p.Name+".log"),
	}, nil
}

// webserverArgs is how each webserver is told to run in the foreground with a
// generated config. Running in the foreground matters: the supervisor tracks
// the process it started, not a daemonised child it never sees.
func webserverArgs(name, confPath string, root layout.Root) []string {
	switch name {
	case "nginx":
		// -e sends startup errors to stderr — the service log — instead of
		// the log path compiled into the binary, which may not exist.
		return []string{"-c", confPath, "-p", root.Dir, "-e", "stderr", "-g", "daemon off;"}
	case "apache":
		return []string{"-f", confPath, "-D", "FOREGROUND"}
	}
	return nil
}

func (r *Resolver) genDir() string { return filepath.Join(r.Root.Data(), "gen") }

func writeFile(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

// forwardSlash normalises a path for config files, which accept forward
// slashes on every OS including Windows.
func forwardSlash(p string) string { return strings.ReplaceAll(p, `\`, "/") }
