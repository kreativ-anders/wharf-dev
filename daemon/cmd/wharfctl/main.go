// Command wharfctl drives a running daemon from the terminal. It is a
// developer tool: it exercises and debugs the daemon without the GUI, and it
// never starts one — it connects through data/wharf.endpoint in the root, so
// the app has to be running (or `make run`). It is not part of a release.
package main

// TODO(wharfctl): remove this command once Wharf has a stable release — the
// GUI and the e2e tests cover what it was for. Remove it from the Makefile,
// both READMEs and the CLAUDE.md map with it.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/core"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/ipc"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/layout"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "wharfctl:", err)
		os.Exit(1)
	}
}

const usage = `usage: wharfctl [--root DIR] <command> [args]

  status                     show services and projects
  add <name|path>            register a folder in www/ by name, or any folder by path
       --name N              the project name, if not the folder's
       --template T | ""     the config template, or none; detected if left out
       --webserver W         pin it to nginx or apache
       --start               start it once it is registered
  inspect <path>             what "Add project…" would propose for a folder
  rm <name>                  unregister a project (the folder is left alone)
  start <name>               start a project
  stop <name>                stop a project
  restart <name>             regenerate a project's config and restart what serves it
  share <name> [off]         share a running project on the local network, or stop sharing it
  network-cert replace       replace the network certificate authority phones install
  stop-all                   stop every service and project
  reset --yes [--delete-projects]
                             unregister every project and delete all settings (downloads are kept);
                             --delete-projects also deletes every folder in www/
  webserver <apache|nginx>   set the globally active webserver
  webserver install <name>   install nginx or Apache
  webserver scan             re-scan the machine for webservers
  appearance <mode>          system, light or dark
  php                        list PHP versions found on this machine
  php use <version>          set the global default PHP version
  php add <version>          register a version without selecting it
  php install <version>      download a version into bin/php/<version>
  php remove <version>       delete a downloaded version, or hide one found on the machine
  php unhide <folder>        show a hidden PHP folder again
  php scan                   re-scan the machine for PHP installations
  php terminal <on|off>      put the default PHP (bin/path) on the terminal PATH, or take it off
  ssl                        install mkcert and trust its certificate authority
  config <name>              print a project's custom config, or the rules it would start from
  set <name> [flags]         per-project overrides
       --php V | --php ""    override or clear the PHP version
       --webserver W | ""    override or clear the webserver
       --ssl true|false      enable or disable SSL
  watch                      stream state changes until interrupted
`

