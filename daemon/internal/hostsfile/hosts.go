// Package hostsfile implements pretty URLs the same way on all three
// operating systems: one hosts-file line per project, written through the
// elevation adapter (dev/architecture.md §4, features/pretty-urls.feature).
//
// No /etc/resolver trick on macOS, no dnsmasq wildcard on Linux — identical
// behaviour beats best-per-OS here, deliberately.
package hostsfile

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/manuel-steinberg/wharf/daemon/internal/elevate"
)

// Domain is the TLD projects are published under: <project>.wharf.
const Domain = "wharf"

// marker tags the lines this tool owns, so removing a project touches exactly
// its own entry and nothing a user added by hand.
const marker = "# wharf:"

// Hostname is the pretty domain for a project name.
func Hostname(project string) string { return project + "." + Domain }

// Manager edits one hosts file through one elevator.
type Manager struct {
	Path     string
	Elevator elevate.Elevator
}

// New returns a Manager for this OS's hosts file.
func New(el elevate.Elevator) *Manager {
	return &Manager{Path: SystemPath(), Elevator: el}
}

// SystemPath is the hosts file location. This switch is the whole of the
// platform difference for pretty URLs.
func SystemPath() string {
	if runtime.GOOS == "windows" {
		systemRoot := os.Getenv("SystemRoot")
		if systemRoot == "" {
			systemRoot = `C:\Windows`
		}
		return systemRoot + `\System32\drivers\etc\hosts`
	}
	return "/etc/hosts"
}

// Add writes "127.0.0.1 <project>.wharf # wharf:<project>", replacing any
// existing entry for the same project. It returns elevate.ErrDeclined
// unchanged when the user dismisses the prompt.
func (m *Manager) Add(project string) error {
	current, err := m.read()
	if err != nil {
		return err
	}
	line := fmt.Sprintf("127.0.0.1\t%s\t%s%s", Hostname(project), marker, project)

	// Editing happens in LF and the file's own line endings are restored, so
	// a Windows hosts file does not end up with mixed endings.
	crlf := strings.Contains(current, "\r\n")
	next := strings.ReplaceAll(removeEntry(current, project), "\r\n", "\n")
	next = strings.TrimRight(next, "\n") + "\n" + line + "\n"
	if next[0] == '\n' {
		next = next[1:] // the file was empty; do not open it with a blank line
	}
	if crlf {
		next = strings.ReplaceAll(next, "\n", "\r\n")
	}
	return m.Elevator.RequestElevatedWrite(m.Path, next)
}

// Remove deletes a project's entry. Removing an absent entry is a no-op and
// does not prompt for elevation.
func (m *Manager) Remove(project string) error {
	current, err := m.read()
	if err != nil {
		return err
	}
	next := removeEntry(current, project)
	if next == current {
		return nil
	}
	return m.Elevator.RequestElevatedWrite(m.Path, next)
}

// Has reports whether this tool owns an entry for the project.
func (m *Manager) Has(project string) (bool, error) {
	current, err := m.read()
	if err != nil {
		return false, err
	}
	return current != removeEntry(current, project), nil
}

// Entries lists the project names this tool currently has entries for.
func (m *Manager) Entries() ([]string, error) {
	current, err := m.read()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(current, "\n") {
		if name, ok := ownedBy(line); ok {
			out = append(out, name)
		}
	}
	return out, nil
}

func (m *Manager) read() (string, error) {
	raw, err := os.ReadFile(m.Path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read hosts file %s: %w", m.Path, err)
	}
	return string(raw), nil
}

// removeEntry drops every line this tool owns for the given project,
// preserving the file's other content and its line endings.
func removeEntry(content, project string) string {
	crlf := strings.Contains(content, "\r\n")
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")

	kept := lines[:0]
	for _, line := range lines {
		if name, ok := ownedBy(line); ok && name == project {
			continue
		}
		kept = append(kept, line)
	}
	out := strings.Join(kept, "\n")
	if crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	return out
}

// ownedBy returns the project a hosts line belongs to, if this tool wrote it.
func ownedBy(line string) (string, bool) {
	idx := strings.Index(line, marker)
	if idx < 0 {
		return "", false
	}
	name := strings.TrimSpace(line[idx+len(marker):])
	if name == "" {
		return "", false
	}
	return name, true
}
