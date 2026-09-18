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

// AddProject registers an existing folder under www/ as a project.
func (d *Daemon) AddProject(ctx context.Context, name string) (Project, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.addProjectLocked(ctx, name, "", "", "")
}

// AddFolder registers the folder at path, wherever it is — the folder
// picker's action (project-folders.feature). A folder directly in www/ is
// added by name as usual; any other folder stays where it is and its location
// is recorded, under a name derived from the folder's own.
func (d *Daemon) AddFolder(ctx context.Context, path string) (Project, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	abs, err := filepath.Abs(path)
	if err != nil {
		return Project{}, invalid("%s is not a usable path: %v", path, err)
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return Project{}, notFound("no folder at %s", abs)
	}
	if sameDir(filepath.Dir(abs), d.root.WWW()) {
		return d.addProjectLocked(ctx, filepath.Base(abs), "", "", "")
	}

	name := project.Slug(filepath.Base(abs))
	if name == "" {
		return Project{}, invalid("the folder name %q has no letter or digit to make a project name from — rename the folder", filepath.Base(abs))
	}
	if err := config.CheckProject(config.Project{Name: name, Path: abs}); err != nil {
		return Project{}, invalid("%s", err)
	}
	if project.Exists(d.root, name) {
		return Project{}, conflict("a folder named %q is already in www/ — rename one of the two folders", name)
	}
	cfg := d.store.Get()
	for _, p := range cfg.Projects {
		if p.Path != "" && sameDir(p.Path, abs) {
			return Project{}, conflict("%s is already the project %q", abs, p.Name)
		}
	}
	return d.addProjectLocked(ctx, name, abs, "", "")
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

	// INFO: Nothing to reload: a new project is not started, and the front door
	// serves started projects only.
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

// Scaffold creates a project from a quick-app template and registers it,
// pinned to server when one was picked; left empty, the project follows the
// global webserver (quick-app-php.feature, "Choosing the webserver while
// creating a project").
func (d *Daemon) Scaffold(ctx context.Context, templateID, name, server string) (Project, error) {
	defer d.track()()

	tpl, ok := project.TemplateByID(templateID)
	if !ok {
		return Project{}, notFound("no template named %q", templateID)
	}
	// INFO: wharfctl and anything else on the socket get the rewrite the GUI's
	// field applies (quick-app-php.feature, "A typed name becomes a project
	// name").
	typed := name
	if name = project.Slug(typed); name == "" {
		return Project{}, invalid("%q has no letter or digit to make a project name from — use at least one", typed)
	}
	// INFO: Checked before anything is downloaded, so a bad choice leaves no folder.
	if server != "" && !slices.Contains(d.store.Get().Services.Webserver.Available, server) {
		return Project{}, invalid("%q is not an available webserver", server)
	}
	// WARNING: The download runs outside mu. A starter kit takes a while to
	// fetch, and every other action — the tray's too — would wait behind it.
	// Create stages the folder and refuses a name www/ already holds, and
	// registering re-checks under mu.
	if err := d.scaf.Create(ctx, tpl, name); err != nil {
		return Project{}, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	p, err := d.addProjectLocked(ctx, name, "", server, tpl.ConfigTemplate)
	if err != nil {
		// WARNING: The folder exists but could not be registered; leaving it behind
		// with no config entry would be a project the GUI cannot see.
		os.RemoveAll(d.root.ProjectDir(name))
		return Project{}, err
	}

	cfg := d.store.Get()
	entry, _ := cfg.Project(name)
	// INFO: Scaffolding implies wanting the project up: the GUI shows "starting"
	// while this runs.
	go func() {
		startCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()
		d.mu.Lock()
		defer d.mu.Unlock()
		if err := d.startProjectLocked(startCtx, cfg, entry); err != nil {
			d.log.Error("start scaffolded project", "project", name, "err", err)
		}
	}()
	return p, nil
}

// Templates lists the registered quick-app templates.
func (d *Daemon) Templates() []project.Template { return project.Templates }

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
