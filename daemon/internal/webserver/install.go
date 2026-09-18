package webserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/download"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/elevate"
)

// ErrFetch reports that a webserver could not be downloaded
// (webserver-install.feature, "A webserver install fails").
var ErrFetch = errors.New("the webserver could not be fetched — check your internet connection")

// Plan says how a webserver would be installed on this platform, or why it
// cannot be. Settings shows it before the user asks, so an install that
// cannot happen never looks like one that failed
// (webserver-install.feature, "A webserver Wharf cannot install says how to
// get it").
type Plan struct {
	Installable bool `json:"installable"`
	// Via names the source: "download", "homebrew" or "package".
	Via string `json:"via,omitempty"`
	// Hint is a sentence for the user: what will happen, or what to do.
	Hint string `json:"hint,omitempty"`
}

// Installer gets a webserver onto the machine. A download lands in dest,
// which the daemon renames into bin/<name> once complete; an install through
// a package manager leaves dest empty and is found by the next detection.
type Installer interface {
	Plan(name string) Plan
	Install(ctx context.Context, name, dest string) error
}

// Downloader installs from the sources in dev/architecture.md §4b. There is
// no single source for all three OS: nginx.org and apache.org publish source
// code, not portable builds.
//
//   - nginx, Linux and Windows: static builds from jirutka/nginx-binaries,
//     one self-contained executable each.
//   - nginx, macOS: Homebrew. The macOS builds in the same index link
//     against Homebrew's libpcre2, so without Homebrew they cannot start.
//   - Apache, Windows: Apache Lounge, the build apache.org itself points to.
//   - Apache, macOS: nothing to install — macOS ships it.
//   - Apache, Linux: the distribution's package, installed through its
//     package manager behind a password prompt.
type Downloader struct {
	Client       *http.Client
	NginxIndex   string
	ApacheLounge string
	GOOS, GOARCH string
	// Brew is Homebrew's binary; "" means look for it.
	Brew string
	// Run runs a package manager. Tests replace it.
	Run func(ctx context.Context, program string, args ...string) error
	// Elevator runs a Linux package manager with administrator rights.
	Elevator elevate.Elevator
	// LookPath finds a Linux package manager; "" means exec.LookPath.
	LookPath func(file string) (string, error)
}

// linuxPackage is how one family of distributions installs Apache.
type linuxPackage struct {
	manager, install, service string
}

// linuxPackages are tried in order; the first manager found is the
// distribution's own.
var linuxPackages = []linuxPackage{
	{"apt-get", "DEBIAN_FRONTEND=noninteractive apt-get install -y apache2", "apache2"},
	{"dnf", "dnf install -y httpd", "httpd"},
	{"zypper", "zypper --non-interactive install apache2", "apache2"},
	{"pacman", "pacman -S --noconfirm --needed apache", "httpd"},
}

func (d *Downloader) linuxPackage() (linuxPackage, bool) {
	look := d.LookPath
	if look == nil {
		look = exec.LookPath
	}
	for _, p := range linuxPackages {
		if _, err := look(p.manager); err == nil {
			return p, true
		}
	}
	return linuxPackage{}, false
}

// NewDownloader returns a Downloader pointed at the public sources.
func NewDownloader() *Downloader {
	return &Downloader{
		NginxIndex:   "https://jirutka.github.io/nginx-binaries",
		ApacheLounge: "https://www.apachelounge.com",
	}
}

func (d *Downloader) platform() (string, string) {
	goos, goarch := d.GOOS, d.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return goos, goarch
}

func (d *Downloader) brew() string {
	if d.Brew != "" {
		return d.Brew
	}
	for _, p := range []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew", "/home/linuxbrew/.linuxbrew/bin/brew"} {
		if isExecutable(p, "darwin") {
			return p
		}
	}
	if p, err := exec.LookPath("brew"); err == nil {
		return p
	}
	return ""
}

// nginxPlatform maps Go's names onto the nginx-binaries index. Windows on ARM
// runs the x64 build, as it has none of its own.
func nginxPlatform(goos, goarch string) (string, string, bool) {
	switch goos + "/" + goarch {
	case "linux/amd64":
		return "linux", "x86_64", true
	case "linux/arm64":
		return "linux", "aarch64", true
	case "windows/amd64", "windows/arm64":
		return "win32", "x86_64", true
	}
	return "", "", false
}

