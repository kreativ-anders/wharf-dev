package core

import (
	"context"
	"errors"
	"path/filepath"
	goruntime "runtime"
	"sync"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/download"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/update"
)

// Update is what "Check for updates" last found (settings.feature,
// "Checking for updates on request"). Nothing is looked up until the user
// asks, so it is empty after every start.
type Update struct {
	// Checked is true once a lookup has answered since the daemon started.
	Checked bool `json:"checked"`
	// Latest is the newest release's version; Newer says whether it is newer
	// than the running one.
	Latest string `json:"latest,omitempty"`
	Newer  bool   `json:"newer"`
	// File is the newest release's file for this system, which a SHA256SUMS
	// can check. Empty when there is none, and Page is offered instead
	// (settings.feature, "A release without a checked file for this system
	// offers its page").
	File string `json:"file,omitempty"`
	Page string `json:"page,omitempty"`
	// Checking and Downloading are true while either is under way.
	Checking    bool `json:"checking"`
	Downloading bool `json:"downloading"`
}

// updates holds the last lookup's release and what is under way. It has its
// own lock because a lookup and a download run outside mu.
type updates struct {
	mu          sync.Mutex
	src         update.Source
	checked     bool
	release     update.Release
	checking    bool
	downloading bool
}

// CheckForUpdates asks the public releases API for Wharf's newest release
// and compares it with the running version. It is the only way Wharf ever
// looks: never at start and never in the background.
func (d *Daemon) CheckForUpdates(ctx context.Context) error {
	if _, ok := update.Newer("0.0.0", d.version); !ok {
		return invalid("This build of Wharf has no version to compare (%q). Updates can be checked from a release build.", d.version)
	}
	u := &d.updates
	u.mu.Lock()
	if u.checking {
		u.mu.Unlock()
		return nil
	}
	u.checking = true
	u.mu.Unlock()
	d.publish()

	// WARNING: Bounded tightly: the button shows "Checking…" while this runs,
	// and a hanging connection would leave it there.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rel, err := u.src.Latest(ctx)

	u.mu.Lock()
	u.checking = false
	if err == nil {
		u.checked, u.release = true, rel
	}
	u.mu.Unlock()
	d.publish()
	if err != nil {
		// INFO: The cause — offline, rate limited, GitHub down — is logged, not
		// shown: none of them is fixed by anything but trying again later.
		d.log.Warn("update check failed", "err", err)
		return update.ErrLookup
	}
	return nil
}

// DownloadUpdate saves the newest release's file for this system to dest,
// once it matches the release's SHA256SUMS (settings.feature, "Downloading
// an update"). Wharf never runs what it downloaded.
func (d *Daemon) DownloadUpdate(ctx context.Context, dest string) error {
	if !filepath.IsAbs(dest) {
		return invalid("Choose where to save the update: %q is not a full path.", dest)
	}
	u := &d.updates
	u.mu.Lock()
	st := u.stateLocked(d.version)
	if !st.Newer || st.File == "" {
		u.mu.Unlock()
		return conflict("There is no update to download. Check for updates first.")
	}
	if u.downloading {
		u.mu.Unlock()
		return conflict("The update is already being downloaded.")
	}
	u.downloading = true
	rel := u.release
	u.mu.Unlock()
	d.publish()
	defer func() {
		u.mu.Lock()
		u.downloading = false
		u.mu.Unlock()
		d.publish()
	}()

	err := u.src.Download(ctx, rel, st.File, dest)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, update.ErrUnchecked):
		return conflict("Wharf %s has no file for this system that can be checked. Download it from the release page: %s", rel.Version, st.Page)
	case errors.Is(err, download.ErrChecksum):
		// INFO: Usually a cut transfer, rarely an altered file; either way
		// nothing was kept (settings.feature, "A download that does not match
		// is not kept").
		return conflict("The download did not match the release's SHA256SUMS and was not saved. Try again, or download it from the release page: %s", st.Page)
	}
	return err
}

func (d *Daemon) updateState() Update {
	u := &d.updates
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.stateLocked(d.version)
}

func (u *updates) stateLocked(running string) Update {
	st := Update{Checking: u.checking, Downloading: u.downloading}
	if !u.checked {
		return st
	}
	st.Checked = true
	st.Latest = u.release.Version
	st.Newer, _ = update.Newer(u.release.Version, running)
	st.Page = u.release.Page
	if st.Newer {
		st.File = update.FileFor(u.release, goruntime.GOOS, goruntime.GOARCH)
	}
	return st
}
