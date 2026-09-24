package core

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/ipc"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/supervisor"
)

func (h *harness) pickTemplate(project, id string) {
	h.t.Helper()
	if _, err := h.d.UpdateSettings(h.ctx(), project, Settings{Template: &id}); err != nil {
		h.t.Fatalf("pick config template %q for %s: %v", id, project, err)
	}
}

func (h *harness) configTemplate(id string) ConfigTemplate {
	h.t.Helper()
	for _, t := range h.d.State().ConfigTemplates {
		if t.ID == id {
			return t
		}
	}
	h.t.Fatalf("config template %q not in state", id)
	return ConfigTemplate{}
}

// touchLater moves a file's modification time forward, so a change saved
// within the file system's clock resolution is still seen as one.
func touchLater(path string) {
	later := time.Now().Add(2 * time.Second)
	os.Chtimes(path, later, later)
}

// features/config-templates.feature — "A project without a config template
// gets the webserver's defaults"
func TestAProjectWithoutAConfigTemplateGetsTheWebserversDefaults(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-site")
	if err := h.d.StartProject(h.ctx(), "my-site"); err != nil {
		t.Fatal(err)
	}

	// INFO: Then its server block serves the project folder and runs .php files
	// with its PHP, and rewrites nothing
	block := vhostBlock(t, h.readGenerated("nginx.conf"), "my-site.localhost")
	root := `root "` + filepath.ToSlash(h.root.ProjectDir("my-site")) + `";`
	for _, want := range []string{root, `location ~ \.php$`, "fastcgi_pass 127.0.0.1:"} {
		if !strings.Contains(block, want) {
			t.Fatalf("nginx block lacks %q:\n%s", want, block)
		}
	}
	for _, not := range []string{"rewrite ", "try_files $uri $uri/", "location / "} {
		if strings.Contains(block, not) {
			t.Fatalf("nginx block has %q without a template:\n%s", not, block)
		}
	}

	// INFO: And served by Apache, the .htaccess files in the project folder apply
	if err := h.d.SetWebserver(h.ctx(), "apache"); err != nil {
		t.Fatal(err)
	}
	conf := h.readGenerated("apache.conf")
	for _, want := range []string{`DocumentRoot "` + filepath.ToSlash(h.root.ProjectDir("my-site")) + `"`, "AllowOverride All"} {
		if !strings.Contains(conf, want) {
			t.Fatalf("apache config lacks %q:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "content|site|kirby") {
		t.Fatalf("Kirby's rules apply to a project without a template:\n%s", conf)
	}

	// INFO: And no "template" key is written
	if strings.Contains(h.configFile(), `"template"`) {
		t.Fatalf("config/wharf.json has a template key:\n%s", h.configFile())
	}
}

// features/config-templates.feature — "Built-in config templates for common CMS
// and frameworks"
func TestBuiltInConfigTemplatesForCommonCMSAndFrameworks(t *testing.T) {
	h := newHarness(t)

	var names []string
	for _, tpl := range h.d.State().ConfigTemplates {
		names = append(names, tpl.Name)
		if !tpl.Builtin || tpl.Changed {
			t.Errorf("%s: builtin %v, changed %v", tpl.Name, tpl.Builtin, tpl.Changed)
		}
		// INFO: And each has rules for nginx and for Apache
		for _, server := range []string{"nginx", "apache"} {
			body, err := h.d.ReadConfigTemplate(tpl.ID, server)
			if err != nil || !strings.Contains(body, "{{root}}") {
				t.Errorf("%s has no %s rules naming the project folder: %v", tpl.Name, server, err)
			}
		}
	}
	want := []string{"Kirby", "Laravel", "WordPress", "Statamic", "Symfony", "Craft CMS", "Drupal"}
	if !slices.Equal(names, want) {
		t.Fatalf("config templates = %v, want %v", names, want)
	}
}

// features/config-templates.feature — "Picking a config template for a project"
func TestPickingAConfigTemplateForAProject(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-app")
	if err := h.d.StartProject(h.ctx(), "my-app"); err != nil {
		t.Fatal(err)
	}
	before := h.countStarts(runtimeWebserverID)

	h.pickTemplate("my-app", "laravel")

	// INFO: Then "template": "laravel" is written to "my-app"'s entry
	if !strings.Contains(h.configFile(), `"template": "laravel"`) {
		t.Fatalf("config/wharf.json does not record it:\n%s", h.configFile())
	}
	if got := h.project("my-app").Template; got != "laravel" {
		t.Errorf("snapshot names %q", got)
	}
	if used := h.configTemplate("laravel").Projects; !slices.Equal(used, []string{"my-app"}) {
		t.Errorf("Laravel is used by %v", used)
	}
	// INFO: And "my-app"'s server block serves "public/" with Laravel's rules
	block := vhostBlock(t, h.readGenerated("nginx.conf"), "my-app.localhost")
	for _, want := range []string{
		`root "` + filepath.ToSlash(h.root.ProjectDir("my-app")) + `/public";`,
		`try_files $uri $uri/ /index.php?$query_string;`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("nginx block lacks %q:\n%s", want, block)
		}
	}
	if strings.Count(block, "root ") != 1 {
		t.Fatalf("nginx refuses a second root:\n%s", block)
	}
	// INFO: And a running "my-app" is restarted with them
	if got := h.countStarts(runtimeWebserverID); got != before+1 {
		t.Fatalf("webserver started %d times, want %d", got, before+1)
	}

	// INFO: And picking "None" removes the key
	h.pickTemplate("my-app", "")
	if strings.Contains(h.configFile(), `"template"`) {
		t.Fatalf("the key is still there:\n%s", h.configFile())
	}

	none := "gone"
	if _, err := h.d.UpdateSettings(h.ctx(), "my-app", Settings{Template: &none}); err == nil {
		t.Fatal("a template that does not exist was accepted")
	}
}

// features/config-templates.feature — "Editing a config template in Wharf"
func TestEditingAConfigTemplateInWharf(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-app")
	h.mustAdd("other")
	h.pickTemplate("my-app", "laravel")
	for _, name := range []string{"my-app", "other"} {
		if err := h.d.StartProject(h.ctx(), name); err != nil {
			t.Fatal(err)
		}
	}
	before := h.countStarts(runtimeWebserverID)

	body, err := h.d.ReadConfigTemplate("laravel", "nginx")
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(body, "\n}", "\n  client_max_body_size 64m;\n}", 1)
	if err := h.d.SaveConfigTemplate(h.ctx(), "laravel", "nginx", changed); err != nil {
		t.Fatal(err)
	}

	// INFO: And saving writes "config/templates/<id>.<webserver>.conf"
	saved, err := os.ReadFile(filepath.Join(h.root.Config(), "templates", "laravel.nginx.conf"))
	if err != nil || !strings.Contains(string(saved), "client_max_body_size 64m;\n}") {
		t.Fatalf("saved file = %q, %v", saved, err)
	}
	// INFO: And every running project using the template is restarted with the change
	if got := h.countStarts(runtimeWebserverID); got != before+1 {
		t.Fatalf("webserver started %d times, want %d", got, before+1)
	}
	conf := h.readGenerated("nginx.conf")
	if !strings.Contains(vhostBlock(t, conf, "my-app.localhost"), "client_max_body_size 64m;") {
		t.Fatalf("my-app does not get the change:\n%s", conf)
	}
	if strings.Contains(vhostBlock(t, conf, "other.localhost"), "client_max_body_size 64m;") {
		t.Fatal("a project without the template got it")
	}
}

// features/config-templates.feature — "Changing a built-in config template, and
// restoring it"
func TestChangingABuiltInConfigTemplateAndRestoringIt(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("blog")
	h.pickTemplate("blog", "wordpress")
	if err := h.d.StartProject(h.ctx(), "blog"); err != nil {
		t.Fatal(err)
	}

	body, _ := h.d.ReadConfigTemplate("wordpress", "nginx")
	mine := strings.Replace(body, "\n}", "\n  # mine\n}", 1)
	if err := h.d.SaveConfigTemplate(h.ctx(), "wordpress", "nginx", mine); err != nil {
		t.Fatal(err)
	}
	// INFO: Then Settings marks "WordPress" as changed
	if !h.configTemplate("wordpress").Changed {
		t.Fatal("WordPress is not marked as changed")
	}
	// INFO: And projects using it get the changed rules
	if !strings.Contains(vhostBlock(t, h.readGenerated("nginx.conf"), "blog.localhost"), "# mine") {
		t.Fatal("blog does not get the changed rules")
	}

	// INFO: When the user chooses "Restore" on "WordPress"
	if err := h.d.DeleteConfigTemplate(h.ctx(), "wordpress"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.root.Config(), "templates", "wordpress.nginx.conf")); !os.IsNotExist(err) {
		t.Fatalf("the changed file is still there: %v", err)
	}
	tpl := h.configTemplate("wordpress")
	if tpl.Changed || !tpl.Builtin {
		t.Fatalf("after restoring: %+v", tpl)
	}
	block := vhostBlock(t, h.readGenerated("nginx.conf"), "blog.localhost")
	if strings.Contains(block, "# mine") || !strings.Contains(block, "try_files $uri $uri/ /index.php?$args;") {
		t.Fatalf("Wharf's own rules do not apply again:\n%s", block)
	}
}

