package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/download"
)

// fakeGitHub serves a releases API and its files over TLS on loopback, so
// the https-only rule is exercised without the network.
type fakeGitHub struct {
	srv    *httptest.Server
	tag    string
	status int
	files  map[string][]byte
	// served counts requests per path, to prove what was never fetched.
	served map[string]int
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{tag: "v1.2.0", status: http.StatusOK, files: map[string][]byte{}, served: map[string]int{}}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.served[r.URL.Path]++
		if r.URL.Path == "/latest" {
			if f.status != http.StatusOK {
				w.WriteHeader(f.status)
				return
			}
			type asset struct {
				Name string `json:"name"`
				URL  string `json:"browser_download_url"`
			}
			body := struct {
				Tag    string  `json:"tag_name"`
				Page   string  `json:"html_url"`
				Assets []asset `json:"assets"`
			}{Tag: f.tag, Page: f.srv.URL + "/page"}
			for name := range f.files {
				body.Assets = append(body.Assets, asset{name, f.srv.URL + "/files/" + name})
			}
			json.NewEncoder(w).Encode(body)
			return
		}
		b, ok := f.files[filepath.Base(r.URL.Path)]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) source() GitHub {
	u, _ := url.Parse(f.srv.URL)
	return GitHub{Client: f.srv.Client(), URL: f.srv.URL + "/latest", Hosts: []string{u.Host}}
}

// release adds a file and a SHA256SUMS that lists it with sum, or with its
// real checksum when sum is empty.
func (f *fakeGitHub) release(name string, body []byte, sum string) {
	if sum == "" {
		h := sha256.Sum256(body)
		sum = hex.EncodeToString(h[:])
	}
	f.files[name] = body
	f.files[Checksums] = []byte(sum + "  " + name + "\n")
}

// features/settings.feature — "Checking for updates on request"
func TestLatestNamesTheNewestRelease(t *testing.T) {
	f := newFakeGitHub(t)
	f.release("Wharf-1.2.0-mac.dmg", []byte("dmg"), "")

	rel, err := f.source().Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "1.2.0" {
		t.Errorf("version = %q, want 1.2.0 without the tag's v", rel.Version)
	}
	if rel.Page != f.srv.URL+"/page" {
		t.Errorf("page = %q", rel.Page)
	}
	if rel.Files["Wharf-1.2.0-mac.dmg"] == "" || rel.Files[Checksums] == "" {
		t.Errorf("files = %v, want the DMG and SHA256SUMS", rel.Files)
	}
}

// features/settings.feature — "Checking for updates on request"
func TestNewerIgnoresWhatFollowsTheVersion(t *testing.T) {
	for _, c := range []struct {
		latest, current string
		newer           bool
	}{
		{"v1.2.0", "1.1.9", true},
		{"v1.2.0", "1.2.0", false},
		{"v1.2.0", "1.2.0+3b2c1ff", false},
		{"v1.2.0", "1.2.0+3b2c1ff.dirty", false},
		{"v1.2.0", "1.10.0", false},
		{"v2.0.0", "1.99.99+7", true},
		{"1.2.1", "v1.2.0", true},
	} {
		newer, ok := Newer(c.latest, c.current)
		if !ok || newer != c.newer {
			t.Errorf("Newer(%q, %q) = %v, %v; want %v, true", c.latest, c.current, newer, ok, c.newer)
		}
	}
	for _, current := range []string{"dev", "", "1.2", "1.2.x"} {
		if _, ok := Newer("v1.2.0", current); ok {
			t.Errorf("Newer(v1.2.0, %q) compared a build that has no version", current)
		}
	}
}

// features/settings.feature — "A failed update check says to try again later"
func TestAFailedLookupIsErrLookup(t *testing.T) {
	for name, adjust := range map[string]func(*fakeGitHub){
		"rate limited":  func(f *fakeGitHub) { f.status = http.StatusForbidden },
		"no release":    func(f *fakeGitHub) { f.status = http.StatusNotFound },
		"not a version": func(f *fakeGitHub) { f.tag = "nightly" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeGitHub(t)
			adjust(f)
			if _, err := f.source().Latest(context.Background()); !errors.Is(err, ErrLookup) {
				t.Fatalf("err = %v, want ErrLookup", err)
			}
		})
	}
	t.Run("offline", func(t *testing.T) {
		f := newFakeGitHub(t)
		src := f.source()
		f.srv.Close()
		if _, err := src.Latest(context.Background()); !errors.Is(err, ErrLookup) {
			t.Fatalf("err = %v, want ErrLookup", err)
		}
	})
}

