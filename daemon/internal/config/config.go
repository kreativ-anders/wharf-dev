// Package config owns wharf.json: the single flat config file that is the
// whole persistent state of the tool (dev/architecture.md §6).
//
// The file is meant to be hand-edited (dev/design-principles.md §2), so
// marshalling is deliberately lossy in one direction only: keys that carry no
// meaning are omitted rather than written as nulls, and indentation is stable
// so a GUI write produces a readable diff against a hand-written file.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/manuel-steinberg/wharf/daemon/internal/php"
)

// Config is the on-disk shape of config/wharf.json.
type Config struct {
	// Appearance is "light" or "dark"; absent means follow the system
	// (settings.feature, "Choosing light or dark appearance").
	Appearance string    `json:"appearance,omitempty"`
	Services   Services  `json:"services"`
	Projects   []Project `json:"projects"`
}

// Appearances are the values Appearance may take; "system" is written as the
// key's absence.
var Appearances = []string{"system", "light", "dark"}

// Services holds the globally active service selections.
type Services struct {
	Webserver Webserver `json:"webserver"`
	PHP       PHP       `json:"php"`
}

// Webserver is the global webserver selection. A project may override it.
type Webserver struct {
	Active    string   `json:"active"`
	Available []string `json:"available"`
}

// PHP is the global PHP runtime selection.
type PHP struct {
	Version   string   `json:"version"`
	Available []string `json:"available"`
	// Paths locates versions that are not vendored under bin/php/<version> —
	// a PHP already installed on the machine that the tool adopted. The key is
	// absent for vendored builds, so a self-contained install writes no paths
	// at all and the file stays as short as dev/architecture.md §6 shows it.
	Paths map[string]string `json:"paths,omitempty"`
}

// Project is one folder under www/. Override fields are pointers so that
// clearing an override removes the key from the file entirely, rather than
// leaving a null behind (app-configuration.feature, "Removing an override").
type Project struct {
	Name              string  `json:"name"`
	WebserverOverride *string `json:"webserver_override,omitempty"`
	PHPVersion        *string `json:"php_version,omitempty"`
	SSL               bool    `json:"ssl"`
	// Port is the raw port the project is served on. Zero means "assign on
	// first start"; it is persisted afterwards so the fallback URL shown when
	// elevation is declined stays stable across restarts.
	Port int `json:"port,omitempty"`
	// HostsEntry records whether a hosts-file line exists for this project.
	// Persisted because removing a project must remove exactly the entry that
	// was written (pretty-urls.feature) and elevation may have been declined.
	HostsEntry bool `json:"hosts_entry,omitempty"`
	// Path is the project's folder when it lives outside www/. Absent for a
	// folder in www/, which is found by name (project-folders.feature).
	Path string `json:"path,omitempty"`
}

// Default returns the config written on first run.
func Default() *Config {
	return &Config{
		Services: Services{
			Webserver: Webserver{Active: "nginx", Available: []string{"apache", "nginx"}},
			PHP:       PHP{Version: "8.3", Available: []string{"8.3"}},
		},
		Projects: []Project{},
	}
}

// Store reads and writes a single config file. All access is serialised; the
// daemon holds exactly one Store for the lifetime of the process.
type Store struct {
	path string

	// Created reports that Load wrote a fresh config because none existed.
	// First-run setup — detecting the PHP versions already on the machine —
	// keys off this.
	Created bool

	mu  sync.RWMutex
	cfg *Config
}

// Load opens the config at path, creating it with defaults if absent.
func Load(path string) (*Store, error) {
	s := &Store{path: path}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.Created = true
		s.cfg = Default()
		if err := s.write(s.cfg); err != nil {
			return nil, err
		}
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.normalise()
	s.cfg = cfg
	return s, nil
}

// Path returns the file this store is backed by.
func (s *Store) Path() string { return s.path }

// Get returns a deep copy of the current config. Callers may mutate it freely.
func (s *Store) Get() *Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.clone()
}

// Update applies fn to a copy of the config and, if fn returns no error,
// persists the result atomically. The mutated copy is passed to fn so a failed
// update never leaves the in-memory config half-changed.
func (s *Store) Update(fn func(*Config) error) (*Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.cfg.clone()
	if err := fn(next); err != nil {
		return nil, err
	}
	next.normalise()
	if err := s.write(next); err != nil {
		return nil, err
	}
	s.cfg = next
	return next.clone(), nil
}

// Reload re-reads the file from disk, discarding the in-memory copy. Used by
// the file watcher so a hand-edit is picked up without restarting the daemon.
func (s *Store) Reload() (*Config, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := &Config{}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	cfg.normalise()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
	return cfg.clone(), nil
}

