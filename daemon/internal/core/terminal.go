package core

import (
	"context"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/shellpath"
)

// Terminal is "Use in terminal" on the PHP page (php-terminal.feature).
type Terminal struct {
	On bool `json:"on"`
	// Dir is bin/path; PHP is the binary its php runs, empty while the global
	// default version is not installed.
	Dir string `json:"dir"`
	PHP string `json:"php"`
	// Places lists where PATH names Dir: shell startup files, or the user
	// environment on Windows. Empty while it is off.
	Places []string `json:"places"`
}

// SetPHPTerminal puts bin/path on the user's PATH or takes it off again
// (php-terminal.feature, "Putting Wharf's PHP on the terminal PATH" and
// "Taking Wharf's PHP off the terminal PATH"). The startup files and the
// user environment belong to the user, so neither asks for a password.
func (d *Daemon) SetPHPTerminal(_ context.Context, on bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	dir := d.root.PathBin()
	var (
		places []string
		err    error
	)
	if on {
		places, err = d.shell.Add(dir)
	} else {
		_, err = d.shell.Remove(dir)
	}
	if err != nil {
		return err
	}
	if _, err := d.store.Update(func(c *config.Config) error {
		c.Services.PHP.Terminal = on
		return nil
	}); err != nil {
		return err
	}
	d.setTerminalPlaces(places)
	d.log.Info("PHP in the terminal", "on", on, "places", places)
	d.publish()
	return nil
}

// applyTerminal brings the PATH in line with the config: at start, which
// repairs a block the user deleted ("Turning it on again adds nothing
// twice"), and when a hand-edit changed the key.
//
// WARNING: Nothing is removed at start while the key is off. A daemon with
// another root — a test run, a second Wharf folder — must not take away the
// block the user's real Wharf wrote.
func (d *Daemon) applyTerminal(on, changed bool) {
	dir := d.root.PathBin()
	switch {
	case on:
		places, err := d.shell.Add(dir)
		if err != nil {
			d.log.Warn("put PHP on the terminal PATH", "err", err)
		}
		d.setTerminalPlaces(places)
	case changed:
		if _, err := d.shell.Remove(dir); err != nil {
			d.log.Warn("take PHP off the terminal PATH", "err", err)
		}
		d.setTerminalPlaces(nil)
	}
}

func (d *Daemon) setTerminalPlaces(places []string) {
	if places == nil {
		places = []string{}
	}
	d.terminalPlaces.Store(&places)
}

func (d *Daemon) terminalState(cfg *config.Config) Terminal {
	t := Terminal{On: cfg.Services.PHP.Terminal, Dir: d.root.PathBin(), Places: []string{}}
	d.linkMu.Lock()
	t.PHP = d.linkTarget
	d.linkMu.Unlock()
	if p := d.terminalPlaces.Load(); p != nil && t.On {
		t.Places = *p
	}
	return t
}

// syncPathLink points bin/path's php at the global default version, or
// removes it while that version is not installed (php-terminal.feature, "The
// global default PHP is kept in one folder"). Every change publishes a
// snapshot, so publish calls this and the link follows every change of the
// default or of what is installed. The link is kept whether or not it is on
// PATH: it costs nothing, and a user may name the folder by hand.
func (d *Daemon) syncPathLink(cfg *config.Config) {
	// INFO: Until the first detection nothing is known to be installed, and
	// removing the link then would leave terminals without php while Wharf starts.
	if d.phpInstalls.Load() == nil {
		return
	}
	target := d.defaultCLI(cfg.Services.PHP.Version)

	d.linkMu.Lock()
	defer d.linkMu.Unlock()
	if d.linkSynced && target == d.linkTarget {
		return
	}
	if err := shellpath.Link(d.root.PathBin(), target); err != nil {
		d.log.Warn("link the default PHP into bin/path", "target", target, "err", err)
		return
	}
	d.linkTarget, d.linkSynced = target, true
}

// defaultCLI is the php binary of a version, picked the way locatePHP picks
// its folder: Wharf's own copy before one adopted from the machine.
func (d *Daemon) defaultCLI(version string) string {
	cli := ""
	for _, in := range d.PHPInstalls() {
		if in.Version != version || in.CLI == "" || !in.Servable() {
			continue
		}
		if in.Source == "vendored" {
			return in.CLI
		}
		if cli == "" {
			cli = in.CLI
		}
	}
	return cli
}