// Plan implements Installer.
func (d *Downloader) Plan(name string) Plan {
	goos, goarch := d.platform()
	switch name + "/" + goos {
	case Nginx + "/darwin":
		if d.brew() != "" {
			return Plan{Installable: true, Via: "homebrew", Hint: "Installs the latest nginx with Homebrew."}
		}
		return Plan{Hint: "nginx for macOS comes from Homebrew: install Homebrew from brew.sh, then choose " +
			"Install again. Or use Apache, which macOS includes."}
	case Apache + "/darwin":
		if d.brew() != "" {
			return Plan{Installable: true, Via: "homebrew", Hint: "Installs the latest Apache with Homebrew."}
		}
		return Plan{Hint: "macOS includes Apache at /usr/sbin/httpd; it seems to be missing on this Mac. " +
			"Install Homebrew from brew.sh to install it."}
	case Apache + "/windows":
		return Plan{Installable: true, Via: "download",
			Hint: "Downloads the latest Apache from Apache Lounge. It needs the Microsoft Visual C++ Redistributable."}
	case Apache + "/linux":
		if p, ok := d.linuxPackage(); ok {
			return Plan{Installable: true, Via: "package",
				Hint: "Installs Apache with " + p.manager + " after asking for your password. The system's own " +
					"Apache service is switched off, so only Wharf's uses port 80."}
		}
		return Plan{Hint: "Install Apache with your package manager, then re-scan. If it starts its own Apache " +
			"service, stop it with sudo systemctl disable --now apache2 (or httpd)."}
	}
	if name == Nginx {
		if _, _, ok := nginxPlatform(goos, goarch); ok {
			return Plan{Installable: true, Via: "download", Hint: "Downloads the latest nginx into Wharf's bin folder."}
		}
		return Plan{Hint: fmt.Sprintf("No nginx build is published for %s/%s — install it yourself, then re-scan.", goos, goarch)}
	}
	return Plan{Hint: fmt.Sprintf("Wharf does not know how to install %q.", name)}
}

// Install implements Installer.
func (d *Downloader) Install(ctx context.Context, name, dest string) error {
	plan := d.Plan(name)
	if !plan.Installable {
		return errors.New(plan.Hint)
	}
	var err error
	switch {
	case plan.Via == "homebrew":
		err = d.brewInstall(ctx, map[string]string{Nginx: "nginx", Apache: "httpd"}[name])
	case plan.Via == "package":
		err = d.packageInstall()
	case name == Nginx:
		err = d.downloadNginx(ctx, dest)
	case name == Apache:
		err = d.downloadApache(ctx, dest)
	}
	if errors.Is(err, download.ErrUnreachable) {
		return fmt.Errorf("%w (%v)", ErrFetch, err)
	}
	return err
}

func (d *Downloader) brewInstall(ctx context.Context, formula string) error {
	run := d.Run
	if run == nil {
		run = runQuiet
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	if err := run(ctx, d.brew(), "install", formula); err != nil {
		return fmt.Errorf("brew install %s failed: %w", formula, err)
	}
	return nil
}

// packageInstall installs Apache from the distribution. Debian's package starts
// the system's Apache on port 80 and enables it at boot; it is switched off
// again at once, or the front door could never take the port. The service is
// left alone on a system without systemd.
func (d *Downloader) packageInstall() error {
	p, _ := d.linuxPackage()
	if d.Elevator == nil {
		return errors.New("Wharf cannot ask for a password here — run " + p.install + " yourself, then re-scan")
	}
	script := p.install + " && { systemctl disable --now " + p.service + " >/dev/null 2>&1 || true; }"
	if err := d.Elevator.RequestElevatedRun("/bin/sh", []string{"-c", script}, nil); err != nil {
		if errors.Is(err, elevate.ErrDeclined) {
			return err
		}
		return fmt.Errorf("installing Apache with %s failed — run %s in a terminal to see why: %w", p.manager, p.install, err)
	}
	return nil
}

func runQuiet(ctx context.Context, program string, args ...string) error {
	cmd := exec.CommandContext(ctx, program, args...)
	// INFO: Homebrew's hints are for a terminal nobody is looking at.
	cmd.Env = append(os.Environ(), "HOMEBREW_NO_ENV_HINTS=1", "HOMEBREW_NO_INSTALL_CLEANUP=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 3 {
			lines = lines[len(lines)-3:]
		}
		return fmt.Errorf("%w: %s", err, strings.Join(lines, " "))
	}
	return nil
}

