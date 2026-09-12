// Package core implements the daemon's behaviour: it owns the config store,
// the supervisor, the hosts file and certificate issuance, and exposes one
// method per user-visible action. The IPC layer is a thin shell over it, so
// the same behaviour backs the main window and the tray menu
// (tray-actions.feature, "Opening the main window").
package core

import (
	"os"
	"path/filepath"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/certs"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/php"
	wruntime "github.com/kreativ-anders/wharf-dev/daemon/internal/runtime"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/supervisor"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/webserver"
)

// State is the full snapshot the GUI renders. One primary view — a list of
// projects with name, status, URL and, while running, what serves them — is
// all that is shown by default
// (dev/design-principles.md §2), so this is deliberately small.
type State struct {
	Root string `json:"root"`
	// Version is the running Wharf's, "dev" for an unstamped build.
	Version string `json:"version"`
	// Appearance is "system", "light" or "dark".
	Appearance string `json:"appearance"`
	// WWW is the folder projects are put in by default, which the GUI offers
	// to open (project-folders.feature, "Opening the www folder").
	WWW          string    `json:"www"`
	Config       string    `json:"config"`
	Domain       string    `json:"domain"`
	Services     Services  `json:"services"`
	Projects     []Project `json:"projects"`
	Unregistered []string  `json:"unregistered"`
	// SSL says whether mkcert is installed and its authority trusted
	// (local-ssl.feature). Neither is required to turn SSL on: the switch
	// sets up whatever is missing.
	SSL  certs.Status `json:"ssl"`
	Busy bool         `json:"busy"`
}

// Services is the global service state shown in Settings. Only Webserver and
// PHP exist in v1: database and mail are roadmap and are not surfaced at all
// (settings.feature, "Settings screen hides roadmap services in v1").
type Services struct {
	Webserver Webserver `json:"webserver"`
	PHP       PHP       `json:"php"`
}

// Webserver is the global webserver selection and the live state of its
// process. Switching is true while a swap is in flight, which is what the GUI
// shows as "switching webserver".
type Webserver struct {
	Active    string   `json:"active"`
	Available []string `json:"available"`
	State     string   `json:"state"`
	Switching bool     `json:"switching"`
	Error     string   `json:"error,omitempty"`
	// Servers describes each available webserver: whether it is installed and
	// which projects it serves. "Stopped" alone reads as broken when it only
	// means no project is running (settings.feature, "Webserver status while
	// nothing is running").
	Servers []Server `json:"servers"`
}

// Server is one available webserver.
type Server struct {
	Name string `json:"name"`
	// Installed is false when no usable copy was found. Binary is the copy
	// in use, or where Wharf's own copy would go.
	Installed bool   `json:"installed"`
	Binary    string `json:"binary"`
	Version   string `json:"version,omitempty"`
	// Source is "wharf", "homebrew" or "system" for an installed server.
	Source string `json:"source,omitempty"`
	// Install says how Wharf would install it here, or why it cannot; and
	// Installing is true while it does (webserver-install.feature).
	Install    webserver.Plan `json:"install"`
	Installing bool           `json:"installing"`
	// Projects lists the projects this webserver serves: those without an
	// override for the active one, those overriding to it for the others.
	Projects []string `json:"projects"`
}

// PHP is the global PHP version, the state of each running backend, and what
// the version picker offers: every installation found on this machine, with
// its support status.
type PHP struct {
	Version   string              `json:"version"`
	Available []string            `json:"available"`
	Backends  []supervisor.Status `json:"backends"`
	Installs  []php.Install       `json:"installs"`
	// Recommended is the newest version in active support — what a first run
	// selects when nothing is installed.
	Recommended string `json:"recommended"`
	// Status is the support status of the selected version, so the picker can
	// say "security fixes only" without repeating the release table.
	Status php.Status `json:"status"`
	// Dir is bin/php, where downloaded versions go.
	Dir string `json:"dir"`
	// Downloadable lists supported versions not installed yet, newest first
	// (php-runtime.feature, "Only supported versions are offered for
	// download"); Downloading those being fetched right now.
	Downloadable []Download `json:"downloadable"`
	Downloading  []string   `json:"downloading"`
	// Hidden lists the folders of PHP installs found on the machine that the
	// user hid, so each can be shown again (php-runtime.feature, "Showing a
	// hidden PHP version again").
	Hidden []string `json:"hidden"`
	// Settings is config/php.ini, the user's own PHP settings, and
	// SettingsExist whether it has been created yet (php-settings.feature).
	Settings      string `json:"settings"`
	SettingsExist bool   `json:"settings_exist"`
}

