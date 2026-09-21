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

	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/layout"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/php"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/supervisor"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/webserver"
)

// Ports the tool binds by convention. They are constants rather than config
// keys because a user who needs different ones is outside v1's scope
// (dev/design-principles.md §2: no settings surfaced that aren't needed).
const (
	HTTPPort  = 80
	HTTPSPort = 443
	// phpBasePort is where the FastCGI ports start; PHPPort adds the version
	// itself, so 8.4 listens on 9804 and 8.5 on 9805.
	phpBasePort = 9000
	// projectBasePort is the first loopback port handed to a project's own
	// webserver instance, behind the front door.
	projectBasePort = 8080
)

// Domain is what projects are published under: <project>.localhost. The name
// is reserved for loopback, and macOS, Linux and every browser resolve it
// themselves, so no hosts file is involved (pretty-urls.feature).
const Domain = "localhost"

// Hostname is a project's name on the web.
func Hostname(project string) string { return project + "." + Domain }

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

// PHPPort is the FastCGI port for a version, derived from the version number
// alone: 8.4 listens on 9804, 8.5 on 9805.
//
// WARNING: Never from a position in the installed list. Downloading 8.4 while
// 8.5 served a project moved 8.5 behind it in the sorted list, so every
// generated config named a port its backend was not listening on, and
// starting a project waited out the stop timeout on a port the old backend
// still held (php-runtime.feature, "Adding a PHP version leaves a running
// backend on its port").
func PHPPort(version string) int {
	var major, minor int
	if _, err := fmt.Sscanf(php.Minor(version), "%d.%d", &major, &minor); err != nil {
		return phpBasePort
	}
	return phpBasePort + major*100 + minor
}

// NextProjectPort returns a port not yet claimed by another project and, as
// far as free can tell, not held by another program: a port the machine
// already uses would only fail the project's own instance on its first start
// (service-management.feature, "A project's own instance never takes a port
// another program holds"). Ports are persisted per project, so a project's
// own instance comes back on the port the front door forwards to.
func NextProjectPort(cfg *config.Config, free func(port int) bool) int {
	taken := map[int]bool{}
	for _, p := range cfg.Projects {
		if p.Port > 0 {
			taken[p.Port] = true
		}
	}
	for port := projectBasePort; port < projectBasePort+1000; port++ {
		if !taken[port] && (free == nil || free(port)) {
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
	port := PHPPort(version)

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
		Logs:    []string{filepath.Join(r.Root.LogDir(), "php-"+version+"-fpm.log")},
		// WARNING: PHP reads the user's config/php.ini after its own php.ini, so its
		// values win (php-settings.feature). The empty first entry keeps the
		// scan directory PHP was built with: a Homebrew PHP loads its
		// extensions from there.
		//
		// WARNING: php-cgi — Windows' FastCGI backend — exits after 500
		// requests unless PHP_FCGI_MAX_REQUESTS says otherwise, and nothing
		// restarts it: every project on that version would stop answering
		// until Wharf was restarted. 0 lifts the limit; php-fpm ignores it.
		Env: []string{
			"PHP_INI_SCAN_DIR=" + string(os.PathListSeparator) + r.Root.Config(),
			"PHP_FCGI_MAX_REQUESTS=0",
		},
	}
	// TODO(xdebug): offer Xdebug, per project rather than per PHP version —
	// it slows every request down while loaded. A likely shape: a project
	// switch that serves the project from a second FastCGI backend of its
	// PHP version, started with zend_extension=xdebug, xdebug.mode=debug and
	// xdebug.start_with_request=trigger. Needs an Xdebug build matching each
	// PHP build Wharf runs, adopted or downloaded.
	if runtime.GOOS == "windows" {
		// INFO: php-cgi has no config file of its own; it is told where to listen,
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

// WebserverSpec builds the globally active webserver: the front door on ports
// 80 and 443. It serves every project on its own webserver and forwards the
// others to their own instance (service-management.feature).
func (r *Resolver) WebserverSpec(cfg *config.Config) (supervisor.Spec, error) {
	name := cfg.Services.Webserver.Active
	in, err := r.Webserver(name)
	if err != nil {
		return supervisor.Spec{}, err
	}

	var served, forwarded []config.Project
	for _, p := range cfg.Projects {
		if cfg.OwnInstance(p) {
			forwarded = append(forwarded, p)
		} else {
			served = append(served, p)
		}
	}

	confPath := filepath.Join(r.genDir(), name+".conf")
	conf, err := r.renderWebserverConf(cfg, in, "global-"+name, true, served, forwarded)
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
		Logs:    r.serverLogs(name, cfg.Projects),
	}, nil
}

// ProjectSpec builds the own instance of a project pinned to the webserver
// that is not active. It listens on the project's loopback port, behind the
// front door, so the global instance is unaffected (service-management
// .feature).
func (r *Resolver) ProjectSpec(cfg *config.Config, p config.Project) (supervisor.Spec, error) {
	if !cfg.OwnInstance(p) {
		return supervisor.Spec{}, fmt.Errorf("project %q is served by the global webserver", p.Name)
	}
	name := cfg.WebserverFor(p)
	in, err := r.Webserver(name)
	if err != nil {
		return supervisor.Spec{}, err
	}
	if p.Port == 0 {
		return supervisor.Spec{}, fmt.Errorf("project %q has no port assigned", p.Name)
	}

	confPath := filepath.Join(r.genDir(), "project-"+p.Name+"-"+name+".conf")
	conf, err := r.renderWebserverConf(cfg, in, "project-"+p.Name, false, []config.Project{p}, nil)
	if err != nil {
		return supervisor.Spec{}, err
	}
	if err := r.write(confPath, conf, in); err != nil {
		return supervisor.Spec{}, err
	}

	return supervisor.Spec{
		ID:    ProjectServiceID(p.Name),
		Label: fmt.Sprintf("%s (%s)", p.Name, name),
		Path:  in.Binary,
		Args:  webserverArgs(name, confPath, r.Root),
		Dir:   r.Root.Dir,
		Port:  p.Port,
		// INFO: Its own instance's output — startup errors above all — belongs with
		// the project's other logs (project-logs.feature).
		LogPath: filepath.Join(r.Root.ProjectLogDir(p.Name), name+".log"),
		Logs:    r.serverLogs(name, []config.Project{p}),
	}, nil
}

// serverLogs are the logs a webserver instance writes itself (render.go): its
// own two in data/log/, and those of each project it serves or forwards. A
// project's custom config may name others; those are the user's to manage.
func (r *Resolver) serverLogs(name string, projects []config.Project) []string {
	out := []string{
		filepath.Join(r.Root.LogDir(), name+"-error.log"),
		filepath.Join(r.Root.LogDir(), name+"-access.log"),
	}
	for _, p := range projects {
		dir := r.Root.ProjectLogDir(p.Name)
		out = append(out, filepath.Join(dir, "access.log"), filepath.Join(dir, "error.log"))
	}
	return out
}

// webserverArgs is how each webserver is told to run in the foreground with a
// generated config. Running in the foreground matters: the supervisor tracks
// the process it started, not a daemonised child it never sees.
func webserverArgs(name, confPath string, root layout.Root) []string {
	switch name {
	case "nginx":
		// INFO: -e sends startup errors to stderr — the service log — instead of
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
