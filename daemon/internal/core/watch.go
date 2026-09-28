package core

import (
	"context"
	"os"
	"slices"
	"time"
)

// WatchConfig reloads wharf.json when it changes on disk, so hand-editing the
// file is a first-class way to use the tool (dev/design-principles.md §2).
//
// It polls rather than using an OS watcher: polling one small file every two
// seconds costs nothing measurable and needs no per-platform watcher API,
// which is the same trade this project makes everywhere else.
func (d *Daemon) WatchConfig(ctx context.Context) {
	const interval = 2 * time.Second
	var last time.Time
	if info, err := os.Stat(d.store.Path()); err == nil {
		last = info.ModTime()
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// INFO: Custom webserver directives live beside wharf.json and are
			// hand-edited the same way (app-configuration.feature).
			d.ApplyCustomConfigs(ctx)
			d.ApplyPHPSettings(ctx)
			// INFO: A laptop moves between networks; a shared project follows its
			// address (sharing.feature).
			d.CheckNetwork(ctx)

			info, err := os.Stat(d.store.Path())
			if err != nil || !info.ModTime().After(last) {
				continue
			}
			last = info.ModTime()

			d.mu.Lock()
			prev := d.store.Get()
			cfg, err := d.store.Reload()
			d.mu.Unlock()
			if err != nil {
				// INFO: A half-saved file is normal during an editor write; the
				// next tick picks up the finished one.
				d.log.Warn("config reload failed, keeping previous config", "err", err)
				continue
			}
			// INFO: Hidden PHP folders decide what detection finds, so a hand-edit
			// of that list re-scans like the GUI's hide and show do.
			if !slices.Equal(prev.Services.PHP.Hidden, cfg.Services.PHP.Hidden) {
				d.RefreshPHP(ctx)
			}
			// INFO: Turning "terminal" on or off by hand does what the switch does.
			if prev.Services.PHP.Terminal != cfg.Services.PHP.Terminal {
				d.mu.Lock()
				d.applyTerminal(cfg.Services.PHP.Terminal, true)
				d.mu.Unlock()
			}
			d.log.Info("config reloaded from disk", "projects", len(cfg.Projects))
			d.publish()
		}
	}
}
