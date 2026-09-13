package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// features/project-logs.feature — "Each project writes its own logs"
func TestEachProjectWritesItsOwnLogs(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mustAdd("other-site")

	// INFO: When they are started, both on the global nginx
	for _, name := range []string{"my-kirby-site", "other-site"} {
		if err := h.d.StartProject(h.ctx(), name); err != nil {
			t.Fatal(err)
		}
	}

	// INFO: Then each logs requests and errors to data/log/projects/<name>/
	conf := h.readGenerated("nginx.conf")
	for _, name := range []string{"my-kirby-site", "other-site"} {
		dir := h.root.ProjectLogDir(name)
		if want := filepath.Join(h.root.Dir, "data", "log", "projects", name); dir != want {
			t.Fatalf("log folder = %s, want %s", dir, want)
		}
		block := vhostBlock(t, conf, name+".localhost")
		for _, want := range []string{
			`access_log "` + filepath.ToSlash(filepath.Join(dir, "access.log")) + `";`,
			`error_log "` + filepath.ToSlash(filepath.Join(dir, "error.log")) + `";`,
		} {
			if !strings.Contains(block, want) {
				t.Fatalf("%s's server block lacks %s:\n%s", name, want, block)
			}
		}
		// INFO: nginx does not create a log folder; one missing keeps it from starting.
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("%s does not exist before nginx starts: %v", dir, err)
		}
		if got := h.project(name).LogDir; got != dir {
			t.Fatalf("snapshot log_dir = %q, want %q", got, dir)
		}
	}

	// INFO: And "other-site" logs to its own folder, not to "my-kirby-site"'s
	mine := filepath.ToSlash(h.root.ProjectLogDir("my-kirby-site"))
	if strings.Contains(vhostBlock(t, conf, "other-site.localhost"), mine) {
		t.Fatal("other-site logs into my-kirby-site's folder")
	}
}

// features/project-logs.feature — "A project keeps its logs on either
// webserver"
func TestAProjectKeepsItsLogsOnEitherWebserver(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mustAdd("other-site")
	apache := "apache"
	if _, err := h.d.UpdateSettings(h.ctx(), "my-kirby-site", Settings{Webserver: &apache}); err != nil {
		t.Fatal(err)
	}

	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}

	// INFO: Then its apache writes to data/log/projects/my-kirby-site/ as well
	dir := h.root.ProjectLogDir("my-kirby-site")
	slashed := filepath.ToSlash(dir)
	own := h.readGenerated("project-my-kirby-site-apache.conf")
	for _, want := range []string{
		`ErrorLog "` + slashed + `/error.log"`,
		`CustomLog "` + slashed + `/access.log" common`,
	} {
		if !strings.Contains(own, want) {
			t.Fatalf("apache config lacks %s:\n%s", want, own)
		}
	}
	// INFO: A request the front door cannot hand on is this project's error too.
	front := vhostBlock(t, h.readGenerated("nginx.conf"), "my-kirby-site.localhost")
	if !strings.Contains(front, `error_log "`+slashed+`/error.log";`) {
		t.Fatalf("the front door does not log my-kirby-site's errors to its folder:\n%s", front)
	}
	// INFO: Its own instance's output — the startup error that names a broken
	// custom config — lands there too.
	spec, ok := h.runner.LastSpec(projectServiceID("my-kirby-site"))
	if !ok || filepath.Dir(spec.LogPath) != dir {
		t.Fatalf("own instance logs to %q, want a file in %s", spec.LogPath, dir)
	}
}
