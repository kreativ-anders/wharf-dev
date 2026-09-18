package main

import (
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
