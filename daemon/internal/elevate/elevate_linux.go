package elevate

import (
	"fmt"
	"os/exec"
	"strings"
)

type systemElevator struct{}

// RequestElevatedRun goes through /usr/bin/env because pkexec clears the
// environment of the program it starts.
func (systemElevator) RequestElevatedRun(program string, args []string, env []string) error {
	argv := append(append([]string{"/usr/bin/env"}, env...), program)
	if err := pkexec(append(argv, args...)...); err != nil {
		return fmt.Errorf("elevated run of %s: %w", program, err)
	}
	return nil
}

func pkexec(argv ...string) error {
	cmd := exec.Command("pkexec", argv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 126 {
			return ErrDeclined
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