// Download is a version the picker offers to download, with its support
// status so the offer can say how long it will last.
type Download struct {
	Version string     `json:"version"`
	Status  php.Status `json:"status"`
	// FullVersion is the release a download would fetch, e.g. "8.5.1";
	// empty until it has been looked up (php-runtime.feature, "A download
	// offer names the release it downloads").
	FullVersion string `json:"full_version,omitempty"`
}

// Project is one row in the project list.
type Project struct {
	Name  string `json:"name"`
	State string `json:"state"`
	// URL is what the GUI shows and the "Open" action follows:
	// <name>.localhost, with no port whichever webserver serves the project
	// (pretty-urls.feature).
	URL string `json:"url"`

	Webserver         string  `json:"webserver"`
	WebserverOverride *string `json:"webserver_override,omitempty"`
	PHPVersion        string  `json:"php_version"`
	PHPOverride       *string `json:"php_override,omitempty"`
	// WebserverVersion and PHPFullVersion are what the serving binaries
	// reported, e.g. "1.27.3" and "8.3.14"; empty when one did not say
	// (app-configuration.feature, "A running project shows what serves it").
	WebserverVersion string `json:"webserver_version,omitempty"`
	PHPFullVersion   string `json:"php_full_version,omitempty"`
	SSL              bool   `json:"ssl"`
	Port             int    `json:"port"`

	// Dir is the project's folder; Linked is true when that folder is not in
	// www/ (project-folders.feature).
	Dir    string `json:"dir"`
	Linked bool   `json:"linked"`
	// LogDir is the project's own log folder, which the GUI offers to open
	// (project-logs.feature).
	LogDir string `json:"log_dir"`
	// CustomConfigs has one entry per available webserver
	// (app-configuration.feature, "Each webserver keeps its own custom
	// config").
	CustomConfigs []CustomConfig `json:"custom_configs"`

	Error string `json:"error,omitempty"`
}

// CustomConfig is one project's custom directives for one webserver.
type CustomConfig struct {
	Webserver string `json:"webserver"`
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	// Active is true for the file of the webserver serving the project now.
	Active bool `json:"active"`
}

// snapshot builds the State from config plus live process state.
func (d *Daemon) snapshot(cfg *config.Config) State {
	st := State{
		Root:         d.root.Dir,
		Version:      d.version,
		Appearance:   appearance(cfg.Appearance),
		WWW:          d.root.WWW(),
		Config:       d.root.Config(),
		Domain:       wruntime.Domain,
		Unregistered: d.unregistered(cfg),
		SSL:          d.certs.Status(),
		Busy:         d.busy.Load() > 0,
	}

	wsState := supervisor.StateStopped
	wsErr := ""
	if s, ok := d.sup.Status(runtimeWebserverID); ok {
		wsState, wsErr = s.State, s.Error
	}
	st.Services.Webserver = Webserver{
		Active:    cfg.Services.Webserver.Active,
		Available: cfg.Services.Webserver.Available,
		State:     string(wsState),
		Switching: wsState == supervisor.StateStarting || wsState == supervisor.StateStopping,
		Error:     wsErr,
		Servers:   d.servers(cfg),
	}

	installs := d.PHPInstalls()
	if installs == nil {
		installs = []php.Install{}
	}
	hidden := cfg.Services.PHP.Hidden
	if hidden == nil {
		hidden = []string{}
	}
	st.Services.PHP = PHP{
		Version:      cfg.Services.PHP.Version,
		Available:    cfg.Services.PHP.Available,
		Backends:     d.phpStatuses(cfg),
		Installs:     installs,
		Recommended:  php.Recommended(d.now()),
		Status:       php.StatusAt(cfg.Services.PHP.Version, d.now()),
		Dir:          filepath.Join(d.root.Bin(), "php"),
		Downloadable: downloadable(installs, d.now(), d.latestPHP()),
		Downloading:  d.downloadingVersions(),
		Hidden:       hidden,
		Settings:     d.root.PHPIni(),
	}
	if _, err := os.Stat(d.root.PHPIni()); err == nil {
		st.Services.PHP.SettingsExist = true
	}

	for _, p := range cfg.Projects {
		st.Projects = append(st.Projects, d.projectState(cfg, p))
	}
	if st.Projects == nil {
		st.Projects = []Project{}
	}
	return st
}

