package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/webserver"
)

// RefreshWebservers re-scans the machine for nginx and Apache.
func (d *Daemon) RefreshWebservers(ctx context.Context) map[string]webserver.Install {
	found := d.web.Detect(ctx)
	d.webInstalls.Store(&found)
	d.publish()
	return found
}

// WebInstalls returns the cached webserver detection.
func (d *Daemon) WebInstalls() map[string]webserver.Install {
	if p := d.webInstalls.Load(); p != nil {
		return *p
	}
	return map[string]webserver.Install{}
}

// InstallWebserver gets a webserver onto the machine — a download into
// bin/<name>, or a package manager — and makes it usable by every project
// (webserver-install.feature, "Installing nginx"). Like InstallPHP it stages
// a download and runs outside mu.
func (d *Daemon) InstallWebserver(ctx context.Context, name string) error {
	if !slices.Contains(d.store.Get().Services.Webserver.Available, name) {
		return invalid("%q is not an available webserver", name)
	}
	if _, ok := d.WebInstalls()[name]; ok {
		return conflict("%s is already installed", name)
	}
	if plan := d.webInstaller.Plan(name); !plan.Installable {
		return invalid("%s", plan.Hint)
	}
	dest := filepath.Join(d.root.Bin(), name)
	if _, err := os.Stat(dest); err == nil {
		return conflict("%s already exists without a working %s in it — remove it to install again", dest, name)
	}
	if !d.begin(d.installing, name) {
		return conflict("%s is already being installed", name)
	}
	defer d.end(d.installing, name)

	if err := os.MkdirAll(d.root.Bin(), 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(d.root.Bin(), ".install-"+name+"-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	if err := d.webInstaller.Install(ctx, name, staging); err != nil {
		return err
	}
	// INFO: A package manager installs elsewhere and leaves staging empty.
	if entries, _ := os.ReadDir(staging); len(entries) > 0 {
		if err := os.Rename(staging, dest); err != nil {
			return fmt.Errorf("move %s into place: %w", name, err)
		}
	}
	if _, ok := d.RefreshWebservers(ctx)[name]; !ok {
		return fmt.Errorf("%s was installed, but Wharf cannot find a usable copy of it — re-scan, or check %s", name, dest)
	}
	d.log.Info("installed webserver", "name", name)
	return nil
}

// SetWebserver makes name the globally active webserver: the running one is
// stopped, the new one started on the port the old one releases, and the
// change written to wharf.json (service-management.feature).
func (d *Daemon) SetWebserver(ctx context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	cfg := d.store.Get()
	if name == "" {
		return invalid("no webserver named")
	}
	if !slices.Contains(cfg.Services.Webserver.Available, name) {
		return invalid("%q is not an available webserver", name)
	}
	if name == cfg.Services.Webserver.Active && d.sup.Running(runtimeWebserverID) {
		return nil
	}

	next, err := d.store.Update(func(c *config.Config) error {
		c.Services.Webserver.Active = name
		return nil
	})
	if err != nil {
		return err
	}

	defer d.track()()

	// INFO: Only swap the process if one is up; activating a webserver while
	// everything is stopped is a config change, not a start.
	if !d.sup.Running(runtimeWebserverID) && !d.sup.AnyRunning() {
		d.publish()
		return nil
	}
	// INFO: A project pinned to the new webserver moves onto the global instance;
	// a started one pinned to the old webserver now runs behind the new one.
	for _, p := range next.Projects {
		if !next.OwnInstance(p) {
			if err := d.sup.Stop(ctx, projectServiceID(p.Name)); err != nil {
				return err
			}
		}
	}
	// INFO: A started project that now runs its own instance needs a port nothing
	// else holds before the front door is told where to forward it.
	for _, p := range next.Projects {
		if next.OwnInstance(p) && d.isStarted(p.Name) {
			if next, _, err = d.ownPort(next, p); err != nil {
				return err
			}
		}
	}
	// INFO: Restarting stops the old process, waits for port 80 to be released,
	// and only then starts the replacement.
	if err := d.applyFrontDoor(ctx, next); err != nil {
		return err
	}
	for _, p := range next.Projects {
		if !next.OwnInstance(p) || !d.isStarted(p.Name) {
			continue
		}
		own, err := d.res.ProjectSpec(next, p)
		if err == nil {
			err = d.sup.Start(ctx, own)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
