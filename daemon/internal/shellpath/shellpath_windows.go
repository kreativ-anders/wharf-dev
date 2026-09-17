package shellpath

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const linkName = "php.cmd"

// userEnvironmentPlace is how the Windows Registrar names where it wrote.
const userEnvironmentPlace = `the user's PATH (HKEY_CURRENT_USER\Environment)`

// userEnvironment is the Registrar for Windows: the Path value of the user
// environment, which every new terminal and editor reads.
type userEnvironment struct{}

// System returns the Registrar for the user running the daemon.
func System() Registrar { return userEnvironment{} }

// pathScript edits the user's Path through the registry rather than
// [Environment]::SetEnvironmentVariable, which would expand entries such as
// %USERPROFILE% and store them as plain strings. Setting and clearing a
// variable afterwards broadcasts WM_SETTINGCHANGE, so terminals started from
// now on see the change without signing out.
const pathScript = `
$dir = $env:WHARF_PATH_DIR.TrimEnd('\')
$key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
$old = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
$all = @($old -split ';' | Where-Object { $_ -ne '' })
$rest = @($all | Where-Object { $_.TrimEnd('\') -ne $dir })
switch ($env:WHARF_PATH_MODE) {
  'where'  { if ($rest.Count -ne $all.Count) { 'yes' }; exit 0 }
  'add'    { $new = (@($dir) + $rest) -join ';' }
  'remove' { $new = $rest -join ';' }
}
if ($new -ne $old) {
  $key.SetValue('Path', $new, [Microsoft.Win32.RegistryValueKind]::ExpandString)
  [Environment]::SetEnvironmentVariable('WHARF_PATH_REFRESH', '1', 'User')
  [Environment]::SetEnvironmentVariable('WHARF_PATH_REFRESH', $null, 'User')
  'changed'
}
$key.Close()
`

func runPathScript(mode, dir string) (string, error) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", pathScript)
	// INFO: The folder travels in the environment, so no quoting of it can
	// break the script.
	cmd.Env = append(os.Environ(), "WHARF_PATH_DIR="+filepath.Clean(dir), "WHARF_PATH_MODE="+mode)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("change the user's PATH: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (userEnvironment) Add(dir string) ([]string, error) {
	if _, err := runPathScript("add", dir); err != nil {
		return nil, err
	}
	return []string{userEnvironmentPlace}, nil
}

func (userEnvironment) Remove(dir string) ([]string, error) {
	out, err := runPathScript("remove", dir)
	if err != nil {
		return nil, err
	}
	if out != "changed" {
		return []string{}, nil
	}
	return []string{userEnvironmentPlace}, nil
}

func (userEnvironment) Where(dir string) []string {
	if out, err := runPathScript("where", dir); err == nil && out == "yes" {
		return []string{userEnvironmentPlace}
	}
	return []string{}
}

// link writes a php.cmd that runs target with every argument passed on. PHP
// finds its DLLs beside php.exe, so php.exe itself cannot be copied here.
//
// WARNING: cmd re-reads the arguments %* passes on. A program that hands php
// a file name holding & or | without quoting it for cmd can have part of the
// name run as a command; Ctrl+C also asks "Terminate batch job". Only a php.exe
// of Wharf's own would avoid both (dev/architecture.md §4).
func link(dir, target string) error {
	path := linkPath(dir)
	if target == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	// INFO: cmd expands %NAME% even inside quotes, so a % in the folder is doubled.
	content := "@\"" + strings.ReplaceAll(target, "%", "%%") + "\" %*\r\n"
	if old, err := os.ReadFile(path); err == nil && string(old) == content {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func linked(dir string) string {
	b, err := os.ReadFile(linkPath(dir))
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(b))
	if !strings.HasPrefix(line, `@"`) {
		return ""
	}
	target, _, _ := strings.Cut(line[2:], `"`)
	return strings.ReplaceAll(target, "%%", "%")
}
