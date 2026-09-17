//go:build unix

package shellpath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const linkName = "php"

// Profiles is the Registrar for macOS and Linux: a block in the user's shell
// startup files that puts the folder first on PATH.
type Profiles struct {
	// Home is the user's home folder.
	Home string
	// Shell is the user's login shell, as $SHELL names it. Its startup file is
	// created when missing; every other one is changed only if it exists.
	Shell string
	// ZDotDir is $ZDOTDIR, where zsh reads .zshrc from instead of Home.
	ZDotDir string
}

// System returns the Registrar for the user running the daemon.
func System() Registrar {
	home, _ := os.UserHomeDir()
	return &Profiles{Home: home, Shell: os.Getenv("SHELL"), ZDotDir: os.Getenv("ZDOTDIR")}
}

// startupFile is one file a shell reads when it starts.
type startupFile struct {
	path string
	fish bool
	// create is true for the login shell's own file, written even if missing.
	create bool
}

// files lists every startup file Wharf's block belongs in. zsh reads .zshrc
// in every interactive shell, which is also what editors start to learn the
// user's PATH; bash reads .bashrc there and one of its login files in a login
// shell; .profile is also what many Linux desktop sessions read at login.
func (p *Profiles) files() []startupFile {
	shell := filepath.Base(p.Shell)
	if p.Shell == "" {
		// INFO: A daemon started from the Dock may have no $SHELL; these are
		// the defaults each OS gives a new user.
		shell = "bash"
		if runtime.GOOS == "darwin" {
			shell = "zsh"
		}
	}
	zdot := p.ZDotDir
	if zdot == "" {
		zdot = p.Home
	}

	var out []startupFile
	add := func(path string, fish, create bool) {
		if create || exists(path) {
			out = append(out, startupFile{path: path, fish: fish, create: create && !exists(path)})
		}
	}
	add(filepath.Join(zdot, ".zshrc"), false, shell == "zsh")
	add(filepath.Join(p.Home, ".bashrc"), false, shell == "bash")
	login := false
	for _, name := range []string{".bash_profile", ".bash_login", ".profile"} {
		if exists(filepath.Join(p.Home, name)) {
			login = true
			add(filepath.Join(p.Home, name), false, false)
		}
	}
	fishDir := filepath.Join(p.Home, ".config", "fish")
	add(filepath.Join(fishDir, "conf.d", "wharf.fish"), true, shell == "fish" || exists(fishDir))
	// INFO: A bash without any login file, or a shell Wharf does not know,
	// still reads .profile.
	if (shell == "bash" && !login) || len(out) == 0 {
		add(filepath.Join(p.Home, ".profile"), false, true)
	}
	return out
}

// edit is one startup file's new content, worked out before any is written.
type edit struct {
	path    string
	content string
	// remove deletes the file instead: fish's wharf.fish is Wharf's alone.
	remove bool
	// same leaves the file as it is; it is still one of the places.
	same bool
}

func (p *Profiles) Add(dir string) ([]string, error) {
	if strings.ContainsAny(dir, "\r\n") {
		return nil, fmt.Errorf("the Wharf folder %q holds a line break, which a shell startup file cannot name; move Wharf to a folder without one", dir)
	}
	var plan []edit
	for _, f := range p.files() {
		if f.fish {
			plan = append(plan, edit{path: f.path, content: fishBlock(dir)})
			continue
		}
		content, err := read(f.path)
		if err != nil {
			return nil, err
		}
		// INFO: Every block goes, not only this folder's: the Wharf switched on
		// last is the one terminals run (php-terminal.feature, "Only the last
		// Wharf folder switched on is on the terminal PATH").
		rest, err := removeBlocks(f.path, content, func(string) bool { return true })
		if err != nil {
			return nil, err
		}
		if strings.Contains(content, strings.TrimRight(shBlock(dir), "\n")) && strings.Count(content, blockStart) == 1 {
			plan = append(plan, edit{path: f.path, same: true})
			continue // INFO: Already there; it stays where it is.
		}
		rest = strings.TrimRight(rest, "\r\n")
		if rest != "" {
			rest += "\n\n"
		}
		plan = append(plan, edit{path: f.path, content: rest + shBlock(dir)})
	}
	return apply(plan)
}

func (p *Profiles) Remove(dir string) ([]string, error) {
	var plan []edit
	for _, f := range p.files() {
		if f.create {
			continue // INFO: It does not exist, so it holds no block.
		}
		content, err := read(f.path)
		if err != nil {
			return nil, err
		}
		if f.fish {
			if strings.Contains(content, fishPathLine(dir)) {
				plan = append(plan, edit{path: f.path, remove: true})
			}
			continue
		}
		// WARNING: Only this folder's block. Another Wharf folder's belongs to
		// the Wharf that switched it on, and turning this one off must not take
		// its PHP out of the user's terminal.
		rest, err := removeBlocks(f.path, content, func(body string) bool {
			return strings.Contains(body, shPathLine(dir))
		})
		if err != nil {
			return nil, err
		}
		if rest != content {
			plan = append(plan, edit{path: f.path, content: rest})
		}
	}
	return apply(plan)
}

