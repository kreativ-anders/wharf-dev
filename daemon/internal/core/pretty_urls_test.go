package core

import (
	"os"
	"strings"
	"testing"
)

// features/pretty-urls.feature — "Every project is reachable under its own
// .localhost name"
func TestEveryProjectIsReachableUnderItsOwnLocalhostName(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mustAdd("legacy-app")
	apache := "apache"
	if _, err := h.d.UpdateSettings(h.ctx(), "legacy-app", Settings{Webserver: &apache}); err != nil {
		t.Fatal(err)
	}

	// INFO: Then it is reachable at "http://my-kirby-site.localhost"
	// And no port is part of that URL, whichever webserver serves it
	for name, want := range map[string]string{
		"my-kirby-site": "http://my-kirby-site.localhost",
		"legacy-app":    "http://legacy-app.localhost",
	} {
		if got := h.project(name).URL; got != want {
			t.Fatalf("%s URL = %q, want %q", name, got, want)
		}
	}
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	block := vhostBlock(t, h.readGenerated("nginx.conf"), "my-kirby-site.localhost")
	if !strings.Contains(block, "listen 80;") {
		t.Fatalf("the project is not served on port 80:\n%s", block)
	}
}

// features/pretty-urls.feature — "Adding a project never asks for a password"
func TestAddingAProjectNeverAsksForAPassword(t *testing.T) {
	h := newHarness(t)
	before := h.hostsContent()

	h.mustAdd("my-kirby-site")
	if _, err := h.d.Scaffold(h.ctx(), "empty", "blank", ""); err != nil {
		t.Fatal(err)
	}

	// INFO: Then the hosts file is not written
	if got := h.hostsContent(); got != before {
		t.Fatalf("the hosts file changed:\n%s", got)
	}
	// INFO: And no elevation prompt is shown
	if len(h.el.Writes) != 0 {
		t.Fatalf("%d elevation prompts were raised", len(h.el.Writes))
	}
}

// features/pretty-urls.feature — "The webserver answers over IPv6 as well as
// IPv4". Whether Safari then reaches it was checked against real nginx and
// Apache; this pins the listeners that make it so.
func TestTheWebserverAnswersOverIPv6AsWellAsIPv4(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}

	block := vhostBlock(t, h.readGenerated("nginx.conf"), "my-kirby-site.localhost")
	for _, want := range []string{"listen 80;", "listen [::]:80;"} {
		if !strings.Contains(block, want) {
			t.Fatalf("nginx lacks %q:\n%s", want, block)
		}
	}
	// INFO: Apache's plain "Listen 80" binds every address family.
	if err := h.d.SetWebserver(h.ctx(), "apache"); err != nil {
		t.Fatal(err)
	}
	if conf := h.readGenerated("apache.conf"); !strings.Contains(conf, "\nListen 80\n") {
		t.Fatalf("apache does not listen on every address:\n%s", conf)
	}
}

// addedUnderTheOldDomain gives projects the hosts line they got when projects
// were published as <name>.wharf.
func addedUnderTheOldDomain(h *harness, names ...string) {
	h.t.Helper()
	lines := h.hostsContent()
	for _, n := range names {
		lines += "127.0.0.1\t" + n + ".wharf\t# wharf:" + n + "\n"
	}
	if err := os.WriteFile(h.hosts, []byte(lines), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// features/pretty-urls.feature — "Removing a project added under the old
// domain removes its hosts entry"
func TestRemovingAProjectAddedUnderTheOldDomainRemovesItsHostsEntry(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mustAdd("other-site")
	addedUnderTheOldDomain(h, "my-kirby-site", "other-site")

	if err := h.d.RemoveProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatalf("remove project: %v", err)
	}

	// INFO: Then that hosts entry is deleted through one elevation prompt
	if len(h.el.Writes) != 1 {
		t.Fatalf("%d elevation prompts, want 1", len(h.el.Writes))
	}
	after := h.hostsContent()
	if strings.Contains(after, "my-kirby-site.wharf") {
		t.Fatalf("entry survived removal:\n%s", after)
	}
	// INFO: And no other line of the hosts file is affected
	if !strings.Contains(after, "other-site.wharf") || !strings.Contains(after, "127.0.0.1\tlocalhost") {
		t.Fatalf("other lines were removed too:\n%s", after)
	}
	// INFO: The folder itself is left alone: the folder is the project.
	if _, err := os.Stat(h.root.ProjectDir("my-kirby-site")); err != nil {
		t.Fatalf("project folder was deleted: %v", err)
	}
}

func TestDeclinedElevationOnRemoveDoesNotFail(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	addedUnderTheOldDomain(h, "my-kirby-site")
	h.el.Decline = true

	if err := h.d.RemoveProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatalf("declined elevation must not block unregistering: %v", err)
	}
	if _, ok := h.d.Config().Project("my-kirby-site"); ok {
		t.Fatal("project is still registered")
	}
}

func TestRemovingANewProjectDoesNotPrompt(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	if err := h.d.RemoveProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	if len(h.el.Writes) != 0 {
		t.Fatal("removing a project with no hosts entry raised an elevation prompt")
	}
}
