// Package configtemplate keeps the config templates: the webserver rules one
// kind of project needs, for nginx and for Apache (config-templates.feature).
//
// A template is a whole nginx server block or Apache virtual host, as a
// recipe from a CMS's docs shows it. Wharf fills in the placeholders — the
// values only it knows, like the port behind its front door — and leaves
// every other line as written (see dev/architecture.md §6).
//
// Wharf's own templates are compiled in. The user's live in
// config/templates/<id>.<webserver>.conf, and a file there named like a
// built-in one replaces it — so restoring a built-in is deleting that file.
// A project's custom config is the same kind of file, for that project alone
// (app-configuration.feature).
package configtemplate

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
)

// The placeholders Wharf fills in. Listen, SSL and ServerName decide where a
// project is reachable, so a template must name them; the others are there
// for a template to use.
const (
	// Listen is nginx's listen lines, or the address of Apache's
	// <VirtualHost>: ports 80 and 443 on the front door, a loopback port
	// behind it.
	Listen = "{{listen}}"
	// SSL is the certificate lines while a block serves HTTPS, else nothing.
	SSL        = "{{ssl}}"
	ServerName = "{{server_name}}"
	// Root is the project's folder.
	Root   = "{{root}}"
	LogDir = "{{log_dir}}"
	// PHP is the address of the project's PHP, host:port.
	PHP = "{{php}}"
	// FastCGI is Wharf's FastCGI parameters file for nginx: the stock ones,
	// SCRIPT_FILENAME, and the port and HTTPS the browser used.
	FastCGI = "{{fastcgi}}"
)

// Required are the placeholders a template cannot do without.
var Required = []string{Listen, SSL, ServerName}

// Servers are the webservers every template has rules for.
var Servers = []string{"nginx", "apache"}

//go:embed builtin/*.conf
var builtinFS embed.FS

// builtins are Wharf's own templates, in the order Settings lists them.
var builtins = []struct{ ID, Name string }{
	{"kirby", "Kirby"},
	{"laravel", "Laravel"},
	{"wordpress", "WordPress"},
	{"statamic", "Statamic"},
	{"symfony", "Symfony"},
	{"craft", "Craft CMS"},
	{"drupal", "Drupal"},
}

// Template is one config template as Settings lists it.
type Template struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Builtin is true for Wharf's own; Changed for one of those the user
	// saved a change to, whose files in config/templates/ now apply.
	Builtin bool `json:"builtin"`
	Changed bool `json:"changed"`
}

// ErrNotFound is returned for an id no template has.
var ErrNotFound = errors.New("no such config template")

// Library is the set of templates one Wharf folder offers.
type Library struct {
	// Dir is config/templates.
	Dir string
}

// Path is the file holding one template's rules for one webserver, whether or
// not it exists yet.
func (l Library) Path(id, server string) string {
	return filepath.Join(l.Dir, id+"."+server+".conf")
}

func builtinName(id string) (string, bool) {
	for _, b := range builtins {
		if b.ID == id {
			return b.Name, true
		}
	}
	return "", false
}

// List returns Wharf's templates in their own order, then the user's by id.
func (l Library) List() []Template {
	out := []Template{}
	for _, b := range builtins {
		out = append(out, Template{ID: b.ID, Name: b.Name, Builtin: true, Changed: l.changed(b.ID)})
	}
	var own []string
	entries, _ := os.ReadDir(l.Dir)
	for _, e := range entries {
		id, server, ok := splitName(e.Name())
		if !ok || e.IsDir() || !slices.Contains(Servers, server) {
			continue
		}
		if _, builtin := builtinName(id); !builtin && !slices.Contains(own, id) {
			own = append(own, id)
		}
	}
	sort.Strings(own)
	for _, id := range own {
		out = append(out, Template{ID: id, Name: id})
	}
	return out
}

