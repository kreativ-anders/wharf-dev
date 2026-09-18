package webserver

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/elevate"
)

// hostExe names a stub the way this OS names executables: Windows knows one
// by its .exe suffix, not by a mode bit.
func hostExe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func stub(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func noProbe(context.Context, string) (string, error) { return "1.2.3", nil }

func TestDetectionPrefersWharfsOwnCopy(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	system := filepath.Join(dir, "usr", "sbin", hostExe("nginx"))
	stub(t, system)

	d := &Detector{BinDir: bin, Probe: noProbe,
		Candidates: map[string][]string{Nginx: {system}}}
	if got := d.Detect(context.Background())[Nginx]; got.Binary != system || got.Source != "system" {
		t.Fatalf("got %+v, want the system nginx", got)
	}

	stub(t, filepath.Join(bin, "nginx", hostExe("nginx")))
	got := d.Detect(context.Background())[Nginx]
	if got.Source != "wharf" || got.Version != "1.2.3" {
		t.Fatalf("got %+v, want Wharf's own copy", got)
	}
}

func TestApacheWithoutModulesIsNotUsable(t *testing.T) {
	dir := t.TempDir()
	httpd := filepath.Join(dir, "sbin", hostExe("httpd"))
	stub(t, httpd)
	d := &Detector{BinDir: filepath.Join(dir, "bin"), Probe: noProbe,
		Candidates: map[string][]string{Apache: {httpd}}}

	if _, ok := d.Detect(context.Background())[Apache]; ok && ModulesDir(httpd) == "" {
		t.Fatal("an Apache without mod_proxy_fcgi was accepted")
	}
	// INFO: Debian and Homebrew layouts both resolve.
	stub(t, filepath.Join(dir, "lib", "httpd", "modules", "mod_proxy_fcgi.so"))
	got := d.Detect(context.Background())[Apache]
	if got.Modules != filepath.Join(dir, "lib", "httpd", "modules") {
		t.Fatalf("modules = %q", got.Modules)
	}
}

func TestPlansDifferByPlatformButAlwaysSaySomething(t *testing.T) {
	cases := []struct {
		goos, goarch, name, brew, manager string
		installable                       bool
		via                               string
	}{
		{"linux", "amd64", Nginx, "", "", true, "download"},
		{"linux", "arm64", Nginx, "", "", true, "download"},
		{"windows", "arm64", Nginx, "", "", true, "download"},
		{"windows", "amd64", Apache, "", "", true, "download"},
		{"linux", "amd64", Apache, "", "dnf", true, "package"},
		{"linux", "amd64", Apache, "", "", false, ""},
		{"darwin", "arm64", Nginx, "/opt/homebrew/bin/brew", "", true, "homebrew"},
	}
	for _, c := range cases {
		d := &Downloader{GOOS: c.goos, GOARCH: c.goarch, Brew: c.brew, LookPath: onlyManager(c.manager)}
		p := d.Plan(c.name)
		if p.Installable != c.installable || p.Via != c.via || p.Hint == "" {
			t.Errorf("%s/%s %s: plan = %+v", c.goos, c.goarch, c.name, p)
		}
	}
}

func TestNginxIsTheNewestBuildAndVerified(t *testing.T) {
	body := []byte("nginx binary")
	sum := sha1.Sum(body)
	index, _ := json.Marshal(map[string]any{"contents": []map[string]string{
		{"name": "nginx", "version": "1.30.4", "variant": "", "os": "linux", "arch": "x86_64",
			"filename": "nginx-1.30.4-x86_64-linux", "checksum": "sha1:00"},
		{"name": "nginx", "version": "1.31.5", "variant": "", "os": "linux", "arch": "x86_64",
			"filename": "nginx-1.31.5-x86_64-linux", "checksum": "sha1:" + hex.EncodeToString(sum[:])},
		{"name": "nginx", "version": "1.31.9", "variant": "debug", "os": "linux", "arch": "x86_64",
			"filename": "nginx-1.31.9-x86_64-linux-debug", "checksum": "sha1:00"},
		{"name": "nginx", "version": "1.40.0", "variant": "", "os": "darwin", "arch": "arm64",
			"filename": "nginx-1.40.0-arm64-darwin", "checksum": "sha1:00"},
	}})
	served := map[string][]byte{"/index.json": index, "/nginx-1.31.5-x86_64-linux": body}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := served[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	defer srv.Close()

	d := &Downloader{NginxIndex: srv.URL, GOOS: "linux", GOARCH: "amd64"}
	dest := t.TempDir()
	if err := d.Install(context.Background(), Nginx, dest); err != nil {
		t.Fatalf("install: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "nginx")); string(got) != "nginx binary" {
		t.Fatalf("installed %q", got)
	}

	served["/nginx-1.31.5-x86_64-linux"] = []byte("tampered")
	if err := d.Install(context.Background(), Nginx, t.TempDir()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v, want a checksum mismatch", err)
	}
	srv.Close()
	if err := d.Install(context.Background(), Nginx, t.TempDir()); !errors.Is(err, ErrFetch) {
		t.Fatalf("err = %v, want ErrFetch when offline", err)
	}
}

func TestApacheForWindowsComesFromApacheLoungeVerified(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"Apache24/bin/httpd.exe", "Apache24/modules/mod_proxy_fcgi.so", "ReadMe.txt"} {
		w, _ := zw.Create(name)
		w.Write([]byte(name))
	}
	zw.Close()
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	zipPath := "/download/VS18/binaries/httpd-2.4.68-260827-Win64-VS18.zip"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download/":
			w.Write([]byte(`<a href="` + zipPath + `">Win64</a> <a href="/download/VS18/binaries/httpd-2.4.68-260827-win32-vs18.zip">32</a>`))
		case zipPath + ".txt":
			w.Write([]byte("SHA1-Checksum for: x:\r\nAA\r\n\r\nSHA256-Checksum for: httpd.zip:\r\n" +
				strings.ToUpper(hex.EncodeToString(sum[:])) + "\r\n"))
		case zipPath:
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	d := &Downloader{ApacheLounge: srv.URL, GOOS: "windows", GOARCH: "amd64"}
	dest := t.TempDir()
	if err := d.Install(context.Background(), Apache, dest); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, p := range []string{"bin/httpd.exe", "modules/mod_proxy_fcgi.so"} {
		if _, err := os.Stat(filepath.Join(dest, p)); err != nil {
			t.Fatalf("%s missing — the Apache24/ wrapper was not stripped: %v", p, err)
		}
	}
}

func TestNginxOnAMacComesFromHomebrew(t *testing.T) {
	var ran []string
	d := &Downloader{GOOS: "darwin", GOARCH: "arm64", Brew: "/opt/homebrew/bin/brew",
		Run: func(_ context.Context, program string, args ...string) error {
			ran = append([]string{program}, args...)
			return nil
		}}
	if err := d.Install(context.Background(), Nginx, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(ran, " ") != "/opt/homebrew/bin/brew install nginx" {
		t.Fatalf("ran %v", ran)
	}
}

// onlyManager plays a machine whose one package manager is name.
func onlyManager(name string) func(string) (string, error) {
	return func(file string) (string, error) {
		if name != "" && file == name {
			return "/usr/bin/" + file, nil
		}
		return "", errors.New("not found")
	}
}

// features/webserver-install.feature — "Installing Apache on Linux". That the
// package's Apache is then found where it put it is detection's part:
// /usr/sbin/apache2 and /usr/sbin/httpd are its Linux candidates.
func TestInstallingApacheOnLinux(t *testing.T) {
	for manager, want := range map[string]string{
		"apt-get": "apt-get install -y apache2 && { systemctl disable --now apache2",
		"dnf":     "dnf install -y httpd && { systemctl disable --now httpd",
		"zypper":  "zypper --non-interactive install apache2 && { systemctl disable --now apache2",
		"pacman":  "pacman -S --noconfirm --needed apache && { systemctl disable --now httpd",
	} {
		el := elevate.NewFake()
		d := &Downloader{GOOS: "linux", GOARCH: "amd64", Elevator: el, LookPath: onlyManager(manager)}
		dest := t.TempDir()
		if err := d.Install(context.Background(), Apache, dest); err != nil {
			t.Fatalf("%s: %v", manager, err)
		}
		// INFO: Then Wharf asks for the password once and installs the package,
		// and switches the system's own Apache service off.
		if len(el.Runs) != 1 || !strings.Contains(strings.Join(el.Runs[0], " "), want) {
			t.Fatalf("%s: ran %q, want one elevated run of %q", manager, el.Runs, want)
		}
		// INFO: Nothing lands in bin/: the package's Apache is used where it is.
		if entries, _ := os.ReadDir(dest); len(entries) != 0 {
			t.Fatalf("%s: staged %d files, want none", manager, len(entries))
		}
	}
	if !slices.Contains(systemCandidates("linux", Apache), "/usr/sbin/apache2") {
		t.Fatal("Debian's apache2 is not a Linux candidate")
	}
}
