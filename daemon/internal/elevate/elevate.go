// Package elevate is the entire platform-specific surface of the daemon
// (dev/architecture.md §4). Every call site in the codebase uses only the two
// Elevator methods; the three adapters behind them are the only code that
// knows which OS it is running on.
package elevate

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrDeclined is returned when the user dismisses the elevation prompt. It is
// a normal outcome, not a failure: callers fall back rather than error out
// (pretty-urls.feature, "Elevation is declined").
var ErrDeclined = errors.New("elevation declined by user")

// Elevator does the two things the daemon needs administrator rights for.
type Elevator interface {
	// RequestElevatedWrite replaces the file at path with content, prompting
	// the user for administrator rights. It returns ErrDeclined if the user
	// dismisses the prompt.
	RequestElevatedWrite(path string, content string) error

	// RequestElevatedRun runs one program with administrator rights, with env
	// ("KEY=value") added to its environment. It exists for one caller:
	// trusting mkcert's local certificate authority, which writes to the
	// system trust store (local-ssl.feature). It returns ErrDeclined if the
	// user dismisses the prompt.
	RequestElevatedRun(program string, args []string, env []string) error
}

// System returns the adapter for the OS the daemon was compiled for.
func System() Elevator { return systemElevator{} }

// stage writes content to a temporary file owned by the current user and
// returns its path. The adapters then run one elevated copy of that file over
// the destination, which keeps each adapter down to a single command and
// avoids passing file content through a shell.
func stage(content string) (string, func(), error) {
	f, err := os.CreateTemp("", "wharf-elevated-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { os.Remove(f.Name()) }
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	// The staged file becomes the destination's content, so it must be
	// world-readable like the hosts file it replaces.
	if err := os.Chmod(f.Name(), 0o644); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return filepath.ToSlash(f.Name()), cleanup, nil
}
