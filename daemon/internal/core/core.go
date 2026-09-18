package core

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/certs"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/elevate"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/layout"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/php"
	wruntime "github.com/kreativ-anders/wharf-dev/daemon/internal/runtime"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/shellpath"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/supervisor"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/webserver"
)

const runtimeWebserverID = wruntime.WebserverID

func projectServiceID(name string) string { return wruntime.ProjectServiceID(name) }

// Daemon is the whole application logic. One instance per process.
type Daemon struct {
	root  layout.Root
	store *config.Store
	sup   *supervisor.Supervisor
	elev  elevate.Elevator
	res   *wruntime.Resolver
	certs certs.Issuer
	php   *php.Detector
	log   *slog.Logger

	// version is the build's own, for Settings → General.
	version string

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
	// phpLatest is the newest release of each PHP version the download
	// source publishes, as last looked up; nil until then.
	phpLatest atomic.Pointer[phpReleases]
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
	// phpIniSeen is config/php.ini's modification time when it was last
	// applied; zero while the file does not exist. Guarded by mu.
	phpIniSeen time.Time

	// started holds the projects the user has started. The front door serves
	// those and no others, so starting one project does not start them all
	// (tray-actions.feature). It has its own lock because the snapshot reads
	// it while an action holds mu.
	startedMu sync.Mutex
	started   map[string]bool
	// failed holds why a project's last start failed. Such a project is taken
	// out of started, so the next project's start does not bring it up again,
	// but its row still shows the reason.
	failed map[string]string

	// shell puts bin/path on the user's PATH (php-terminal.feature), and
	// terminalPlaces is where it last did.
	shell          shellpath.Registrar
	noTerminal     bool
	terminalPlaces atomic.Pointer[[]string]
	// linkTarget is the binary bin/path's php runs, as last linked; linkSynced
	// is false until the link was first brought in line. Both guarded by linkMu.
	linkMu     sync.Mutex
	linkTarget string
	linkSynced bool

	onState atomic.Pointer[func(State)]
}

