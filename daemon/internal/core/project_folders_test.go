package core

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/ipc"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/project"
)

// outsideFolder creates a folder with one file in it, somewhere that is not
// the harness's www/.
func outsideFolder(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(append([]string{t.TempDir()}, parts...)...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php echo 'hi';"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeFile puts a file into dir, creating the folders on its way.
func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// features/project-folders.feature — "Adding a project with the folder picker"
func TestAddingAProjectWithTheFolderPicker(t *testing.T) {
	h := newHarness(t)
	dir := outsideFolder(t, "Code", "Client Site")
	writeFile(t, dir, "kirby/bootstrap.php", "<?php")

	// INFO: Then a sheet proposes the name "client-site", and the config template
	// detected in the folder
	got, err := h.d.InspectFolder(dir)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	want := Proposal{Path: dir, Name: "client-site", Template: "kirby"}
	if got != want {
		t.Fatalf("proposal = %+v, want %+v", got, want)
	}
	if len(h.d.Config().Projects) != 0 {
		t.Fatal("inspecting a folder registered it")
	}

	// INFO: When the user chooses "Add", then a project "client-site" is
	// registered and started
	p, err := h.d.AddFolder(h.ctx(), dir, Adding{Template: &got.Template, Start: true})
	if err != nil {
		t.Fatalf("add folder: %v", err)
	}
	if p.Name != "client-site" || p.Template != "kirby" {
		t.Fatalf("added %q with template %q, want client-site with kirby", p.Name, p.Template)
	}
	if err := waitFor(func() bool { return h.project("client-site").State == "running" }); err != nil {
		t.Fatalf("state = %q, want running", h.project("client-site").State)
	}

	// INFO: And its files stay where they are
	if _, err := os.Stat(filepath.Join(dir, "index.php")); err != nil {
		t.Fatalf("the folder's files moved: %v", err)
	}
	if _, err := os.Stat(h.root.ProjectDir("client-site")); !os.IsNotExist(err) {
		t.Fatal("a folder was created in www/")
	}
	// INFO: And the project's config entry records the folder's location as "path"
	entry, _ := h.d.Config().Project("client-site")
	if entry.Path != dir {
		t.Fatalf("path = %q, want %q", entry.Path, dir)
	}
	if st := h.project("client-site"); !st.Linked || st.Dir != dir {
		t.Fatalf("snapshot dir = %q linked = %v, want %q linked", st.Dir, st.Linked, dir)
	}
	// INFO: And its document root is that folder
	block := vhostBlock(t, h.readGenerated("nginx.conf"), "client-site.localhost")
	if !strings.Contains(block, `root "`+filepath.ToSlash(dir)+`";`) {
		t.Fatalf("document root is not the added folder:\n%s", block)
	}

	// INFO: Added without "start", as wharfctl does by default, it stays stopped.
	quiet, err := h.d.AddFolder(h.ctx(), outsideFolder(t, "Code", "Quiet"), Adding{})
	if err != nil {
		t.Fatal(err)
	}
	if quiet.State != "stopped" {
		t.Fatalf("a project added without start is %q", quiet.State)
	}
}

// features/project-folders.feature — "The proposed name comes from the folder
// name"
func TestTheProposedNameComesFromTheFolderName(t *testing.T) {
	h := newHarness(t)

	// INFO: Then its name is rewritten into a project name. The whole table is
	// project.Slug's test; this proves the proposal and the add both use it.
	dir := outsideFolder(t, "Code", "Müller & Söhne")
	got, err := h.d.InspectFolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "mueller-soehne" || got.Fixed {
		t.Fatalf("proposal = %+v, want the changeable name mueller-soehne", got)
	}
	p, err := h.d.AddFolder(h.ctx(), dir, Adding{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "mueller-soehne" || p.URL != "http://mueller-soehne.localhost" {
		t.Fatalf("added %q at %q", p.Name, p.URL)
	}

	// INFO: And a name sent to the daemon without the GUI gets the same rewrite
	p, err = h.d.AddFolder(h.ctx(), outsideFolder(t, "Code", "site"), Adding{Name: "Café_Relaunch 2026"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "cafe-relaunch-2026" {
		t.Fatalf("name = %q, want cafe-relaunch-2026", p.Name)
	}

	// INFO: And a name with no letter or digit in it is refused, asking for at least one
	_, err = h.d.AddFolder(h.ctx(), outsideFolder(t, "Code", "other"), Adding{Name: "!!!"})
	if err == nil || !strings.Contains(err.Error(), "letter or digit") {
		t.Fatalf("error = %v, want it to ask for a letter or digit", err)
	}
	if n := len(h.d.Config().Projects); n != 2 {
		t.Fatalf("%d projects registered, want 2", n)
	}
}

// features/project-folders.feature — "The config template is detected from the
// folder"
func TestTheDetectedConfigTemplateIsAddedWithTheProject(t *testing.T) {
	h := newHarness(t)

	// INFO: Every template detection can propose is one Wharf has.
	for _, id := range project.DetectedTemplates() {
		if !h.d.res.Templates().Exists(id) {
			t.Errorf("detection proposes %q, which is no config template", id)
		}
	}

	laravel := outsideFolder(t, "Code", "shop")
	writeFile(t, laravel, "artisan", "#!/usr/bin/env php")

	// INFO: Left to the daemon, the detected template is taken.
	if _, err := h.d.AddFolder(h.ctx(), laravel, Adding{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.d.Config().Project("shop"); got.Template != "laravel" {
		t.Fatalf("template = %q, want laravel", got.Template)
	}

	// INFO: And the user can pick another one, or "None", before adding
	none := outsideFolder(t, "Code", "plain")
	writeFile(t, none, "artisan", "#!/usr/bin/env php")
	if _, err := h.d.AddFolder(h.ctx(), none, Adding{Template: ptr("")}); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.d.Config().Project("plain"); got.Template != "" {
		t.Fatalf("template = %q, want none", got.Template)
	}
	if _, err := h.d.AddFolder(h.ctx(), outsideFolder(t, "Code", "odd"), Adding{Template: ptr("gone")}); err == nil {
		t.Fatal("a config template that does not exist was accepted")
	}

	// INFO: A folder in www/ added from its row gets the detected one too.
	h.mkProject("blog")
	writeFile(t, h.root.ProjectDir("blog"), "wp-config.php", "<?php")
	if _, err := h.d.AddProject(h.ctx(), "blog"); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.d.Config().Project("blog"); got.Template != "wordpress" {
		t.Fatalf("template = %q, want wordpress", got.Template)
	}
}

// features/project-folders.feature — "Choosing the webserver while adding a
// project"
func TestChoosingTheWebserverWhileAddingAProject(t *testing.T) {
	h := newHarness(t)
	if active := h.d.Config().Services.Webserver.Active; active != "nginx" {
		t.Fatalf("the harness's active webserver is %q, want nginx", active)
	}

	p, err := h.d.AddFolder(h.ctx(), outsideFolder(t, "Code", "legacy-app"), Adding{Webserver: "apache"})
	if err != nil {
		t.Fatal(err)
	}

	// INFO: Then "legacy-app" is registered with "webserver_override: apache"
	entry, _ := h.d.Config().Project("legacy-app")
	if entry.WebserverOverride == nil || *entry.WebserverOverride != "apache" {
		t.Fatalf("override = %v, want apache", entry.WebserverOverride)
	}
	if p.Webserver != "apache" {
		t.Fatalf("webserver = %q, want apache", p.Webserver)
	}

	// INFO: And a project added with the globally active webserver picked has no
	// override: the GUI sends none for it.
	if _, err := h.d.AddFolder(h.ctx(), outsideFolder(t, "Code", "plain"), Adding{}); err != nil {
		t.Fatal(err)
	}
	if plain, _ := h.d.Config().Project("plain"); plain.WebserverOverride != nil {
		t.Fatalf("a default project was pinned to %q", *plain.WebserverOverride)
	}

	// INFO: A webserver Wharf does not know is refused, and nothing registered.
	if _, err := h.d.AddFolder(h.ctx(), outsideFolder(t, "Code", "odd"), Adding{Webserver: "lighttpd"}); err == nil {
		t.Fatal("an unknown webserver was accepted")
	}
	if _, ok := h.d.Config().Project("odd"); ok {
		t.Fatal("a refused project was registered")
	}
}

// features/project-folders.feature — "Choosing a folder inside www/ registers
// it by name"
func TestChoosingAFolderInsideWWWRegistersItByName(t *testing.T) {
	h := newHarness(t)
	h.mkProject("my-kirby-site")

	// INFO: Then the sheet proposes "my-kirby-site", and the name cannot be
	// changed: it is the folder's
	got, err := h.d.InspectFolder(h.root.ProjectDir("my-kirby-site"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "my-kirby-site" || !got.Fixed {
		t.Fatalf("proposal = %+v, want the fixed name my-kirby-site", got)
	}
	// INFO: A name sent anyway is not the folder's, so it is ignored.
	p, err := h.d.AddFolder(h.ctx(), h.root.ProjectDir("my-kirby-site"), Adding{Name: "renamed"})
	if err != nil {
		t.Fatalf("add folder: %v", err)
	}
	if p.Name != "my-kirby-site" || p.Linked {
		t.Fatalf("got %q linked=%v, want my-kirby-site from www/", p.Name, p.Linked)
	}
	// INFO: And after "Add" no "path" key is written
	raw, err := os.ReadFile(h.root.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"path"`) {
		t.Fatalf("a path key was written for a folder in www/:\n%s", raw)
	}
}

// features/project-folders.feature — "A folder whose name is already taken"
func TestAFolderWhoseNameIsAlreadyTaken(t *testing.T) {
	h := newHarness(t)
	if _, err := h.d.AddFolder(h.ctx(), outsideFolder(t, "one", "client-site"), Adding{}); err != nil {
		t.Fatal(err)
	}
	second := outsideFolder(t, "two", "client-site")

	_, err := h.d.AddFolder(h.ctx(), second, Adding{})

	// INFO: Then the sheet says the name is taken
	var c *ConflictError
	if !errors.As(err, &c) {
		t.Fatalf("err = %v, want a conflict", err)
	}
	if !strings.Contains(err.Error(), "client-site") || !strings.Contains(err.Error(), "pick another name") {
		t.Fatalf("the error does not name the project and the fix: %v", err)
	}
	// INFO: And nothing is registered until the user picks another name
	if n := len(h.d.Config().Projects); n != 1 {
		t.Fatalf("%d projects registered, want 1", n)
	}
	if p, err := h.d.AddFolder(h.ctx(), second, Adding{Name: "client-site-2"}); err != nil || p.Name != "client-site-2" {
		t.Fatalf("another name was refused: %v", err)
	}

	// INFO: A folder in www/ with the same name would share its hostname too.
	h.mkProject("taken")
	if _, err := h.d.AddFolder(h.ctx(), outsideFolder(t, "three", "Taken"), Adding{}); !errors.As(err, &c) {
		t.Fatalf("err = %v, want a conflict with www/taken", err)
	}
}

// features/project-folders.feature — "Choosing a folder that is already a
// project"
func TestChoosingAFolderThatIsAlreadyAProject(t *testing.T) {
	h := newHarness(t)
	dir := outsideFolder(t, "Code", "Client Site")
	if _, err := h.d.AddFolder(h.ctx(), dir, Adding{}); err != nil {
		t.Fatal(err)
	}
	h.mustAdd("blog")

	for folder, want := range map[string]string{dir: "client-site", h.root.ProjectDir("blog"): "blog"} {
		got, err := h.d.InspectFolder(folder)
		if err != nil {
			t.Fatal(err)
		}
		if got.Project != want {
			t.Errorf("inspecting %s names the project %q, want %q", folder, got.Project, want)
		}
	}
	// INFO: And nothing is registered, even when asked directly.
	if _, err := h.d.AddFolder(h.ctx(), dir, Adding{Name: "again"}); err == nil {
		t.Fatal("a folder that is a project was added a second time")
	}
	names := []string{}
	for _, p := range h.d.Config().Projects {
		names = append(names, p.Name)
	}
	if slices.Contains(names, "again") || len(names) != 2 {
		t.Fatalf("projects = %v, want client-site and blog", names)
	}
}

// features/project-folders.feature — "Removing a project added from elsewhere
// leaves its folder alone"
func TestRemovingALinkedProjectLeavesItsFolderAlone(t *testing.T) {
	h := newHarness(t)
	dir := outsideFolder(t, "Code", "Client Site")
	if _, err := h.d.AddFolder(h.ctx(), dir, Adding{}); err != nil {
		t.Fatal(err)
	}

	if err := h.d.RemoveProject(h.ctx(), "client-site"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "index.php")); err != nil {
		t.Fatalf("the folder's files were touched: %v", err)
	}
	if _, ok := h.d.Config().Project("client-site"); ok {
		t.Fatal("project still registered")
	}
}

// features/project-folders.feature — "Adding a project with the folder picker"
// — as the GUI sends it: over IPC, where a template left out means "detect
// one" and "" means none, and both params carry a "name".
func TestAddingAProjectOverIPC(t *testing.T) {
	h := newHarness(t)
	socket := shortSocket(t)
	srv := ipc.NewServer(socket, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.SetTransport(ipc.DefaultTransport())
	h.d.Register(srv)
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	defer srv.Close()
	c, err := ipc.DialEndpoint(srv.Endpoint())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	shop := outsideFolder(t, "Code", "Shop")
	writeFile(t, shop, "artisan", "#!/usr/bin/env php")

	// INFO: projects.inspect proposes and registers nothing.
	var proposal Proposal
	if err := c.Call(ctx, ipc.MethodProjectInspect, map[string]string{"path": shop}, &proposal); err != nil {
		t.Fatalf("projects.inspect: %v", err)
	}
	if proposal.Name != "shop" || proposal.Template != "laravel" || proposal.Fixed || proposal.Project != "" {
		t.Fatalf("proposal = %+v", proposal)
	}
	if len(h.d.Config().Projects) != 0 {
		t.Fatal("projects.inspect registered the folder")
	}

	// INFO: What the sheet sends: its name, "" for no template, the other
	// webserver, and start.
	var added Project
	if err := c.Call(ctx, ipc.MethodProjectAdd, map[string]any{
		"path": shop, "name": "My Shop", "template": "", "webserver": "apache", "start": true,
	}, &added); err != nil {
		t.Fatalf("projects.add: %v", err)
	}
	entry, _ := h.d.Config().Project("my-shop")
	if added.Name != "my-shop" || entry.Template != "" ||
		entry.WebserverOverride == nil || *entry.WebserverOverride != "apache" {
		t.Fatalf("added %+v as %+v", added, entry)
	}
	if err := waitFor(func() bool { return h.project("my-shop").State == "running" }); err != nil {
		t.Fatalf("state = %q, want running", h.project("my-shop").State)
	}

	// INFO: No "template" key detects one, as the tray and wharfctl send it.
	blog := outsideFolder(t, "Code", "Blog")
	writeFile(t, blog, "wp-config.php", "<?php")
	if err := c.Call(ctx, ipc.MethodProjectAdd, map[string]any{"path": blog}, &added); err != nil {
		t.Fatalf("projects.add: %v", err)
	}
	if got, _ := h.d.Config().Project("blog"); got.Template != "wordpress" {
		t.Fatalf("template = %q, want wordpress", got.Template)
	}

	// INFO: A taken name arrives as a conflict, which the sheet shows in place.
	err = c.Call(ctx, ipc.MethodProjectAdd, map[string]any{"path": outsideFolder(t, "Other", "Blog")}, nil)
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) || ipcErr.Code != ipc.CodeConflict {
		t.Fatalf("error = %v, want a conflict", err)
	}

	// INFO: A folder that is a project names it.
	if err := c.Call(ctx, ipc.MethodProjectInspect, map[string]string{"path": blog}, &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.Project != "blog" {
		t.Fatalf("proposal names the project %q, want blog", proposal.Project)
	}
}
