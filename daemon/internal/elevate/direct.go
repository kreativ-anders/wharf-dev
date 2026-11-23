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

// TrustAuthority behaves as if the prompt were declined: on every OS it is
// a password prompt.
func (Direct) TrustAuthority(mkcert, _ string) error {
	slog.Warn("elevation disabled: not trusting the local certificate authority", "program", mkcert)
	return ErrDeclined
}

// AllowLowPorts behaves as if the prompt were declined, where there is one to
// decline.
func (Direct) AllowLowPorts() error {
	if lowPortsAllowed() {
		return nil
	}
	slog.Warn("elevation disabled: ports below 1024 stay reserved for administrators")
	return ErrDeclined
}
