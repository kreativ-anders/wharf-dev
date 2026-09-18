# Wharf daemon (`wharfd`)

The core daemon from `dev/architecture.md` §3: process orchestration, config
state, and the hosts/elevation adapter, behind a unix-socket IPC the Flutter
GUI talks to. One Go module, no third-party dependencies, one binary per OS.

## Build and run

```sh
make build            # build/wharfd and build/wharfctl
make test             # unit and behaviour tests
make race             # same, under the race detector
make cross            # build for macOS, Linux and Windows (arm64 + amd64)
make run              # run against build/dev/ with elevation disabled
```

Users never start `wharfd` themselves: the desktop app finds it, starts it with
`--parent-pid <app pid>`, and stops it on quit by asking over IPC
(`daemon.shutdown`) — the same on every OS, because on Windows a signal is
`TerminateProcess` and the daemon would never get to stop its webservers. It
is killed only if it does not exit in time. The parent pid is a safety net —
if the app crashes, the daemon notices within two seconds and shuts down
([`internal/watchdog`](internal/watchdog/watchdog.go)). It ignores `SIGPIPE`,
because a crashed app also takes the daemon's stdout and stderr pipes with it,
and a daemon killed by a broken pipe never gets to stop its webservers.

The daemon logs to `<root>/data/log/wharfd.log` as well as stderr — when the
app runs it, the file is the only place the log survives.

Projects are `<name>.localhost`, so the hosts file is never read or written.
The one password prompt left is trusting mkcert's certificate authority;
`make run` uses `--elevator direct`, which treats that prompt as declined.

## Layout

| Package | Responsibility |
|---|---|
| `internal/layout` | Resolves the portable root folder (`bin/`, `www/`, `config/`, `data/`) |
| `internal/config` | `wharf.json`: load, atomic write, hand-edit repair |
| `internal/elevate` | **Platform-specific**: UAC / osascript / pkexec |
| `internal/proctree` | **Platform-specific**: a service and its workers, stopped as one (process group / job object) |
| `internal/shellpath` | **Platform-specific**: `bin/path`'s php, and `bin/path` on the user's PATH (shell startup files / user environment) |
| `internal/supervisor` | Process lifecycle, port waiting, state transitions |
| `internal/runtime` | Config → process specs; generates nginx/apache/php-fpm config |
| `internal/project` | Folder-as-project discovery, quick-app scaffolding |
| `internal/certs` | mkcert: pinned download, local CA trust, per-project certificates |
| `internal/download` | HTTPS fetch with checksum, untar/unzip — for PHP builds and mkcert |
| `internal/php` | Release timeline, support status, detection, downloads |
| `internal/webserver` | Finds nginx/Apache (Wharf's, Homebrew's, the OS's) and installs them |
| `internal/ipc` | Newline-delimited JSON over `AF_UNIX`, server and client |
| `internal/watchdog` | Shuts the daemon down when the app that started it is gone |
| `internal/core` | The behaviour itself; the IPC layer is a thin shell over it |

The platform-specific surface is three files in `internal/elevate`
(~50 lines each, two functions), two in `internal/proctree` (a process group
on macOS/Linux, a job object on Windows), two in `internal/shellpath` (shell
startup files and a link on macOS/Linux, the user environment and a `php.cmd`
on Windows), plus two constants: the hosts-file path and the Windows `.exe`
suffix. Everything else compiles unmodified for all six targets.

## Talking to the daemon

Requests and responses are one JSON object per line over the socket at
`<root>/data/wharf.sock`:

```jsonc
// client → daemon
{"id":"1","method":"projects.start","params":{"name":"my-kirby-site"}}
// daemon → client
{"id":"1","ok":true,"result":{ /* full state snapshot */ }}
// daemon → every client, unsolicited, on any state change
{"event":"state","data":{ /* full state snapshot */ }}
```

Every change broadcasts a **full** snapshot rather than a delta, so the GUI has
no merge logic and the tray menu and main window cannot disagree about what is
running (`features/tray-actions.feature`).

Methods: `ping`, `state.get`, `templates.list`, `services.setWebserver`,
`services.addPHPVersion`, `services.stopAll`, `projects.add`,
`projects.remove`, `projects.start`, `projects.stop`, `projects.settings`,
`projects.scaffold`.

Error codes the GUI branches on: `elevation_denied`, `missing_binary`,
`offline`, `not_found`, `conflict`, `bad_request`, `unknown_method`,
`internal`.

## `wharfctl`

The same API from a terminal, for development and debugging. It connects to a
daemon that is already running — found through `data/wharf.endpoint` in the
root — and never starts one, so the app (or `make run`) has to be up. It is not
shipped in releases, and goes once Wharf has a stable one:

```sh
wharfctl status
wharfctl add my-kirby-site        # register a folder already in www/
wharfctl add ~/Code/client-site   # or any folder, left where it is
wharfctl php install 8.4          # download a PHP build into bin/php/8.4
wharfctl php terminal on          # put the default PHP (bin/path) on the terminal PATH
wharfctl ssl                      # install mkcert, trust its authority
wharfctl config my-kirby-site         # its custom config, or the rules it would start from
wharfctl webserver install nginx  # install a webserver
wharfctl appearance dark          # system, light or dark
wharfctl new kirby my-kirby-site  # scaffold from the Kirby starter kit
wharfctl set my-kirby-site --php 8.1 --ssl true
wharfctl set legacy-app --webserver apache
wharfctl set legacy-app --webserver ""   # clear the override
wharfctl webserver apache
wharfctl watch                    # stream state changes
```

## Behaviour coverage

Each `@v1` scenario in `features/` has a test named after it:

| Feature file | Tests |
|---|---|
| `service-management.feature` | `internal/core/service_management_test.go` |
| `app-configuration.feature` | `internal/core/app_configuration_test.go` |
| `local-ssl.feature` | `internal/core/local_ssl_test.go` |
| `php-runtime.feature` | `internal/core/php_runtime_test.go` |
| `php-terminal.feature` | `internal/core/php_terminal_test.go`, `internal/shellpath/shellpath_unix_test.go` |
| `project-folders.feature` | `internal/core/project_folders_test.go` |
| `webserver-install.feature` | `internal/core/webserver_install_test.go` |
| `pretty-urls.feature` | `internal/core/pretty_urls_test.go` |
| `quick-app-php.feature` | `internal/core/quick_app_test.go` |
| `settings.feature` | `internal/core/settings_test.go` |
| `tray-actions.feature` | `internal/core/tray_actions_test.go` |

They run against a daemon whose collaborators — processes, ports,
mkcert, the network — are all faked, so they exercise real behaviour without
shipped binaries or a password prompt.

## Not implemented yet

- **Apache on Linux** is not installed by Wharf: the distribution's package
  is the only sane source, and Settings names the command
  (`dev/architecture.md` §4b).
- Everything tagged `@roadmap` — deliberately, per `dev/architecture.md` §2.
