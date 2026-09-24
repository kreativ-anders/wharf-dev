package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/configtemplate"
	wruntime "github.com/kreativ-anders/wharf-dev/daemon/internal/runtime"
)

// CustomConfigRules is a project's custom config as the editor opens it.
type CustomConfigRules struct {
	// Webserver is the one serving the project, the only one whose custom
	// config can be edited (app-configuration.feature, "A custom config
	// belongs to the webserver serving the project").
	Webserver string `json:"webserver"`
	Content   string `json:"content"`
	// Exists is false while Content is only the starting point.
	Exists bool `json:"exists"`
}

// ReadCustomConfig returns a project's custom config for the webserver
// serving it, or — while it has none — its config template's rules for that
// webserver, placeholders intact, to start from (app-configuration.feature,
// "A project's custom config starts from its config template").
func (d *Daemon) ReadCustomConfig(name string) (CustomConfigRules, error) {
	cfg := d.store.Get()
	p, ok := cfg.Project(name)
	if !ok {
		return CustomConfigRules{}, notFound("no project named %q", name)
	}
	server := cfg.WebserverFor(p)
	out := CustomConfigRules{Webserver: server}
	body, err := os.ReadFile(d.root.CustomConfig(name, server))
	if err == nil {
		out.Content, out.Exists = string(body), true
		return out, nil
	}
	if !os.IsNotExist(err) {
		return out, err
	}
	lib := d.res.Templates()
	rules, templateName := configtemplate.Plain(server), ""
	if p.Template != "" {
		if rules, err = lib.Read(p.Template, server); err != nil {
			return out, notFound("the config template %q does not exist — pick another in %s's settings first", p.Template, name)
		}
		templateName = p.Template
		for _, t := range lib.List() {
			if t.ID == p.Template {
				templateName = t.Name
			}
		}
	}
	out.Content = configtemplate.CustomStarter(name, templateName, server, rules)
	return out, nil
}

// SaveCustomConfig writes a project's custom config for the webserver serving
// it and restarts that webserver with it. Rules without {{listen}}, {{ssl}}
// or {{server_name}} are refused, and so is a save for a webserver that no
// longer serves the project — the editor was opened before a switch, and its
// rules would land in the other webserver's file.
func (d *Daemon) SaveCustomConfig(ctx context.Context, name, server, content string) error {
	path, err := d.customConfigPath(name, server)
	if err != nil {
		return err
	}
	if err := configtemplate.SaveFile(path, server, content); err != nil {
		if errors.Is(err, configtemplate.ErrIncomplete) {
			return invalid("%s", err)
		}
		return err
	}
	// INFO: Applied at once rather than on the watcher's next tick, so the
	// project already runs with the change when the editor closes.
	d.ApplyCustomConfigs(ctx)
	d.publish()
	return nil
}

// DeleteCustomConfig deletes a project's custom config for the webserver
// serving it, which serves the project with its config template again
// (app-configuration.feature, "Removing a custom config returns to the
// config template").
func (d *Daemon) DeleteCustomConfig(ctx context.Context, name, server string) error {
	path, err := d.customConfigPath(name, server)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	d.ApplyCustomConfigs(ctx)
	d.publish()
	return nil
}

func (d *Daemon) customConfigPath(name, server string) (string, error) {
	cfg := d.store.Get()
	p, ok := cfg.Project(name)
	if !ok {
		return "", notFound("no project named %q", name)
	}
	if serving := cfg.WebserverFor(p); server != serving {
		return "", conflict("%s is served by %s, not %s — open its custom config again", name, serving, server)
	}
	return d.root.CustomConfig(name, server), nil
}

// PHPSettings returns config/php.ini, creating it with a commented starting
// point if it does not exist yet (php-settings.feature, "Editing PHP
// settings"). Every PHP version reads it after its own php.ini; the file is
// the user's from then on.
func (d *Daemon) PHPSettings(ctx context.Context) (string, error) {
	path := d.root.PHPIni()
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(phpIniStub), 0o644); err != nil {
		return "", err
	}
	d.ApplyPHPSettings(ctx)
	d.publish()
	return path, nil
}

