package core

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/runtime"
)

// features/php-settings.feature — "Editing PHP settings"
func TestEditingPHPSettings(t *testing.T) {
	h := newHarness(t)

	path, err := h.d.PHPSettings(h.ctx())
	if err != nil {
		t.Fatalf("create php.ini: %v", err)
	}

	// INFO: Then "config/php.ini" is created with a commented starting point
	if want := filepath.Join(h.root.Dir, "config", "php.ini"); path != want {
		t.Fatalf("path = %s, want %s", path, want)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line != "" && !strings.HasPrefix(line, ";") {
			t.Fatalf("the starting point has a live setting %q", line)
		}
	}
	if php := h.d.State().Services.PHP; php.Settings != path || !php.SettingsExist {
		t.Fatalf("snapshot does not show the file: %q exists=%v", php.Settings, php.SettingsExist)
	}

	// INFO: And every PHP version Wharf runs reads it after its own php.ini: the
	// scan directory is added to the one PHP was built with, not swapped in.
	h.mustAdd("my-kirby-site")
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	id := runtime.PHPServiceID(h.d.Config().Services.PHP.Version)
	spec, ok := h.runner.LastSpec(id)
	want := "PHP_INI_SCAN_DIR=" + string(os.PathListSeparator) + h.root.Config()
	if !ok || !slices.Contains(spec.Env, want) {
		t.Fatalf("%s started with env %v, want %s", id, spec.Env, want)
	}

	// INFO: Asking again returns the same file and leaves the user's edits alone.
	os.WriteFile(path, []byte("memory_limit = 1G\n"), 0o644)
	if again, _ := h.d.PHPSettings(h.ctx()); again != path {
		t.Fatalf("second call returned %s", again)
	}
	if body, _ := os.ReadFile(path); string(body) != "memory_limit = 1G\n" {
		t.Fatal("an existing php.ini was overwritten")
	}
}

// features/php-settings.feature — "Saving PHP settings applies them"
func TestSavingPHPSettingsAppliesThem(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	path, err := h.d.PHPSettings(h.ctx())
	if err != nil {
		t.Fatal(err)
	}
	php := runtime.PHPServiceID(h.d.Config().Services.PHP.Version)
	beforePHP, beforeWeb := h.countStarts(php), h.countStarts(runtimeWebserverID)

	// INFO: Nothing changed since the file was created: no restart.
	h.d.ApplyPHPSettings(h.ctx())
	if got := h.countStarts(php); got != beforePHP {
		t.Fatalf("an unchanged php.ini restarted PHP (%d → %d)", beforePHP, got)
	}

	// INFO: When the user saves a change to "config/php.ini"
	if err := os.WriteFile(path, []byte("memory_limit = 1G\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	os.Chtimes(path, later, later)
	h.d.ApplyPHPSettings(h.ctx())

	// INFO: Then every running PHP backend is restarted with the change
	if got := h.countStarts(php); got != beforePHP+1 {
		t.Fatalf("PHP started %d times after the save, want %d", got, beforePHP+1)
	}
	if !h.sup.Running(php) {
		t.Fatal("PHP not running after the restart")
	}
	// INFO: And no webserver is restarted
	if got := h.countStarts(runtimeWebserverID); got != beforeWeb {
		t.Fatalf("the webserver was restarted (%d → %d)", beforeWeb, got)
	}
}
