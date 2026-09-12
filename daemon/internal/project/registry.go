// Package project implements folder-as-project: a folder under www/ is a
// project, exactly as a folder is a page in Kirby
// (dev/design-principles.md §1).
package project

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/layout"
)

// nameRe constrains project names to what is safe in a folder name, a
// hostname and a generated config file at the same time.
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,61}[a-z0-9]$`)

// ValidateName reports why a name cannot be used, or nil.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("project name is required")
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("project name %q must be lowercase letters, digits and hyphens, and start and end with a letter or digit", name)
	}
	return nil
}

// Discover lists the folders under www/ in name order. Folders are the source
// of truth for what exists; wharf.json only records what has been registered
// and how it deviates from the defaults.
func Discover(root layout.Root) ([]string, error) {
	entries, err := os.ReadDir(root.WWW())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", root.WWW(), err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// Exists reports whether a project folder is present.
func Exists(root layout.Root, name string) bool {
	info, err := os.Stat(root.ProjectDir(name))
	return err == nil && info.IsDir()
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slug turns a folder name from anywhere on the machine into a project name:
// "Client Site" becomes "client-site". The name must also work as a hostname,
// which an arbitrary folder name need not (project-folders.feature).
func Slug(folder string) string {
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(folder), "-"), "-")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	return s
}