// features/config-templates.feature — "Creating a config template"
func TestCreatingAConfigTemplate(t *testing.T) {
	h := newHarness(t)

	tpl, err := h.d.CreateConfigTemplate(h.ctx(), "My API")
	if err != nil {
		t.Fatal(err)
	}
	if tpl.ID != "my-api" || tpl.Builtin {
		t.Fatalf("created %+v", tpl)
	}
	for _, server := range []string{"nginx", "apache"} {
		body, err := h.d.ReadConfigTemplate("my-api", server)
		if err != nil || !strings.Contains(body, "{{root}}") {
			t.Fatalf("%s starting point = %q, %v", server, body, err)
		}
	}
	if got := h.d.State().ConfigTemplates; got[len(got)-1].ID != "my-api" {
		t.Fatalf("the new template is not listed after Wharf's: %+v", got)
	}

	// INFO: And a name another config template already has is refused
	for _, taken := range []string{"my api", "Laravel"} {
		_, err := h.d.CreateConfigTemplate(h.ctx(), taken)
		var ipcErr *ipc.Error
		if !errors.As(asIPCError(err), &ipcErr) || ipcErr.Code != ipc.CodeConflict {
			t.Errorf("creating %q: %v, want a conflict", taken, err)
		}
	}
}

// features/config-templates.feature — "Deleting a config template"
func TestDeletingAConfigTemplate(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("api")
	if _, err := h.d.CreateConfigTemplate(h.ctx(), "my-api"); err != nil {
		t.Fatal(err)
	}
	h.pickTemplate("api", "my-api")

	// INFO: But a config template a project still uses is not deleted, and the
	// message names those projects
	err := h.d.DeleteConfigTemplate(h.ctx(), "my-api")
	if err == nil || !strings.Contains(err.Error(), "api") {
		t.Fatalf("deleting a template in use: %v", err)
	}
	if _, err := h.d.ReadConfigTemplate("my-api", "nginx"); err != nil {
		t.Fatalf("the template in use was deleted: %v", err)
	}

	h.pickTemplate("api", "")
	if err := h.d.DeleteConfigTemplate(h.ctx(), "my-api"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(h.root.Config(), "templates"))
	if len(entries) != 0 {
		t.Fatalf("files are left in config/templates/: %v", entries)
	}
	for _, tpl := range h.d.State().ConfigTemplates {
		if tpl.ID == "my-api" {
			t.Fatal("the deleted template is still listed")
		}
	}
}

