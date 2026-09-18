package elevate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
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

// unprivilegedPortStart is the lowest port Linux lets a process without
// administrator rights listen on; 1024 unless someone lowered it.
const unprivilegedPortStart = "/proc/sys/net/ipv4/ip_unprivileged_port_start"

// lowPortsAllowed reports whether the front door can listen on port 80
// without a prompt.
func lowPortsAllowed() bool {
	if os.Geteuid() == 0 {
		return true
	}
	b, err := os.ReadFile(unprivilegedPortStart)
	if err != nil {
		// INFO: Kernels before 4.11 have no such setting and nothing to lower;
		// the webserver's own bind error says what went wrong.
		return true
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return err != nil || n <= 80
}

// AllowLowPorts lowers the port limit to 80, now and after every reboot.
func (systemElevator) AllowLowPorts() error {
	if lowPortsAllowed() {
		return nil
	}
	// INFO: A sysctl rather than a capability on the webserver binary: it holds
	// for nginx and Apache alike, a distribution's Apache included, and
	// survives every update of either (dev/architecture.md §4c).
	script := "printf 'net.ipv4.ip_unprivileged_port_start = 80\\n' > /etc/sysctl.d/60-wharf.conf" +
		" && echo 80 > " + unprivilegedPortStart
	if err := pkexec("/bin/sh", "-c", script); err != nil {
		if errors.Is(err, ErrDeclined) {
			return err
		}
		return fmt.Errorf("allow ports from 80: %w", err)
	}
	return nil
}
