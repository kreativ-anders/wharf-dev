package core

import (
	"context"
	"errors"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/elevate"
	wruntime "github.com/kreativ-anders/wharf-dev/daemon/internal/runtime"
)

const lowPortsDeclined = "Wharf needs permission to use ports 80 and 443 — start the project again and allow it, " +
	"or run: sudo sysctl -w net.ipv4.ip_unprivileged_port_start=80"

const noProjectPort = "every project port from 8080 up is taken — quit a program listening there, or remove a project"

// ownPort gives a project's own instance a port nothing else holds, before it
// starts. The recorded port was free when the project was added, but another
// program may have taken it since; left there, the instance would wait out
// the stop timeout and fail. The move is recorded, and the front door,
// rendered from the returned config, forwards to the new port
// (service-management.feature, "A project's own instance never takes a port
// another program holds").
func (d *Daemon) ownPort(cfg *config.Config, p config.Project) (*config.Config, config.Project, error) {
	if !cfg.OwnInstance(p) || d.sup.Running(projectServiceID(p.Name)) {
		return cfg, p, nil
	}
	if p.Port > 0 && d.sup.PortFree(p.Port) {
		return cfg, p, nil
	}
	port := wruntime.NextProjectPort(cfg, d.sup.PortFree)
	if port == 0 {
		return cfg, p, conflict("%s", noProjectPort)
	}
	d.log.Info("project port is held by another program; moving", "project", p.Name, "from", p.Port, "to", port)
	p.Port = port
	next, err := d.store.Update(func(c *config.Config) error {
		c.SetProject(p)
		return nil
	})
	if err != nil {
		return cfg, p, err
	}
	return next, p, nil
}

// applyFrontDoor brings the front door in line with the started projects:
// restarted with them if it runs, started if it does not, stopped once no
// project is left to serve.
func (d *Daemon) applyFrontDoor(ctx context.Context, cfg *config.Config) error {
	if !d.anyStarted() {
		return d.sup.Stop(ctx, runtimeWebserverID)
	}
	// INFO: Asked before every start, not once: the answer is the machine's, and
	// it costs nothing once given (service-management.feature, "Linux asks
	// once before the front door first takes port 80").
	if err := d.elev.AllowLowPorts(); errors.Is(err, elevate.ErrDeclined) {
		return conflict("%s", lowPortsDeclined)
	} else if err != nil {
		return err
	}
	spec, err := d.res.WebserverSpec(d.served(cfg))
	if err != nil {
		return err
	}
	if d.sup.Running(runtimeWebserverID) {
		return d.sup.Restart(ctx, spec)
	}
	return d.sup.Start(ctx, spec)
}

// settleFrontDoor follows a project leaving the front door: a running front
// door restarts with the projects still started, and stops once none is.
func (d *Daemon) settleFrontDoor(ctx context.Context, cfg *config.Config) error {
	if !d.anyStarted() {
		return d.sup.Stop(ctx, runtimeWebserverID)
	}
	return d.reloadGlobalWebserver(ctx, cfg)
}

// served is cfg narrowed to the started projects: what the front door
// serves or forwards.
func (d *Daemon) served(cfg *config.Config) *config.Config {
	out := *cfg
	out.Projects = nil
	for _, p := range cfg.Projects {
		if d.isStarted(p.Name) {
			out.Projects = append(out.Projects, p)
		}
	}
	return &out
}

func (d *Daemon) setStarted(name string, on bool) {
	d.startedMu.Lock()
	defer d.startedMu.Unlock()
	delete(d.failed, name)
	if on {
		d.started[name] = true
	} else {
		delete(d.started, name)
		// INFO: Sharing ends with the project (sharing.feature, "Sharing ends
		// with the project").
		delete(d.shared, name)
	}
}

// startFailed takes a project whose start failed out of the front door's set:
// left in, every later start of another project would try it again, and show
// it starting alongside (tray-actions.feature, "A project that failed to start
// is not started with the next one").
func (d *Daemon) startFailed(name string, err error) {
	d.startedMu.Lock()
	defer d.startedMu.Unlock()
	delete(d.started, name)
	delete(d.shared, name)
	d.failed[name] = err.Error()
}

// startFailure is why a project's last start failed, if it did.
func (d *Daemon) startFailure(name string) (string, bool) {
	d.startedMu.Lock()
	defer d.startedMu.Unlock()
	msg, ok := d.failed[name]
	return msg, ok
}

func (d *Daemon) isStarted(name string) bool {
	d.startedMu.Lock()
	defer d.startedMu.Unlock()
	return d.started[name]
}

func (d *Daemon) anyStarted() bool {
	d.startedMu.Lock()
	defer d.startedMu.Unlock()
	return len(d.started) > 0
}

func (d *Daemon) clearStarted() {
	d.startedMu.Lock()
	defer d.startedMu.Unlock()
	d.started = map[string]bool{}
	d.failed = map[string]string{}
	d.shared = map[string]bool{}
}

// reloadGlobalWebserver regenerates the shared webserver's config and restarts
// it if it is running. It is a no-op when nothing is up, so adding a project
// while everything is stopped does not start a server.
func (d *Daemon) reloadGlobalWebserver(ctx context.Context, cfg *config.Config) error {
	if !d.sup.Running(runtimeWebserverID) {
		return nil
	}
	spec, err := d.res.WebserverSpec(d.served(cfg))
	if err != nil {
		return err
	}
	return d.sup.Restart(ctx, spec)
}