// features/config-templates.feature — "A config template edited in another
// editor applies too"
func TestAConfigTemplateEditedInAnotherEditorApplies(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-app")
	h.pickTemplate("my-app", "laravel")
	if err := h.d.StartProject(h.ctx(), "my-app"); err != nil {
		t.Fatal(err)
	}
	before := h.countStarts(runtimeWebserverID)

	path := filepath.Join(h.root.Config(), "templates", "laravel.nginx.conf")
	os.MkdirAll(filepath.Dir(path), 0o755)
	body, _ := h.d.ReadConfigTemplate("laravel", "nginx")
	if err := os.WriteFile(path, []byte(strings.Replace(body, "\n}", "\n  # by hand\n}", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	touchLater(path)
	h.d.ApplyCustomConfigs(h.ctx())

	if got := h.countStarts(runtimeWebserverID); got != before+1 {
		t.Fatalf("webserver started %d times, want %d", got, before+1)
	}
	if !strings.Contains(h.readGenerated("nginx.conf"), "# by hand") {
		t.Fatal("the hand-edited rules are not applied")
	}
}

// features/config-templates.feature — "A project naming a config template that
// does not exist says so"
func TestAProjectNamingAMissingConfigTemplateSaysSo(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-app")
	h.mustAdd("other")
	if err := h.d.StartProject(h.ctx(), "other"); err != nil {
		t.Fatal(err)
	}
	// INFO: Given "my-app" names the config template "gone" in config/wharf.json
	raw := strings.Replace(h.configFile(), `"name": "my-app",`, `"name": "my-app", "template": "gone",`, 1)
	if err := os.WriteFile(h.root.ConfigFile(), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.d.store.Reload(); err != nil {
		t.Fatal(err)
	}

	if err := h.d.StartProject(h.ctx(), "my-app"); err == nil {
		t.Fatal("my-app started with a template that does not exist")
	}
	p := h.project("my-app")
	if p.State != string(supervisor.StateFailed) || !strings.Contains(p.Error, `"gone" does not exist`) ||
		!strings.Contains(p.Error, "settings") {
		t.Fatalf("my-app = %q %q", p.State, p.Error)
	}
	if !h.sup.Running(runtimeWebserverID) {
		t.Fatal("the front door went down with it")
	}
}

// features/config-templates.feature — "A config template is a whole server
// block, with placeholders for what only Wharf knows"
func TestAConfigTemplateIsAWholeServerBlock(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.pickTemplate("my-kirby-site", "kirby")
	if _, err := h.d.UpdateSettings(h.ctx(), "my-kirby-site", Settings{SSL: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}

	// INFO: Then the project's generated file holds the template's block once,
	// listening on port 80 and on 443, with Wharf's values filled in
	file, err := os.ReadFile(filepath.Join(h.root.Data(), "gen", "nginx", "my-kirby-site.conf"))
	if err != nil {
		t.Fatal(err)
	}
	conf := string(file)
	if strings.Contains(conf, "{{") {
		t.Fatalf("a placeholder is left:\n%s", conf)
	}
	if n := strings.Count(conf, "server {"); n != 1 {
		t.Fatalf("%d server blocks, want one for HTTP and HTTPS together:\n%s", n, conf)
	}
	for _, want := range []string{
		"listen 80;", "listen 443 ssl;", "ssl_certificate \"", "server_name my-kirby-site.localhost;",
		`location ~* \.php$ {`, "fastcgi_param PATH_INFO $fastcgi_path_info;",
	} {
		if !strings.Contains(conf, want) {
			t.Fatalf("generated file lacks %q:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "Wharf fills in every") {
		t.Fatal("the template's header comment is repeated in the generated file")
	}

	// INFO: And the rules without {{listen}}, {{ssl}} or {{server_name}} are refused
	body, _ := h.d.ReadConfigTemplate("kirby", "nginx")
	broken := strings.Replace(body, "  {{listen}}\n", "  listen 8080;\n", 1)
	err = h.d.SaveConfigTemplate(h.ctx(), "kirby", "nginx", broken)
	var ipcErr *ipc.Error
	if !errors.As(asIPCError(err), &ipcErr) || ipcErr.Code != ipc.CodeBadRequest || !strings.Contains(err.Error(), "{{listen}}") {
		t.Fatalf("saving without {{listen}}: %v", err)
	}
	if h.configTemplate("kirby").Changed {
		t.Fatal("the refused rules were saved")
	}
}

func ptr[T any](v T) *T { return &v }
