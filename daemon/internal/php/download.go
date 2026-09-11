package php

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/manuel-steinberg/wharf/daemon/internal/download"
)

// ErrDownload reports that a build could not be fetched. The GUI shows it as
// is (php-runtime.feature, "A PHP download fails").
var ErrDownload = errors.New("the PHP build could not be fetched — check your internet connection")

// Installer puts a runnable PHP build for a minor version into an empty
// directory and returns the full version it installed. It is an interface so
// the daemon can be tested without network access.
type Installer interface {
	Install(ctx context.Context, version, destDir string) (string, error)
}

// Downloader installs prebuilt PHP binaries. PHP publishes official builds
// for Windows only, so the other two platforms use static-php-cli's builds:
// single static binaries with no library dependencies, carrying every
// extension Kirby needs (ctype, curl, dom, gd, mbstring, openssl, …). Both
// sources publish a machine-readable index, so the newest patch release of a
// minor version is found rather than hard-coded.
type Downloader struct {
	Client *http.Client
	// StaticBase lists static-php-cli builds for macOS and Linux.
	StaticBase string
	// WindowsBase lists the official Windows builds.
	WindowsBase string
	// GOOS and GOARCH pick the build; they default to the running platform
	// and exist so both code paths are testable on any machine.
	GOOS, GOARCH string
}

// NewDownloader returns a Downloader pointed at the public build servers.
func NewDownloader() *Downloader {
	return &Downloader{
		StaticBase:  "https://dl.static-php.dev/static-php-cli/common",
		WindowsBase: "https://windows.php.net/downloads/releases",
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

// Install downloads the newest build of version into destDir.
func (d *Downloader) Install(ctx context.Context, version, destDir string) (string, error) {
	goos, goarch := d.platform()
	tmp, err := os.MkdirTemp("", "wharf-php-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	var full string
	if goos == "windows" {
		full, err = d.installWindows(ctx, version, destDir, tmp)
	} else {
		full, err = d.installStatic(ctx, goos, goarch, version, destDir, tmp)
	}
	if errors.Is(err, download.ErrUnreachable) {
		return "", fmt.Errorf("%w (%v)", ErrDownload, err)
	}
	return full, err
}

// installStatic fetches the php-fpm and php CLI tarballs for one platform.
func (d *Downloader) installStatic(ctx context.Context, goos, goarch, version, destDir, tmp string) (string, error) {
	osName := map[string]string{"darwin": "macos", "linux": "linux"}[goos]
	archName := map[string]string{"arm64": "aarch64", "amd64": "x86_64"}[goarch]
	if osName == "" || archName == "" {
		return "", fmt.Errorf("no prebuilt PHP is published for %s/%s — install PHP %s yourself, then re-scan", goos, goarch, version)
	}

	var index []struct {
		Name string `json:"name"`
	}
	if err := download.JSON(ctx, d.Client, d.StaticBase+"/?format=json", &index); err != nil {
		return "", err
	}

	re := regexp.MustCompile(`^php-` + regexp.QuoteMeta(version) + `\.(\d+)-fpm-` + osName + `-` + archName + `\.tar\.gz$`)
	patch := -1
	for _, e := range index {
		if m := re.FindStringSubmatch(e.Name); m != nil {
			if n, _ := strconv.Atoi(m[1]); n > patch {
				patch = n
			}
		}
	}
	if patch < 0 {
		return "", fmt.Errorf("no PHP %s build is published for %s-%s", version, osName, archName)
	}
	full := fmt.Sprintf("%s.%d", version, patch)

	for _, kind := range []struct{ sapi, binary string }{{"fpm", "php-fpm"}, {"cli", "php"}} {
		name := fmt.Sprintf("php-%s-%s-%s-%s.tar.gz", full, kind.sapi, osName, archName)
		archive := filepath.Join(tmp, name)
		if _, err := download.File(ctx, d.Client, d.StaticBase+"/"+name, archive, ""); err != nil {
			return "", err
		}
		if err := download.UntarGz(archive, destDir, kind.binary); err != nil {
			return "", err
		}
		bin := filepath.Join(destDir, kind.binary)
		if _, err := os.Stat(bin); err != nil {
			return "", fmt.Errorf("%s did not contain %s", name, kind.binary)
		}
		if err := os.Chmod(bin, 0o755); err != nil {
			return "", err
		}
	}
	return full, nil
}

// windowsRelease is one minor version in windows.php.net's releases.json.
// Build variants are keyed like "nts-vs17-x64", each with a zip and its
// SHA-256.
type windowsRelease map[string]any

// installWindows fetches the official non-thread-safe x64 build. NTS is the
// build meant for FastCGI; x64 also runs on Windows on ARM, which has no
// build of its own.
func (d *Downloader) installWindows(ctx context.Context, version, destDir, tmp string) (string, error) {
	var index map[string]windowsRelease
	if err := download.JSON(ctx, d.Client, d.WindowsBase+"/releases.json", &index); err != nil {
		return "", err
	}
	rel, ok := index[version]
	if !ok {
		return "", fmt.Errorf("no PHP %s build is published for Windows", version)
	}
	full, _ := rel["version"].(string)

	var zipPath, sum string
	for key, v := range rel {
		if !strings.HasPrefix(key, "nts-") || !strings.HasSuffix(key, "-x64") {
			continue
		}
		variant, _ := v.(map[string]any)
		z, _ := variant["zip"].(map[string]any)
		zipPath, _ = z["path"].(string)
		sum, _ = z["sha256"].(string)
	}
	if zipPath == "" {
		return "", fmt.Errorf("no PHP %s build is published for Windows x64", version)
	}

	archive := filepath.Join(tmp, filepath.Base(zipPath))
	if _, err := download.File(ctx, d.Client, d.WindowsBase+"/"+zipPath, archive, sum); err != nil {
		return "", err
	}
	if err := download.Unzip(archive, destDir); err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(destDir, "php-cgi.exe")); err != nil {
		return "", fmt.Errorf("%s did not contain php-cgi.exe", filepath.Base(zipPath))
	}
	// The Windows build loads its extensions from DLLs, and without a
	// php.ini it loads none — Kirby would not boot. PHP looks for php.ini
	// beside the binary, so one is written there. extension_dir is not in
	// it: the folder is renamed after this returns, so the daemon passes the
	// real location when it starts php-cgi.
	return full, os.WriteFile(filepath.Join(destDir, "php.ini"), []byte(windowsINI()), 0o644)
}

func windowsINI() string {
	var sb strings.Builder
	sb.WriteString("; Written by Wharf when it downloaded this build. Edit freely.\n")
	for _, ext := range []string{"curl", "fileinfo", "gd", "intl", "mbstring", "openssl", "pdo_sqlite", "sqlite3", "zip"} {
		fmt.Fprintf(&sb, "extension=%s\n", ext)
	}
	return sb.String()
}

// Downloadable lists the versions worth offering for download at a point in
// time: every release still receiving at least security fixes, newest first.
// End-of-life releases are left out on purpose (php-runtime.feature, "Only
// supported versions are offered for download").
func Downloadable(now time.Time) []string {
	var out []string
	for i := len(Releases) - 1; i >= 0; i-- {
		switch StatusAt(Releases[i].Version, now) {
		case StatusActive, StatusSecurity:
			out = append(out, Releases[i].Version)
		}
	}
	return out
}
