package project

import "testing"

func TestSafeJoinRejectsEscapes(t *testing.T) {
	for _, rel := range []string{"../escaped.txt", "a/../../escaped.txt"} {
		if _, err := safeJoin("/tmp/dest", rel); err == nil {
			t.Errorf("safeJoin(%q) = nil, want an error", rel)
		}
	}
	if _, err := safeJoin("/tmp/dest", "site/config.php"); err != nil {
		t.Errorf("safeJoin rejected a legitimate path: %v", err)
	}
	// An absolute entry name is not an escape: joining roots it inside the
	// destination, which is where it belongs.
	got, err := safeJoin("/tmp/dest", "/abs/path")
	if err != nil || got != "/tmp/dest/abs/path" {
		t.Errorf("safeJoin(\"/abs/path\") = %q, %v", got, err)
	}
}