// apply writes a plan. It runs only once every file was read and understood,
// so a broken block in one file leaves every file as it was.
func apply(plan []edit) ([]string, error) {
	changed := []string{}
	for _, e := range plan {
		switch {
		case e.same:
		case e.remove:
			if err := os.Remove(e.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return changed, err
			}
		default:
			if err := os.MkdirAll(filepath.Dir(e.path), 0o755); err != nil {
				return changed, err
			}
			if err := writeIfChanged(e.path, e.content); err != nil {
				return changed, err
			}
		}
		changed = append(changed, e.path)
	}
	return changed, nil
}

func (p *Profiles) Where(dir string) []string {
	out := []string{}
	for _, f := range p.files() {
		if f.create {
			continue
		}
		content, err := read(f.path)
		if err != nil {
			continue
		}
		want := shBlock(dir)
		if f.fish {
			want = fishBlock(dir)
		}
		if strings.Contains(content, strings.TrimRight(want, "\n")) {
			out = append(out, f.path)
		}
	}
	return out
}

// shBlock is Wharf's block for sh-compatible shells. The case statement keeps
// a shell started from another one from adding the folder twice.
func shBlock(dir string) string {
	q := shQuote(dir)
	return blockStart + "\n" +
		"# Added by Wharf. Turn off \"Use in terminal\" in Settings → PHP to remove it.\n" +
		"case \":$PATH:\" in\n" +
		"  *\":" + q + ":\"*) ;;\n" +
		"  *) export PATH=\"" + q + ":$PATH\" ;;\n" +
		"esac\n" +
		blockEnd + "\n"
}

// fishBlock is the whole of conf.d/wharf.fish, a file that is Wharf's alone.
func fishBlock(dir string) string {
	q := fishQuote(dir)
	return blockStart + "\n" +
		"# Added by Wharf. Turn off \"Use in terminal\" in Settings → PHP to remove it.\n" +
		"contains -- \"" + q + "\" $PATH; or set -gx PATH \"" + q + "\" $PATH\n" +
		blockEnd + "\n"
}

// shQuote escapes a path for the inside of a double-quoted sh string.
func shQuote(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`").Replace(s)
}

// fishQuote escapes a path for the inside of a double-quoted fish string.
func fishQuote(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`).Replace(s)
}

// shPathLine and fishPathLine are the line of a block that names its folder.
func shPathLine(dir string) string   { return `export PATH="` + shQuote(dir) + `:$PATH"` }
func fishPathLine(dir string) string { return `set -gx PATH "` + fishQuote(dir) + `" $PATH` }

// removeBlocks removes each of Wharf's blocks whose body drop chooses, with
// the blank line Add put before it. Markers are matched without trailing
// spaces or a carriage return, so an editor that adds them does not hide a block.
//
// WARNING: A start line without its end line is an error, never removed up to
// the end of the file: that would delete every line the user wrote after it.
func removeBlocks(path, content string, drop func(body string) bool) (string, error) {
	marker := func(line string) string { return strings.TrimRight(line, " \t\r") }
	lines := strings.Split(content, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		if marker(lines[i]) != blockStart {
			out = append(out, lines[i])
			continue
		}
		end := -1
		for j := i + 1; j < len(lines); j++ {
			if m := marker(lines[j]); m == blockEnd {
				end = j
				break
			} else if m == blockStart {
				break
			}
		}
		if end < 0 {
			return "", &BrokenBlockError{Path: path}
		}
		if !drop(strings.Join(lines[i+1:end], "\n")) {
			out = append(out, lines[i:end+1]...)
		} else if n := len(out); n > 0 && strings.TrimSpace(out[n-1]) == "" {
			out = out[:n-1]
		}
		i = end
	}
	return strings.Join(out, "\n"), nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func read(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

// writeIfChanged replaces a file's content atomically. A startup file is
// often a symbolic link into a dotfiles repository, so the link's target is
// written and the link is kept, with the target's permissions.
func writeIfChanged(path, content string) error {
	if old, err := read(path); err == nil && old == content && exists(path) {
		return nil
	}
	real := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		real = resolved
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(real); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(real), ".wharf-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), real); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func link(dir, target string) error {
	path := linkPath(dir)
	if target == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if current, err := os.Readlink(path); err == nil && current == target {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// INFO: Made beside and renamed over, so a terminal never finds no php
	// while the default changes.
	tmp := path + ".new"
	os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func linked(dir string) string {
	target, err := os.Readlink(linkPath(dir))
	if err != nil {
		return ""
	}
	return target
}
