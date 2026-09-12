package php

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tarGz(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte(body))
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// files serves a fixed set of paths and records which were asked for.
type files struct {
	body map[string][]byte
	hits []string
}

func (f *files) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Path
	if r.URL.RawQuery != "" {
		key += "?" + r.URL.RawQuery
	}
	f.hits = append(f.hits, key)
	b, ok := f.body[key]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Write(b)
}

func TestTheStaticBuildIsTheNewestPatchForThisPlatform(t *testing.T) {
	index, _ := json.Marshal([]map[string]string{
		{"name": "php-8.4.9-fpm-macos-aarch64.tar.gz"},
		{"name": "php-8.4.12-fpm-macos-aarch64.tar.gz"},
		{"name": "php-8.4.12-cli-macos-aarch64.tar.gz"},
		{"name": "php-8.4.30-fpm-linux-x86_64.tar.gz"}, // another platform
		{"name": "php-8.5.1-fpm-macos-aarch64.tar.gz"}, // another version
	})
	srv := &files{body: map[string][]byte{
		"/?format=json":                        index,
		"/php-8.4.12-fpm-macos-aarch64.tar.gz": tarGz(t, "php-fpm", "fpm"),
		"/php-8.4.12-cli-macos-aarch64.tar.gz": tarGz(t, "./php", "cli"),
	}}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	d := &Downloader{StaticBase: ts.URL, GOOS: "darwin", GOARCH: "arm64"}
	dest := t.TempDir()
	full, err := d.Install(context.Background(), "8.4", dest)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if full != "8.4.12" {
		t.Fatalf("installed %s, want the newest patch 8.4.12", full)
	}
	for _, name := range []string{"php-fpm", "php"} {
		info, err := os.Stat(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Fatalf("%s is not executable", name)
		}
	}
}

func TestLatestNamesTheNewestReleaseOfEachVersion(t *testing.T) {
	index, _ := json.Marshal([]map[string]string{
		{"name": "php-8.4.9-fpm-macos-aarch64.tar.gz"},
		{"name": "php-8.4.12-fpm-macos-aarch64.tar.gz"},
		{"name": "php-8.4.30-fpm-linux-x86_64.tar.gz"}, // another platform
		{"name": "php-8.5.1-fpm-macos-aarch64.tar.gz"},
		{"name": "php-8.5.2-cli-macos-aarch64.tar.gz"}, // no FPM: cannot serve
	})
	releases, _ := json.Marshal(map[string]any{
		"8.4": map[string]any{"version": "8.4.25"},
		"8.5": map[string]any{"version": "8.5.3"},
	})
	ts := httptest.NewServer(&files{body: map[string][]byte{
		"/?format=json":  index,
		"/releases.json": releases,
	}})
	defer ts.Close()

	for _, c := range []struct {
		goos, goarch string
		want         map[string]string
	}{
		{"darwin", "arm64", map[string]string{"8.4": "8.4.12", "8.5": "8.5.1"}},
		{"windows", "amd64", map[string]string{"8.4": "8.4.25", "8.5": "8.5.3"}},
	} {
		d := &Downloader{StaticBase: ts.URL, WindowsBase: ts.URL, GOOS: c.goos, GOARCH: c.goarch}
		got, err := d.Latest(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", c.goos, err)
		}
		if len(got) != len(c.want) || got["8.4"] != c.want["8.4"] || got["8.5"] != c.want["8.5"] {
			t.Fatalf("%s: latest = %v, want %v", c.goos, got, c.want)
		}
	}
}

func TestAnOfflineDownloadIsReportedAsSuch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	d := &Downloader{StaticBase: ts.URL, WindowsBase: ts.URL, GOOS: "linux", GOARCH: "amd64"}
	if _, err := d.Install(context.Background(), "8.4", t.TempDir()); !errors.Is(err, ErrDownload) {
		t.Fatalf("err = %v, want ErrDownload", err)
	}
}

func TestAPlatformWithoutABuildSaysSo(t *testing.T) {
	d := &Downloader{GOOS: "freebsd", GOARCH: "amd64"}
	_, err := d.Install(context.Background(), "8.4", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "freebsd") {
		t.Fatalf("err = %v, want it to name the platform", err)
	}
}

func windowsZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"php.exe", "php-cgi.exe", "ext/php_mbstring.dll"} {
		w, _ := zw.Create(name)
		w.Write([]byte(name))
	}
	zw.Close()
	return buf.Bytes()
}

func TestTheWindowsBuildIsVerifiedAndGetsAPHPIni(t *testing.T) {
	archive := windowsZip(t)
	sum := sha256.Sum256(archive)
	releases := map[string]any{
		"8.4": map[string]any{
			"version": "8.4.25",
			"ts-vs17-x64": map[string]any{
				"zip": map[string]string{"path": "php-8.4.25-Win32-vs17-x64.zip", "sha256": "ignored"},
			},
			"nts-vs17-x64": map[string]any{
				"zip": map[string]string{"path": "php-8.4.25-nts-Win32-vs17-x64.zip", "sha256": hex.EncodeToString(sum[:])},
			},
		},
	}
	index, _ := json.Marshal(releases)
	srv := &files{body: map[string][]byte{
		"/releases.json":                     index,
		"/php-8.4.25-nts-Win32-vs17-x64.zip": archive,
	}}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// Windows on ARM runs the x64 build: there is no other.
	d := &Downloader{WindowsBase: ts.URL, GOOS: "windows", GOARCH: "arm64"}
	dest := t.TempDir()
	full, err := d.Install(context.Background(), "8.4", dest)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if full != "8.4.25" {
		t.Fatalf("installed %s, want 8.4.25", full)
	}
	for _, name := range []string{"php-cgi.exe", "ext/php_mbstring.dll", "php.ini"} {
		if _, err := os.Stat(filepath.Join(dest, name)); err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
	}
	ini, _ := os.ReadFile(filepath.Join(dest, "php.ini"))
	if !strings.Contains(string(ini), "extension=mbstring") {
		t.Fatalf("php.ini does not load Kirby's extensions:\n%s", ini)
	}

	// A tampered archive is refused.
	srv.body["/php-8.4.25-nts-Win32-vs17-x64.zip"] = append(archive, 0)
	if _, err := d.Install(context.Background(), "8.4", t.TempDir()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v, want a checksum mismatch", err)
	}
}

func TestDownloadableLeavesOutEndOfLifeReleases(t *testing.T) {
	got := Downloadable(at("2026-03-01"))
	want := []string{"8.5", "8.4", "8.3", "8.2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Downloadable = %v, want %v", got, want)
	}
}