const phpIniStub = `; Your own PHP settings, for every PHP version Wharf runs.
;
; PHP reads this file after its own php.ini, so a value here wins. Saving it
; restarts PHP; the next request sees the change.

memory_limit = 512M
upload_max_filesize = 64M
post_max_size = 64M
max_execution_time = 120
display_errors = On
error_reporting = E_ALL

; Examples — remove the ";" in front of a line to use it:
;
; date.timezone = Europe/Berlin
; opcache.enable = 0
; extension = curl
; extension = mbstring
`

// ApplyPHPSettings restarts every running PHP backend when config/php.ini
// was created, changed or deleted since it last looked (php-settings
// .feature, "Saving PHP settings applies them"). Webservers are left alone:
// they reach PHP over FastCGI on a port that does not change. The watcher
// calls it on every tick.
func (d *Daemon) ApplyPHPSettings(ctx context.Context) {
	d.mu.Lock()
	defer d.mu.Unlock()

	seen := modTime(d.root.PHPIni())
	if seen.Equal(d.phpIniSeen) {
		return
	}
	d.phpIniSeen = seen

	cfg := d.store.Get()
	for _, v := range cfg.Services.PHP.Available {
		if !d.sup.Running(wruntime.PHPServiceID(v)) {
			continue
		}
		spec, err := d.res.PHPSpec(cfg, v)
		if err == nil {
			err = d.sup.Restart(ctx, spec)
		}
		if err != nil {
			d.log.Error("apply PHP settings", "php", v, "err", err)
		}
	}
	d.publish()
}

// modTime is a file's modification time, or zero when it does not exist.
func modTime(path string) time.Time {
	if info, err := os.Stat(path); err == nil {
		return info.ModTime()
	}
	return time.Time{}
}

// customConfigTimes records the modification time of every custom config
// file that belongs to a registered project, and of every file in
// config/templates/ (config-templates.feature, "A config template edited in
// another editor applies too").
func (d *Daemon) customConfigTimes(cfg *config.Config) map[string]time.Time {
	out := d.res.Templates().ModTimes()
	for _, p := range cfg.Projects {
		for _, server := range cfg.Services.Webserver.Available {
			path := d.root.CustomConfig(p.Name, server)
			if info, err := os.Stat(path); err == nil {
				out[path] = info.ModTime()
			}
		}
	}
	return out
}

// ApplyCustomConfigs restarts the webservers whose projects' custom config
// was created, changed or deleted since it last looked (app-configuration
// .feature, "Saving a custom config applies it"). The watcher calls it on
// every tick; nothing is restarted unless something changed.
func (d *Daemon) ApplyCustomConfigs(ctx context.Context) {
	d.mu.Lock()
	defer d.mu.Unlock()

	cfg := d.store.Get()
	now := d.customConfigTimes(cfg)
	changed := map[string]bool{}
	for path, t := range now {
		if old, ok := d.customSeen[path]; !ok || !old.Equal(t) {
			changed[path] = true
		}
	}
	for path := range d.customSeen {
		if _, ok := now[path]; !ok {
			changed[path] = true
		}
	}
	d.customSeen = now
	if len(changed) == 0 {
		return
	}

	reloadGlobal := false
	for _, p := range cfg.Projects {
		server := cfg.WebserverFor(p)
		touched := changed[d.root.CustomConfig(p.Name, server)] ||
			(p.Template != "" && changed[d.res.Templates().Path(p.Template, server)])
		if !touched || !d.isStarted(p.Name) {
			continue
		}
		// WARNING: Rules Wharf cannot use fail this project alone. Rendered
		// into the front door, they would fail every project it serves.
		if err := d.checkRules(cfg, p); err != nil {
			d.startFailed(p.Name, err)
			if cfg.OwnInstance(p) {
				if err := d.sup.Stop(ctx, projectServiceID(p.Name)); err != nil {
					d.log.Error("apply custom config", "project", p.Name, "err", err)
				}
			}
			reloadGlobal = true
			continue
		}
		if !cfg.OwnInstance(p) {
			reloadGlobal = true
			continue
		}
		if !d.sup.Running(projectServiceID(p.Name)) {
			continue
		}
		spec, err := d.res.ProjectSpec(cfg, p)
		if err == nil {
			err = d.sup.Restart(ctx, spec)
		}
		if err != nil {
			d.log.Error("apply custom config", "project", p.Name, "err", err)
		}
	}
	if reloadGlobal {
		if err := d.settleFrontDoor(ctx, cfg); err != nil {
			d.log.Error("apply custom config", "err", err)
		}
	}
	d.publish()
}
