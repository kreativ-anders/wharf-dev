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
`--parent-pid <app pid>`, and stops it on quit. The parent pid is a safety net —
if the app crashes, the daemon notices within two seconds and shuts down
([`internal/watchdog`](internal/watchdog/watchdog.go)). It ignores `SIGPIPE`,
because a crashed app also takes the daemon's stdout and stderr pipes with it,
and a daemon killed by a broken pipe never gets to stop its webservers.

The daemon logs to `<root>/data/log/wharfd.log` as well as stderr — when the
app runs it, the file is the only place the log survives.

`make run` uses `--elevator direct` and a throwaway hosts file, so it raises no
password prompt and never touches the real `/etc/hosts`. Anything else uses the
real elevation adapter for the OS it is running on.

## Layout

| Package | Responsibility |
|---|---|
| `internal/layout` | Resolves the portable root folder (`bin/`, `www/`, `config/`, `data/`) |
| `internal/config` | `wharf.json`: load, atomic write, hand-edit repair |
| `internal/elevate` | **The only platform-specific code**: UAC / osascript / pkexec |
| `internal/hostsfile` | Pretty URLs — one marked hosts line per project |
| `internal/supervisor` | Process lifecycle, port waiting, state transitions |
| `internal/runtime` | Config → process specs; generates nginx/apache/php-fpm config |
| `internal/project` | Folder-as-project discovery, quick-app scaffolding |
| `internal/certs` | mkcert: pinned download, local CA trust, per-project certificates |
| `internal/download` | HTTPS fetch with checksum, untar/unzip — for PHP builds and mkcert |
| `internal/php` | Release timeline, support status, detection, downloads |
| `internal/ipc` | Newline-delimited JSON over `AF_UNIX`, server and client |
| `internal/watchdog` | Shuts the daemon down when the app that started it is gone |
| `internal/core` | The behaviour itself; the IPC layer is a thin shell over it |

The platform-specific surface is three files in `internal/elevate`
(~50 lines each, two functions) plus two constants: the hosts-file path and the Windows `.exe`
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

The same API from a terminal — useful for development, and a complete way to
use Wharf without the GUI:

```sh
wharfctl status
wharfctl add my-kirby-site        # register a folder already in www/
wharfctl add ~/Code/client-site   # or any folder, left where it is
wharfctl php install 8.4          # download a PHP build into bin/php/8.4
wharfctl ssl                      # install mkcert, trust its authority
wharfctl config my-kirby-site nginx   # path of the custom nginx directives
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
| `project-folders.feature` | `internal/core/project_folders_test.go` |
| `pretty-urls.feature` | `internal/core/pretty_urls_test.go` |
| `quick-app-php.feature` | `internal/core/quick_app_test.go` |
| `settings.feature` | `internal/core/settings_test.go` |
| `tray-actions.feature` | `internal/core/tray_actions_test.go` |

They run against a daemon whose collaborators — processes, ports, hosts file,
mkcert, the network — are all faked, so they exercise real behaviour without
shipped binaries or a password prompt.

## Not implemented yet

- **Vendored webservers.** No nginx or apache ships with the repo, and neither
  publishes portable builds for all three OS; the resolver reports a clear
  "not installed, expected `<path>`" instead. PHP and mkcert are downloaded on
  request (`dev/architecture.md` §4b).
- Everything tagged `@roadmap` — deliberately, per `dev/architecture.md` §2.
