package elevate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type systemElevator struct{}

// RequestElevatedRun runs the program through /usr/bin/env, which is how the
// environment reaches a shell that AppleScript starts afresh.
func (systemElevator) RequestElevatedRun(program string, args []string, env []string) error {
	argv := append([]string{"/usr/bin/env"}, env...)
	argv = append(argv, program)
	argv = append(argv, args...)
	if err := runAsAdmin(argv); err != nil {
		return fmt.Errorf("elevated run of %s: %w", program, err)
	}
	return nil
}

// TrustAuthority trusts the authority for this user, run as the user: macOS
// shows its own password prompt for the change. mkcert is not needed here.
//
// WARNING: Not `mkcert -install` through osascript with administrator
// privileges. Changing trust settings needs a confirmation from macOS's own
// dialog, which a process started that way cannot show, so the trust was
// refused after the password had been typed ("no user interaction was
// possible", issue #19). See dev/architecture.md §4.
func (systemElevator) TrustAuthority(_, caRoot string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("trust the local certificate authority: %w", err)
	}
	out, err := exec.Command("/usr/bin/security", "add-trusted-cert",
		"-r", "trustRoot",
		"-k", filepath.Join(home, "Library", "Keychains", "login.keychain-db"),
		filepath.Join(caRoot, "rootCA.pem"),
	).CombinedOutput()
	return trustResult(string(out), err)
}

// trustResult tells a dismissed trust prompt apart from a failure.
func trustResult(out string, err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(out), "canceled by the user") {
		return ErrDeclined
	}
	return fmt.Errorf("trust the local certificate authority: %w: %s", err, strings.TrimSpace(out))
}

func runAsAdmin(argv []string) error { return runScriptAsAdmin(shellJoin(argv)) }

// shellJoin single-quotes each argument for the shell.
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

// runScriptAsAdmin runs a shell command line with administrator rights,
// escaped once more for the AppleScript string literal around it.
func runScriptAsAdmin(shell string) error {
	shell = strings.ReplaceAll(shell, `\`, `\\`)
	shell = strings.ReplaceAll(shell, `"`, `\"`)
	script := fmt.Sprintf(`do shell script "%s" with administrator privileges`, shell)

	out, err := exec.Command("/usr/bin/osascript", "-e", script).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "-128") || strings.Contains(string(out), "User canceled") {
			return ErrDeclined
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// lowPortsAllowed is always true here: this OS lets any process listen on the
// front door's ports (dev/architecture.md §4c).
func lowPortsAllowed() bool { return true }

// AllowLowPorts has nothing to ask for.
func (systemElevator) AllowLowPorts() error { return nil }
