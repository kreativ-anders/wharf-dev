package hostsfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manuel-steinberg/wharf/daemon/internal/elevate"
)

func newManager(t *testing.T, initial string) (*Manager, *elevate.Fake) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	el := elevate.NewFake()
	el.Apply = true
	return &Manager{Path: path, Elevator: el}, el
}

const userHosts = "##\n# Host Database\n##\n127.0.0.1\tlocalhost\n255.255.255.255\tbroadcasthost\n::1\tlocalhost\n"

// legacy is what Wharf wrote while projects were published as <name>.wharf.
func legacy(names ...string) string {
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "127.0.0.1\t%s.wharf\t# wharf:%s\n", n, n)
	}
	return b.String()
}

func TestRemovePreservesTheUsersOwnEntries(t *testing.T) {
	m, _ := newManager(t, userHosts+legacy("my-kirby-site"))

	if err := m.Remove("my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	body := read(t, m.Path)
	if strings.Contains(body, "my-kirby-site") {
		t.Fatalf("entry survived removal:\n%s", body)
	}
	if !strings.Contains(body, "broadcasthost") || !strings.Contains(body, "# Host Database") {
		t.Fatalf("user content lost on removal:\n%s", body)
	}
}

func TestRemoveOnlyTouchesTheNamedProject(t *testing.T) {
	m, _ := newManager(t, userHosts+legacy("one", "two", "three"))
	if err := m.Remove("two"); err != nil {
		t.Fatal(err)
	}
	body := read(t, m.Path)
	for _, keep := range []string{"one.wharf", "three.wharf"} {
		if !strings.Contains(body, keep) {
			t.Fatalf("%s was removed too:\n%s", keep, body)
		}
	}
	if strings.Contains(body, "two.wharf") {
		t.Fatalf("two.wharf remains:\n%s", body)
	}
}

// A user's own line for the same hostname is not ours to delete: only lines
// carrying this tool's marker are touched.
func TestHandWrittenEntriesAreNotRemoved(t *testing.T) {
	m, _ := newManager(t, userHosts+"127.0.0.1\tmy-kirby-site.wharf\n")
	if err := m.Remove("my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, m.Path), "my-kirby-site.wharf") {
		t.Fatal("a hand-written entry was deleted")
	}
}

func TestRemovingAnAbsentEntryDoesNotPrompt(t *testing.T) {
	m, el := newManager(t, userHosts)
	if err := m.Remove("never-added"); err != nil {
		t.Fatal(err)
	}
	if len(el.Writes) != 0 {
		t.Fatal("an elevation prompt was raised for a no-op removal")
	}
}

func TestDeclinedElevationIsReportedAsSuch(t *testing.T) {
	m, el := newManager(t, userHosts+legacy("site"))
	el.Decline = true
	if err := m.Remove("site"); err != elevate.ErrDeclined {
		t.Fatalf("error = %v, want ErrDeclined", err)
	}
	if !strings.Contains(read(t, m.Path), "site.wharf") {
		t.Fatal("the file changed despite the declined prompt")
	}
}

func TestCRLFFilesStayCRLF(t *testing.T) {
	m, _ := newManager(t, "127.0.0.1\tlocalhost\r\n127.0.0.1\tsite.wharf\t# wharf:site\r\n")
	if err := m.Remove("site"); err != nil {
		t.Fatal(err)
	}
	body := read(t, m.Path)
	if strings.Contains(body, "site.wharf") {
		t.Fatalf("entry survived removal:\n%q", body)
	}
	if strings.Contains(strings.ReplaceAll(body, "\r\n", ""), "\n") {
		t.Fatalf("a Windows hosts file gained bare LF line endings:\n%q", body)
	}
}

func TestHasAndEntries(t *testing.T) {
	m, _ := newManager(t, userHosts+legacy("site"))
	if ok, _ := m.Has("other"); ok {
		t.Fatal("Has reported an entry that does not exist")
	}
	if ok, err := m.Has("site"); err != nil || !ok {
		t.Fatalf("Has = %v, %v", ok, err)
	}
	got, err := m.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "site" {
		t.Fatalf("Entries = %v", got)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