// Options configures a Daemon. The zero value of each collaborator is
// replaced by the real system implementation; tests substitute fakes.
type Options struct {
	Root       layout.Root
	Store      *config.Store
	Supervisor *supervisor.Supervisor
	Elevator   elevate.Elevator
	Certs      certs.Issuer
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
	// ShellPath puts bin/path on the user's PATH. The default one writes the
	// user's shell startup files, or the user environment on Windows; tests
	// supply one that remembers instead.
	ShellPath shellpath.Registrar
	// NoTerminal leaves "Use in terminal" off at a first start. A Wharf folder
	// made only for a test run sets it: switched on, it would take the
	// terminal PATH from the user's real Wharf folder (php-terminal.feature,
	// "Use in terminal is on from the first start").
	NoTerminal bool
	Log        *slog.Logger
	// Now fixes the clock used to judge PHP support status.
	Now func() time.Time
	// Version is what wharfd was built as, published in the snapshot so the
	// window and the tray name the same one (settings.feature, "General
	// shows the version, and no update check yet").
	Version string
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
	issuer := opts.Certs
	if issuer == nil {
		issuer = certs.New(opts.Root, el)
	}
	detector := opts.Detector
	if detector == nil {
		detector = php.NewDetector(filepath.Join(opts.Root.Bin(), "php"))
	}
	if detector.VendorDir == "" {
		detector.VendorDir = filepath.Join(opts.Root.Bin(), "php")
	}
	if detector.PathDir == "" {
		detector.PathDir = opts.Root.PathBin()
	}
	shell := opts.ShellPath
	if shell == nil {
		shell = shellpath.System()
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
		dl := webserver.NewDownloader()
		dl.Elevator = el
		webInstaller = dl
	}

	// WARNING: Set before anything below can publish a snapshot, which reads
	// the clock for PHP support statuses.
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	if detector.Now == nil {
		detector.Now = now
	}

	d := &Daemon{
		root:         opts.Root,
		store:        store,
		sup:          sup,
		elev:         el,
		res:          wruntime.New(opts.Root),
		certs:        issuer,
		php:          detector,
		phpInstaller: installer,
		downloading:  map[string]bool{},
		installing:   map[string]bool{},
		started:      map[string]bool{},
		failed:       map[string]string{},
		web:          webDetector,
		webInstaller: webInstaller,
		now:          now,
		log:          opts.Log,
		version:      opts.Version,
		shell:        shell,
		noTerminal:   opts.NoTerminal,
	}
	d.setTerminalPlaces(nil)
	d.res.Installs = d.WebInstalls
	detector.Hidden = d.phpHidden
	d.RefreshWebservers(context.Background())
	// INFO: Custom configs present at start are applied by the first webserver
	// start; only later edits should trigger a restart.
	d.customSeen = d.customConfigTimes(store.Get())
	d.phpIniSeen = modTime(opts.Root.PHPIni())

	// INFO: Any process state change re-publishes the snapshot, so the tray menu and
	// the window never disagree about what is running.
	sup.OnChange(func(supervisor.Status) { d.publish() })

	// INFO: A fresh config has no idea what is installed; first run finds out, so
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
	d.applyTerminal(store.Get().Services.PHP.Terminal, false)
	d.publish()
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
		// INFO: The default webserver is one that is already here, if any is: a
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
		// INFO: On from the first start, so "php" in a new terminal is the PHP
		// projects are served with (php-terminal.feature). New and Reset put
		// bin/path on PATH once this is written.
		c.Services.PHP.Terminal = !d.noTerminal
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

// SetAppearance chooses the light or dark theme, or following the system
// (settings.feature, "Choosing light or dark appearance"). It lives in
// wharf.json like every other setting, so the window never keeps state of
// its own.
func (d *Daemon) SetAppearance(mode string) error {
	if !slices.Contains(config.Appearances, mode) {
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

// OnState registers the callback that receives every new snapshot.
func (d *Daemon) OnState(fn func(State)) { d.onState.Store(&fn) }

// State returns the current snapshot.
func (d *Daemon) State() State { return d.snapshot(d.store.Get()) }

// Config exposes the current config, for callers that need the raw shape.
func (d *Daemon) Config() *config.Config { return d.store.Get() }

// Root returns the resolved root folder.
func (d *Daemon) Root() layout.Root { return d.root }

func (d *Daemon) publish() {
	d.syncPathLink(d.store.Get())
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
	d.clearStarted()
	return d.sup.StopAll(ctx)
}

// Reset returns Wharf to a first start (settings.feature, "Resetting Wharf"):
// everything stopped, every project unregistered, everything in config/
// deleted along with project certificates, generated files and service logs,
// and wharf.json written again as a first start writes it. The folders in
// www/ are deleted only when deleteProjects is set; a folder added from
// elsewhere never is. Downloaded PHP versions and webservers stay: they are
// tools rather than projects, and fetching them again takes minutes. Nothing
// here needs administrator rights, so a reset never prompts.
func (d *Daemon) Reset(ctx context.Context, deleteProjects bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	defer d.track()()

	if err := d.sup.StopAll(ctx); err != nil {
		return err
	}
	d.clearStarted()
	cfg := d.store.Get()

	// INFO: wharf.json goes back to a first start. Where that leaves "Use in
	// terminal" off, the PATH goes back too; bin/path stays, like bin/php.
	if cfg.Services.PHP.Terminal && d.noTerminal {
		d.applyTerminal(false, true)
	}

	// INFO: The folder is the project, and it holds the user's own work: it is
	// deleted only when they ticked the box that says so (settings.feature,
	// "Resetting Wharf and deleting the projects in www/").
	if deleteProjects {
		if err := d.deleteWWW(); err != nil {
			return err
		}
	}
	// WARNING: Only the projects' own certificates: the certificate authority is in
	// the system trust store, and a new one would need another prompt.
	for _, p := range cfg.Projects {
		os.Remove(wruntime.CertPath(d.root, p.Name))
		os.Remove(wruntime.KeyPath(d.root, p.Name))
	}
	// INFO: config/ goes whole — wharf.json, custom webserver configs, php.ini and
	// anything else put there — so nothing configured survives. data/gen is
	// generated again on the next start.
	for _, dir := range []string{d.root.Config(), filepath.Join(d.root.Data(), "gen")} {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	if err := d.clearLogs(); err != nil {
		return err
	}
	if err := d.root.Ensure(); err != nil {
		return err
	}
	if _, err := d.store.Update(func(c *config.Config) error {
		*c = *config.Default()
		return nil
	}); err != nil {
		return err
	}
	d.customSeen = map[string]time.Time{}
	d.phpIniSeen = time.Time{}
	d.log.Info("reset to a first start")
	// INFO: A first start adopts what is installed and picks the defaults again,
	// "Use in terminal" among them (php-terminal.feature, "Resetting Wharf
	// leaves PHP in the terminal, as a first start does").
	if err := d.FirstRunSetup(ctx); err != nil {
		return err
	}
	d.applyTerminal(d.store.Get().Services.PHP.Terminal, false)
	d.publish()
	return nil
}

// deleteWWW deletes every folder in www/, with everything in it.
func (d *Daemon) deleteWWW() error {
	entries, err := os.ReadDir(d.root.WWW())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(d.root.WWW(), e.Name())); err != nil {
			return fmt.Errorf("delete www/%s: %w", e.Name(), err)
		}
	}
	return nil
}

// clearLogs deletes every service and project log but the daemon's own: it
// is writing to that one, and Windows refuses to delete an open file.
func (d *Daemon) clearLogs() error {
	entries, err := os.ReadDir(d.root.LogDir())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(d.root.LogDir(), e.Name())
		if path == d.root.DaemonLog() {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("delete log %s: %w", e.Name(), err)
		}
	}
	return nil
}

// Shutdown stops every managed process. Leaving orphaned webservers bound to
// port 80 after the GUI quits is the one failure mode a tray app must not have.
func (d *Daemon) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sup.StopAll(ctx)
}
