package elevate

import (
	"log/slog"
	"os"
)

// Direct writes without prompting. It is for development and CI, where the
// hosts file under test is an ordinary writable file — never the default, so
// that the production path always goes through a real elevation prompt.
type Direct struct{}

func (Direct) RequestElevatedWrite(path string, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// RequestElevatedRun behaves as if the prompt were declined. Running the
// program unprivileged would let it ask for a sudo password on whatever
// terminal launched the daemon, and "no password prompt" is the whole point
// of this elevator.
func (Direct) RequestElevatedRun(program string, _ []string, _ []string) error {
	slog.Warn("elevation disabled: not running privileged program", "program", program)
	return ErrDeclined
}
