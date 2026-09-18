package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/elevate"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/project"
	wruntime "github.com/kreativ-anders/wharf-dev/daemon/internal/runtime"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/supervisor"
)

// AddProject registers an existing folder under www/ as a project, with the
// config template detected in it.
func (d *Daemon) AddProject(ctx context.Context, name string) (Project, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := project.ValidateName(name); err != nil {
		return Project{}, invalid("%s", err)
	}
	return d.addProjectLocked(ctx, name, "", "", project.Detect(d.root.ProjectDir(name)))
}

// Proposal is what "Add project…" proposes for a folder before anything is
// registered (project-folders.feature, "Adding a project with the folder
// picker").
type Proposal struct {
	Path string `json:"path"`
	// Name is the project name made from the folder's. Fixed is true for a
	// folder in www/, whose name is the folder's and cannot be changed.
	Name  string `json:"name"`
	Fixed bool   `json:"fixed"`
	// Template is the config template detected in the folder; "" for none.
	Template string `json:"template"`
	// Project is the project this folder already is; "" while it is none
	// (project-folders.feature, "Choosing a folder that is already a
	// project").
	Project string `json:"project"`
}

// InspectFolder proposes a name and a config template for the folder at
// path, and says whether it is a project already. It registers nothing.
func (d *Daemon) InspectFolder(path string) (Proposal, error) {
	abs, err := folderAt(path)
	if err != nil {
		return Proposal{}, err
	}
	out := Proposal{Path: abs, Template: project.Detect(abs)}
	cfg := d.store.Get()
	if sameDir(filepath.Dir(abs), d.root.WWW()) {
		out.Name, out.Fixed = filepath.Base(abs), true
		if _, ok := cfg.Project(out.Name); ok {
			out.Project = out.Name
		}
		return out, nil
	}
	out.Name = project.Slug(filepath.Base(abs))
	for _, p := range cfg.Projects {
		if p.Path != "" && sameDir(p.Path, abs) {
			out.Project = p.Name
		}
	}
	return out, nil
}

// Adding is what the user confirmed in the "Add project…" sheet. A zero
// value adds the folder under its own name, with the detected config
// template, served by the active webserver, and does not start it.
type Adding struct {
	// Name replaces the name made from the folder's; it gets the same
	// rewrite. A folder in www/ keeps its own.
	Name string
	// Template is the config template's id, "" for none; nil detects one.
	Template *string
	// Webserver pins the project to one; "" follows the active webserver.
	Webserver string
	// Start starts the project once it is registered.
	Start bool
}

// AddFolder registers the folder at path, wherever it is — the "Add
// project…" sheet's action (project-folders.feature). A folder directly in
// www/ is added by name as usual; any other folder stays where it is and its
// location is recorded.
func (d *Daemon) AddFolder(ctx context.Context, path string, a Adding) (Project, error) {
	abs, err := folderAt(path)
	if err != nil {
		return Project{}, err
	}
	template := ""
	if a.Template == nil {
		template = project.Detect(abs)
	} else if template = *a.Template; template != "" && !d.res.Templates().Exists(template) {
		return Project{}, notFound("no config template named %q", template)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if sameDir(filepath.Dir(abs), d.root.WWW()) {
		return d.addedLocked(ctx, filepath.Base(abs), "", a.Webserver, template, a.Start)
	}

	// INFO: wharfctl and anything else on the socket get the rewrite the GUI's
	// field applies (project-folders.feature, "The proposed name comes from
	// the folder name").
	typed := a.Name
	if typed == "" {
		typed = filepath.Base(abs)
	}
	name := project.Slug(typed)
	if name == "" {
		return Project{}, invalid("%q has no letter or digit to make a project name from — use at least one", typed)
	}
	if err := config.CheckProject(config.Project{Name: name, Path: abs}); err != nil {
		return Project{}, invalid("%s", err)
	}
	if project.Exists(d.root, name) {
		return Project{}, conflict("the name %q is taken by a folder in www/ — pick another name", name)
	}
	cfg := d.store.Get()
	for _, p := range cfg.Projects {
		if p.Path != "" && sameDir(p.Path, abs) {
			return Project{}, conflict("%s is already the project %q", abs, p.Name)
		}
	}
	if _, taken := cfg.Project(name); taken {
		return Project{}, conflict("the name %q is taken by another project — pick another name", name)
	}
	return d.addedLocked(ctx, name, abs, a.Webserver, template, a.Start)
}

// addedLocked registers a project, then starts it if asked: adding from the
// sheet means wanting the project up, and its row shows it starting.
func (d *Daemon) addedLocked(ctx context.Context, name, path, server, template string, start bool) (Project, error) {
	p, err := d.addProjectLocked(ctx, name, path, server, template)
	if err != nil || !start {
		return p, err
	}
	// INFO: The start runs once mu is free, so the sheet closes at once and the
	// row shows the project coming up.
	go func() {
		startCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()
		defer d.track()()
		d.mu.Lock()
		defer d.mu.Unlock()
		cfg := d.store.Get()
		// INFO: Removed again before its start came round: nothing to start.
		entry, ok := cfg.Project(name)
		if !ok {
			return
		}
		if err := d.startProjectLocked(startCtx, cfg, entry); err != nil {
			d.log.Error("start added project", "project", name, "err", err)
		}
	}()
	cfg := d.store.Get()
	entry, _ := cfg.Project(name)
	return d.projectState(cfg, entry), nil
}

// folderAt resolves path to an absolute path, and refuses one that is not a
// folder.
func folderAt(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", invalid("%s is not a usable path: %v", path, err)
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return "", notFound("no folder at %s", abs)
	}
	return abs, nil
}

