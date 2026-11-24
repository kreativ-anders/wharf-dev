package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

// add's flags become the params the "Add project…" sheet sends
// (project-folders.feature).
func TestAddFlagsMatchWhatTheSheetSends(t *testing.T) {
	params := map[string]any{"path": "/code/shop"}
	err := addFlags([]string{"--name", "My Shop", "--template", "", "--webserver", "apache", "--start"}, params)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"path": "/code/shop", "name": "My Shop", "template": "", "webserver": "apache", "start": true,
	}
	if !reflect.DeepEqual(params, want) {
		t.Fatalf("params = %v, want %v", params, want)
	}

	// INFO: Without --template no key is sent, so the daemon detects one.
	params = map[string]any{"path": "/code/shop"}
	if err := addFlags(nil, params); err != nil {
		t.Fatal(err)
	}
	if _, sent := params["template"]; sent {
		t.Fatal("a template was sent without --template")
	}

	for _, bad := range [][]string{{"--name"}, {"--port", "80"}} {
		if err := addFlags(bad, map[string]any{}); err == nil {
			t.Errorf("addFlags(%q) accepted it", bad)
		}
	}
}

// "add shop --start" used to drop its flags, because only a path was read
// for them: shop was registered and left stopped.
func TestAddByNameKeepsItsFlags(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Wharf")
	params, err := wwwAddParams(root, "shop", []string{"--webserver", "apache", "--start"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"path": filepath.Join(root, "www", "shop"), "webserver": "apache", "start": true,
	}
	if !reflect.DeepEqual(params, want) {
		t.Fatalf("params = %v, want %v", params, want)
	}

	if _, err := wwwAddParams(root, "shop", []string{"--name", "Other"}); err == nil {
		t.Fatal("--name was accepted for a folder in www/, which keeps its own name")
	}
	if _, err := wwwAddParams(root, "shop", []string{"--port", "80"}); err == nil {
		t.Fatal("an unknown flag was accepted")
	}
}
