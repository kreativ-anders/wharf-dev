package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manuel-steinberg/wharf/daemon/internal/certs"
	"github.com/manuel-steinberg/wharf/daemon/internal/config"
	"github.com/manuel-steinberg/wharf/daemon/internal/elevate"
	"github.com/manuel-steinberg/wharf/daemon/internal/hostsfile"
	"github.com/manuel-steinberg/wharf/daemon/internal/layout"
	"github.com/manuel-steinberg/wharf/daemon/internal/php"
	"github.com/manuel-steinberg/wharf/daemon/internal/project"
	wruntime "github.com/manuel-steinberg/wharf/daemon/internal/runtime"
	"github.com/manuel-steinberg/wharf/daemon/internal/supervisor"
	"github.com/manuel-steinberg/wharf/daemon/internal/webserver"
)

const runtimeWebserverID = wruntime.WebserverID

func projectServiceID(name string) string { return wruntime.ProjectServiceID(name) }

// Daemon is the whole application logic. One instance per process.
type Daemon struct {
	root  layout.Root
	store *config.Store
	sup   *supervisor.Supervisor
	hosts *hostsfile.Manager
	res   *wruntime.Resolver
	certs certs.Issuer
	scaf  *project.Scaffolder
	php   *php.Detector
	log   *slog.Logger

	// mu serialises state-changing operations. Every one of them touches the
	// config file and the process table together; interleaving two would let
	// a webserver switch race a project start.
	mu sync.Mutex

	busy atomic.Int64

	now func() time.Time

	// phpInstalls caches what the last detection found. Probing binaries is
	// too slow to redo on every state snapshot, and the answer only changes
	// when the user installs something.
	phpInstalls atomic.Pointer[[]php.Install]

	phpInstaller php.Installer
	// downloading holds the PHP versions being downloaded right now, and
	// installing the webservers being installed. They have their own lock
	// because an install runs for a while outside mu, so the rest of the
	// daemon stays responsive meanwhile.
	dlMu        sync.Mutex
	downloading map[string]bool
	installing  map[string]bool

	web          *webserver.Detector
	webInstaller webserver.Installer
	// webInstalls caches webserver detection, like phpInstalls.
	webInstalls atomic.Pointer[map[string]webserver.Install]

	// customSeen is the modification time of each custom config file the
	// last time it was applied, keyed by path. Guarded by mu.
	customSeen map[string]time.Time

	onState atomic.Pointer[func(State)]
}

// Options configures a Daemon. The zero value of each collaborator is
// replaced by the real system implementation; tests substitute fakes.
type Options struct {
	Root       layout.Root
	Store      *config.Store
	Supervisor *supervisor.Supervisor
	Elevator   elevate.Elevator
	HostsPath  string
	Certs      certs.Issuer
	Fetcher    project.Fetcher
	// Detector finds PHP installations. The default one probes the machine;
	// tests supply one that only sees what they staged.
	Detector *php.Detector
	// PHPInstaller downloads PHP builds. The default one fetches from the
	// public build servers; tests supply one that writes stubs.
	PHPInstaller php.Installer
	// WebDetector and WebInstaller find and install webservers; tests
	// supply ones that see only what they staged.
	WebDetector  *webserver.Detector
	WebInstaller webserver.Installer
	Log          *slog.Logger
	// Now fixes the clock used to judge PHP support status.
	Now func() time.Time
}