// downloadNginx fetches the newest build for this platform and verifies it
// against the index's checksum.
func (d *Downloader) downloadNginx(ctx context.Context, dest string) error {
	goos, goarch := d.platform()
	osName, archName, _ := nginxPlatform(goos, goarch)

	var index struct {
		Contents []struct {
			Name     string `json:"name"`
			Version  string `json:"version"`
			Variant  string `json:"variant"`
			OS       string `json:"os"`
			Arch     string `json:"arch"`
			Filename string `json:"filename"`
			Checksum string `json:"checksum"`
		} `json:"contents"`
	}
	if err := download.JSON(ctx, d.Client, d.NginxIndex+"/index.json", &index); err != nil {
		return err
	}
	best, file, sum := "", "", ""
	for _, e := range index.Contents {
		if e.Name != "nginx" || e.Variant != "" || e.OS != osName || e.Arch != archName {
			continue
		}
		if best == "" || versionLess(best, e.Version) {
			best, file, sum = e.Version, e.Filename, e.Checksum
		}
	}
	if file == "" {
		return fmt.Errorf("no nginx build is published for %s-%s", osName, archName)
	}

	bin := filepath.Join(dest, "nginx")
	if goos == "windows" {
		bin += ".exe"
	}
	if _, err := download.File(ctx, d.Client, d.NginxIndex+"/"+file, bin, sum); err != nil {
		return err
	}
	return os.Chmod(bin, 0o755)
}

var loungeZipRe = regexp.MustCompile(`/download/VS\d+/binaries/httpd-2\.4\.\d+-\d+-[Ww]in64-VS\d+\.zip`)
var loungeSumRe = regexp.MustCompile(`SHA256-Checksum for: [^\n]+\n\s*([0-9A-Fa-f]{64})`)

// downloadApache fetches Apache Lounge's newest 64-bit build. Its download
// page is the index; each zip has a checksum file beside it.
func (d *Downloader) downloadApache(ctx context.Context, dest string) error {
	page, err := d.text(ctx, d.ApacheLounge+"/download/")
	if err != nil {
		return err
	}
	path := loungeZipRe.FindString(page)
	if path == "" {
		return fmt.Errorf("no Apache build found on %s/download/", d.ApacheLounge)
	}
	sums, err := d.text(ctx, d.ApacheLounge+path+".txt")
	if err != nil {
		return err
	}
	m := loungeSumRe.FindStringSubmatch(strings.ReplaceAll(sums, "\r", ""))
	if m == nil {
		return fmt.Errorf("no SHA-256 checksum published for %s", filepath.Base(path))
	}

	tmp, err := os.MkdirTemp("", "wharf-apache-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	archive := filepath.Join(tmp, filepath.Base(path))
	if _, err := download.File(ctx, d.Client, d.ApacheLounge+path, archive, m[1]); err != nil {
		return err
	}
	unpacked := filepath.Join(tmp, "unpacked")
	if err := download.Unzip(archive, unpacked); err != nil {
		return err
	}
	// INFO: The archive wraps everything in Apache24/; its bin/ and modules/ become
	// bin/apache/bin and bin/apache/modules.
	root := filepath.Join(unpacked, "Apache24")
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("%s has no Apache24 folder", filepath.Base(path))
	}
	for _, e := range entries {
		if err := os.Rename(filepath.Join(root, e.Name()), filepath.Join(dest, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (d *Downloader) text(ctx context.Context, url string) (string, error) {
	tmp, err := os.CreateTemp("", "wharf-page-*")
	if err != nil {
		return "", err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if _, err := download.File(ctx, d.Client, url, tmp.Name(), ""); err != nil {
		return "", err
	}
	b, err := os.ReadFile(tmp.Name())
	return string(b), err
}

// versionLess orders dotted versions numerically.
func versionLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		var x, y int
		fmt.Sscan(as[i], &x)
		fmt.Sscan(bs[i], &y)
		if x != y {
			return x < y
		}
	}
	return len(as) < len(bs)
}
