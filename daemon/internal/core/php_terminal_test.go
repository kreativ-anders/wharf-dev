package core

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/ipc"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/php"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/shellpath"
)

// withCLI stages the php binary beside each version's FastCGI binary — the
// harness stages FastCGI alone — and re-scans, so the versions can be linked.
func (h *harness) withCLI(versions ...string) {
	h.t.Helper()
	for _, v := range versions {
		stubBinary(h.t, filepath.Join(h.root.PHPBin(v), php.CLIName()))
	}
	h.d.RefreshPHP(h.ctx())
}

func (h *harness) configFile() string {
	h.t.Helper()
	raw, err := os.ReadFile(h.root.ConfigFile())
	if err != nil {
		h.t.Fatal(err)
	}
	return string(raw)
}

// features/php-terminal.feature — "The global default PHP is kept in one folder"
func TestTheDefaultPHPIsLinkedIntoBinPath(t *testing.T) {
	h := newHarness(t)
	h.withCLI("8.2", "8.3")

	if err := h.d.SetPHPVersion(h.ctx(), "8.3"); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(h.root.PHPBin("8.3"), php.CLIName())
	if got := shellpath.Linked(h.root.PathBin()); got != want {
		t.Fatalf("bin/path runs %q, want %q", got, want)
	}

	// INFO: When the user selects "8.3" — here the other way round, to "8.2".
	if err := h.d.SetPHPVersion(h.ctx(), "8.2"); err != nil {
		t.Fatal(err)
	}
	want = filepath.Join(h.root.PHPBin("8.2"), php.CLIName())
	if got := shellpath.Linked(h.root.PathBin()); got != want {
		t.Fatalf("after switching, bin/path runs %q, want %q", got, want)
	}
	if got := h.d.State().Services.PHP.Terminal.PHP; got != want {
		t.Errorf("snapshot names %q, want %q", got, want)
	}
}

// features/php-terminal.feature — "The global default PHP is not installed"
func TestBinPathHoldsNoPHPWhileTheDefaultIsMissing(t *testing.T) {
	h := newHarness(t)
	h.withCLI("8.2", "8.3")
	if err := h.d.SetPHPVersion(h.ctx(), "8.3"); err != nil {
		t.Fatal(err)
	}

	// INFO: The default's copy goes, as when its folder is deleted by hand.
	if err := os.RemoveAll(h.root.PHPBin("8.3")); err != nil {
		t.Fatal(err)
	}
	h.d.RefreshPHP(h.ctx())

	if got := shellpath.Linked(h.root.PathBin()); got != "" {
		t.Fatalf("bin/path still runs %q", got)
	}
	if _, err := os.Lstat(filepath.Join(h.root.PathBin(), shellpath.LinkName())); !os.IsNotExist(err) {
		t.Fatalf("bin/path still holds %s: %v", shellpath.LinkName(), err)
	}
	if got := h.d.State().Services.PHP.Terminal.PHP; got != "" {
		t.Errorf("snapshot names %q, want none so the page can say so", got)
	}
}

// features/php-terminal.feature — "Putting Wharf's PHP on the terminal PATH"
func TestUseInTerminalPutsBinPathOnPath(t *testing.T) {
	h := newHarness(t)

	if err := h.d.SetPHPTerminal(h.ctx(), true); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(h.configFile(), `"terminal": true`) {
		t.Errorf("config/wharf.json does not record it:\n%s", h.configFile())
	}
	if !h.shell.OnPath(h.root.PathBin()) {
		t.Fatalf("bin/path is not on PATH; calls %v", h.shell.Calls())
	}
	term := h.d.State().Services.PHP.Terminal
	if !term.On || term.Dir != h.root.PathBin() || !slices.Equal(term.Places, []string{"~/.zshrc"}) {
		t.Errorf("snapshot = %+v, want it on, naming bin/path and the place changed", term)
	}
	if h.el.RunCount() != 0 || len(h.el.Writes) != 0 {
		t.Errorf("a password was asked for: %d runs, %d writes", h.el.RunCount(), len(h.el.Writes))
	}
}

func TestAFailedChangeLeavesTheSwitchOff(t *testing.T) {
	h := newHarness(t)
	h.shell.Err = os.ErrPermission

	if err := h.d.SetPHPTerminal(h.ctx(), true); err == nil {
		t.Fatal("want the error")
	}
	if h.d.Config().Services.PHP.Terminal {
		t.Fatal("the switch was recorded as on although nothing was changed")
	}
}

