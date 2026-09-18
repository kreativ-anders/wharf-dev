package elevate

import (
	"fmt"
	"os/exec"
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
