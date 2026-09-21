package elevate

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

type systemElevator struct{}

// RequestElevatedRun goes through cmd.exe's `set`, because a process started
// with RunAs does not inherit the caller's environment.
func (systemElevator) RequestElevatedRun(program string, args []string, env []string) error {
	for _, part := range append(append([]string{program}, args...), env...) {
		if err := expandable(part); err != nil {
			return err
		}
	}
	var line strings.Builder
	for _, kv := range env {
		fmt.Fprintf(&line, `set "%s"&& `, kv)
	}
	fmt.Fprintf(&line, `"%s"`, program)
	for _, a := range args {
		fmt.Fprintf(&line, ` "%s"`, a)
	}
	// WARNING: The outer quotes are for cmd.exe, which strips the first and
	// the last quote of a /c line that holds more than two. Without them a
	// line that starts with the quoted program would lose its own quotes.
	if err := runAs(fmt.Sprintf(`'/c','"%s"'`, quoteForPowerShell(line.String()))); err != nil {
		return fmt.Errorf("elevated run of %s: %w", program, err)
	}
	return nil
}

// runAs starts cmd.exe elevated with a PowerShell argument list.
func runAs(argumentList string) error {
	script := fmt.Sprintf(
		`$p = Start-Process -FilePath cmd.exe -ArgumentList %s -Verb RunAs -Wait -PassThru; exit $p.ExitCode`,
		argumentList)

	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "cancelled by the user") || strings.Contains(string(out), "canceled by the user") {
			return ErrDeclined
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

var cmdVariable = regexp.MustCompile(`%([^%]+)%`)

// expandable refuses a part of the command line that cmd.exe would change.
//
// WARNING: cmd.exe expands %NAME% even inside quotes when NAME is set, so a
// Wharf folder named like one would send the elevated program somewhere else.
// A lone % or an unset name passes through as written and stays allowed.
func expandable(part string) error {
	for _, m := range cmdVariable.FindAllStringSubmatch(part, -1) {
		if _, set := os.LookupEnv(m[1]); set {
			return fmt.Errorf("%q holds %s, which Windows would replace with the value of %s before the administrator "+
				"prompt runs it — rename the folder so it has no %%…%% in it", part, m[0], m[1])
		}
	}
	return nil
}

func quoteForPowerShell(p string) string { return strings.ReplaceAll(p, "'", "''") }

// lowPortsAllowed is always true here: this OS lets any process listen on the
// front door's ports (dev/architecture.md §4c).
func lowPortsAllowed() bool { return true }

// AllowLowPorts has nothing to ask for.
func (systemElevator) AllowLowPorts() error { return nil }
