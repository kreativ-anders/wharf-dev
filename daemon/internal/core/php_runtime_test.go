package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/certs"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/elevate"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/ipc"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/layout"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/php"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/runtime"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/supervisor"
)

// firstRun builds a daemon with no existing config, seeing exactly the PHP
// installations the test staged on a fake machine.
type firstRun struct {
	t      *testing.T
	d      *Daemon
	root   layout.Root
	sysCLI map[string]string // version → staged cli path
}

// newFirstRun stages the given versions as system installs (outside bin/php)
// and starts a daemon whose config file does not exist yet.
func newFirstRun(t *testing.T, now time.Time, systemVersions ...string) *firstRun {
	t.Helper()
	dir := t.TempDir()
	root := layout.Root{Dir: dir}
	if err := root.Ensure(); err != nil {
		t.Fatal(err)
	}
	stubWebservers(t, root)

	machine := filepath.Join(dir, "machine")
	versions := map[string]string{}
	full := map[string]string{}
	var candidates []string
	for _, v := range systemVersions {
		binDir := filepath.Join(machine, "php"+v, "bin")
		cli := filepath.Join(binDir, php.CLIName())
		stubBinary(t, cli)
		stubBinary(t, filepath.Join(binDir, php.FastCGIName()))
		candidates = append(candidates, cli)
		versions[v] = cli
		full[cli] = v + ".1"
	}

	store, err := config.Load(root.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if !store.Created {
		t.Fatal("the config file already existed; this is not a first run")
	}

	ports := supervisor.NewFakePorts()
	sup := supervisor.New(supervisor.NewFakeRunner(ports), ports)
	sup.StartTimeout, sup.StopTimeout, sup.Poll = 2*time.Second, 2*time.Second, time.Millisecond

	el := elevate.NewFake()
	el.Apply = true
	hostsPath := filepath.Join(dir, "hosts")
	if err := os.WriteFile(hostsPath, []byte("127.0.0.1\tlocalhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := New(Options{
		Root:         root,
		Store:        store,
		Supervisor:   sup,
		Elevator:     el,
		HostsPath:    hostsPath,
		Certs:        certs.NewFake(),
		Now:          func() time.Time { return now },
		WebDetector:  testWebDetector(),
		WebInstaller: &fakeWebInstaller{},
		Detector: &php.Detector{
			VendorDir:  filepath.Join(root.Bin(), "php"),
			Candidates: candidates,
			Probe: func(_ context.Context, bin string) (string, error) {
				return full[bin], nil
			},
			Now: func() time.Time { return now },
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &firstRun{t: t, d: d, root: root, sysCLI: versions}
}

func (f *firstRun) ctx() context.Context { return context.Background() }

// features/php-runtime.feature — "First start adopts the PHP versions already
// on the machine"
func TestFirstStartAdoptsInstalledPHPVersions(t *testing.T) {
	// March 2026: 8.4 is in active support, 8.1 is end of life.
	f := newFirstRun(t, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), "8.1", "8.4")

	st := f.d.State().Services.PHP

	// Then both "8.1" and "8.4" are offered as runtime versions
	if !contains(st.Available, "8.1") || !contains(st.Available, "8.4") {
		t.Fatalf("available = %v, want both installed versions", st.Available)
	}

	// And "8.4" is selected as the global default, being the newest in active support
	if st.Version != "8.4" {
		t.Fatalf("default = %q, want 8.4", st.Version)
	}
	if st.Status != php.StatusActive {
		t.Fatalf("status of the default = %q, want active", st.Status)
	}

	// And the location of each adopted version is recorded in config/wharf.json
	raw, err := os.ReadFile(f.root.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	var onDisk config.Config
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"8.1", "8.4"} {
		path, ok := onDisk.PHPPath(v)
		if !ok {
			t.Fatalf("no recorded location for adopted PHP %s in:\n%s", v, raw)
		}
		if _, err := os.Stat(filepath.Join(path, php.FastCGIName())); err != nil {
			t.Fatalf("recorded location for %s is not usable: %v", v, err)
		}
	}

	// And no PHP binary is downloaded
	if _, err := os.Stat(f.root.PHPBin("8.4")); !os.IsNotExist(err) {
		t.Fatalf("something was written into bin/php/8.4: %v", err)
	}
}

// features/php-runtime.feature — "First start prefers a supported version over
// a newer unsupported one"
func TestFirstStartPrefersASupportedVersion(t *testing.T) {
	// March 2026: 8.1 is end of life, 8.3 still receives security fixes.
	f := newFirstRun(t, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), "8.1", "8.3")

	st := f.d.State().Services.PHP
	if st.Version != "8.3" {
		t.Fatalf("default = %q, want the security-supported 8.3 over the EOL 8.1", st.Version)
	}
	if st.Status != php.StatusSecurity {
		t.Fatalf("status = %q, want security", st.Status)
	}

	// And the version's support status is shown alongside it in the picker
	byVersion := map[string]php.Install{}
	for _, in := range st.Installs {
		byVersion[in.Version] = in
	}
	if got := byVersion["8.1"].Status; got != php.StatusEndOfLife {
		t.Fatalf("8.1 shown as %q, want eol", got)
	}
	if got := byVersion["8.3"].Status; got != php.StatusSecurity {
		t.Fatalf("8.3 shown as %q, want security", got)
	}
}

// features/php-runtime.feature — "First start with no PHP installed"
func TestFirstStartWithNoPHPInstalled(t *testing.T) {
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	f := newFirstRun(t, now)

	st := f.d.State().Services.PHP

	// Then the newest actively supported PHP version is selected as the default
	want := php.Recommended(now)
	if st.Version != want {
		t.Fatalf("default = %q, want the recommended %q", st.Version, want)
	}
	if st.Recommended != want {
		t.Fatalf("recommended = %q, want %q", st.Recommended, want)
	}

	// And no runtime versions are offered as installed
	if len(st.Installs) != 0 {
		t.Fatalf("installs = %+v, want none", st.Installs)
	}

	// And starting a project reports where the missing binary is expected
	if err := os.MkdirAll(f.root.ProjectDir("site"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.AddProject(f.ctx(), "site"); err != nil {
		t.Fatal(err)
	}
	err := f.d.StartProject(f.ctx(), "site")
	if err == nil {
		t.Fatal("starting a project with no PHP should fail")
	}
	if !strings.Contains(err.Error(), filepath.Join("bin", "php", want)) {
		t.Fatalf("error = %q, want it to name the expected folder", err)
	}
}

// features/php-runtime.feature — "Choosing a different PHP version globally"
func TestChoosingADifferentPHPVersionGlobally(t *testing.T) {
	f := newFirstRun(t, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), "8.3", "8.4")
	if got := f.d.State().Services.PHP.Version; got != "8.4" {
		t.Fatalf("default = %q, want 8.4 to begin with", got)
	}

	for _, name := range []string{"plain", "pinned"} {
		if err := os.MkdirAll(f.root.ProjectDir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := f.d.AddProject(f.ctx(), name); err != nil {
			t.Fatal(err)
		}
	}
	pinned := "8.4"
	if _, err := f.d.UpdateSettings(f.ctx(), "pinned", Settings{PHP: &pinned}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.StartProject(f.ctx(), "plain"); err != nil {
		t.Fatal(err)
	}

	if err := f.d.SetPHPVersion(f.ctx(), "8.3"); err != nil {
		t.Fatalf("select 8.3: %v", err)
	}

	// Then "8.3" is written to config/wharf.json as the global default
	reread, err := config.Load(f.root.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if got := reread.Get().Services.PHP.Version; got != "8.3" {
		t.Fatalf("persisted default = %q, want 8.3", got)
	}

	// And projects without their own PHP override are served by "8.3"
	state := f.d.State()
	byName := map[string]Project{}
	for _, p := range state.Projects {
		byName[p.Name] = p
	}
	if byName["plain"].PHPVersion != "8.3" {
		t.Fatalf("plain project on PHP %q, want 8.3", byName["plain"].PHPVersion)
	}

	// And projects with an override keep the version they had
	if byName["pinned"].PHPVersion != "8.4" {
		t.Fatalf("pinned project on PHP %q, want it to keep 8.4", byName["pinned"].PHPVersion)
	}

	// The running site must actually be routed to the new backend.
	conf, err := os.ReadFile(filepath.Join(f.root.Data(), "gen", "nginx", "plain.conf"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := f.d.Config()
	want := "fastcgi_pass 127.0.0.1:" + itoa(runtime.PHPPort(cfg, "8.3")) + ";"
	if !strings.Contains(vhostBlock(t, string(conf), "plain.localhost"), want) {
		t.Fatalf("plain is not routed to the new backend:\n%s", conf)
	}
}

// features/php-runtime.feature — "Re-scanning after installing a PHP version"
func TestRescanningAfterInstallingAPHPVersion(t *testing.T) {
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	f := newFirstRun(t, now, "8.3")
	if len(f.d.State().Services.PHP.Installs) != 1 {
		t.Fatalf("installs = %+v, want just 8.3", f.d.State().Services.PHP.Installs)
	}

	// The user installs 8.4 while the daemon is running.
	binDir := filepath.Join(f.root.Dir, "machine", "php8.4", "bin")
	stubBinary(t, filepath.Join(binDir, php.CLIName()))
	stubBinary(t, filepath.Join(binDir, php.FastCGIName()))
	f.d.php.Candidates = append(f.d.php.Candidates, filepath.Join(binDir, php.CLIName()))
	probe := f.d.php.Probe
	f.d.php.Probe = func(ctx context.Context, bin string) (string, error) {
		if strings.Contains(bin, "php8.4") {
			return "8.4.1", nil
		}
		return probe(ctx, bin)
	}

	f.d.RefreshPHP(f.ctx())

	// Then "8.4" appears in the picker without restarting the daemon
	var found bool
	for _, in := range f.d.State().Services.PHP.Installs {
		if in.Version == "8.4" && in.Servable() {
			found = true
		}
	}
	if !found {
		t.Fatalf("8.4 did not appear after a re-scan: %+v", f.d.State().Services.PHP.Installs)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// features/php-runtime.feature — "Downloading a PHP version that is not
// installed"
func TestDownloadingAPHPVersionThatIsNotInstalled(t *testing.T) {
	h := newHarness(t)
	if slices.Contains(h.d.Config().Services.PHP.Available, "8.4") {
		t.Fatal("8.4 offered before it was downloaded")
	}

	// The download is held open so the snapshot can be looked at mid-way.
	inside := make(chan []string)
	release := make(chan struct{})
	stub := h.php.fn
	h.php.fn = func(ctx context.Context, version, dest string) (string, error) {
		inside <- h.d.State().Services.PHP.Downloading
		<-release
		return (&fakeInstaller{fn: stub}).Install(ctx, version, dest)
	}

	done := make(chan error)
	go func() { done <- h.d.InstallPHP(h.ctx(), "8.4") }()

	// And the picker shows it as downloading until it is done
	if got := <-inside; !slices.Equal(got, []string{"8.4"}) {
		t.Fatalf("downloading = %v, want [8.4]", got)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("install: %v", err)
	}
	if got := h.d.State().Services.PHP.Downloading; len(got) != 0 {
		t.Fatalf("still downloading %v after it finished", got)
	}

	// Then the newest "8.4" build for this OS and CPU is downloaded into
	// "bin/php/8.4"
	if _, err := os.Stat(filepath.Join(h.root.PHPBin("8.4"), "php-fpm")); err != nil {
		t.Fatalf("no build in bin/php/8.4: %v", err)
	}
	// And it becomes selectable without any further setup
	if !slices.Contains(h.d.Config().Services.PHP.Available, "8.4") {
		t.Fatal("8.4 is not offered after the download")
	}
	if slices.Contains(downloadableVersions(h), "8.4") {
		t.Fatal("8.4 is still offered for download")
	}
	if err := h.d.SetPHPVersion(h.ctx(), "8.4"); err != nil {
		t.Fatalf("select the downloaded version: %v", err)
	}
}

// features/php-runtime.feature — "A PHP download fails"
func TestAPHPDownloadFails(t *testing.T) {
	h := newHarness(t)
	h.php.fn = func(_ context.Context, _, dest string) (string, error) {
		// Half a download, then the network goes away.
		_ = os.WriteFile(filepath.Join(dest, "php-fpm"), []byte("partial"), 0o755)
		return "", php.ErrDownload
	}

	err := h.d.InstallPHP(h.ctx(), "8.4")

	// Then the GUI reports that the build could not be fetched
	var ipcErr *ipc.Error
	if !errors.Is(err, php.ErrDownload) || !asIPC(asIPCError(err), &ipcErr) || ipcErr.Code != ipc.CodeOffline {
		t.Fatalf("err = %v, want an offline error", err)
	}
	// And no partial "bin/php/8.4" folder is left behind
	if _, err := os.Stat(h.root.PHPBin("8.4")); !os.IsNotExist(err) {
		t.Fatal("bin/php/8.4 exists after a failed download")
	}
	entries, _ := os.ReadDir(filepath.Dir(h.root.PHPBin("8.4")))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".download") {
			t.Fatalf("staging folder %s left behind", e.Name())
		}
	}
	if slices.Contains(h.d.Config().Services.PHP.Available, "8.4") {
		t.Fatal("8.4 registered despite the failure")
	}
}

// features/php-runtime.feature — "Only supported versions are offered for
// download"
func TestOnlySupportedVersionsAreOfferedForDownload(t *testing.T) {
	h := newHarness(t) // 8.1, 8.2 and 8.3 installed; clock at 2026-03-01

	got := downloadableVersions(h)

	// Every version still receiving security fixes that is not installed…
	if !slices.Equal(got, []string{"8.5", "8.4"}) {
		t.Fatalf("downloadable = %v, want [8.5 8.4]", got)
	}
	// …and never an end-of-life one, installed or not.
	if slices.Contains(php.Downloadable(testNow), "8.1") {
		t.Fatal("end-of-life 8.1 is offered for download")
	}
}

// features/php-runtime.feature — "A download offer names the release it
// downloads"
func TestADownloadOfferNamesTheReleaseItDownloads(t *testing.T) {
	h := newHarness(t) // 8.4 and 8.5 are not installed

	fullVersions := func() map[string]string {
		out := map[string]string{}
		for _, d := range h.d.State().Services.PHP.Downloadable {
			out[d.Version] = d.FullVersion
		}
		return out
	}

	// And without a connection the offer names "8.5" alone
	h.php.latestErr = php.ErrDownload
	if err := h.d.CheckPHPReleases(h.ctx()); !errors.Is(err, php.ErrDownload) {
		t.Fatalf("err = %v, want ErrDownload", err)
	}
	if got := fullVersions(); got["8.5"] != "" {
		t.Fatalf("offline, 8.5 is offered as %q", got["8.5"])
	}

	// When the user opens the PHP page, Wharf looks up the newest releases
	h.php.latestErr = nil
	h.php.latest = map[string]string{"8.5": "8.5.1", "8.4": "8.4.12", "8.1": "8.1.34"}
	if err := h.d.CheckPHPReleases(h.ctx()); err != nil {
		t.Fatal(err)
	}
	// Then "8.5" is offered as that release
	if got := fullVersions(); got["8.5"] != "8.5.1" || got["8.4"] != "8.4.12" {
		t.Fatalf("offers = %v, want 8.5.1 and 8.4.12", got)
	}
}

func TestDownloadingAnUnknownVersionIsRefused(t *testing.T) {
	h := newHarness(t)
	var invalid *InvalidError
	if err := h.d.InstallPHP(h.ctx(), "7.0"); !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want invalid", err)
	}
	if err := h.d.InstallPHP(h.ctx(), "8.3"); err == nil {
		t.Fatal("downloading over an existing bin/php/8.3 succeeded")
	}
}

func downloadableVersions(h *harness) []string {
	var out []string
	for _, d := range h.d.State().Services.PHP.Downloadable {
		out = append(out, d.Version)
	}
	return out
}
