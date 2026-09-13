package elevate

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

type systemElevator struct{}

// RequestElevatedWrite prompts via UAC. Start-Process -Verb RunAs raises the
// consent dialog; a dismissed dialog surfaces as a Win32 "operation was
// cancelled by the user" error, which maps to ErrDeclined.
func (systemElevator) RequestElevatedWrite(path string, content string) error {
	tmp, cleanup, err := stage(content)
	if err != nil {
		return err
	}
	defer cleanup()

	// WARNING: Start-Process joins -ArgumentList verbatim, so each path is
	// quoted for cmd.exe itself: a user name with a space in it would split
	// the temp path in two, and copy reads an unquoted "C:/…" as a switch.
	args := fmt.Sprintf(`'/c','copy','/y','"%s"','"%s"'`,
		quoteForPowerShell(filepath.FromSlash(tmp)), quoteForPowerShell(path))
	if err := runAs(args); err != nil {
		return fmt.Errorf("elevated write to %s: %w", path, err)
	}
	return nil
}

// RequestElevatedRun goes through cmd.exe's `set`, because a process started
// with RunAs does not inherit the caller's environment.
func (systemElevator) RequestElevatedRun(program string, args []string, env []string) error {
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

func quoteForPowerShell(p string) string { return strings.ReplaceAll(p, "'", "''") }
