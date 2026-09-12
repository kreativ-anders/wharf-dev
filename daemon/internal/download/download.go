// Package download fetches files over HTTPS for what Wharf installs on the
// user's behalf: PHP builds, webservers and mkcert. Callers wrap ErrUnreachable
// in their own sentence, so the GUI says what could not be fetched rather
// than showing a transport error.
package download

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrUnreachable reports a network failure or a non-200 response.
var ErrUnreachable = errors.New("could not be fetched — check your internet connection")

// Client is the HTTP client used when a caller supplies none. Builds are tens
// of megabytes, so the timeout is generous.
var Client = &http.Client{Timeout: 10 * time.Minute}

func get(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	if client == nil {
		client = Client
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// Some download sites turn away Go's default agent as a bot.
	req.Header.Set("User-Agent", "Wharf (+https://github.com/kreativ-anders/wharf-dev)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%w: %s returned %s", ErrUnreachable, url, resp.Status)
	}
	return resp, nil
}

// JSON decodes the document at url into out.
func JSON(ctx context.Context, client *http.Client, url string, out any) error {
	resp, err := get(ctx, client, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%w: %s is not valid JSON: %v", ErrUnreachable, url, err)
	}
	return nil
}

// File saves the body at url to dest and returns its checksum. want is a hex
// SHA-256, or "sha1:<hex>" where a source publishes nothing stronger. When
// want is non-empty a mismatch is an error and dest is removed: a binary
// downloaded from the internet is untrusted until it matches.
func File(ctx context.Context, client *http.Client, url, dest, want string) (string, error) {
	resp, err := get(ctx, client, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	out, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if algo, sum, ok := strings.Cut(want, ":"); ok && algo == "sha1" {
		h, want = sha1.New(), sum
	}
	if _, err := io.Copy(io.MultiWriter(out, h), resp.Body); err != nil {
		out.Close()
		os.Remove(dest)
		return "", fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if err := out.Close(); err != nil {
		os.Remove(dest)
		return "", err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if want != "" && !strings.EqualFold(sum, want) {
		os.Remove(dest)
		return "", fmt.Errorf("checksum mismatch for %s: got %s, want %s", url, sum, want)
	}
	return sum, nil
}

// UntarGz extracts the regular files of a .tar.gz into destDir, flattening
// any folders, and keeping only the names in keep when keep is non-empty.
func UntarGz(archive, destDir string, keep ...string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s is not a gzip archive: %w", filepath.Base(archive), err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Base(hdr.Name)
		if len(keep) > 0 && !contains(keep, name) {
			continue
		}
		if err := writeFile(filepath.Join(destDir, name), tr, os.FileMode(hdr.Mode).Perm()|0o600); err != nil {
			return err
		}
	}
}

// Unzip extracts a zip into destDir, preserving its folders and refusing any
// entry that would land outside destDir.
func Unzip(archive, destDir string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("%s is not a zip archive: %w", filepath.Base(archive), err)
	}
	defer zr.Close()

	clean := filepath.Clean(destDir) + string(os.PathSeparator)
	for _, f := range zr.File {
		target := filepath.Join(destDir, filepath.FromSlash(f.Name))
		if !strings.HasPrefix(target, clean) {
			return fmt.Errorf("archive contains an unsafe path %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		err = writeFile(target, rc, mode)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFile(path string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