// splitName takes "<id>.<server>.conf" apart.
func splitName(file string) (id, server string, ok bool) {
	base, found := strings.CutSuffix(file, ".conf")
	if !found {
		return "", "", false
	}
	dot := strings.LastIndexByte(base, '.')
	if dot <= 0 {
		return "", "", false
	}
	id, server = base[:dot], base[dot+1:]
	return id, server, config.TemplateIDRe.MatchString(id)
}

func (l Library) changed(id string) bool {
	for _, server := range Servers {
		if fileExists(l.Path(id, server)) {
			return true
		}
	}
	return false
}

// Exists reports whether id names a template.
func (l Library) Exists(id string) bool {
	// WARNING: An id is joined into a path: one like "../x" would reach files
	// outside config/templates/ through Read, Save and Delete.
	if !config.TemplateIDRe.MatchString(id) {
		return false
	}
	if _, ok := builtinName(id); ok {
		return true
	}
	return l.changed(id)
}

// Read returns a template's rules for one webserver: the user's file where
// there is one, Wharf's own otherwise.
func (l Library) Read(id, server string) (string, error) {
	if !config.TemplateIDRe.MatchString(id) {
		return "", fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if !slices.Contains(Servers, server) {
		return "", fmt.Errorf("%w: config templates have rules for nginx and apache, not %q", ErrNotFound, server)
	}
	body, err := os.ReadFile(l.Path(id, server))
	if err == nil {
		return string(body), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if _, ok := builtinName(id); ok {
		body, err := builtinFS.ReadFile("builtin/" + id + "." + server + ".conf")
		return string(body), err
	}
	if l.changed(id) {
		return "", fmt.Errorf("%w: the config template %q has no %s rules — create %s", ErrNotFound, id, server, l.Path(id, server))
	}
	return "", fmt.Errorf("%w: %q", ErrNotFound, id)
}

// Plain is the block a project without a template is served with: its
// folder, its logs, PHP, and the webserver's own defaults otherwise. It is
// also where a new template of the user's starts.
func Plain(server string) string {
	body, _ := builtinFS.ReadFile("builtin/plain." + server + ".conf")
	return string(body)
}

// Fill replaces the placeholders in a template with values. A value of
// several lines keeps the indentation of the line it replaces, and a line
// holding nothing but a placeholder whose value is empty goes away. Comment
// lines are left alone, so a comment can name a placeholder.
func Fill(body string, values map[string]string) string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || !strings.Contains(line, "{{") {
			out = append(out, line)
			continue
		}
		if value, ok := values[trimmed]; ok && value == "" {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		for name, value := range values {
			value = strings.ReplaceAll(value, "\n", "\n"+indent)
			line = strings.ReplaceAll(line, name, value)
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// ErrIncomplete is returned for rules that leave out a placeholder Wharf
// cannot serve a project without.
var ErrIncomplete = errors.New("rules are incomplete")

// reason is an error that reads as its own sentence but still matches a
// sentinel, so a message is not prefixed with the sentinel's words.
type reason struct {
	msg  string
	kind error
}

func (e reason) Error() string        { return e.msg }
func (e reason) Is(target error) bool { return target == e.kind }

// Missing is an ErrNotFound worded for the user.
func Missing(format string, args ...any) error {
	return reason{fmt.Sprintf(format, args...), ErrNotFound}
}

// Check refuses rules that leave out a required placeholder, naming each one
// missing.
func Check(server, body string) error {
	var missing []string
	for _, name := range Required {
		if !strings.Contains(stripComments(body), name) {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return reason{fmt.Sprintf("the %s rules leave out %s — put %s back; Wharf fills in where the project is reachable",
		serverName(server), strings.Join(missing, ", "), pronoun(len(missing))), ErrIncomplete}
}

func pronoun(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

func stripComments(body string) string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// Save writes a template's rules for one webserver into config/templates/.
// Saving a built-in one is what changes it.
func (l Library) Save(id, server, body string) error {
	if !l.Exists(id) {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if !slices.Contains(Servers, server) {
		return fmt.Errorf("%w: config templates have rules for nginx and apache, not %q", ErrNotFound, server)
	}
	return SaveFile(l.Path(id, server), server, body)
}

// SaveFile writes the rules for one webserver to path, refusing them when
// they leave out a required placeholder. A config template and a project's
// custom config are saved alike.
func SaveFile(path, server, body string) error {
	if err := Check(server, body); err != nil {
		return err
	}
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return writeFile(path, body)
}

// Create adds a template of the user's under id, starting each webserver's
// file from a commented starting point.
func (l Library) Create(id string) (Template, error) {
	if !config.TemplateIDRe.MatchString(id) {
		return Template{}, fmt.Errorf("%q cannot name a config template — use lowercase letters, digits and hyphens", id)
	}
	if l.Exists(id) {
		return Template{}, ErrExists
	}
	for _, server := range Servers {
		if err := writeFile(l.Path(id, server), starter(id, server)); err != nil {
			return Template{}, err
		}
	}
	return Template{ID: id, Name: id}, nil
}

// ErrExists is returned when a new template's name is taken.
var ErrExists = errors.New("a config template with that name already exists")

// Delete removes a template's files: the user's own template is gone, and a
// built-in one is back to Wharf's rules.
func (l Library) Delete(id string) error {
	if !l.Exists(id) {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	for _, server := range Servers {
		if err := os.Remove(l.Path(id, server)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// ModTimes is the modification time of every file in config/templates/, by
// path, so a change made in another editor is noticed.
func (l Library) ModTimes() map[string]time.Time {
	out := map[string]time.Time{}
	entries, _ := os.ReadDir(l.Dir)
	for _, e := range entries {
		// INFO: A dot-file is Save's temporary file, not a template yet.
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if info, err := e.Info(); err == nil && !e.IsDir() {
			out[filepath.Join(l.Dir, e.Name())] = info.ModTime()
		}
	}
	return out
}

func starter(id, server string) string {
	return fmt.Sprintf("# %s, for %s — your config template.\n#\n", id, serverName(server)) + legend + "\n" + Plain(server)
}

func serverName(server string) string {
	return map[string]string{"nginx": "nginx", "apache": "Apache"}[server]
}

// CustomStarter is where a project's custom config starts: the rules its
// config template has for the webserver serving it — Plain's when it has
// none — under a header of its own, placeholders intact
// (app-configuration.feature, "A project's custom config starts from its
// config template").
func CustomStarter(project, template, server, rules string) string {
	from := "the block a project without a config template gets"
	if template != "" {
		from = "the " + template + " config template"
	}
	return fmt.Sprintf(`# %[1]s's own %[2]s config, started from %[3]s.
#
# Wharf serves %[1]s with this file instead of its config template, and only
# while %[2]s serves it. Saving restarts that webserver.
#
# Keep every {{…}}: Wharf fills them in each time it starts the project, and
# they change — the port behind the front door, the certificate once SSL is
# on. A fixed "listen 80;" or server name in their place makes the project
# unreachable or takes the port another project needs.
#
`, project, serverName(server), from) + legend + "\n" + strings.TrimLeft(WithoutHeader(rules), "\n")
}

// WithoutHeader drops the comment lines a template starts with.
func WithoutHeader(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") {
			return strings.Join(lines[i:], "\n")
		}
	}
	return ""
}

// legend is what every template's header says about the placeholders.
const legend = `# Wharf fills in every {{…}} for each project using this template; the
# rest is used as written:
#   {{listen}}       where the block listens — ports 80 and 443, or a
#                    loopback port behind Wharf's front door
#   {{ssl}}          the certificate lines while it serves HTTPS
#   {{server_name}}  <project>.localhost
#   {{root}}         the project's folder
#   {{log_dir}}      the project's log folder
#   {{php}}          the project's PHP, as host:port
#   {{fastcgi}}      (nginx) Wharf's FastCGI parameters, SCRIPT_FILENAME and
#                    the port and HTTPS the browser used included
# {{listen}}, {{ssl}} and {{server_name}} are required.
`

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// writeFile replaces a file through a rename, so the watcher never applies a
// half-written template.
func writeFile(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".template-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