// features/settings.feature — "Downloading an update"
func TestDownloadSavesAFileThatMatches(t *testing.T) {
	f := newFakeGitHub(t)
	f.release("Wharf-1.2.0-mac.dmg", []byte("the new app"), "")
	src := f.source()
	rel, err := src.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "Wharf-1.2.0-mac.dmg")

	if err := src.Download(context.Background(), rel, "Wharf-1.2.0-mac.dmg", dest); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(dest); err != nil || string(b) != "the new app" {
		t.Fatalf("saved %q, %v", b, err)
	}
	if left := entries(t, dir); len(left) != 1 {
		t.Errorf("folder holds %v, want the file alone", left)
	}
}

// features/settings.feature — "A download that does not match is not kept"
func TestDownloadKeepsNothingThatDoesNotMatch(t *testing.T) {
	f := newFakeGitHub(t)
	f.release("Wharf-1.2.0-mac.dmg", []byte("altered"), hex.EncodeToString(make([]byte, 32)))
	src := f.source()
	rel, err := src.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	err = src.Download(context.Background(), rel, "Wharf-1.2.0-mac.dmg", filepath.Join(dir, "Wharf.dmg"))
	if !errors.Is(err, download.ErrChecksum) {
		t.Fatalf("err = %v, want a checksum mismatch", err)
	}
	if left := entries(t, dir); len(left) != 0 {
		t.Errorf("folder holds %v, want nothing: no file, no partial file", left)
	}
}

// features/settings.feature — "A release without a checked file for this
// system offers its page"
func TestNothingUncheckedIsDownloaded(t *testing.T) {
	t.Run("no SHA256SUMS", func(t *testing.T) {
		f := newFakeGitHub(t)
		f.files["Wharf-1.2.0-mac.dmg"] = []byte("dmg")
		src := f.source()
		rel, _ := src.Latest(context.Background())
		if got := FileFor(rel, "darwin", "arm64"); got != "" {
			t.Errorf("FileFor = %q, want none without SHA256SUMS", got)
		}
		err := src.Download(context.Background(), rel, "Wharf-1.2.0-mac.dmg", filepath.Join(t.TempDir(), "x"))
		if !errors.Is(err, ErrUnchecked) || f.served["/files/Wharf-1.2.0-mac.dmg"] != 0 {
			t.Errorf("err = %v, fetched %d times; want ErrUnchecked and no fetch", err, f.served["/files/Wharf-1.2.0-mac.dmg"])
		}
	})
	t.Run("not listed in SHA256SUMS", func(t *testing.T) {
		f := newFakeGitHub(t)
		f.release("Wharf-1.2.0-linux-x64.AppImage", []byte("appimage"), "")
		f.files["Wharf-1.2.0-mac.dmg"] = []byte("dmg")
		src := f.source()
		rel, _ := src.Latest(context.Background())
		err := src.Download(context.Background(), rel, "Wharf-1.2.0-mac.dmg", filepath.Join(t.TempDir(), "x"))
		if !errors.Is(err, ErrUnchecked) || f.served["/files/Wharf-1.2.0-mac.dmg"] != 0 {
			t.Errorf("err = %v, want ErrUnchecked and no fetch", err)
		}
	})
	t.Run("an address that is not GitHub's", func(t *testing.T) {
		f := newFakeGitHub(t)
		f.release("Wharf-1.2.0-mac.dmg", []byte("dmg"), "")
		src := f.source()
		src.Hosts = []string{"github.com"}
		rel, err := src.Latest(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(rel.Files) != 0 || rel.Page != ReleasesPage {
			t.Errorf("release = %+v, want no files and Wharf's own releases page", rel)
		}
	})
}

// features/settings.feature — "Downloading an update"
func TestFileForPicksThisSystemsFile(t *testing.T) {
	rel := Release{Files: map[string]string{
		Checksums:                           "x",
		"Wharf-1.2.0-mac.dmg":               "x",
		"Wharf-1.2.0-linux-x64.AppImage":    "x",
		"Wharf-1.2.0-windows-x64-setup.exe": "x",
	}}
	for _, c := range []struct{ goos, goarch, want string }{
		{"darwin", "arm64", "Wharf-1.2.0-mac.dmg"},
		{"darwin", "amd64", "Wharf-1.2.0-mac.dmg"},
		{"linux", "amd64", "Wharf-1.2.0-linux-x64.AppImage"},
		{"linux", "arm64", ""},
		{"windows", "amd64", "Wharf-1.2.0-windows-x64-setup.exe"},
		{"windows", "arm64", "Wharf-1.2.0-windows-x64-setup.exe"},
		{"freebsd", "amd64", ""},
	} {
		if got := FileFor(rel, c.goos, c.goarch); got != c.want {
			t.Errorf("FileFor(%s/%s) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

func TestParseChecksums(t *testing.T) {
	sum := "ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	got := ParseChecksums("# comment\r\n" + sum + " *Wharf-1.2.0-mac.dmg\r\nnot a line\n" + sum[:10] + "  short\n")
	if len(got) != 1 || got["Wharf-1.2.0-mac.dmg"] != "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789" {
		t.Fatalf("got %v", got)
	}
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}