// sameDir compares folders after resolving symlinks, so that /tmp and
// /private/tmp on macOS are recognised as one place.
func sameDir(a, b string) bool {
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// addProjectLocked registers a project; path is its folder when that is not
// www/<name>, server the webserver it is pinned to and template its config
// template, if any.
func (d *Daemon) addProjectLocked(ctx context.Context, name, path, server, template string) (Project, error) {
	if err := project.ValidateName(name); err != nil {
		return Project{}, invalid("%s", err)
	}
	if path == "" && !project.Exists(d.root, name) {
		return Project{}, notFound("no folder named %q under www/", name)
	}
	cfg := d.store.Get()
	if _, exists := cfg.Project(name); exists {
		return Project{}, conflict("a project named %q already exists — rename the folder or remove that project first", name)
	}

	port := wruntime.NextProjectPort(cfg, d.sup.PortFree)
	if port == 0 {
		return Project{}, conflict("%s", noProjectPort)
	}

	// INFO: No hosts entry: <name>.localhost resolves to loopback without one, so
	// adding a project never asks for a password (pretty-urls.feature).
	entry := config.Project{Name: name, Port: port, Path: path, Template: template}
	if server != "" {
		if !slices.Contains(cfg.Services.Webserver.Available, server) {
			return Project{}, invalid("%q is not an available webserver", server)
		}
		entry.WebserverOverride = &server
	}

	next, err := d.store.Update(func(c *config.Config) error {
		c.SetProject(entry)
		return nil
	})
	if err != nil {
		return Project{}, err
	}

	// INFO: Nothing to reload: registering starts nothing — addedLocked starts it
	// afterwards when asked — and the front door serves started projects only.
	d.publish()
	return d.projectState(next, entry), nil
}

// RemoveProject unregisters a project: its services are stopped and its
// certificate revoked. The folder itself is left on disk — the folder is the project, and deleting a user's files is
// not implied by removing it from the list.
func (d *Daemon) RemoveProject(ctx context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	cfg := d.store.Get()
	p, ok := cfg.Project(name)
	if !ok {
		return notFound("no project named %q", name)
	}
	defer d.track()()

	d.setStarted(name, false)
	if err := d.sup.Stop(ctx, projectServiceID(name)); err != nil {
		d.log.Error("stop project before removal", "project", name, "err", err)
	}
	if p.SSL {
		_ = d.certs.Revoke(wruntime.CertPath(d.root, name), wruntime.KeyPath(d.root, name))
	}

	next, err := d.store.Update(func(c *config.Config) error {
		if !c.RemoveProject(name) {
			return notFound("no project named %q", name)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := d.settleFrontDoor(ctx, next); err != nil {
		d.log.Error("reload webserver after removing project", "project", name, "err", err)
	}
	d.publish()
	return nil
}

// StartProject starts everything the project needs: its PHP backend and the
// webserver that serves it (tray-actions.feature, "Starting a project").
func (d *Daemon) StartProject(ctx context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	defer d.track()()

	cfg := d.store.Get()
	p, ok := cfg.Project(name)
	if !ok {
		return notFound("no project named %q", name)
	}
	return d.startProjectLocked(ctx, cfg, p)
}

func (d *Daemon) startProjectLocked(ctx context.Context, cfg *config.Config, p config.Project) (err error) {
	if st, _ := d.projectStatus(cfg, p); st == supervisor.StateRunning {
		return nil
	}
	// INFO: Marked started before anything runs, so the front door takes it on and
	// its row shows it starting.
	d.setStarted(p.Name, true)
	defer func() {
		if err != nil {
			d.startFailed(p.Name, err)
		}
	}()
	if err := d.checkRules(cfg, p); err != nil {
		return err
	}
	if err := d.ensurePHP(ctx, cfg, cfg.PHPVersionFor(p)); err != nil {
		return err
	}
	if cfg.OwnInstance(p) {
		if cfg, p, err = d.ownPort(cfg, p); err != nil {
			return err
		}
		spec, err := d.res.ProjectSpec(cfg, p)
		if err != nil {
			return err
		}
		if err := d.sup.Start(ctx, spec); err != nil {
			return err
		}
	}
	// INFO: The front door takes the project on: serving it itself, or forwarding
	// it to its own instance, which listens on loopback only.
	return d.applyFrontDoor(ctx, cfg)
}

// StopProject stops serving one project and nothing else: the front door
// restarts without it and keeps serving every other started project, and
// stops only once none is left (tray-actions.feature, "Stopping a project").
func (d *Daemon) StopProject(ctx context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	defer d.track()()

	cfg := d.store.Get()
	p, ok := cfg.Project(name)
	if !ok {
		return notFound("no project named %q", name)
	}
	d.setStarted(name, false)
	if cfg.OwnInstance(p) {
		if err := d.sup.Stop(ctx, projectServiceID(name)); err != nil {
			return err
		}
	}
	return d.settleFrontDoor(ctx, cfg)
}

// RestartProject generates a project's webserver config again and restarts
// the process serving it, starting its PHP backend if that is not running
// (tray-actions.feature, "Restarting a project"). It is also the retry for a
// project that failed to start: the restart of a stopped process is a start.
func (d *Daemon) RestartProject(ctx context.Context, name string) (err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	defer d.track()()

	cfg := d.store.Get()
	p, ok := cfg.Project(name)
	if !ok {
		return notFound("no project named %q", name)
	}
	if err := d.ensurePHP(ctx, cfg, cfg.PHPVersionFor(p)); err != nil {
		return err
	}
	d.setStarted(p.Name, true)
	defer func() {
		if err != nil {
			d.startFailed(p.Name, err)
		}
	}()
	if err := d.checkRules(cfg, p); err != nil {
		return err
	}
	if cfg.OwnInstance(p) {
		if cfg, p, err = d.ownPort(cfg, p); err != nil {
			return err
		}
		spec, err := d.res.ProjectSpec(cfg, p)
		if err != nil {
			return err
		}
		if err := d.sup.Restart(ctx, spec); err != nil {
			return err
		}
		return d.applyFrontDoor(ctx, cfg)
	}
	spec, err := d.res.WebserverSpec(d.served(cfg))
	if err != nil {
		return err
	}
	return d.sup.Restart(ctx, spec)
}

// Settings are the per-project overrides. A nil field leaves that setting
// unchanged; a pointer to the empty string clears the override and reverts to
// the global default (app-configuration.feature).
type Settings struct {
	Webserver *string `json:"webserver_override"`
	PHP       *string `json:"php_version"`
	SSL       *bool   `json:"ssl"`
	// Template is the config template's id; "" picks none
	// (config-templates.feature, "Picking a config template for a project").
	Template *string `json:"template"`
}

// UpdateSettings applies per-project overrides and restarts only what the
// change affects, leaving every other project alone.
func (d *Daemon) UpdateSettings(ctx context.Context, name string, s Settings) (Project, error) {
	defer d.track()()

	// INFO: Turning SSL on sets SSL up first — mkcert downloaded, its authority
	// trusted — outside mu, because both can take a while. A declined trust
	// prompt does not stop SSL: certificates still work, browsers warn, and
	// Settings offers to ask again (local-ssl.feature, "Trust is declined").
	if s.SSL != nil && *s.SSL {
		if st := d.certs.Status(); !st.Installed || !st.Trusted {
			err := d.certs.Setup(ctx)
			switch {
			case errors.Is(err, elevate.ErrDeclined):
				d.log.Info("trusting the local certificate authority was declined; browsers will warn", "project", name)
			case err != nil:
				return Project{}, err
			}
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	cfg := d.store.Get()
	before, ok := cfg.Project(name)
	if !ok {
		return Project{}, notFound("no project named %q", name)
	}

	after := before
	if s.Webserver != nil {
		if *s.Webserver == "" {
			after.WebserverOverride = nil
		} else {
			if !slices.Contains(cfg.Services.Webserver.Available, *s.Webserver) {
				return Project{}, invalid("%q is not an available webserver", *s.Webserver)
			}
			v := *s.Webserver
			after.WebserverOverride = &v
		}
	}
	if s.PHP != nil {
		if *s.PHP == "" {
			after.PHPVersion = nil
		} else {
			if !slices.Contains(cfg.Services.PHP.Available, *s.PHP) {
				return Project{}, invalid("PHP %s is not installed", *s.PHP)
			}
			v := *s.PHP
			after.PHPVersion = &v
		}
	}
	if s.SSL != nil {
		after.SSL = *s.SSL
	}
	if s.Template != nil {
		if *s.Template != "" && !d.res.Templates().Exists(*s.Template) {
			return Project{}, notFound("no config template named %q", *s.Template)
		}
		after.Template = *s.Template
	}

	// WARNING: Certificates are issued before the config is written, so a failure to
	// issue leaves the project exactly as it was.
	if after.SSL && !before.SSL {
		host := wruntime.Hostname(name)
		if err := d.certs.Issue(ctx, host, wruntime.CertPath(d.root, name), wruntime.KeyPath(d.root, name)); err != nil {
			return Project{}, err
		}
	}
	if !after.SSL && before.SSL {
		_ = d.certs.Revoke(wruntime.CertPath(d.root, name), wruntime.KeyPath(d.root, name))
	}

	next, err := d.store.Update(func(c *config.Config) error {
		c.SetProject(after)
		return nil
	})
	if err != nil {
		return Project{}, err
	}

	if err := d.applySettingsChange(ctx, next, before, after); err != nil {
		return Project{}, err
	}
	d.publish()
	updated, _ := next.Project(name)
	return d.projectState(next, updated), nil
}

// applySettingsChange restarts exactly the processes a settings change
// invalidated, and nothing else. A project that is not started needs
// nothing: the front door does not serve it, and its next start picks the
// change up. For a started one, its own instance is stopped, started or
// restarted as the change requires, and the front door restarted with it.
func (d *Daemon) applySettingsChange(ctx context.Context, cfg *config.Config, before, after config.Project) error {
	if !d.isStarted(after.Name) {
		return nil
	}
	if cfg.OwnInstance(before) && !cfg.OwnInstance(after) {
		// INFO: The project moves onto the global instance.
		if err := d.sup.Stop(ctx, projectServiceID(after.Name)); err != nil {
			return err
		}
	}
	if err := d.ensurePHP(ctx, cfg, cfg.PHPVersionFor(after)); err != nil {
		return err
	}
	if cfg.OwnInstance(after) {
		var err error
		if cfg, after, err = d.ownPort(cfg, after); err != nil {
			return err
		}
		spec, err := d.res.ProjectSpec(cfg, after)
		if err != nil {
			return err
		}
		if err := d.sup.Restart(ctx, spec); err != nil {
			return err
		}
	}
	return d.applyFrontDoor(ctx, cfg)
}

// unregistered lists folders sitting in www/ that are not projects yet — the
// "drop a folder in www/" path made visible.
func (d *Daemon) unregistered(cfg *config.Config) []string {
	folders, err := project.Discover(d.root)
	if err != nil {
		d.log.Error("scan www/", "err", err)
		return []string{}
	}
	known := map[string]bool{}
	for _, p := range cfg.Projects {
		known[p.Name] = true
	}
	out := []string{}
	for _, f := range folders {
		if !known[f] {
			out = append(out, f)
		}
	}
	return out
}
