package elevate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// features/pretty-urls.feature — "Removing a project added under the old
// domain removes its hosts entry": on macOS the one prompt that writes the
// hosts file also restarts mDNSResponder, so it re-reads the file. The script is run here without the prompt, against a scratch
// file, with the resolver commands swapped for harmless stand-ins.
func TestTheHostsWriteAlsoRefreshesTheResolver(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "it's staged")
	dst := filepath.Join(dir, "hosts")
	os.WriteFile(src, []byte("127.0.0.1\tsite.wharf\n"), 0o644)

	script := writeScript(src, dst)
	for _, want := range []string{"dscacheutil -flushcache", "killall mDNSResponder"} {
		if !strings.Contains(script, want) {
			t.Fatalf("script does not run %q:\n%s", want, script)
		}
	}

	// The copy must survive quoting of an awkward path, and a refresh that
	// fails must not turn a successful write into an error.
	dry := strings.NewReplacer("/usr/bin/dscacheutil", "/usr/bin/false", "/usr/bin/killall", "/usr/bin/false").Replace(script)
	if out, err := exec.Command("/bin/sh", "-c", dry).CombinedOutput(); err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	if body, _ := os.ReadFile(dst); string(body) != "127.0.0.1\tsite.wharf\n" {
		t.Fatalf("hosts = %q", body)
	}
}
