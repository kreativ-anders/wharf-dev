package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/php"
	wruntime "github.com/kreativ-anders/wharf-dev/daemon/internal/runtime"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/supervisor"
)

// RefreshPHP re-scans the machine for PHP installations and caches the result
// for the version picker.
func (d *Daemon) RefreshPHP(ctx context.Context) []php.Install {
	installs := d.php.Detect(ctx)
	d.phpInstalls.Store(&installs)
	d.publish()
	return installs
}

// PHPInstalls returns the cached detection result.
func (d *Daemon) PHPInstalls() []php.Install {
	if p := d.phpInstalls.Load(); p != nil {
		return *p
	}
	return nil
}

// AddPHPVersion makes a PHP version selectable (settings.feature, "Adding a
// PHP version"). The build may be vendored under bin/php/<version> or already
// installed on the machine, in which case its location is recorded in the
// config and the tool uses it where it is.
//
// When nothing can be found the error says where a build belongs; InstallPHP
// downloads one.
func (d *Daemon) AddPHPVersion(ctx context.Context, version string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if version == "" {
		return invalid("no PHP version given")
	}
	version = php.Minor(version)

	cfg := d.store.Get()
	dir, adopted, err := d.locatePHP(ctx, cfg, version)
	if err != nil {
		return err
	}

	_, err = d.store.Update(func(c *config.Config) error {
		registerPHP(c, version, dir, adopted)
		return nil
	})
	if err != nil {
		return err
	}
	d.publish()
	return nil
}

// registerPHP makes a version selectable, recording its folder when it was
// adopted from the machine rather than vendored.
func registerPHP(c *config.Config, version, dir string, adopted bool) {
	if !slices.Contains(c.Services.PHP.Available, version) {
		c.Services.PHP.Available = append(c.Services.PHP.Available, version)
		php.Sort(c.Services.PHP.Available)
	}
	if adopted {
		if c.Services.PHP.Paths == nil {
			c.Services.PHP.Paths = map[string]string{}
		}
		c.Services.PHP.Paths[version] = dir
	} else {
		delete(c.Services.PHP.Paths, version)
	}
}

