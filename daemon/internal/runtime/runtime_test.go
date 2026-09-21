package runtime

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/layout"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/php"
)

// php-cgi, Windows' FastCGI backend, exits after 500 requests by default, and
// nothing restarts it.
func TestPHPServesWithoutARequestLimit(t *testing.T) {
	root := layout.Root{Dir: t.TempDir()}
	bin := filepath.Join(root.PHPBin("8.3"), php.FastCGIName())
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	spec, err := New(root).PHPSpec(config.Default(), "8.3")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(spec.Env, "PHP_FCGI_MAX_REQUESTS=0") {
		t.Fatalf("env = %v, want PHP_FCGI_MAX_REQUESTS=0", spec.Env)
	}
}

// features/project-logs.feature — "A log that has grown large starts afresh"
func TestAWebserverNamesTheLogsItWrites(t *testing.T) {
	root := layout.Root{Dir: t.TempDir()}
	cfg := config.Default()
	cfg.Projects = []config.Project{{Name: "my-kirby-site"}}
	r := New(root)
	got := r.serverLogs("nginx", cfg.Projects)
	for _, want := range []string{
		filepath.Join(root.LogDir(), "nginx-error.log"),
		filepath.Join(root.LogDir(), "nginx-access.log"),
		filepath.Join(root.ProjectLogDir("my-kirby-site"), "access.log"),
		filepath.Join(root.ProjectLogDir("my-kirby-site"), "error.log"),
	} {
		if !slices.Contains(got, want) {
			t.Fatalf("logs %v do not name %s", got, want)
		}
	}
}
