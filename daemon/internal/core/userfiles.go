package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	wruntime "github.com/kreativ-anders/wharf-dev/daemon/internal/runtime"
)

// CustomConfig returns the path of a project's custom directives for one
// webserver, creating the file with a commented starting point if it does not
// exist yet (app-configuration.feature, "Custom webserver directives for one
// project"). The file is the user's from then on: Wharf only ever includes it.
func (d *Daemon) CustomConfig(ctx context.Context, name, server string) (string, error) {
	cfg := d.store.Get()
	if _, ok := cfg.Project(name); !ok {
		return "", notFound("no project named %q", name)
	}
	if !slices.Contains(cfg.Services.Webserver.Available, server) {
		return "", invalid("%q is not an available webserver", server)
	}
	path := d.root.CustomConfig(name, server)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(customConfigStub(name, server)), 0o644); err != nil {
		return "", err
	}
	// INFO: Apply at once rather than on the watcher's next tick, so the project
	// is already using the file by the time the editor opens it.
	d.ApplyCustomConfigs(ctx)
	return path, nil
}

func customConfigStub(name, server string) string {
	other, block, example := "apache", "server { } block", `#   client_max_body_size 64m;
#   location /api/ { proxy_pass http://127.0.0.1:3000; }
#
# Wharf already sets listen, server_name, root, index, "location /", the PHP
# location and Kirby's own rules — the whole recipe from Kirby's docs. A
# server { } block or any of those directives here makes nginx refuse to
# start.`
	if server == "apache" {
		other, block, example = "nginx", "<VirtualHost> section", `#   php_value upload_max_filesize 64M
#   Header set X-Robots-Tag "noindex"
#
# .htaccess files in the project work as well: AllowOverride is All, so
# Kirby's own .htaccess applies without anything here.`
	}
	return fmt.Sprintf(`# Custom %[1]s directives for %[2]s.
#
# Wharf includes this file inside the project's %[3]s, after its own
# directives: write single directives, not a block of your own. It is used
# only while %[2]s is served by %[1]s; the %[4]s file beside it is used when
# the project is served by %[4]s.
#
# Saving the file restarts the webserver serving %[2]s. A mistake here keeps
# that webserver from starting — its error appears in Wharf.
#
# Examples:
%[5]s
`, server, name, block, other, example)
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
;
; Examples — remove the ";" in front of a line to use it:
;
; memory_limit = 512M
; upload_max_filesize = 64M
; post_max_size = 64M
; max_execution_time = 120
; display_errors = On
; error_reporting = E_ALL
; date.timezone = Europe/Berlin
; opcache.enable = 0
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
// file that belongs to a registered project.
func (d *Daemon) customConfigTimes(cfg *config.Config) map[string]time.Time {
	out := map[string]time.Time{}
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
		if !changed[d.root.CustomConfig(p.Name, server)] || !d.isStarted(p.Name) {
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
		if err := d.reloadGlobalWebserver(ctx, cfg); err != nil {
			d.log.Error("apply custom config", "err", err)
		}
	}
	d.publish()
}
