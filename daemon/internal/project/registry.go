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
var nameRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

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

// spelled rewrites the letters that have a conventional spelled-out form or
// no base letter to fall back to. The umlauts are listed decomposed as well,
// because that is how macOS stores them in folder names.
var spelled = strings.NewReplacer(
	"ä", "ae", "ö", "oe", "ü", "ue",
	"a\u0308", "ae", "o\u0308", "oe", "u\u0308", "ue",
	"ß", "ss", "æ", "ae", "œ", "oe", "ø", "o",
	"ł", "l", "đ", "d", "ð", "d", "þ", "th", "ı", "i",
)

// accented maps an accented letter to its base letter.
var accented = func() map[rune]rune {
	m := map[rune]rune{}
	for base, letters := range map[rune]string{
		'a': "àáâãåāăą", 'c': "çćĉċč", 'd': "ď", 'e': "èéêëēĕėęě",
		'g': "ĝğġģ", 'h': "ĥħ", 'i': "ìíîïĩīĭį", 'j': "ĵ", 'k': "ķ",
		'l': "ĺļľŀ", 'n': "ñńņň", 'o': "òóôõōŏő", 'r': "ŕŗř",
		's': "śŝşšș", 't': "ţťŧț", 'u': "ùúûũūŭůűų", 'w': "ŵ",
		'y': "ýÿŷ", 'z': "źżž",
	} {
		for _, r := range letters {
			m[r] = base
		}
	}
	return m
}()

// Slug turns what a user typed, or a folder name from anywhere on the machine,
// into a project name: "Müller & Söhne" becomes "mueller-soehne". The name
// must also work as a hostname, which an arbitrary folder name need not. It
// returns "" when nothing usable is left (project-folders.feature, "The
// proposed name comes from the folder name"). The GUI mirrors this in gui/lib/project_name.dart;
// both are tested against testdata/slug.json.
func Slug(typed string) string {
	s := spelled.Replace(strings.ToLower(typed))
	s = strings.Map(func(r rune) rune {
		if base, ok := accented[r]; ok {
			return base
		}
		if r >= 0x300 && r <= 0x36f {
			return -1 // INFO: a combining accent, as in a decomposed "é"
		}
		return r
	}, s)
	s = strings.Trim(slugRe.ReplaceAllString(s, "-"), "-")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	return s
}
