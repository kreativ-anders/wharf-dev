package elevate

import (
	"errors"
	"testing"
)

func TestADismissedTrustPromptIsDeclined(t *testing.T) {
	exit := errors.New("exit status 1")
	cases := []struct {
		out  string
		want error
	}{
		// INFO: What `security add-trusted-cert` printed when the dialog was
		// cancelled on macOS 26.
		{"SecTrustSettingsSetTrustSettings: The authorization was canceled by the user.", ErrDeclined},
		{"SecTrustSettingsSetTrustSettings: The authorization was denied since no user interaction was possible.", nil},
	}
	for _, c := range cases {
		err := trustResult(c.out, exit)
		if c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%q: got %v, want %v", c.out, err, c.want)
		}
		if c.want == nil && (err == nil || errors.Is(err, ErrDeclined)) {
			t.Errorf("%q: got %v, want a failure that names the output", c.out, err)
		}
	}
	if err := trustResult("", nil); err != nil {
		t.Errorf("a trusted authority returned %v", err)
	}
}