// write persists cfg atomically: a temp file in the same directory, then a
// rename, so a crash mid-write cannot truncate a user's hand-edited config.
func (s *Store) write(cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".wharf-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// Project returns the named project and whether it exists.
func (c *Config) Project(name string) (Project, bool) {
	for _, p := range c.Projects {
		if p.Name == name {
			return p, true
		}
	}
	return Project{}, false
}

// SetProject inserts or replaces a project by name.
func (c *Config) SetProject(p Project) {
	for i := range c.Projects {
		if c.Projects[i].Name == p.Name {
			c.Projects[i] = p
			return
		}
	}
	c.Projects = append(c.Projects, p)
}

// RemoveProject deletes a project by name, reporting whether it was present.
func (c *Config) RemoveProject(name string) bool {
	for i := range c.Projects {
		if c.Projects[i].Name == name {
			c.Projects = append(c.Projects[:i], c.Projects[i+1:]...)
			return true
		}
	}
	return false
}

// WebserverFor resolves the webserver a project is served by: its own
// override if set, otherwise the global active one
// (service-management.feature, "Per-project override takes precedence").
func (c *Config) WebserverFor(p Project) string {
	if p.WebserverOverride != nil && *p.WebserverOverride != "" {
		return *p.WebserverOverride
	}
	return c.Services.Webserver.Active
}

// PHPVersionFor resolves the PHP version a project is served by
// (app-configuration.feature, "Overriding the PHP version for one project").
func (c *Config) PHPVersionFor(p Project) string {
	if p.PHPVersion != nil && *p.PHPVersion != "" {
		return *p.PHPVersion
	}
	return c.Services.PHP.Version
}

// normalise fills in anything a hand-edited file may have left out and drops
// overrides that are empty strings, so the rest of the daemon can assume the
// invariants without re-checking them.
func (c *Config) normalise() {
	def := Default()
	if c.Appearance != "light" && c.Appearance != "dark" {
		c.Appearance = ""
	}
	if c.Services.Webserver.Active == "" {
		c.Services.Webserver.Active = def.Services.Webserver.Active
	}
	if len(c.Services.Webserver.Available) == 0 {
		c.Services.Webserver.Available = def.Services.Webserver.Available
	}
	if c.Services.PHP.Version == "" {
		c.Services.PHP.Version = def.Services.PHP.Version
	}
	if len(c.Services.PHP.Available) == 0 {
		c.Services.PHP.Available = []string{c.Services.PHP.Version}
	}
	if !contains(c.Services.Webserver.Available, c.Services.Webserver.Active) {
		c.Services.Webserver.Available = append(c.Services.Webserver.Available, c.Services.Webserver.Active)
	}
	if !contains(c.Services.PHP.Available, c.Services.PHP.Version) {
		c.Services.PHP.Available = append(c.Services.PHP.Available, c.Services.PHP.Version)
	}
	sort.Strings(c.Services.Webserver.Available)
	// Versions sort numerically, so that 8.10 lands after 8.9 rather than
	// before it.
	php.Sort(c.Services.PHP.Available)

	for v := range c.Services.PHP.Paths {
		if !contains(c.Services.PHP.Available, v) {
			delete(c.Services.PHP.Paths, v)
		}
	}
	if len(c.Services.PHP.Paths) == 0 {
		c.Services.PHP.Paths = nil
	}

	if c.Projects == nil {
		c.Projects = []Project{}
	}
	for i := range c.Projects {
		p := &c.Projects[i]
		p.Name = strings.TrimSpace(p.Name)
		p.Path = strings.TrimSpace(p.Path)
		if p.WebserverOverride != nil && strings.TrimSpace(*p.WebserverOverride) == "" {
			p.WebserverOverride = nil
		}
		if p.PHPVersion != nil && strings.TrimSpace(*p.PHPVersion) == "" {
			p.PHPVersion = nil
		}
	}
}

func (c *Config) clone() *Config {
	out := &Config{
		Appearance: c.Appearance,
		Services: Services{
			Webserver: Webserver{
				Active:    c.Services.Webserver.Active,
				Available: append([]string(nil), c.Services.Webserver.Available...),
			},
			PHP: PHP{
				Version:   c.Services.PHP.Version,
				Available: append([]string(nil), c.Services.PHP.Available...),
				Paths:     clonePaths(c.Services.PHP.Paths),
			},
		},
		Projects: make([]Project, len(c.Projects)),
	}
	for i, p := range c.Projects {
		out.Projects[i] = p
		if p.WebserverOverride != nil {
			out.Projects[i].WebserverOverride = strptr(*p.WebserverOverride)
		}
		if p.PHPVersion != nil {
			out.Projects[i].PHPVersion = strptr(*p.PHPVersion)
		}
	}
	return out
}

// PHPPath returns where a version's binaries live, and whether the tool
// adopted it from the machine rather than vendoring it.
func (c *Config) PHPPath(version string) (string, bool) {
	p, ok := c.Services.PHP.Paths[version]
	return p, ok && p != ""
}

func clonePaths(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func strptr(s string) *string { return &s }

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
