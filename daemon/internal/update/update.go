// Package update looks up Wharf's newest release on GitHub and downloads the
// file for this system, checked against the release's SHA256SUMS
// (settings.feature, "Checking for updates on request"). It looks only when
// asked, and it never runs what it downloaded.
package update

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/download"
)

// LatestURL is the public releases API's newest release of Wharf.
const LatestURL = "https://api.github.com/repos/kreativ-anders/wharf-dev/releases/latest"

// ReleasesPage is where every release is listed, for when a release names no
// page of its own that can be trusted.
const ReleasesPage = "https://github.com/kreativ-anders/wharf-dev/releases/latest"

// Checksums is the file the release workflow attaches to every release
// (dev/releasing.md §3).
const Checksums = "SHA256SUMS"

// ErrLookup reports a lookup that failed for any reason: offline, rate
// limited, GitHub down, an answer that names no version. None of them is
// something the user can fix other than by trying again later.
var ErrLookup = errors.New("Wharf could not look for updates — try again later")

// ErrFetch reports a release file that could not be downloaded.
var ErrFetch = errors.New("the update could not be downloaded — check your internet connection and try again")

// ErrUnchecked reports a release file that cannot be checked: the release has
// no SHA256SUMS, or no line for the file, or names an address for either that
// is not GitHub's.
var ErrUnchecked = errors.New("the release has no checked file for this system")

// githubHosts are the hosts a release file may come from.
// WARNING: These addresses arrive from the network; without this check they
// decide where the daemon downloads from.
var githubHosts = []string{
	"github.com",
	"objects.githubusercontent.com",
	"release-assets.githubusercontent.com",
}

// Release is the newest release, as the releases API names it.
type Release struct {
	// Version is the tag without its "v": "1.2.0".
	Version string
	// Page is the release's page on GitHub.
	Page string
	// Files maps each attached file's name to where it is downloaded from.
	Files map[string]string
}

// Source looks up the newest release and downloads its files. GitHub is the
// real one; tests supply one that never touches the network.
type Source interface {
	Latest(ctx context.Context) (Release, error)
	Download(ctx context.Context, rel Release, name, dest string) error
}

// GitHub is the public releases API.
type GitHub struct {
	// Client defaults to download.Client.
	Client *http.Client
	// URL defaults to LatestURL; Hosts to GitHub's own. Only tests set them.
	URL   string
	Hosts []string
}

// Latest asks the releases API for the newest release. Any failure is
// ErrLookup.
func (g GitHub) Latest(ctx context.Context) (Release, error) {
	var body struct {
		Tag    string `json:"tag_name"`
		Page   string `json:"html_url"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	api := g.URL
	if api == "" {
		api = LatestURL
	}
	if err := download.JSON(ctx, g.Client, api, &body); err != nil {
		return Release{}, fmt.Errorf("%w (%v)", ErrLookup, err)
	}
	if _, ok := parse(body.Tag); !ok {
		return Release{}, fmt.Errorf("%w (the newest release is tagged %q, not a version)", ErrLookup, body.Tag)
	}
	rel := Release{
		Version: strings.TrimPrefix(strings.TrimSpace(body.Tag), "v"),
		Page:    ReleasesPage,
		Files:   map[string]string{},
	}
	if g.trusted(body.Page) {
		rel.Page = body.Page
	}
	for _, a := range body.Assets {
		// INFO: A file whose address is not GitHub's is left out, not refused:
		// what remains decides whether there is anything to download.
		if a.Name != "" && g.trusted(a.URL) {
			rel.Files[a.Name] = a.URL
		}
	}
	return rel, nil
}

// Download saves the release's file name to dest, but only once it matches
// the release's SHA256SUMS. It is downloaded beside dest and moved into
// place, so dest never holds a file that did not match
// (settings.feature, "A download that does not match is not kept").
func (g GitHub) Download(ctx context.Context, rel Release, name, dest string) error {
	fileURL, sumsURL := rel.Files[name], rel.Files[Checksums]
	if fileURL == "" || sumsURL == "" || !g.trusted(fileURL) || !g.trusted(sumsURL) {
		return ErrUnchecked
	}
	sums, err := download.Text(ctx, g.Client, sumsURL, 64<<10)
	if err != nil {
		return fmt.Errorf("%w (%v)", ErrFetch, err)
	}
	want := ParseChecksums(sums)[name]
	if want == "" {
		return ErrUnchecked
	}
	staged := filepath.Join(filepath.Dir(dest), "."+filepath.Base(dest)+".download")
	if _, err := download.File(ctx, g.Client, fileURL, staged, want); err != nil {
		os.Remove(staged)
		if errors.Is(err, download.ErrUnreachable) {
			return fmt.Errorf("%w (%v)", ErrFetch, err)
		}
		return err
	}
	if err := os.Rename(staged, dest); err != nil {
		os.Remove(staged)
		return fmt.Errorf("could not save %s: %w", filepath.Base(dest), err)
	}
	return nil
}

func (g GitHub) trusted(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	hosts := g.Hosts
	if hosts == nil {
		hosts = githubHosts
	}
	for _, h := range hosts {
		if u.Host == h {
			return true
		}
	}
	return false
}

// Newer reports whether latest is a higher version than current. Anything
// after the version — "+3b2c1ff", "+3b2c1ff.dirty", "+9" — is ignored, so a
// development build of a release is that release. ok is false when either is
// not a version at all, such as an unstamped "dev" build.
func Newer(latest, current string) (newer, ok bool) {
	l, lok := parse(latest)
	c, cok := parse(current)
	if !lok || !cok {
		return false, false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i], true
		}
	}
	return false, true
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "+-"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Suffix is the end of the release file's name for a system, "" where the
// release workflow builds none.
// WARNING: The same suffixes are written by .github/workflows/release.yml and
// listed in dev/releasing.md §3; a rename there without here leaves that
// system with nothing to download.
func Suffix(goos, goarch string) string {
	switch {
	case goos == "darwin":
		// INFO: The DMG holds a universal app, for Apple Silicon and Intel.
		return "-mac.dmg"
	case goos == "linux" && goarch == "amd64":
		return "-linux-x64.AppImage"
	case goos == "windows" && (goarch == "amd64" || goarch == "arm64"):
		// INFO: Windows on Arm runs the x64 installer through its emulation.
		return "-windows-x64-setup.exe"
	}
	return ""
}

// FileFor picks the release's file for a system, "" when it has none or no
// SHA256SUMS to check it against — nothing is guessed.
func FileFor(rel Release, goos, goarch string) string {
	suffix := Suffix(goos, goarch)
	if suffix == "" || rel.Files[Checksums] == "" {
		return ""
	}
	for name := range rel.Files {
		if strings.HasPrefix(name, "Wharf-") && strings.HasSuffix(name, suffix) {
			return name
		}
	}
	return ""
}

// ParseChecksums reads a SHA256SUMS file into file name → lower-case hex.
// Lines that are not a checksum are skipped, so one foreign entry cannot
// block the check of the rest.
func ParseChecksums(content string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		sum, name, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		if b, err := hex.DecodeString(sum); !ok || err != nil || len(b) != 32 {
			continue
		}
		// INFO: A "*" before the name marks sha256sum's binary mode and is not
		// part of the name.
		name = strings.TrimPrefix(strings.TrimSpace(name), "*")
		if name != "" {
			out[name] = strings.ToLower(sum)
		}
	}
	return out
}
