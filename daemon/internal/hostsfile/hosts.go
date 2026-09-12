// Package hostsfile removes the hosts-file lines Wharf wrote while projects
// were published as <name>.wharf. Projects are <name>.localhost now, which
// needs no hosts file (features/pretty-urls.feature); this package only
// cleans up after the old scheme, through the elevation adapter.
package hostsfile

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/elevate"
)

// marker tags the lines this tool owns, so removing a project touches exactly
// its own entry and nothing a user added by hand.
const marker = "# wharf:"

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

// RemoveAll deletes every line Wharf owns, whichever project it was for — what
// a reset leaves of the <name>.wharf days. With no such line it asks nothing.
func (m *Manager) RemoveAll() error {
	names, err := m.Entries()
	if err != nil || len(names) == 0 {
		return err
	}
	current, err := m.read()
	if err != nil {
		return err
	}
	next := current
	for _, n := range names {
		next = removeEntry(next, strings.TrimSpace(n))
	}
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
