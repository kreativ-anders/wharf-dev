// Package elevate is where the daemon asks for administrator rights, and one
// of its two platform-specific packages; internal/proctree is the other
// (dev/architecture.md §4). Call sites use only the Elevator methods; the
// three adapters behind them are the only code here that knows which OS it
// is running on.
package elevate

import "errors"

// ErrDeclined is returned when the user dismisses the elevation prompt. It is
// a normal outcome, not a failure: callers fall back rather than error out
// (local-ssl.feature).
var ErrDeclined = errors.New("elevation declined by user")

// Elevator does the things the daemon needs administrator rights for.
type Elevator interface {
	// RequestElevatedRun runs one program with administrator rights, with env
	// ("KEY=value") added to its environment: installing Apache with a Linux
	// package manager (webserver-install.feature). It returns ErrDeclined if
	// the user dismisses the prompt.
	RequestElevatedRun(program string, args []string, env []string) error

	// TrustAuthority makes browsers trust mkcert's local certificate
	// authority in caRoot, behind one prompt (local-ssl.feature, "Trusting
	// the local certificate authority"). mkcert is the binary to run where
	// mkcert itself does the trusting. It returns ErrDeclined if the user
	// dismisses the prompt.
	TrustAuthority(mkcert, caRoot string) error

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

// mkcertTrustEnv is the environment for `mkcert -install` when it adds the
// authority to the system's trust store and nothing else; Firefox's NSS
// store is filled unprivileged when the authority is created.
func mkcertTrustEnv(caRoot string) []string {
	return []string{"CAROOT=" + caRoot, "TRUST_STORES=system"}
}
