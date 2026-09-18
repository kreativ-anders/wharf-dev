package elevate

import "log/slog"

// Direct never prompts. It is for development and CI, where a password prompt
// would stall an unattended run — never the default, so that the production
// path always goes through a real elevation prompt.
type Direct struct{}

// RequestElevatedRun behaves as if the prompt were declined. Running the
// program unprivileged would let it ask for a sudo password on whatever
// terminal launched the daemon, and "no password prompt" is the whole point
// of this elevator.
func (Direct) RequestElevatedRun(program string, _ []string, _ []string) error {
	slog.Warn("elevation disabled: not running privileged program", "program", program)
	return ErrDeclined
}