// RemovePHP takes a PHP version out of the picker. A build Wharf downloaded
// is deleted with its folder (php-runtime.feature, "Removing a downloaded PHP
// version"); one found on the machine belongs to whatever installed it, so
// its folder is only hidden ("Hiding a PHP version found on the machine").
// Both stay inside bin/php and wharf.json, which the user owns, so neither
// asks for a password on any OS.
func (d *Daemon) RemovePHP(ctx context.Context, version string) error {
	if version == "" {
		return invalid("no PHP version given")
	}
	version = php.Minor(version)
	if d.inProgress(d.downloading, version) {
		return conflict("PHP %s is downloading — wait until it has finished", version)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	install, ok := d.phpInstall(version)
	if !ok {
		return notFound("PHP %s is not installed", version)
	}
	if err := phpInUse(d.store.Get(), version); err != nil {
		return err
	}
	vendored := install.Source == "vendored"
	if vendored && filepath.Dir(filepath.Clean(install.Dir)) != filepath.Clean(d.php.VendorDir) {
		return conflict("%s is not in %s — Wharf deletes only the PHP builds it downloaded", install.Dir, d.php.VendorDir)
	}

	defer d.track()()
	// WARNING: A running backend holds its binaries open, and on Windows its folder
	// cannot be deleted until it has exited. Stopping first on every OS keeps
	// the behaviour the same everywhere.
	if err := d.sup.Stop(ctx, wruntime.PHPServiceID(version)); err != nil {
		return err
	}
	if vendored {
		if err := os.RemoveAll(install.Dir); err != nil {
			return fmt.Errorf("delete %s: %w — close any program using it and try again", install.Dir, err)
		}
		d.log.Info("removed PHP", "version", version, "dir", install.Dir)
	} else {
		_, err := d.store.Update(func(c *config.Config) error {
			c.Services.PHP.Hidden = append(c.Services.PHP.Hidden, install.Dir)
			return nil
		})
		if err != nil {
			return err
		}
		d.log.Info("hid PHP", "version", version, "dir", install.Dir)
	}
	return d.settlePHP(ctx, version)
}

// UnhidePHP puts a hidden folder back in front of detection
// (php-runtime.feature, "Showing a hidden PHP version again").
func (d *Daemon) UnhidePHP(ctx context.Context, dir string) error {
	if strings.TrimSpace(dir) == "" {
		return invalid("no folder given")
	}
	dir = filepath.Clean(strings.TrimSpace(dir))

	d.mu.Lock()
	defer d.mu.Unlock()

	if !slices.Contains(d.store.Get().Services.PHP.Hidden, dir) {
		return notFound("%s is not hidden", dir)
	}
	_, err := d.store.Update(func(c *config.Config) error {
		c.Services.PHP.Hidden = slices.DeleteFunc(c.Services.PHP.Hidden, func(h string) bool { return h == dir })
		return nil
	})
	if err != nil {
		return err
	}
	for _, in := range d.RefreshPHP(ctx) {
		// INFO: Another copy of the same version may be the one detection prefers;
		// the folder is no longer hidden either way.
		if filepath.Clean(in.Dir) != dir || !in.Servable() {
			continue
		}
		_, err := d.store.Update(func(c *config.Config) error {
			registerPHP(c, in.Version, in.Dir, in.Source != "vendored")
			return nil
		})
		if err != nil {
			return err
		}
	}
	d.publish()
	return nil
}

// settlePHP brings the config in line once a copy of a version has gone:
// another copy found on the machine takes its place, or the version leaves
// the picker and, if still supported, is offered for download again.
func (d *Daemon) settlePHP(ctx context.Context, version string) error {
	var next php.Install
	for _, in := range d.RefreshPHP(ctx) {
		if in.Version == version && in.Servable() {
			next = in
		}
	}
	_, err := d.store.Update(func(c *config.Config) error {
		if next.Dir != "" {
			registerPHP(c, version, next.Dir, next.Source != "vendored")
			return nil
		}
		c.Services.PHP.Available = slices.DeleteFunc(c.Services.PHP.Available, func(v string) bool { return v == version })
		delete(c.Services.PHP.Paths, version)
		return nil
	})
	if err != nil {
		return err
	}
	d.publish()
	return nil
}

// phpInUse refuses to take away a version something is served by, naming
// what to change first (php-runtime.feature, "A PHP version in use is neither
// removed nor hidden").
func phpInUse(cfg *config.Config, version string) error {
	if cfg.Services.PHP.Version == version {
		return conflict("PHP %s is the global default — select another version first", version)
	}
	var users []string
	for _, p := range cfg.Projects {
		if p.PHPVersion != nil && *p.PHPVersion == version {
			users = append(users, p.Name)
		}
	}
	switch len(users) {
	case 0:
		return nil
	case 1:
		return conflict("PHP %s is used by %s — choose another PHP version for it first", version, users[0])
	}
	return conflict("PHP %s is used by %s — choose another PHP version for them first", version, strings.Join(users, ", "))
}

// phpInstall is the install the picker shows for a version.
func (d *Daemon) phpInstall(version string) (php.Install, bool) {
	for _, in := range d.PHPInstalls() {
		if in.Version == version {
			return in, true
		}
	}
	return php.Install{}, false
}

// phpHidden reports whether the user hid a PHP folder found on the machine.
func (d *Daemon) phpHidden(dir string) bool {
	return slices.Contains(d.store.Get().Services.PHP.Hidden, filepath.Clean(dir))
}

// SetPHPVersion changes the global default version, adopting it first if it is
// not registered yet — the version picker's one action.
func (d *Daemon) SetPHPVersion(ctx context.Context, version string) error {
	if version == "" {
		return invalid("no PHP version given")
	}
	version = php.Minor(version)
	if !slices.Contains(d.store.Get().Services.PHP.Available, version) {
		if err := d.AddPHPVersion(ctx, version); err != nil {
			return err
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	defer d.track()()

	cfg, err := d.store.Update(func(c *config.Config) error {
		c.Services.PHP.Version = version
		return nil
	})
	if err != nil {
		return err
	}

	// INFO: Projects on the old default must move to the new backend; those with
	// their own override are untouched.
	if d.sup.Running(runtimeWebserverID) {
		if err := d.ensurePHP(ctx, cfg, version); err != nil {
			return err
		}
		if err := d.reloadGlobalWebserver(ctx, cfg); err != nil {
			return err
		}
	}
	d.publish()
	return nil
}

// locatePHP finds a version's binaries, reporting whether it was adopted from
// the machine rather than vendored.
func (d *Daemon) locatePHP(ctx context.Context, cfg *config.Config, version string) (dir string, adopted bool, err error) {
	vendored := d.root.PHPBin(version)
	if _, statErr := os.Stat(filepath.Join(vendored, php.FastCGIName())); statErr == nil {
		return vendored, false, nil
	}
	for _, in := range d.RefreshPHP(ctx) {
		if in.Version == version && in.Servable() {
			return in.Dir, in.Source != "vendored", nil
		}
	}
	return "", false, &wruntime.MissingBinaryError{
		Service: "PHP " + version,
		Path:    filepath.Join(vendored, php.FastCGIName()),
	}
}

// InstallPHP downloads the newest build of a PHP version into
// bin/php/<version> and makes it selectable (php-runtime.feature,
// "Downloading a PHP version that is not installed").
//
// The download is staged beside its destination and renamed into place only
// once complete, so a failure leaves no partial folder behind ("A PHP
// download fails"). It runs outside mu: a download takes a while, and the
// rest of the daemon — starting projects, the tray — must not wait for it.
func (d *Daemon) InstallPHP(ctx context.Context, version string) error {
	version = php.Minor(version)
	if _, known := php.Known(version); !known {
		return invalid("PHP %q is not a version Wharf knows how to download", version)
	}
	dest := d.root.PHPBin(version)
	if _, err := os.Stat(dest); err == nil {
		return conflict("%s already exists — re-scan to use it, or remove it to download again", dest)
	}
	if !d.begin(d.downloading, version) {
		return conflict("PHP %s is already downloading", version)
	}
	defer d.end(d.downloading, version)

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(dest), ".download-"+version+"-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	full, err := d.phpInstaller.Install(ctx, version, staging)
	if err != nil {
		return err
	}
	if err := os.Rename(staging, dest); err != nil {
		return fmt.Errorf("move PHP %s into place: %w", version, err)
	}
	d.log.Info("downloaded PHP", "version", full, "dir", dest)
	// INFO: The picker lists what detection found, so the new build must be found
	// before it can be shown.
	d.RefreshPHP(ctx)
	return d.AddPHPVersion(ctx, version)
}

type phpReleases struct {
	at     time.Time
	latest map[string]string
}

// CheckPHPReleases looks up the newest release of each PHP version the
// download source publishes, so the offers to download can name it
// (php-runtime.feature, "A download offer names the release it downloads").
// It fetches an index, never a build, and not more than every ten minutes.
// Without a connection it fails and the offers keep the minor version alone.
func (d *Daemon) CheckPHPReleases(ctx context.Context) error {
	lister, ok := d.phpInstaller.(php.Lister)
	if !ok {
		return nil
	}
	if last := d.phpLatest.Load(); last != nil && time.Since(last.at) < 10*time.Minute {
		return nil
	}
	// WARNING: Bounded tightly: the offers name no release while a lookup
	// hangs on a bad connection, and one started later waits for it.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	latest, err := lister.Latest(ctx)
	if err != nil {
		return err
	}
	d.phpLatest.Store(&phpReleases{at: time.Now(), latest: latest})
	d.publish()
	return nil
}

func (d *Daemon) latestPHP() map[string]string {
	if p := d.phpLatest.Load(); p != nil {
		return p.latest
	}
	return nil
}

func (d *Daemon) downloadingVersions() []string {
	d.dlMu.Lock()
	defer d.dlMu.Unlock()
	out := []string{}
	for v := range d.downloading {
		out = append(out, v)
	}
	php.Sort(out)
	return out
}

// ensurePHP starts the FastCGI backend for a version if it is not running.
func (d *Daemon) ensurePHP(ctx context.Context, cfg *config.Config, version string) error {
	spec, err := d.res.PHPSpec(cfg, version)
	if err != nil {
		return err
	}
	return d.sup.Start(ctx, spec)
}

func (d *Daemon) phpStatuses(cfg *config.Config) []supervisor.Status {
	var out []supervisor.Status
	for _, v := range cfg.Services.PHP.Available {
		if s, ok := d.sup.Status(wruntime.PHPServiceID(v)); ok {
			out = append(out, s)
		}
	}
	if out == nil {
		out = []supervisor.Status{}
	}
	return out
}
