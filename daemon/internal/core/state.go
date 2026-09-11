// Package core implements the daemon's behaviour: it owns the config store,
// the supervisor, the hosts file and certificate issuance, and exposes one
// method per user-visible action. The IPC layer is a thin shell over it, so
// the same behaviour backs the main window and the tray menu
// (tray-actions.feature, "Opening the main window").
package core

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/manuel-steinberg/wharf/daemon/internal/certs"

	"github.com/manuel-steinberg/wharf/daemon/internal/config"
	"github.com/manuel-steinberg/wharf/daemon/internal/hostsfile"
	"github.com/manuel-steinberg/wharf/daemon/internal/php"
	wruntime "github.com/manuel-steinberg/wharf/daemon/internal/runtime"
	"github.com/manuel-steinberg/wharf/daemon/internal/supervisor"
	"github.com/manuel-steinberg/wharf/daemon/internal/webserver"
)

// State is the full snapshot the GUI renders. One primary view — a list of
// projects with name, status and URL — is all that is shown by default
// (dev/design-principles.md §2), so this is deliberately small.
type State struct {
	Root string `json:"root"`
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
}

// Download is a version the picker offers to download, with its support
// status so the offer can say how long it will last.
type Download struct {
	Version string     `json:"version"`
	Status  php.Status `json:"status"`
}

// Project is one row in the project list.
type Project struct {
	Name  string `json:"name"`
	State string `json:"state"`
	// URL is what the GUI shows and the "Open" action follows: the pretty URL
	// when a hosts entry exists, the raw-port URL otherwise.
	URL         string `json:"url"`
	PrettyURL   string `json:"pretty_url,omitempty"`
	FallbackURL string `json:"fallback_url"`
	HostsEntry  bool   `json:"hosts_entry"`

	Webserver         string  `json:"webserver"`
	WebserverOverride *string `json:"webserver_override,omitempty"`
	PHPVersion        string  `json:"php_version"`
	PHPOverride       *string `json:"php_override,omitempty"`
	SSL               bool    `json:"ssl"`
	Port              int     `json:"port"`

	// Dir is the project's folder; Linked is true when that folder is not in
	// www/ (project-folders.feature).
	Dir    string `json:"dir"`
	Linked bool   `json:"linked"`
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
		Appearance:   appearance(cfg.Appearance),
		WWW:          d.root.WWW(),
		Config:       d.root.Config(),
		Domain:       hostsfile.Domain,
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
	st.Services.PHP = PHP{
		Version:      cfg.Services.PHP.Version,
		Available:    cfg.Services.PHP.Available,
		Backends:     d.phpStatuses(cfg),
		Installs:     installs,
		Recommended:  php.Recommended(d.now()),
		Status:       php.StatusAt(cfg.Services.PHP.Version, d.now()),
		Dir:          filepath.Join(d.root.Bin(), "php"),
		Downloadable: downloadable(installs, d.now()),
		Downloading:  d.downloadingVersions(),
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
	host := hostsfile.Hostname(p.Name)

	out := Project{
		Name:              p.Name,
		Webserver:         cfg.WebserverFor(p),
		WebserverOverride: p.WebserverOverride,
		PHPVersion:        cfg.PHPVersionFor(p),
		PHPOverride:       p.PHPVersion,
		SSL:               p.SSL,
		Port:              p.Port,
		HostsEntry:        p.HostsEntry,
		FallbackURL:       fmt.Sprintf("http://127.0.0.1:%d", p.Port),
		Dir:               wruntime.ProjectDir(d.root, p),
		Linked:            p.Path != "",
	}
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

	// A project served by its own webserver instance is only reachable on its
	// own ports, so its pretty URL carries the port too — the HTTPS one when
	// SSL is on (local-ssl.feature).
	if p.WebserverOverride != nil {
		port := p.Port
		if p.SSL {
			port = wruntime.ProjectSSLPort(p)
		}
		if p.HostsEntry {
			out.PrettyURL = fmt.Sprintf("%s://%s:%d", scheme, host, port)
		}
	} else if p.HostsEntry {
		out.PrettyURL = scheme + "://" + host
	}

	out.URL = out.FallbackURL
	if out.PrettyURL != "" {
		out.URL = out.PrettyURL
	}

	st, err := d.projectStatus(p)
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

// downloadable is every supported version that is not already installed and
// able to serve.
func downloadable(installs []php.Install, now time.Time) []Download {
	have := map[string]bool{}
	for _, in := range installs {
		if in.Servable() {
			have[in.Version] = true
		}
	}
	out := []Download{}
	for _, v := range php.Downloadable(now) {
		if !have[v] {
			out = append(out, Download{Version: v, Status: php.StatusAt(v, now)})
		}
	}
	return out
}

// projectStatus derives a project's status from the process actually serving
// it: its own instance when it overrides the webserver, the global one
// otherwise.
func (d *Daemon) projectStatus(p config.Project) (supervisor.State, string) {
	id := runtimeWebserverID
	if p.WebserverOverride != nil {
		id = projectServiceID(p.Name)
	}
	s, ok := d.sup.Status(id)
	if !ok {
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
