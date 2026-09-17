//go:build unix

package shellpath

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// features/php-terminal.feature — "Putting Wharf's PHP on the terminal PATH"
func TestAddWritesTheLoginShellFileAndEveryExistingOne(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".bashrc"), "alias ll='ls -l'\n")
	writeFile(t, filepath.Join(home, ".profile"), "umask 022")
	if err := os.MkdirAll(filepath.Join(home, ".config", "fish"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Profiles{Home: home, Shell: "/bin/zsh"}
	dir := "/Users/x/Wharf/bin/path"

	got, err := p.Add(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".profile"),
		filepath.Join(home, ".config", "fish", "conf.d", "wharf.fish"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("changed %v, want %v", got, want)
	}

	zshrc := readFile(t, want[0])
	if !strings.HasPrefix(zshrc, blockStart) || !strings.HasSuffix(zshrc, blockEnd+"\n") {
		t.Errorf(".zshrc was created holding only the block:\n%s", zshrc)
	}
	bashrc := readFile(t, want[1])
	if !strings.HasPrefix(bashrc, "alias ll='ls -l'\n\n"+blockStart) {
		t.Errorf("the block must follow the user's lines after one blank line:\n%s", bashrc)
	}
	if !strings.HasPrefix(readFile(t, want[2]), "umask 022\n\n"+blockStart) {
		t.Errorf(".profile without a final newline must keep its last line:\n%s", readFile(t, want[2]))
	}
	if !strings.Contains(readFile(t, want[3]), `set -gx PATH "/Users/x/Wharf/bin/path" $PATH`) {
		t.Errorf("fish block:\n%s", readFile(t, want[3]))
	}
	if !slices.Equal(p.Where(dir), want) {
		t.Errorf("Where = %v, want %v", p.Where(dir), want)
	}
	if info, _ := os.Stat(want[1]); info.Mode().Perm() != 0o600 {
		t.Errorf(".bashrc mode = %v, want the user's 0600 kept", info.Mode().Perm())
	}
}

// features/php-terminal.feature — "Putting Wharf's PHP on the terminal PATH"
func TestTheBlockPutsTheFolderFirstOnPathOnce(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	home := t.TempDir()
	p := &Profiles{Home: home, Shell: "/bin/sh"}
	dir := filepath.Join(home, `odd "dir" $HOME`, "bin", "path")
	if _, err := p.Add(dir); err != nil {
		t.Fatal(err)
	}

	// INFO: Sourced twice, as a shell started from another shell would.
	profile := filepath.Join(home, ".profile")
	script := `PATH=/usr/bin:/bin; . "$1"; . "$1"; printf %s "$PATH"`
	out, err := exec.Command(sh, "-c", script, "sh", profile).Output()
	if err != nil {
		t.Fatal(err)
	}
	if want := dir + ":/usr/bin:/bin"; string(out) != want {
		t.Fatalf("PATH = %q, want %q", out, want)
	}
}

// features/php-terminal.feature — "Turning it on again adds nothing twice"
func TestAddingAgainChangesNothing(t *testing.T) {
	home := t.TempDir()
	zshrc := filepath.Join(home, ".zshrc")
	writeFile(t, zshrc, "export EDITOR=vim\n")
	p := &Profiles{Home: home, Shell: "/bin/zsh"}
	dir := "/w/bin/path"

	if _, err := p.Add(dir); err != nil {
		t.Fatal(err)
	}
	// INFO: A line the user adds after the block keeps its place.
	writeFile(t, zshrc, readFile(t, zshrc)+"alias g=git\n")
	first := readFile(t, zshrc)
	if _, err := p.Add(dir); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, zshrc); got != first {
		t.Fatalf("adding again changed the file:\n%s\nwant:\n%s", got, first)
	}
	if n := strings.Count(first, blockStart); n != 1 {
		t.Fatalf("block appears %d times", n)
	}

	// INFO: A moved Wharf folder replaces the old block instead of adding one.
	if _, err := p.Add("/moved/bin/path"); err != nil {
		t.Fatal(err)
	}
	moved := readFile(t, zshrc)
	if strings.Count(moved, blockStart) != 1 || strings.Contains(moved, "/w/bin/path") {
		t.Fatalf("moved folder:\n%s", moved)
	}
}

