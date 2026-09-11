package core

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// features/pretty-urls.feature — "Creating a project registers a hosts entry"
func TestCreatingAProjectRegistersAHostsEntry(t *testing.T) {
	h := newHarness(t)
	h.mkProject("my-kirby-site")

	p, err := h.d.AddProject(h.ctx(), "my-kirby-site")
	if err != nil {
		t.Fatalf("add project: %v", err)
	}

	// Then an elevation prompt is shown via RequestElevatedWrite
	if _, prompted := h.el.Content(h.hosts); !prompted {
		t.Fatal("no elevated write was requested for the hosts file")
	}

	// And, once approved, a hosts entry "127.0.0.1 my-kirby-site.wharf" is written
	entry := hostsLineFor(t, h.hostsContent(), "my-kirby-site.wharf")
	if !strings.HasPrefix(entry, "127.0.0.1") {
		t.Fatalf("hosts entry = %q, want it to map to 127.0.0.1", entry)
	}

	// And the project is reachable at "http://my-kirby-site.wharf"
	if p.URL != "http://my-kirby-site.wharf" {
		t.Fatalf("URL = %q, want http://my-kirby-site.wharf", p.URL)
	}
	if !p.HostsEntry {
		t.Fatal("project is not recorded as having a hosts entry")
	}
}

// features/pretty-urls.feature — "Elevation is declined"
func TestElevationDeclined(t *testing.T) {
	h := newHarness(t)
	h.el.Decline = true
	h.mkProject("my-kirby-site")

	p, err := h.d.AddProject(h.ctx(), "my-kirby-site")
	if err != nil {
		t.Fatalf("a declined prompt must not fail the add: %v", err)
	}

	// Then no hosts entry is written
	if strings.Contains(h.hostsContent(), "my-kirby-site.wharf") {
		t.Fatal("a hosts entry was written despite the declined prompt")
	}
	if p.HostsEntry {
		t.Fatal("project claims a hosts entry it does not have")
	}

	// And the project remains reachable only via its raw port
	// And the GUI shows the fallback URL instead of the pretty URL
	if p.PrettyURL != "" {
		t.Fatalf("pretty URL = %q, want none", p.PrettyURL)
	}
	if p.Port == 0 {
		t.Fatal("project has no raw port to fall back to")
	}
	if p.URL != p.FallbackURL || !strings.Contains(p.URL, "127.0.0.1:") {
		t.Fatalf("URL = %q, want the raw-port fallback %q", p.URL, p.FallbackURL)
	}

	// The generated config must actually listen on that port, or the fallback
	// URL is a promise the daemon does not keep.
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	conf := h.readGenerated("nginx.conf")
	if !strings.Contains(conf, "listen "+strconv.Itoa(p.Port)+";") {
		t.Fatalf("nginx does not listen on the fallback port %d:\n%s", p.Port, conf)
	}
}

// features/pretty-urls.feature — "Removing a project removes its hosts entry"
func TestRemovingAProjectRemovesItsHostsEntry(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mustAdd("other-site")

	before := h.hostsContent()
	if !strings.Contains(before, "my-kirby-site.wharf") || !strings.Contains(before, "other-site.wharf") {
		t.Fatalf("both entries should exist first:\n%s", before)
	}

	if err := h.d.RemoveProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatalf("remove project: %v", err)
	}

	// Then the corresponding hosts entry is deleted
	after := h.hostsContent()
	if strings.Contains(after, "my-kirby-site.wharf") {
		t.Fatalf("entry survived removal:\n%s", after)
	}

	// And no other project's hosts entries are affected
	if !strings.Contains(after, "other-site.wharf") {
		t.Fatalf("another project's entry was removed too:\n%s", after)
	}
	// Nor is anything the user put there by hand.
	if !strings.Contains(after, "127.0.0.1\tlocalhost") {
		t.Fatalf("pre-existing hosts content was lost:\n%s", after)
	}

	// The folder itself is left alone: the folder is the project.
	if _, err := os.Stat(h.root.ProjectDir("my-kirby-site")); err != nil {
		t.Fatalf("project folder was deleted: %v", err)
	}
}

func TestDeclinedElevationOnRemoveDoesNotFail(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.el.Decline = true

	if err := h.d.RemoveProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatalf("declined elevation must not block unregistering: %v", err)
	}
	if _, ok := h.d.Config().Project("my-kirby-site"); ok {
		t.Fatal("project is still registered")
	}
}

func TestElevationErrorsOtherThanDeclineFailTheAdd(t *testing.T) {
	h := newHarness(t)
	h.el.Err = errFake
	h.mkProject("my-kirby-site")

	if _, err := h.d.AddProject(h.ctx(), "my-kirby-site"); err == nil {
		t.Fatal("a real elevation failure should surface, unlike a decline")
	}
	if _, ok := h.d.Config().Project("my-kirby-site"); ok {
		t.Fatal("project was registered despite the failed hosts write")
	}
}

var errFake = fakeErr{}

type fakeErr struct{}

func (fakeErr) Error() string { return "hosts file is read-only" }

func hostsLineFor(t *testing.T, content, hostname string) string {
	t.Helper()
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, hostname) {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("no hosts entry for %s in:\n%s", hostname, content)
	return ""
}