// features/php-terminal.feature — "Turning it on again adds nothing twice"
func TestStartingAgainPutsBinPathOnPathOnce(t *testing.T) {
	h := newHarness(t)
	if err := h.d.SetPHPTerminal(h.ctx(), true); err != nil {
		t.Fatal(err)
	}
	if err := h.d.SetPHPTerminal(h.ctx(), true); err != nil {
		t.Fatal(err)
	}

	// INFO: Wharf starts again on the same folder, its config saying "on".
	opts := h.opts
	store, err := config.Load(h.root.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	opts.Store = store
	again, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}

	adds := 0
	for _, call := range h.shell.Calls() {
		if strings.HasPrefix(call, "add ") {
			adds++
		} else {
			t.Errorf("unexpected %q", call)
		}
	}
	if adds != 3 {
		t.Errorf("added %d times, want once per switch and once at start", adds)
	}
	if places := again.State().Services.PHP.Terminal.Places; !slices.Equal(places, []string{"~/.zshrc"}) {
		t.Errorf("after starting again the snapshot names %v", places)
	}
	// INFO: Exactly once in each file is proven against real files in
	// internal/shellpath, "TestAddingAgainChangesNothing".
}

func TestStartingWithTheSwitchOffTouchesNothing(t *testing.T) {
	h := newHarness(t)
	if calls := h.shell.Calls(); len(calls) != 0 {
		t.Fatalf("a start with the switch off changed the PATH: %v", calls)
	}
}

// features/php-terminal.feature — "Taking Wharf's PHP off the terminal PATH"
func TestTurningItOffTakesBinPathOffPath(t *testing.T) {
	h := newHarness(t)
	if err := h.d.SetPHPTerminal(h.ctx(), true); err != nil {
		t.Fatal(err)
	}
	if err := h.d.SetPHPTerminal(h.ctx(), false); err != nil {
		t.Fatal(err)
	}

	if h.shell.OnPath(h.root.PathBin()) {
		t.Fatal("bin/path is still on PATH")
	}
	if strings.Contains(h.configFile(), `"terminal"`) {
		t.Errorf("the key is still in config/wharf.json:\n%s", h.configFile())
	}
	if term := h.d.State().Services.PHP.Terminal; term.On || len(term.Places) != 0 {
		t.Errorf("snapshot = %+v", term)
	}
}

// features/php-terminal.feature — "Quitting Wharf leaves PHP in the terminal"
func TestCastingOffLeavesBinPathOnPath(t *testing.T) {
	h := newHarness(t)
	h.withCLI("8.3")
	if err := h.d.SetPHPVersion(h.ctx(), "8.3"); err != nil {
		t.Fatal(err)
	}
	if err := h.d.SetPHPTerminal(h.ctx(), true); err != nil {
		t.Fatal(err)
	}

	// INFO: Cast off stops everything, then the daemon shuts down.
	if err := h.d.StopAll(h.ctx()); err != nil {
		t.Fatal(err)
	}
	if err := h.d.Shutdown(h.ctx()); err != nil {
		t.Fatal(err)
	}

	if !h.shell.OnPath(h.root.PathBin()) {
		t.Fatal("quitting took bin/path off PATH")
	}
	if got := shellpath.Linked(h.root.PathBin()); got == "" {
		t.Fatal("quitting removed bin/path's php, so a new terminal finds none")
	}
}

// features/php-terminal.feature — "Resetting Wharf takes PHP off the terminal PATH"
func TestResetTakesBinPathOffPath(t *testing.T) {
	h := newHarness(t)
	h.withCLI("8.1", "8.2", "8.3")
	if err := h.d.SetPHPTerminal(h.ctx(), true); err != nil {
		t.Fatal(err)
	}

	if err := h.d.Reset(h.ctx()); err != nil {
		t.Fatal(err)
	}

	if h.shell.OnPath(h.root.PathBin()) {
		t.Fatal("bin/path is still on PATH after a reset")
	}
	if h.d.Config().Services.PHP.Terminal {
		t.Fatal("the switch is still on after a reset")
	}
	cfg := h.d.Config()
	want := filepath.Join(h.root.PHPBin(cfg.Services.PHP.Version), php.CLIName())
	if got := shellpath.Linked(h.root.PathBin()); got != want {
		t.Fatalf("after a reset bin/path runs %q, want the first start's default %q", got, want)
	}
}

// features/php-terminal.feature — "A Wharf block broken by hand is not guessed at"
func TestABrokenBlockIsAConflictNotAFailure(t *testing.T) {
	h := newHarness(t)
	h.shell.Err = &shellpath.BrokenBlockError{Path: "/Users/x/.zshrc"}

	err := h.d.SetPHPTerminal(h.ctx(), true)
	var ipcErr *ipc.Error
	if !errors.As(asIPCError(err), &ipcErr) || ipcErr.Code != ipc.CodeConflict {
		t.Fatalf("err = %v, want a conflict the GUI shows as a message", asIPCError(err))
	}
	if !strings.Contains(err.Error(), "/Users/x/.zshrc") {
		t.Errorf("the message does not name the file: %v", err)
	}
	if h.d.Config().Services.PHP.Terminal {
		t.Error("the switch was recorded as on")
	}
}
