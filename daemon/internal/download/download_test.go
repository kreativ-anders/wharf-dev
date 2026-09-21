package download

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func serve(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestFileVerifiesItsChecksum(t *testing.T) {
	body := []byte("a build")
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:])
	url := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.Write(body) })
	dest := filepath.Join(t.TempDir(), "build")

	if got, err := File(context.Background(), nil, url, dest, want); err != nil || got != want {
		t.Fatalf("File = %q, %v", got, err)
	}

	_, err := File(context.Background(), nil, url, dest, strings.Repeat("0", 64))
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("File with a wrong checksum = %v, want a mismatch", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("a download that failed its checksum was left behind")
	}
}

func TestACutConnectionIsUnreachable(t *testing.T) {
	url := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.Write([]byte("only part of it"))
	})
	dest := filepath.Join(t.TempDir(), "build")
	if _, err := File(context.Background(), nil, url, dest, ""); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("File = %v, want ErrUnreachable", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("a cut download was left behind")
	}
}

func TestUntarGzFlattensAndStaysInside(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range []struct{ name, body string }{
		{"buildroot/bin/php", "php"},
		{"x/..", "escape"},
		{"buildroot/README", "readme"},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(e.body))
	}
	tw.Close()
	gz.Close()

	base := t.TempDir()
	archive := filepath.Join(base, "a.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(base, "out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := UntarGz(archive, dest, "php", ".."); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 1 || entries[0].Name() != "php" {
		t.Fatalf("extracted %v, want php alone", entries)
	}
	if entries, _ := os.ReadDir(base); len(entries) != 2 {
		t.Fatalf("%s holds %v, want only the archive and out/", base, entries)
	}
}

func TestUnzipRefusesAnEntryOutsideItsFolder(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("../escaped.txt")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("escape"))
	zw.Close()

	base := t.TempDir()
	archive := filepath.Join(base, "a.zip")
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	err = Unzip(archive, filepath.Join(base, "out"))
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("Unzip = %v, want an unsafe path refused", err)
	}
	if _, err := os.Stat(filepath.Join(base, "escaped.txt")); !os.IsNotExist(err) {
		t.Fatal("an entry was written outside the destination")
	}
}