func (d *Daemon) projectState(cfg *config.Config, p config.Project) Project {
	scheme := "http"
	if p.SSL {
		scheme = "https"
	}
	host := wruntime.Hostname(p.Name)

	out := Project{
		Name:              p.Name,
		Webserver:         cfg.WebserverFor(p),
		WebserverOverride: p.WebserverOverride,
		PHPVersion:        cfg.PHPVersionFor(p),
		PHPOverride:       p.PHPVersion,
		SSL:               p.SSL,
		Port:              p.Port,
		Dir:               wruntime.ProjectDir(d.root, p),
		Linked:            p.Path != "",
		LogDir:            d.root.ProjectLogDir(p.Name),
	}
	out.WebserverVersion = d.WebInstalls()[out.Webserver].Version
	out.PHPFullVersion = d.phpFullVersion(out.PHPVersion)
	for _, server := range cfg.Services.Webserver.Available {
		path := d.root.CustomConfig(p.Name, server)
		_, err := os.Stat(path)
		out.CustomConfigs = append(out.CustomConfigs, CustomConfig{
			Webserver: server,
			Path:      path,
			Exists:    err == nil,
			Active:    server == out.Webserver,
		})
	}

	// The front door serves every project on ports 80 and 443, forwarding
	// those on the other webserver, so no URL carries a port.
	out.URL = scheme + "://" + host

	st, err := d.projectStatus(cfg, p)
	out.State = string(st)
	if err != "" {
		out.Error = err
	}
	return out
}

// servers describes each available webserver for Settings.
func (d *Daemon) servers(cfg *config.Config) []Server {
	out := []Server{}
	for _, name := range cfg.Services.Webserver.Available {
		srv := Server{Name: name, Projects: []string{}, Installing: d.inProgress(d.installing, name)}
		srv.Binary, _ = d.res.WebserverPath(name)
		if in, ok := d.WebInstalls()[name]; ok {
			srv.Installed, srv.Binary, srv.Version, srv.Source = true, in.Binary, in.Version, in.Source
		} else {
			srv.Install = d.webInstaller.Plan(name)
		}
		for _, p := range cfg.Projects {
			if cfg.WebserverFor(p) == name {
				srv.Projects = append(srv.Projects, p.Name)
			}
		}
		out = append(out, srv)
	}
	return out
}

// phpFullVersion is the build a version resolves to, picked the way locatePHP
// picks it — Wharf's own copy before one adopted from the machine — so the
// row names the binary that actually serves.
func (d *Daemon) phpFullVersion(version string) string {
	full := ""
	for _, in := range d.PHPInstalls() {
		if in.Version != version || !in.Servable() {
			continue
		}
		if in.Source == "vendored" {
			return in.FullVersion
		}
		if full == "" {
			full = in.FullVersion
		}
	}
	return full
}

// downloadable is every supported version that is not already installed and
// able to serve, named by the release it would fetch where that is known.
func downloadable(installs []php.Install, now time.Time, latest map[string]string) []Download {
	have := map[string]bool{}
	for _, in := range installs {
		if in.Servable() {
			have[in.Version] = true
		}
	}
	out := []Download{}
	for _, v := range php.Downloadable(now) {
		if !have[v] {
			out = append(out, Download{Version: v, Status: php.StatusAt(v, now), FullVersion: latest[v]})
		}
	}
	return out
}

// projectStatus derives a project's status from the process actually serving
// it: its own instance when it overrides the webserver, the global one
// otherwise.
func (d *Daemon) projectStatus(cfg *config.Config, p config.Project) (supervisor.State, string) {
	// A project nobody started is stopped, whatever the shared webserver does
	// for the others (tray-actions.feature).
	if !d.isStarted(p.Name) {
		if msg, ok := d.startFailure(p.Name); ok {
			return supervisor.StateFailed, msg
		}
		return supervisor.StateStopped, ""
	}
	own := cfg.OwnInstance(p)
	id := runtimeWebserverID
	if own {
		id = projectServiceID(p.Name)
	}
	s, ok := d.sup.Status(id)
	if !ok {
		return supervisor.StateStopped, ""
	}
	// An own instance is reachable only through the front door: while that is
	// down, the project is not being served, whatever its instance does.
	if own && s.State == supervisor.StateRunning && !d.sup.Running(runtimeWebserverID) {
		if front, ok := d.sup.Status(runtimeWebserverID); ok && front.State == supervisor.StateFailed {
			return supervisor.StateFailed, front.Error
		}
		return supervisor.StateStopped, ""
	}
	return s.State, s.Error
}

func appearance(mode string) string {
	if mode == "" {
		return "system"
	}
	return mode
}
