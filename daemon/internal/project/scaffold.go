package project

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/layout"
)

// Template is a quick-app starting point (quick-app-php.feature). v1
// registers Kirby and an empty folder; the WordPress and Laravel entries in
// that feature file are roadmap and deliberately absent here.
type Template struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Runtime is informational in v1, where PHP is the only runtime.
	Runtime string `json:"runtime"`
	// ZipURL is a zip archive whose single top-level folder becomes the
	// project folder. Empty for a template that downloads nothing.
	ZipURL string `json:"-"`
}

// Templates is the v1 registry.
//
// TODO(generic-templates): Kirby was the inspiration, not the target. The
// Kirby starter kit here and the Kirby rules hard-wired into the generated
// nginx and Apache configs (runtime.kirbyRules, runtime.apacheSite) go once
// templates are generic: each template brings its own document root and
// rewrite recipe, and a folder added by hand gets a neutral default.
var Templates = []Template{
	{
		ID:      "kirby",
		Name:    "Kirby",
		Runtime: "php",
		ZipURL:  "https://github.com/getkirby/starterkit/archive/refs/heads/main.zip",
	},
	{
		ID:      "empty",
		Name:    "Empty folder",
		Runtime: "php",
	},
}

// emptyIndex is the one file an empty project starts with: enough to see the
// project served, and by what (quick-app-php.feature, "Creating an empty
// project").
const emptyIndex = `<?php
// A new Wharf project. Replace this file with your own.
$name = htmlspecialchars(basename(__DIR__));
$server = htmlspecialchars($_SERVER['SERVER_SOFTWARE'] ?? 'a webserver');
?>
<!doctype html>
<title><?= $name ?></title>
<h1><?= $name ?></h1>
<p>Served by <?= $server ?> with PHP <?= PHP_VERSION ?>.</p>
`

// TemplateByID looks up a registered template.
func TemplateByID(id string) (Template, bool) {
	for _, t := range Templates {
		if t.ID == id {
			return t, true
		}
	}
	return Template{}, false
}

// ErrOffline reports that the template could not be fetched. The GUI shows
// this verbatim rather than a transport error.
var ErrOffline = errors.New("template could not be fetched — check your internet connection")

// Fetcher downloads a template archive into an empty directory. It is an
// interface so scaffolding can be tested without network access.
type Fetcher interface {
	Fetch(ctx context.Context, url, destDir string) error
}

// HTTPFetcher downloads a zip over HTTP and unpacks it, stripping the
// archive's single top-level folder.
type HTTPFetcher struct {
	Client *http.Client
}

func (f HTTPFetcher) Fetch(ctx context.Context, url, destDir string) error {
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrOffline, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: server returned %s", ErrOffline, resp.Status)
	}

	// INFO: zip.NewReader needs random access, so the archive is buffered to a
	// temp file rather than held in memory.
	tmp, err := os.CreateTemp("", "wharf-template-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	size, err := io.Copy(tmp, resp.Body)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrOffline, err)
	}
	zr, err := zip.NewReader(tmp, size)
	if err != nil {
		return fmt.Errorf("template archive is not a valid zip: %w", err)
	}
	return unzipStripRoot(zr, destDir)
}

// Scaffolder creates new projects from templates.
type Scaffolder struct {
	Root    layout.Root
	Fetcher Fetcher
	// URLOverrides replaces a template's archive URL by template ID. It exists
	// so a test — or a user on a mirror — can point a template somewhere else
	// without editing the registry.
	URLOverrides map[string]string
}

// NewScaffolder returns a Scaffolder that downloads over HTTP.
func NewScaffolder(root layout.Root) *Scaffolder {
	return &Scaffolder{Root: root, Fetcher: HTTPFetcher{}}
}

// Create scaffolds a project. The template is unpacked into a staging folder
// and only moved into www/ once it is complete, so a failed fetch leaves no
// partial project folder behind (quick-app-php.feature).
func (s *Scaffolder) Create(ctx context.Context, tpl Template, name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	dest := s.Root.ProjectDir(name)
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("a folder named %q already exists in www/", name)
	}
	if err := os.MkdirAll(s.Root.WWW(), 0o755); err != nil {
		return err
	}

	// INFO: Staged next to the destination so the final move is a rename within one
	// filesystem, not a copy that can fail halfway.
	staging, err := os.MkdirTemp(s.Root.WWW(), ".wharf-new-"+name+"-*")
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(staging)
		}
	}()

	url := tpl.ZipURL
	if alt, ok := s.URLOverrides[tpl.ID]; ok && alt != "" {
		url = alt
	}
	if url == "" {
		if err := os.WriteFile(filepath.Join(staging, "index.php"), []byte(emptyIndex), 0o644); err != nil {
			return err
		}
	} else if err := s.Fetcher.Fetch(ctx, url, staging); err != nil {
		return err
	}
	if err := os.Rename(staging, dest); err != nil {
		return fmt.Errorf("move project into www/: %w", err)
	}
	cleanup = false
	return nil
}

// unzipStripRoot extracts an archive, dropping its single top-level directory
// (GitHub archives wrap everything in "<repo>-<ref>/").
func unzipStripRoot(zr *zip.Reader, destDir string) error {
	root := commonRoot(zr)
	for _, f := range zr.File {
		rel := strings.TrimPrefix(f.Name, root)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			continue
		}
		target, err := safeJoin(destDir, rel)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := writeZipEntry(f, target); err != nil {
			return err
		}
	}
	return nil
}

func writeZipEntry(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	mode := f.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

// commonRoot returns the archive's single top-level directory, or "" if its
// entries do not share one.
func commonRoot(zr *zip.Reader) string {
	root := ""
	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, "./")
		idx := strings.Index(name, "/")
		if idx < 0 {
			// INFO: A file at the archive root means there is nothing to strip.
			return ""
		}
		top := name[:idx]
		if root == "" {
			root = top
			continue
		}
		if top != root {
			return ""
		}
	}
	return root
}

// safeJoin refuses archive entries that would escape the destination — a zip
// downloaded from the internet is untrusted input.
func safeJoin(dest, rel string) (string, error) {
	target := filepath.Join(dest, filepath.FromSlash(rel))
	cleanDest := filepath.Clean(dest) + string(os.PathSeparator)
	if !strings.HasPrefix(target, cleanDest) {
		return "", fmt.Errorf("template archive contains an unsafe path %q", rel)
	}
	return target, nil
}
