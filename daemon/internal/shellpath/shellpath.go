// Package shellpath puts Wharf's PHP within reach of terminals and editors
// (php-terminal.feature), and is the third of the daemon's platform-specific
// packages, beside internal/elevate and internal/proctree
// (dev/architecture.md §4). Everything else uses the Registrar interface and
// Link; only the files behind them know which OS they run on:
//
//   - macOS and Linux: a marked block in the user's shell startup files, and
//     bin/path/php as a symbolic link.
//   - Windows: the user's PATH in the user environment, and bin/path/php.cmd,
//     because a symbolic link needs Developer Mode there.
//
// Neither needs administrator rights: both belong to the user.
package shellpath

import (
	"fmt"
	"path/filepath"
)

// Registrar puts one folder first on PATH for terminals started from now on,
// and takes it off again.
type Registrar interface {
	// Add puts dir on PATH, returning every place that now does — a file, or
	// the user environment on Windows. Adding again changes nothing.
	Add(dir string) ([]string, error)
	// Remove takes dir off PATH, returning every place it was removed from.
	// Nothing else in those places is changed.
	Remove(dir string) ([]string, error)
	// Where lists the places that put dir on PATH now.
	Where(dir string) []string
}

// LinkName is what bin/path holds for the global default PHP on this OS.
func LinkName() string { return linkName }

// Link makes dir's LinkName run target, the php binary of the global default
// version. An empty target removes it, so no stale version is left behind.
func Link(dir, target string) error { return link(dir, target) }

// Linked reports the binary dir's LinkName runs, or "" when there is none.
func Linked(dir string) string { return linked(dir) }

// linkPath is where the link lives.
func linkPath(dir string) string { return filepath.Join(dir, linkName) }

// Markers delimit Wharf's block in a startup file. Everything between them is
// Wharf's; everything outside them is the user's and is never touched.
const (
	blockStart = "# >>> Wharf: PHP in the terminal >>>"
	blockEnd   = "# <<< Wharf: PHP in the terminal <<<"
)

// BrokenBlockError is a startup file holding Wharf's start line without its
// end line after it, most likely deleted by hand
// (php-terminal.feature, "A Wharf block broken by hand is not guessed at").
type BrokenBlockError struct{ Path string }

func (e *BrokenBlockError) Error() string {
	return fmt.Sprintf("%s has the line %q but not %q after it, so Wharf cannot tell where its lines end. Delete the first line, or put the second back after Wharf's lines, and try again.", e.Path, blockStart, blockEnd)
}