// features/php-terminal.feature — "Taking Wharf's PHP off the terminal PATH"
func TestRemoveLeavesEveryOtherLine(t *testing.T) {
	home := t.TempDir()
	original := "# my settings\nexport EDITOR=vim\n"
	zshrc := filepath.Join(home, ".zshrc")
	writeFile(t, zshrc, original)
	if err := os.MkdirAll(filepath.Join(home, ".config", "fish"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Profiles{Home: home, Shell: "/bin/zsh"}

	if _, err := p.Add("/w/bin/path"); err != nil {
		t.Fatal(err)
	}
	got, err := p.Remove("/w/bin/path")
	if err != nil {
		t.Fatal(err)
	}
	fish := filepath.Join(home, ".config", "fish", "conf.d", "wharf.fish")
	if !slices.Equal(got, []string{zshrc, fish}) {
		t.Fatalf("removed from %v", got)
	}
	if after := readFile(t, zshrc); after != original {
		t.Fatalf("after removing:\n%q\nwant:\n%q", after, original)
	}
	if _, err := os.Stat(fish); !os.IsNotExist(err) {
		t.Errorf("wharf.fish is Wharf's alone and must be deleted, stat err = %v", err)
	}
	if again, _ := p.Remove("/w/bin/path"); len(again) != 0 {
		t.Errorf("removing again changed %v", again)
	}
	if w := p.Where("/w/bin/path"); len(w) != 0 {
		t.Errorf("Where = %v after removing", w)
	}
}

func TestAStartupFileThatIsALinkStaysALink(t *testing.T) {
	home := t.TempDir()
	dotfiles := filepath.Join(home, "dotfiles", "zshrc")
	writeFile(t, dotfiles, "setopt autocd\n")
	if err := os.Symlink(dotfiles, filepath.Join(home, ".zshrc")); err != nil {
		t.Fatal(err)
	}
	p := &Profiles{Home: home, Shell: "/bin/zsh"}
	if _, err := p.Add("/w/bin/path"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(home, ".zshrc")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf(".zshrc is no longer a link: %v", err)
	}
	if !strings.Contains(readFile(t, dotfiles), blockStart) {
		t.Fatal("the link's target did not get the block")
	}
}

func TestZshReadsFromZDOTDIR(t *testing.T) {
	home, zdot := t.TempDir(), t.TempDir()
	p := &Profiles{Home: home, Shell: "/usr/local/bin/zsh", ZDotDir: zdot}
	got, err := p.Add("/w/bin/path")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{filepath.Join(zdot, ".zshrc")}) {
		t.Fatalf("changed %v", got)
	}
}

func TestAnUnknownShellGetsProfile(t *testing.T) {
	home := t.TempDir()
	p := &Profiles{Home: home, Shell: "/bin/ksh"}
	got, err := p.Add("/w/bin/path")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{filepath.Join(home, ".profile")}) {
		t.Fatalf("changed %v", got)
	}
}

// features/php-terminal.feature — "The global default PHP is kept in one folder"
func TestLinkFollowsTheTargetAndGoesAway(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bin", "path")
	for _, target := range []string{"/php/8.4/php", "/php/8.3/php", "/php/8.3/php"} {
		if err := Link(dir, target); err != nil {
			t.Fatal(err)
		}
		if got := Linked(dir); got != target {
			t.Fatalf("Linked = %q, want %q", got, target)
		}
	}
	if err := Link(dir, ""); err != nil {
		t.Fatal(err)
	}
	if got := Linked(dir); got != "" {
		t.Fatalf("Linked = %q after removing", got)
	}
	if err := Link(dir, ""); err != nil {
		t.Fatalf("removing a missing link: %v", err)
	}
}