// New builds a Daemon, filling in system implementations where none is given.
func New(opts Options) (*Daemon, error) {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if err := opts.Root.Ensure(); err != nil {
		return nil, err
	}
	store := opts.Store
	if store == nil {
		s, err := config.Load(opts.Root.ConfigFile())
		if err != nil {
			return nil, err
		}
		store = s
	}
	sup := opts.Supervisor
	if sup == nil {
		sup = supervisor.NewSystem()
	}
	el := opts.Elevator
	if el == nil {
		el = elevate.System()
	}
	hosts := &hostsfile.Manager{Path: opts.HostsPath, Elevator: el}
	if hosts.Path == "" {
		hosts.Path = hostsfile.SystemPath()
	}
	issuer := opts.Certs
	if issuer == nil {
		issuer = certs.New(opts.Root, el)
	}
	scaf := project.NewScaffolder(opts.Root)
	if opts.Fetcher != nil {
		scaf.Fetcher = opts.Fetcher
	}
	detector := opts.Detector
	if detector == nil {
		detector = php.NewDetector(filepath.Join(opts.Root.Bin(), "php"))
	}
	if detector.VendorDir == "" {
		detector.VendorDir = filepath.Join(opts.Root.Bin(), "php")
	}

	installer := opts.PHPInstaller
	if installer == nil {
		installer = php.NewDownloader()
	}
	webDetector := opts.WebDetector
	if webDetector == nil {
		webDetector = &webserver.Detector{}
	}
	if webDetector.BinDir == "" {
		webDetector.BinDir = opts.Root.Bin()
	}
	webInstaller := opts.WebInstaller
	if webInstaller == nil {
		webInstaller = webserver.NewDownloader()
	}

	d := &Daemon{
		root:         opts.Root,
		store:        store,
		sup:          sup,
		hosts:        hosts,
		res:          wruntime.New(opts.Root),
		certs:        issuer,
		scaf:         scaf,
		php:          detector,
		phpInstaller: installer,
		downloading:  map[string]bool{},
		installing:   map[string]bool{},
		web:          webDetector,
		webInstaller: webInstaller,
		now:          opts.Now,
		log:          opts.Log,
	}
	d.res.Installs = d.WebInstalls
	d.RefreshWebservers(context.Background())
	// Custom configs present at start are applied by the first webserver
	// start; only later edits should trigger a restart.
	d.customSeen = d.customConfigTimes(store.Get())
	if d.now == nil {
		d.now = time.Now
	}
	if detector.Now == nil {
		detector.Now = d.now
	}

	// Any process state change re-publishes the snapshot, so the tray menu and
	// the window never disagree about what is running.
	sup.OnChange(func(supervisor.Status) { d.publish() })

	// A fresh config has no idea what is installed; first run finds out, so
	// the tool is usable without a download step.
	if store.Created {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := d.FirstRunSetup(ctx); err != nil {
			opts.Log.Warn("first-run PHP detection failed", "err", err)
		}
	} else {
		d.RefreshPHP(context.Background())
	}
	return d, nil
}

// FirstRunSetup adopts the PHP versions already installed on the machine and
// picks the default: the newest one still in active support, falling back to
// the newest with security support. Nothing installed leaves the recommended
// version selected, which the GUI shows as missing along with where it goes.
func (d *Daemon) FirstRunSetup(ctx context.Context) error {
	installs := d.RefreshPHP(ctx)

	var usable []string
	paths := map[string]string{}
	for _, in := range installs {
		if !in.Servable() {
			continue
		}
		usable = append(usable, in.Version)
		if in.Source != "vendored" {
			paths[in.Version] = in.Dir
		}
	}

	chosen := php.Preferred(usable, d.now())
	if chosen == "" {
		chosen = php.Recommended(d.now())
	}

	_, err := d.store.Update(func(c *config.Config) error {
		// The default webserver is one that is already here, if any is: a
		// Mac ships Apache, and a first project should start without an
		// install step (webserver-install.feature, "First start adopts a
		// webserver already on the machine").
		found := d.WebInstalls()
		if _, ok := found[c.Services.Webserver.Active]; !ok {
			for _, name := range c.Services.Webserver.Available {
				if _, ok := found[name]; ok {
					c.Services.Webserver.Active = name
					break
				}
			}
		}
		c.Services.PHP.Version = chosen
		c.Services.PHP.Available = usable
		if len(paths) > 0 {
			c.Services.PHP.Paths = paths
		}
		return nil
	})
	if err != nil {
		return err
	}
	d.log.Info("first run", "php", chosen, "installed", usable)
	d.publish()
	return nil
}

