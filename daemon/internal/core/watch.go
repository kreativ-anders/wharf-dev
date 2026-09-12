package core

import (
	"context"
	"os"
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
			// Custom webserver directives live beside wharf.json and are
			// hand-edited the same way (app-configuration.feature).
			d.ApplyCustomConfigs(ctx)
			d.ApplyPHPSettings(ctx)

			info, err := os.Stat(d.store.Path())
			if err != nil || !info.ModTime().After(last) {
				continue
			}
			last = info.ModTime()

			d.mu.Lock()
			cfg, err := d.store.Reload()
			d.mu.Unlock()
			if err != nil {
				// A half-saved file is normal during an editor write; the
				// next tick picks up the finished one.
				d.log.Warn("config reload failed, keeping previous config", "err", err)
				continue
			}
			d.log.Info("config reloaded from disk", "projects", len(cfg.Projects))
			d.publish()
		}
	}
}
