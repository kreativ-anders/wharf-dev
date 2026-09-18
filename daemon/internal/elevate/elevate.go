// Package elevate is where the daemon asks for administrator rights, and one
// of its two platform-specific packages; internal/proctree is the other
// (dev/architecture.md §4). Every call site uses only the one Elevator
// method; the three adapters behind it are the only code here that knows
// which OS it is running on.
package elevate

import "errors"

// ErrDeclined is returned when the user dismisses the elevation prompt. It is
// a normal outcome, not a failure: callers fall back rather than error out
// (local-ssl.feature).
var ErrDeclined = errors.New("elevation declined by user")

// Elevator does the things the daemon needs administrator rights for.
type Elevator interface {
	// RequestElevatedRun runs one program with administrator rights, with env
	// ("KEY=value") added to its environment: trusting mkcert's local
	// certificate authority, which writes to the system trust store
	// (local-ssl.feature), and installing Apache with a Linux package manager
	// (webserver-install.feature). It returns ErrDeclined if the user
	// dismisses the prompt.
	RequestElevatedRun(program string, args []string, env []string) error

	// AllowLowPorts lets processes without administrator rights listen on
	// ports from 80 up, so the front door can take 80 and 443
	// (service-management.feature, "Linux asks once before the front door
	// first takes port 80"). Where that is allowed already — macOS, Windows,
	// a Linux that was set up before — it returns nil without a prompt. It
	// returns ErrDeclined if the user dismisses the prompt.
	AllowLowPorts() error
}

// System returns the adapter for the OS the daemon was compiled for.
func System() Elevator { return systemElevator{} }