func run() error {
	root := flag.String("root", "", "wharf root folder")
	socket := flag.String("socket", "", "IPC socket path")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		return fmt.Errorf("no command given")
	}

	var c *ipc.Client
	if *socket != "" {
		var err error
		c, err = ipc.Dial(*socket)
		if err != nil {
			return fmt.Errorf("%w\nis wharfd running?", err)
		}
	} else {
		r, err := layout.Resolve(*root)
		if err != nil {
			return err
		}
		c, err = ipc.DialFile(filepath.Join(r.Data(), ipc.EndpointFileName))
		if err != nil {
			return fmt.Errorf("%w\nis wharfd running?", err)
		}
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	switch args[0] {
	case "status":
		var st core.State
		if err := c.Call(ctx, ipc.MethodState, nil, &st); err != nil {
			return err
		}
		printState(st)
		return nil

	case "add":
		if len(args) < 2 {
			return fmt.Errorf("add needs a project name or a folder path")
		}
		// INFO: Anything that looks like a path is a folder from anywhere; a bare
		// name is a folder in www/ (project-folders.feature).
		params := map[string]any{"name": args[1]}
		if strings.ContainsAny(args[1], `/\`) || args[1] == "." {
			abs, err := filepath.Abs(args[1])
			if err != nil {
				return err
			}
			params = map[string]any{"path": abs}
			if err := addFlags(args[2:], params); err != nil {
				return err
			}
		}
		var p core.Project
		if err := c.Call(ctx, ipc.MethodProjectAdd, params, &p); err != nil {
			return err
		}
		printProject(p)
		return nil

	case "inspect":
		if len(args) < 2 {
			return fmt.Errorf("usage: wharfctl inspect <path>")
		}
		abs, err := filepath.Abs(args[1])
		if err != nil {
			return err
		}
		var out core.Proposal
		if err := c.Call(ctx, ipc.MethodProjectInspect, map[string]string{"path": abs}, &out); err != nil {
			return err
		}
		template := out.Template
		if template == "" {
			template = "none"
		}
		fmt.Printf("name      %s\ntemplate  %s\n", out.Name, template)
		if out.Project != "" {
			fmt.Printf("already   the project %s\n", out.Project)
		}
		return nil

	case "rm", "start", "stop", "restart":
		if len(args) < 2 {
			return fmt.Errorf("%s needs a project name", args[0])
		}
		method := map[string]string{
			"rm":      ipc.MethodProjectRemove,
			"start":   ipc.MethodProjectStart,
			"stop":    ipc.MethodProjectStop,
			"restart": ipc.MethodProjectRestart,
		}[args[0]]
		return callAndShow(ctx, c, method, map[string]string{"name": args[1]})

	case "share":
		if len(args) < 2 {
			return fmt.Errorf("share needs a project name")
		}
		on := len(args) < 3 || args[2] != "off"
		return callAndShow(ctx, c, ipc.MethodProjectShare, map[string]any{"name": args[1], "on": on})

	case "network-cert":
		if len(args) < 2 || args[1] != "replace" {
			return fmt.Errorf("run: wharfctl network-cert replace")
		}
		return callAndShow(ctx, c, ipc.MethodReplaceNetworkCA, nil)

	case "reset":
		if len(args) < 2 || args[1] != "--yes" {
			return fmt.Errorf("reset deletes all settings — run: wharfctl reset --yes (add --delete-projects to delete every folder in www/ too)")
		}
		deleteProjects := slices.Contains(args[2:], "--delete-projects")
		return callAndShow(ctx, c, ipc.MethodReset, map[string]bool{"delete_projects": deleteProjects})

	case "stop-all":
		return callAndShow(ctx, c, ipc.MethodStopAll, nil)

	case "webserver":
		switch {
		case len(args) < 2:
			return fmt.Errorf("webserver needs a name")
		case args[1] == "install" && len(args) > 2:
			return callAndShow(ctx, c, ipc.MethodInstallWebserver, map[string]string{"name": args[2]})
		case args[1] == "scan":
			return callAndShow(ctx, c, ipc.MethodDetectWebservers, nil)
		}
		return callAndShow(ctx, c, ipc.MethodSetWebserver, map[string]string{"name": args[1]})

	case "appearance":
		if len(args) < 2 {
			return fmt.Errorf("usage: wharfctl appearance <system|light|dark>")
		}
		return callAndShow(ctx, c, ipc.MethodSetAppearance, map[string]string{"mode": args[1]})

	case "php":
		return runPHP(ctx, c, args[1:])

	case "set":
		return runSet(ctx, c, args[1:])

	case "ssl":
		return callAndShow(ctx, c, ipc.MethodSetupSSL, nil)

	case "config":
		if len(args) < 2 {
			return fmt.Errorf("usage: wharfctl config <name>")
		}
		var out struct {
			Webserver string `json:"webserver"`
			Content   string `json:"content"`
			Exists    bool   `json:"exists"`
		}
		if err := c.Call(ctx, ipc.MethodCustomConfigRead, map[string]string{"name": args[1]}, &out); err != nil {
			return err
		}
		if !out.Exists {
			fmt.Fprintf(os.Stderr, "# no custom %s config yet — this is where it would start\n", out.Webserver)
		}
		fmt.Print(out.Content)
		return nil

	case "watch":
		var st core.State
		if err := c.Call(ctx, ipc.MethodState, nil, &st); err != nil {
			return err
		}
		printState(st)
		for ev := range c.Events() {
			if ev.Event != ipc.EventState {
				continue
			}
			var next core.State
			if err := json.Unmarshal(ev.Data, &next); err != nil {
				continue
			}
			fmt.Println(strings.Repeat("─", 60))
			printState(next)
		}
		return nil
	}

	flag.Usage()
	return fmt.Errorf("unknown command %q", args[0])
}

func runPHP(ctx context.Context, c *ipc.Client, args []string) error {
	if len(args) == 0 || args[0] == "list" {
		var st core.State
		if err := c.Call(ctx, ipc.MethodState, nil, &st); err != nil {
			return err
		}
		printPHP(st.Services.PHP)
		return nil
	}
	switch args[0] {
	case "scan":
		if err := c.Call(ctx, ipc.MethodDetectPHP, nil, nil); err != nil {
			return err
		}
		var st core.State
		if err := c.Call(ctx, ipc.MethodState, nil, &st); err != nil {
			return err
		}
		printPHP(st.Services.PHP)
		return nil
	case "use", "add", "install", "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: wharfctl php %s <version>", args[0])
		}
		method := map[string]string{
			"use":     ipc.MethodSetPHPVersion,
			"add":     ipc.MethodAddPHPVersion,
			"install": ipc.MethodInstallPHP,
			"remove":  ipc.MethodRemovePHP,
		}[args[0]]
		var st core.State
		if err := c.Call(ctx, method, map[string]string{"version": args[1]}, &st); err != nil {
			return err
		}
		printPHP(st.Services.PHP)
		return nil
	case "terminal":
		if len(args) < 2 || (args[1] != "on" && args[1] != "off") {
			return fmt.Errorf("usage: wharfctl php terminal <on|off>")
		}
		var st core.State
		if err := c.Call(ctx, ipc.MethodSetPHPTerminal, map[string]bool{"on": args[1] == "on"}, &st); err != nil {
			return err
		}
		printPHP(st.Services.PHP)
		return nil
	case "unhide":
		if len(args) < 2 {
			return fmt.Errorf("usage: wharfctl php unhide <folder>")
		}
		var st core.State
		if err := c.Call(ctx, ipc.MethodUnhidePHP, map[string]string{"dir": args[1]}, &st); err != nil {
			return err
		}
		printPHP(st.Services.PHP)
		return nil
	}
	return fmt.Errorf("usage: wharfctl php [list|use <version>|add <version>|install <version>|remove <version>|unhide <folder>|scan]")
}

// printPHP renders the version picker as a list: which version is selected,
// what else is installed, and how well supported each one still is.
func printPHP(p core.PHP) {
	fmt.Printf("selected   %s (%s)\n", p.Version, p.Status)
	fmt.Printf("recommended %s — newest version in active support\n", p.Recommended)
	if len(p.Downloadable) > 0 {
		var vs []string
		for _, d := range p.Downloadable {
			vs = append(vs, d.Version)
		}
		fmt.Printf("download   %s  (wharfctl php install <version>)\n", strings.Join(vs, ", "))
	}
	if len(p.Installs) == 0 {
		fmt.Printf("\nno PHP installation found on this machine\n")
		return
	}
	fmt.Println()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\tVERSION\tSUPPORT\tSOURCE\tLOCATION")
	for _, in := range p.Installs {
		marker := " "
		if in.Version == p.Version {
			marker = "*"
		}
		support := string(in.Status)
		if !in.Servable() {
			support += ", cli only"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", marker, in.FullVersion, support, in.Source, in.Dir)
	}
	w.Flush()
	printHiddenPHP(p.Hidden)
	printTerminal(p.Terminal)
}

// printTerminal says whether the default PHP is on the terminal PATH, and where.
func printTerminal(t core.Terminal) {
	if !t.On {
		fmt.Printf("\nterminal   off  (wharfctl php terminal on)\n")
		return
	}
	fmt.Printf("\nterminal   on — %s is on PATH in new terminals\n", t.Dir)
	if t.PHP == "" {
		fmt.Printf("           the default version is not installed, so it holds no php\n")
	}
	for _, place := range t.Places {
		fmt.Printf("           %s\n", place)
	}
}

func printHiddenPHP(hidden []string) {
	if len(hidden) == 0 {
		return
	}
	fmt.Printf("\nhidden  (wharfctl php unhide <folder>)\n")
	for _, dir := range hidden {
		fmt.Printf("  %s\n", dir)
	}
}

func runSet(ctx context.Context, c *ipc.Client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("set needs a project name")
	}
	name := args[0]
	fs := flag.NewFlagSet("set", flag.ContinueOnError)
	php := fs.String("php", "\x00", `PHP version override, or "" to clear`)
	ws := fs.String("webserver", "\x00", `webserver override, or "" to clear`)
	ssl := fs.String("ssl", "", "true or false")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	var settings core.Settings
	if *php != "\x00" {
		settings.PHP = php
	}
	if *ws != "\x00" {
		settings.Webserver = ws
	}
	if *ssl != "" {
		v := *ssl == "true"
		settings.SSL = &v
	}

	var p core.Project
	err := c.Call(ctx, ipc.MethodProjectSettings, map[string]any{"name": name, "settings": settings}, &p)
	if err != nil {
		return err
	}
	printProject(p)
	return nil
}

// printProject reports one project after an action that changed it.
func printProject(p core.Project) {
	fmt.Printf("%s  %s  %s  php %s  %s\n", p.Name, p.State, p.URL, p.PHPVersion, p.Webserver)
	if p.Error != "" {
		fmt.Printf("  %s\n", p.Error)
	}
}

func callAndShow(ctx context.Context, c *ipc.Client, method string, params any) error {
	var st core.State
	if err := c.Call(ctx, method, params, &st); err != nil {
		return err
	}
	if st.Root != "" {
		printState(st)
	}
	return nil
}

func printState(st core.State) {
	ws := st.Services.Webserver
	suffix := ""
	if ws.Switching {
		suffix = "  (switching webserver…)"
	}
	fmt.Printf("root       %s\n", st.Root)
	fmt.Printf("webserver  %s [%s]%s\n", ws.Active, ws.State, suffix)
	fmt.Printf("php        %s  installed: %s\n", st.Services.PHP.Version, strings.Join(st.Services.PHP.Available, ", "))
	for _, srv := range ws.Servers {
		switch {
		case srv.Installing:
			fmt.Printf("  %-8s installing…\n", srv.Name)
		case srv.Installed:
			fmt.Printf("  %-8s %s %s (%s)\n", srv.Name, srv.Version, srv.Binary, srv.Source)
		case srv.Install.Installable:
			fmt.Printf("  %-8s not installed — run: wharfctl webserver install %s\n", srv.Name, srv.Name)
		default:
			fmt.Printf("  %-8s not installed — %s\n", srv.Name, srv.Install.Hint)
		}
	}
	if ws.Error != "" {
		fmt.Printf("error      %s\n", ws.Error)
	}
	switch {
	case !st.SSL.Installed:
		fmt.Println("ssl        mkcert not installed yet — enabling SSL installs it")
	case !st.SSL.Trusted:
		fmt.Println("ssl        certificate authority not trusted — browsers warn; run: wharfctl ssl")
	}

	if len(st.Projects) == 0 {
		fmt.Println("\nno projects yet — run: wharfctl add <folder>")
	} else {
		fmt.Println()
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "PROJECT\tSTATUS\tURL\tPHP\tSERVER")
		for _, p := range st.Projects {
			server := p.Webserver
			if p.WebserverOverride != nil {
				server += " (override)"
			}
			php := p.PHPVersion
			if p.PHPOverride != nil {
				php += " (override)"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", p.Name, p.State, p.URL, php, server)
		}
		w.Flush()
	}
	if len(st.Unregistered) > 0 {
		fmt.Printf("\nunregistered folders in www/: %s\n", strings.Join(st.Unregistered, ", "))
	}
}

// addFlags reads add's flags into params, as the "Add project…" sheet sends
// them.
func addFlags(args []string, params map[string]any) error {
	for i := 0; i < len(args); i++ {
		switch flag := args[i]; flag {
		case "--start":
			params["start"] = true
		case "--name", "--template", "--webserver":
			if i+1 >= len(args) {
				return fmt.Errorf("%s needs a value", flag)
			}
			i++
			params[strings.TrimPrefix(flag, "--")] = args[i]
		default:
			return fmt.Errorf("unknown flag %s for add", flag)
		}
	}
	return nil
}
