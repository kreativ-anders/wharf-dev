package configtemplate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// An id arrives over IPC and is joined into a path, so one that climbs out of
// config/templates/ must name no template at all.
func TestAnIDOutsideTheTemplateFolderNamesNoTemplate(t *testing.T) {
	base := t.TempDir()
	lib := Library{Dir: filepath.Join(base, "config", "templates")}
	outside := filepath.Join(base, "victim.nginx.conf")
	if err := os.WriteFile(outside, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id := "../../victim"

	if lib.Exists(id) {
		t.Fatal("Exists accepted an id outside config/templates/")
	}
	if _, err := lib.Read(id, "nginx"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Read = %v, want ErrNotFound", err)
	}
	if err := lib.Save(id, "nginx", "{{listen}} {{ssl}} {{server_name}}"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Save = %v, want ErrNotFound", err)
	}
	if err := lib.Delete(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete = %v, want ErrNotFound", err)
	}
	if body, err := os.ReadFile(outside); err != nil || string(body) != "keep me\n" {
		t.Fatalf("the file outside was changed: %q, %v", body, err)
	}
}

func TestAnOwnTemplateIsStillFound(t *testing.T) {
	lib := Library{Dir: t.TempDir()}
	if _, err := lib.Create("my-api"); err != nil {
		t.Fatal(err)
	}
	if !lib.Exists("my-api") {
		t.Fatal("a created template does not exist")
	}
	if _, err := lib.Read("my-api", "nginx"); err != nil {
		t.Fatal(err)
	}
	if !lib.Exists("kirby") {
		t.Fatal("a built-in template does not exist")
	}
}