// RefreshPHP re-scans the machine for PHP installations and caches the result
// for the version picker.
func (d *Daemon) RefreshPHP(ctx context.Context) []php.Install {
	installs := d.php.Detect(ctx)
	d.phpInstalls.Store(&installs)
	d.publish()
	return installs
}

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
	if !containsStr(d.store.Get().Services.Webserver.Available, name) {
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
	// A package manager installs elsewhere and leaves staging empty.
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

// SetAppearance chooses the light or dark theme, or following the system
// (settings.feature, "Choosing light or dark appearance"). It lives in
// wharf.json like every other setting, so the window never keeps state of
// its own.
func (d *Daemon) SetAppearance(mode string) error {
	if !containsStr(config.Appearances, mode) {
		return invalid("appearance must be system, light or dark, not %q", mode)
	}
	_, err := d.store.Update(func(c *config.Config) error {
		c.Appearance = mode
		return nil
	})
	if err != nil {
		return err
	}
	d.publish()
	return nil
}

// PHPInstalls returns the cached detection result.
func (d *Daemon) PHPInstalls() []php.Install {
	if p := d.phpInstalls.Load(); p != nil {
		return *p
	}
	return nil
}

// OnState registers the callback that receives every new snapshot.
func (d *Daemon) OnState(fn func(State)) { d.onState.Store(&fn) }

// State returns the current snapshot.
func (d *Daemon) State() State { return d.snapshot(d.store.Get()) }

// Config exposes the current config, for callers that need the raw shape.
func (d *Daemon) Config() *config.Config { return d.store.Get() }

// Root returns the resolved root folder.
func (d *Daemon) Root() layout.Root { return d.root }

func (d *Daemon) publish() {
	if fn := d.onState.Load(); fn != nil {
		(*fn)(d.State())
	}
}

// track marks the daemon busy for the duration of a long action, so the GUI
// can show a spinner without each action inventing its own flag.
func (d *Daemon) track() func() {
	d.busy.Add(1)
	d.publish()
	return func() {
		d.busy.Add(-1)
		d.publish()
	}
}

// ---------------------------------------------------------------- services

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
	if !containsStr(cfg.Services.Webserver.Available, name) {
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

	// Only swap the process if one is up; activating a webserver while
	// everything is stopped is a config change, not a start.
	if !d.sup.Running(runtimeWebserverID) && !d.sup.AnyRunning() {
		d.publish()
		return nil
	}
	spec, err := d.res.WebserverSpec(next)
	if err != nil {
		return err
	}
	// Restart stops the old process, waits for port 80 to be released, and
	// only then starts the replacement.
	return d.sup.Restart(ctx, spec)
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
		if !containsStr(c.Services.PHP.Available, version) {
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
		return nil
	})
	if err != nil {
		return err
	}
	d.publish()
	return nil
}