// features/php-terminal.feature — "Only the last Wharf folder switched on is on the terminal PATH"
func TestTheLastWharfSwitchedOnOwnsTheTerminal(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".profile")
	writeFile(t, profile, "umask 022\n")
	if err := os.MkdirAll(filepath.Join(home, ".config", "fish"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Profiles{Home: home, Shell: "/bin/sh"}
	first, other := filepath.Join(home, "Wharf", "bin", "path"), filepath.Join(home, "Wharf 2", "bin", "path")

	if _, err := p.Add(first); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Add(other); err != nil {
		t.Fatal(err)
	}
	content := readFile(t, profile)
	if strings.Count(content, blockStart) != 1 || strings.Contains(content, shPathLine(first)) {
		t.Fatalf("want one block, naming only the other folder:\n%s", content)
	}
	if sh, err := exec.LookPath("sh"); err == nil {
		out, err := exec.Command(sh, "-c", `PATH=/usr/bin:/bin; . "$1"; printf %s "$PATH"`, "sh", profile).Output()
		if err != nil {
			t.Fatal(err)
		}
		if want := other + ":/usr/bin:/bin"; string(out) != want {
			t.Fatalf("PATH = %q, want %q", out, want)
		}
	}

	fish := filepath.Join(home, ".config", "fish", "conf.d", "wharf.fish")
	fishContent := readFile(t, fish)
	changed, err := p.Remove(first)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 0 {
		t.Errorf("turning off the first folder changed %v", changed)
	}
	if got := readFile(t, profile); got != content {
		t.Errorf("the other folder's block was touched:\n%s", got)
	}
	if got := readFile(t, fish); got != fishContent {
		t.Errorf("the other folder's wharf.fish was touched:\n%s", got)
	}
}

// features/php-terminal.feature — "A Wharf block broken by hand is not guessed at"
func TestABrokenBlockLeavesEveryFileAsItWas(t *testing.T) {
	home := t.TempDir()
	zshrc, bashrc := filepath.Join(home, ".zshrc"), filepath.Join(home, ".bashrc")
	writeFile(t, bashrc, "alias ll='ls -l'\n")
	p := &Profiles{Home: home, Shell: "/bin/zsh"}
	dir := "/w/bin/path"
	if _, err := p.Add(dir); err != nil {
		t.Fatal(err)
	}

	// INFO: The user deletes the end line, then keeps writing below the block.
	broken := strings.Replace(readFile(t, zshrc), blockEnd+"\n", "", 1) + "export LATER=1\nsource ~/.secrets\n"
	writeFile(t, zshrc, broken)
	bash := readFile(t, bashrc)

	for name, act := range map[string]func(string) ([]string, error){"on": p.Add, "off": p.Remove} {
		for _, target := range []string{dir, "/moved/bin/path"} {
			_, err := act(target)
			var be *BrokenBlockError
			if !errors.As(err, &be) || be.Path != zshrc {
				t.Fatalf("turning it %s for %s: err = %v, want a BrokenBlockError naming .zshrc", name, target, err)
			}
			if !strings.Contains(err.Error(), blockStart) || !strings.Contains(err.Error(), blockEnd) {
				t.Errorf("the message must name both lines: %v", err)
			}
			if got := readFile(t, zshrc); got != broken {
				t.Fatalf("turning it %s changed .zshrc:\n%s", name, got)
			}
			if got := readFile(t, bashrc); got != bash {
				t.Fatalf("turning it %s changed .bashrc although only .zshrc is broken:\n%s", name, got)
			}
		}
	}
}

// INFO: An editor that saves with CRLF or trailing spaces must not make the
// block invisible, or every start would add another one.
func TestMarkersAreFoundThroughCRLFAndTrailingSpaces(t *testing.T) {
	home := t.TempDir()
	zshrc := filepath.Join(home, ".zshrc")
	p := &Profiles{Home: home, Shell: "/bin/zsh"}
	dir := "/w/bin/path"
	writeFile(t, zshrc, "export A=1\r\n")
	if _, err := p.Add(dir); err != nil {
		t.Fatal(err)
	}
	crlf := strings.ReplaceAll(strings.ReplaceAll(readFile(t, zshrc), "\r\n", "\n"), "\n", "\r\n")
	writeFile(t, zshrc, strings.Replace(crlf, blockEnd, blockEnd+"  ", 1))

	if _, err := p.Add(dir); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(readFile(t, zshrc), blockStart); n != 1 {
		t.Fatalf("block appears %d times:\n%q", n, readFile(t, zshrc))
	}
	if _, err := p.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, zshrc); strings.Contains(got, "Wharf") || !strings.HasPrefix(got, "export A=1") {
		t.Fatalf("after removing:\n%q", got)
	}
}

func TestAFolderWithALineBreakIsRefused(t *testing.T) {
	home := t.TempDir()
	p := &Profiles{Home: home, Shell: "/bin/zsh"}
	if _, err := p.Add("/w/evil\n" + blockEnd + "\nrm -rf ~\n#/bin/path"); err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); !os.IsNotExist(err) {
		t.Fatalf(".zshrc was written: %v", err)
	}
}
