package core

import (
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

	h.mustAdd("my-kirby-site")
	if _, err := h.d.Scaffold(h.ctx(), "empty", "blank", ""); err != nil {
		t.Fatal(err)
	}

	// INFO: Then the hosts file is not written
	// And no elevation prompt is shown — Wharf has no code that writes the
	// hosts file, so the one elevated action left is trusting mkcert's authority.
	if n := h.el.RunCount(); n != 0 {
		t.Fatalf("%d elevation prompts were raised", n)
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