// SetPHPVersion changes the global default version, adopting it first if it is
// not registered yet — the version picker's one action.
func (d *Daemon) SetPHPVersion(ctx context.Context, version string) error {
	if version == "" {
		return invalid("no PHP version given")
	}
	version = php.Minor(version)
	if !containsStr(d.store.Get().Services.PHP.Available, version) {
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

	// Projects on the old default must move to the new backend; those with
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
	// The picker lists what detection found, so the new build must be found
	// before it can be shown.
	d.RefreshPHP(ctx)
	return d.AddPHPVersion(ctx, version)
}

// begin marks key as in progress in set, reporting false if it already was.
func (d *Daemon) begin(set map[string]bool, key string) bool {
	d.dlMu.Lock()
	if set[key] {
		d.dlMu.Unlock()
		return false
	}
	set[key] = true
	d.dlMu.Unlock()
	d.publish()
	return true
}

func (d *Daemon) end(set map[string]bool, key string) {
	d.dlMu.Lock()
	delete(set, key)
	d.dlMu.Unlock()
	d.publish()
}

func (d *Daemon) inProgress(set map[string]bool, key string) bool {
	d.dlMu.Lock()
	defer d.dlMu.Unlock()
	return set[key]
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

// SetupSSL installs mkcert and trusts its local certificate authority —
// the "Trust" action in Settings, and what the SSL switch does on first use
// (local-ssl.feature). A declined prompt is returned as elevate.ErrDeclined.
func (d *Daemon) SetupSSL(ctx context.Context) error {
	defer d.track()()
	return d.certs.Setup(ctx)
}

// StopAll stops every running service and project (tray-actions.feature).
func (d *Daemon) StopAll(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	defer d.track()()
	return d.sup.StopAll(ctx)
}

// ---------------------------------------------------------------- projects

// AddProject registers an existing folder under www/ as a project and asks for
// its hosts entry. A declined elevation prompt is not a failure: the project
// is kept and its raw-port URL is shown instead
// (pretty-urls.feature, "Elevation is declined").
func (d *Daemon) AddProject(ctx context.Context, name string) (Project, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.addProjectLocked(ctx, name, "")
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
		return d.addProjectLocked(ctx, filepath.Base(abs), "")
	}

	name := project.Slug(filepath.Base(abs))
	if project.Exists(d.root, name) {
		return Project{}, conflict("a folder named %q is already in www/ — rename one of the two folders", name)
	}
	cfg := d.store.Get()
	for _, p := range cfg.Projects {
		if p.Path != "" && sameDir(p.Path, abs) {
			return Project{}, conflict("%s is already the project %q", abs, p.Name)
		}
	}
	return d.addProjectLocked(ctx, name, abs)
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
// www/<name>.
func (d *Daemon) addProjectLocked(ctx context.Context, name, path string) (Project, error) {
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

	port := wruntime.NextProjectPort(cfg)
	if port == 0 {
		return Project{}, conflict("no free project port available")
	}

	entry := config.Project{Name: name, Port: port, Path: path}

	// The hosts write is attempted before the project is persisted only in
	// the sense of ordering the prompt first; the project is stored either
	// way, so a declined prompt still leaves a usable project.
	hostsErr := d.hosts.Add(name)
	switch {
	case hostsErr == nil:
		entry.HostsEntry = true
	case errors.Is(hostsErr, elevate.ErrDeclined):
		d.log.Info("elevation declined; project keeps its raw-port URL", "project", name)
	default:
		return Project{}, hostsErr
	}

	next, err := d.store.Update(func(c *config.Config) error {
		c.SetProject(entry)
		return nil
	})
	if err != nil {
		return Project{}, err
	}

	// A newly registered project must appear in the running webserver's
	// config, not only after the next restart.
	if err := d.reloadGlobalWebserver(ctx, next); err != nil {
		d.log.Error("reload webserver after adding project", "project", name, "err", err)
	}
	d.publish()
	return d.projectState(next, entry), nil
}

// RemoveProject unregisters a project: its services are stopped and its hosts
// entry deleted, leaving other projects' entries untouched. The folder itself
// is left on disk — the folder is the project, and deleting a user's files is
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

	if err := d.sup.Stop(ctx, projectServiceID(name)); err != nil {
		d.log.Error("stop project before removal", "project", name, "err", err)
	}
	if p.HostsEntry {
		if err := d.hosts.Remove(name); err != nil && !errors.Is(err, elevate.ErrDeclined) {
			return err
		}
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
	if err := d.reloadGlobalWebserver(ctx, next); err != nil {
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

func (d *Daemon) startProjectLocked(ctx context.Context, cfg *config.Config, p config.Project) error {
	if err := d.ensurePHP(ctx, cfg, cfg.PHPVersionFor(p)); err != nil {
		return err
	}
	if p.WebserverOverride != nil {
		spec, err := d.res.ProjectSpec(cfg, p)
		if err != nil {
			return err
		}
		return d.sup.Start(ctx, spec)
	}
	spec, err := d.res.WebserverSpec(cfg)
	if err != nil {
		return err
	}
	if d.sup.Running(runtimeWebserverID) {
		// The shared instance is already serving; it only needs the project's
		// vhost, which WebserverSpec has just written.
		return d.sup.Restart(ctx, spec)
	}
	return d.sup.Start(ctx, spec)
}

// StopProject stops the process serving a project. For a project on the shared
// webserver this stops that webserver, which is honest: there is one process
// serving all of them, and the GUI shows the others stopping too.
func (d *Daemon) StopProject(ctx context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	defer d.track()()

	cfg := d.store.Get()
	p, ok := cfg.Project(name)
	if !ok {
		return notFound("no project named %q", name)
	}
	if p.WebserverOverride != nil {
		return d.sup.Stop(ctx, projectServiceID(name))
	}
	return d.sup.Stop(ctx, runtimeWebserverID)
}

// Settings are the per-project overrides. A nil field leaves that setting
// unchanged; a pointer to the empty string clears the override and reverts to
// the global default (app-configuration.feature).
type Settings struct {
	Webserver *string `json:"webserver_override"`
	PHP       *string `json:"php_version"`
	SSL       *bool   `json:"ssl"`
}

// UpdateSettings applies per-project overrides and restarts only what the
// change affects, leaving every other project alone.
func (d *Daemon) UpdateSettings(ctx context.Context, name string, s Settings) (Project, error) {
	defer d.track()()

	// Turning SSL on sets SSL up first — mkcert downloaded, its authority
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
			if !containsStr(cfg.Services.Webserver.Available, *s.Webserver) {
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
			if !containsStr(cfg.Services.PHP.Available, *s.PHP) {
				return Project{}, invalid("PHP %s is not installed", *s.PHP)
			}
			v := *s.PHP
			after.PHPVersion = &v
		}
	}
	if s.SSL != nil {
		after.SSL = *s.SSL
	}

	// Certificates are issued before the config is written, so a failure to
	// issue leaves the project exactly as it was.
	if after.SSL && !before.SSL {
		host := hostsfile.Hostname(name)
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
// invalidated, and nothing else: a project moving onto its own webserver
// instance leaves the shared one serving everybody else, and a change made
// while everything is stopped starts nothing.
func (d *Daemon) applySettingsChange(ctx context.Context, cfg *config.Config, before, after config.Project) error {
	hadOwn := before.WebserverOverride != nil
	hasOwn := after.WebserverOverride != nil

	// Was this project actually being served a moment ago? Only then does a
	// settings change need to restart anything.
	wasServed := d.sup.Running(projectServiceID(after.Name))
	if !hadOwn {
		wasServed = d.sup.Running(runtimeWebserverID)
	}

	if hadOwn && !hasOwn {
		// The project moves back onto the shared instance.
		if err := d.sup.Stop(ctx, projectServiceID(after.Name)); err != nil {
			return err
		}
	}

	if hasOwn {
		if !hadOwn {
			// Drop its vhost from the shared instance first, so two servers
			// never claim the same hostname.
			if err := d.reloadGlobalWebserver(ctx, cfg); err != nil {
				return err
			}
		}
		if !wasServed {
			return nil
		}
		if err := d.ensurePHP(ctx, cfg, cfg.PHPVersionFor(after)); err != nil {
			return err
		}
		spec, err := d.res.ProjectSpec(cfg, after)
		if err != nil {
			return err
		}
		return d.sup.Restart(ctx, spec)
	}

	if wasServed {
		if err := d.ensurePHP(ctx, cfg, cfg.PHPVersionFor(after)); err != nil {
			return err
		}
	}
	return d.reloadGlobalWebserver(ctx, cfg)
}

// CustomConfig returns the path of a project's custom directives for one
// webserver, creating the file with a commented starting point if it does not
// exist yet (app-configuration.feature, "Custom webserver directives for one
// project"). The file is the user's from then on: Wharf only ever includes it.
func (d *Daemon) CustomConfig(ctx context.Context, name, server string) (string, error) {
	cfg := d.store.Get()
	if _, ok := cfg.Project(name); !ok {
		return "", notFound("no project named %q", name)
	}
	if !containsStr(cfg.Services.Webserver.Available, server) {
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
	// Apply at once rather than on the watcher's next tick, so the project
	// is already using the file by the time the editor opens it.
	d.ApplyCustomConfigs(ctx)
	return path, nil
}

func customConfigStub(name, server string) string {
	other, block, example := "apache", "server { } block", `#   client_max_body_size 64m;
#   location /api/ { proxy_pass http://127.0.0.1:3000; }
#
# Wharf already sets root, index, "location /" and the PHP location;
# repeating those here makes nginx refuse to start.`
	if server == "apache" {
		other, block, example = "nginx", "<VirtualHost> section", `#   php_value upload_max_filesize 64M
#   Header set X-Robots-Tag "noindex"
#
# .htaccess files in the project work as well: AllowOverride is All.`
	}
	return fmt.Sprintf(`# Custom %[1]s directives for %[2]s.
#
# Wharf includes this file inside the project's %[3]s, after its own
# directives. It is used only while %[2]s is served by %[1]s; the %[4]s file
# beside it is used when the project is served by %[4]s.
#
# Saving the file restarts the webserver serving %[2]s. A mistake here keeps
# that webserver from starting — its error appears in Wharf.
#
# Examples:
%[5]s
`, server, name, block, other, example)
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
		if !changed[d.root.CustomConfig(p.Name, server)] {
			continue
		}
		if p.WebserverOverride == nil {
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

// Scaffold creates a project from a quick-app template and registers it
// (quick-app-php.feature).
func (d *Daemon) Scaffold(ctx context.Context, templateID, name string) (Project, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	defer d.track()()

	tpl, ok := project.TemplateByID(templateID)
	if !ok {
		return Project{}, notFound("no template named %q", templateID)
	}
	if err := d.scaf.Create(ctx, tpl, name); err != nil {
		return Project{}, err
	}
	p, err := d.addProjectLocked(ctx, name, "")
	if err != nil {
		// The folder exists but could not be registered; leaving it behind
		// with no config entry would be a project the GUI cannot see.
		os.RemoveAll(d.root.ProjectDir(name))
		return Project{}, err
	}

	cfg := d.store.Get()
	entry, _ := cfg.Project(name)
	// Scaffolding implies wanting the project up: the GUI shows "starting"
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

// ---------------------------------------------------------------- internals

// ensurePHP starts the FastCGI backend for a version if it is not running.
func (d *Daemon) ensurePHP(ctx context.Context, cfg *config.Config, version string) error {
	spec, err := d.res.PHPSpec(cfg, version)
	if err != nil {
		return err
	}
	return d.sup.Start(ctx, spec)
}

// reloadGlobalWebserver regenerates the shared webserver's config and restarts
// it if it is running. It is a no-op when nothing is up, so adding a project
// while everything is stopped does not start a server.
func (d *Daemon) reloadGlobalWebserver(ctx context.Context, cfg *config.Config) error {
	if !d.sup.Running(runtimeWebserverID) {
		return nil
	}
	spec, err := d.res.WebserverSpec(cfg)
	if err != nil {
		return err
	}
	return d.sup.Restart(ctx, spec)
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

// Shutdown stops every managed process. Leaving orphaned webservers bound to
// port 80 after the GUI quits is the one failure mode a tray app must not have.
func (d *Daemon) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sup.StopAll(ctx)
}

func containsStr(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
