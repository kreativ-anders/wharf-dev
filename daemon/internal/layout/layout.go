// Package layout resolves the portable root folder described in
// dev/architecture.md §5. The folder is the shared internal model across all
// three operating systems; only the outer package format differs, never this.
package layout

import (
	"os"
	"path/filepath"
)

// Root is a resolved wharf root folder.
//
//	wharf/
//	├── bin/     php/<version>, path/, nginx/, apache/, mkcert/
//	├── www/     one folder per project
//	├── config/  wharf.json, php.ini, vhosts/ (custom configs), templates/ (config templates)
//	└── data/    runtime state, sockets, logs
type Root struct{ Dir string }

// Resolve picks the root folder. An explicit dir wins; otherwise WHARF_ROOT;
// otherwise the per-user default. The path is made absolute so that every
// later join is unambiguous regardless of the daemon's working directory.
func Resolve(dir string) (Root, error) {
	if dir == "" {
		dir = os.Getenv("WHARF_ROOT")
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Root{}, err
		}
		dir = filepath.Join(home, "Wharf")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Root{}, err
	}
	return Root{Dir: abs}, nil
}

func (r Root) Bin() string        { return filepath.Join(r.Dir, "bin") }
func (r Root) WWW() string        { return filepath.Join(r.Dir, "www") }
func (r Root) Config() string     { return filepath.Join(r.Dir, "config") }
func (r Root) Data() string       { return filepath.Join(r.Dir, "data") }
func (r Root) ConfigFile() string { return filepath.Join(r.Config(), "wharf.json") }
func (r Root) LogDir() string     { return filepath.Join(r.Data(), "log") }
func (r Root) CertDir() string    { return filepath.Join(r.Data(), "certs") }

// DaemonLog is wharfd's own log. It sits among the service logs but is
// never cleared with them: the daemon is writing to it.
func (r Root) DaemonLog() string { return filepath.Join(r.LogDir(), "wharfd.log") }

// ProjectLogDir holds one project's own logs: the requests and errors of the
// webserver serving it (project-logs.feature).
func (r Root) ProjectLogDir(name string) string {
	return filepath.Join(r.LogDir(), "projects", name)
}

// PHPIni is the user's own PHP settings, read by every PHP version after its
// own php.ini (php-settings.feature).
func (r Root) PHPIni() string { return filepath.Join(r.Config(), "php.ini") }

// Socket is the IPC endpoint. It lives inside the root rather than in a
// system runtime directory so that AF_UNIX works unchanged on Windows,
// macOS and Linux (dev/architecture.md §4).
func (r Root) Socket() string { return filepath.Join(r.Data(), "wharf.sock") }

// ProjectDir is where a project in www/ keeps its files: folder = project.
// A project added from elsewhere records its own path instead
// (project-folders.feature).
func (r Root) ProjectDir(name string) string { return filepath.Join(r.WWW(), name) }

// VhostDir holds the projects' custom configs. It sits in config/
// rather than data/ because the files are user-authored and worth backing up.
func (r Root) VhostDir() string { return filepath.Join(r.Config(), "vhosts") }

// CustomConfig is one project's own rules for one webserver, used instead of
// its config template (app-configuration.feature, "A custom config belongs to
// the webserver serving the project").
func (r Root) CustomConfig(project, server string) string {
	return filepath.Join(r.VhostDir(), project+"."+server+".conf")
}

// TemplateDir holds the user's config templates, and their changes to the
// built-in ones (config-templates.feature).
func (r Root) TemplateDir() string { return filepath.Join(r.Config(), "templates") }

// MkcertBin is where a downloaded mkcert lives.
func (r Root) MkcertBin() string { return filepath.Join(r.Bin(), "mkcert") }

// PHPBin is the directory holding one PHP version's binaries.
func (r Root) PHPBin(version string) string { return filepath.Join(r.Bin(), "php", version) }

// PathBin holds the php of the global default version, the folder "Use in
// terminal" puts on PATH (php-terminal.feature).
func (r Root) PathBin() string { return filepath.Join(r.Bin(), "path") }

// Ensure creates the directories the daemon expects to exist.
func (r Root) Ensure() error {
	for _, d := range []string{r.Dir, r.Bin(), r.WWW(), r.Config(), r.Data(), r.LogDir(), r.CertDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
