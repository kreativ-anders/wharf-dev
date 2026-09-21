package elevate

import "testing"

func TestAPathCmdWouldExpandIsRefused(t *testing.T) {
	t.Setenv("WHARF_TEST_VAR", "elsewhere")
	if err := expandable(`C:\Users\me\%WHARF_TEST_VAR%\Wharf`); err == nil {
		t.Fatal("a path naming a set variable was allowed")
	}
	for _, ok := range []string{`C:\Users\me\100%\Wharf`, `C:\Users\me\%NOT_SET_ANYWHERE%\Wharf`, `CAROOT=C:\Users\me\Wharf`} {
		if err := expandable(ok); err != nil {
			t.Fatalf("%s: %v", ok, err)
		}
	}
}
