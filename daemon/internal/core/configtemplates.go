package core

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/configtemplate"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/project"
)

// ConfigTemplate is one config template in Settings, with the projects that
// use it (config-templates.feature).
type ConfigTemplate struct {
	configtemplate.Template
	Projects []string `json:"projects"`
}

func (d *Daemon) configTemplates(cfg *config.Config) []ConfigTemplate {
	out := []ConfigTemplate{}
	for _, t := range d.res.Templates().List() {
		out = append(out, ConfigTemplate{Template: t, Projects: usedBy(cfg, t.ID)})
	}
	return out
}

func usedBy(cfg *config.Config, id string) []string {
	names := []string{}
	for _, p := range cfg.Projects {
		if p.Template == id {
			names = append(names, p.Name)
		}
	}
	return names
}

// ReadConfigTemplate returns a config template's rules for one webserver, for
// the editor (config-templates.feature, "Editing a config template in Wharf").
func (d *Daemon) ReadConfigTemplate(id, server string) (string, error) {
	body, err := d.res.Templates().Read(id, server)
	if errors.Is(err, configtemplate.ErrNotFound) {
		return "", notFound("%s", err)
	}
	return body, err
}

// SaveConfigTemplate writes a config template's rules for one webserver and
// restarts what serves the projects using it. Saving a built-in one changes
// it until it is restored.
func (d *Daemon) SaveConfigTemplate(ctx context.Context, id, server, body string) error {
	if err := d.res.Templates().Save(id, server, body); err != nil {
		switch {
		case errors.Is(err, configtemplate.ErrNotFound):
			return notFound("%s", err)
		case errors.Is(err, configtemplate.ErrIncomplete):
			return invalid("%s", err)
		}
		return err
	}
	// INFO: Applied at once rather than on the watcher's next tick, so the
	// project already runs with the change when the editor closes.
	d.ApplyCustomConfigs(ctx)
	d.publish()
	return nil
}

// CreateConfigTemplate adds a config template of the user's. The typed name
// gets the rewrite a project name gets, so "My API" becomes "my-api"
// (config-templates.feature, "Creating a config template").
func (d *Daemon) CreateConfigTemplate(_ context.Context, name string) (configtemplate.Template, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	id := project.Slug(name)
	// INFO: A template id names files only, never a host, so dots are left out.
	id = strings.Trim(strings.NewReplacer(".", "-", "_", "-").Replace(id), "-")
	if id == "" {
		return configtemplate.Template{}, invalid("%q has no letter or digit to name a config template with — use at least one", name)
	}
	t, err := d.res.Templates().Create(id)
	switch {
	case errors.Is(err, configtemplate.ErrExists):
		return t, conflict("a config template named %q already exists — choose another name", id)
	case err != nil:
		return t, invalid("%s", err)
	}
	d.log.Info("config template created", "id", id)
	d.publish()
	return t, nil
}

// DeleteConfigTemplate deletes the user's own config template, or restores a
// built-in one to Wharf's rules. A template of the user's that a project
// still uses stays: the project would no longer start (config-templates
// .feature, "Deleting a config template").
func (d *Daemon) DeleteConfigTemplate(ctx context.Context, id string) error {
	d.mu.Lock()
	lib := d.res.Templates()
	builtin := slices.ContainsFunc(lib.List(), func(t configtemplate.Template) bool { return t.ID == id && t.Builtin })
	if used := usedBy(d.store.Get(), id); !builtin && len(used) > 0 {
		d.mu.Unlock()
		return conflict("the config template %q is used by %s — pick another template for %s first",
			id, strings.Join(used, ", "), pronoun(len(used)))
	}
	err := lib.Delete(id)
	d.mu.Unlock()
	if errors.Is(err, configtemplate.ErrNotFound) {
		return notFound("%s", err)
	}
	if err != nil {
		return err
	}
	d.ApplyCustomConfigs(ctx)
	d.publish()
	return nil
}

func pronoun(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// checkRules refuses a project's start while the rules it would be served
// with cannot be used — a config template that does not exist, a custom
// config without Wharf's placeholders — so the project says why instead of
// taking the front door down with it (config-templates.feature, "A project
// naming a config template that does not exist says so";
// app-configuration.feature, "A custom config keeps Wharf's placeholders").
func (d *Daemon) checkRules(cfg *config.Config, p config.Project) error {
	_, err := d.res.ProjectRules(p, cfg.WebserverFor(p))
	switch {
	case errors.Is(err, configtemplate.ErrNotFound):
		return notFound("%s", err)
	case errors.Is(err, configtemplate.ErrIncomplete):
		return invalid("%s", err)
	}
	return err
}
