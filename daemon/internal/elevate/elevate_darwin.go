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

	if err := runScriptAsAdmin(writeScript(tmp, path)); err != nil {
		return fmt.Errorf("elevated write to %s: %w", path, err)
	}
	return nil
}

// writeScript copies the staged file over path, then tells the resolver to
// re-read it. The hosts file is the only file Wharf writes this way, and
// macOS resolves names two ways: getaddrinfo reads /etc/hosts itself, so curl
// sees a new entry at once, but Safari and every other app on Apple's
// networking stack ask mDNSResponder, which can lose track of the file and
// then answer "No Such Record" for every entry in it — "Safari can't find the
// server" for a project curl reaches (pretty-urls.feature, "Creating a project
// registers a hosts entry"). A SIGHUP does not bring it back; a restart does,
// and launchd starts it again at once. Doing it in the same script keeps it
// to one prompt; a failed refresh does not undo a write that succeeded.
func writeScript(tmp, path string) string {
	return shellJoin([]string{"/bin/cp", tmp, path}) +
		" && { /usr/bin/dscacheutil -flushcache; /usr/bin/killall mDNSResponder; true; }"
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
