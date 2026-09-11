package elevate

import (
	"fmt"
	"os/exec"
	"strings"
)

type systemElevator struct{}

// RequestElevatedWrite prompts via the macOS authorisation dialog. AppleScript
// reports a dismissed dialog as error -128, which maps to ErrDeclined.
func (systemElevator) RequestElevatedWrite(path string, content string) error {
	tmp, cleanup, err := stage(content)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := runAsAdmin([]string{"/bin/cp", tmp, path}); err != nil {
		return fmt.Errorf("elevated write to %s: %w", path, err)
	}
	return nil
}

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

func runAsAdmin(argv []string) error {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = quoteForAppleScriptShell(a)
	}
	script := fmt.Sprintf(`do shell script "%s" with administrator privileges`, strings.Join(quoted, " "))

	out, err := exec.Command("/usr/bin/osascript", "-e", script).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "-128") || strings.Contains(string(out), "User canceled") {
			return ErrDeclined
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// quoteForAppleScriptShell single-quotes an argument for the shell, then
// escapes it again for the enclosing AppleScript string literal.
func quoteForAppleScriptShell(p string) string {
	shellQuoted := "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
	shellQuoted = strings.ReplaceAll(shellQuoted, `\`, `\\`)
	return strings.ReplaceAll(shellQuoted, `"`, `\"`)
}
