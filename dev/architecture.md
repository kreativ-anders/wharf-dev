# Architecture

## 1. Goal

A native, GUI-first local development environment manager for Windows,
macOS and Linux. No Docker, no VM layer, no container runtime dependency.
Single tray-resident application; one-click service and project control.

## 2. Non-goals (v1)

- No team-shared/reproducible environment guarantee (that is DDEV's domain,
  not this tool's — see trade-off discussion in project history).
- No Explorer/Finder/Nautilus context-menu integration (three independent,
  non-abstractable platform APIs — deferred, tray menu covers the same use
  case for v1).
- No multi-runtime support beyond PHP (Node/Go/Python are `@roadmap`).
- No DNS resolver of its own (dnsmasq, `/etc/resolver`). Projects are
  `<name>.localhost`, which the OS and every browser resolve to loopback
  themselves (RFC 6761) — a wildcard with nothing to install. *Changed from
  the first plan*, which wrote one hosts-file line per project under a
  made-up `.wharf` TLD: on macOS 26 the system resolver (mDNSResponder)
  answers made-up TLDs with a cached "No Such Record" and lost track of the
  hosts file after Wharf wrote to it, so Safari could not find projects that
  curl reached. `.test` fails the same way; `.localhost` does not depend on
  the hosts file at all, and adding a project no longer asks for a password.
  No released build ever wrote a `.wharf` line, so Wharf has no code that
  cleans them up and never touches the hosts file at all.
- Chained webservers only as a front door, never for load. *Changed from the
  first plan*, which ruled chaining out and gave a project on the other
  webserver its own port — so its URL carried one (`legacy-app.wharf:8081`),
  unlike every other project's. Now the active webserver owns ports 80 and
  443 and forwards such a project, by name, to its own instance on a loopback
  port (§4c). The production reasons for chaining still do not apply; the
  reason here is one URL shape for every project.

## 3. Components

| Component | Responsibility | Technology |
|---|---|---|
| GUI | Tray icon, project list, settings, one-click actions | Flutter (desktop target) |
| Core daemon | Process orchestration, config state, elevation adapter | Go, single cross-compiled binary |
| IPC | GUI ↔ daemon transport | Unix domain socket on macOS/Linux; loopback TCP + token on Windows (see §4a) |
| Service registry | Which services exist, which is active, per-project overrides | Single config file (JSON/TOML) read by daemon, watched for changes |
| Elevation adapter | One of the three platform-branching code paths; process trees and the terminal PATH are the others (§4) | 3 thin implementations behind one interface (see §4) |
| Runtime binaries | PHP (v1); Nginx/Apache; mkcert; roadmap: Node, Go, Python, MySQL, PostgreSQL, Mailpit | Per-OS portable builds under `bin/` — see §4b |

## 4. Cross-OS strategy: one code path, minimal branching

Every design decision favours identical behaviour across OS over the most
"elegant" per-platform solution:

- **Pretty URLs** — `<name>.localhost` everywhere, resolved to loopback by
  the OS or the browser itself; nothing is written anywhere, and the hosts
  file is never read or written.
- **Elevation** — one interface with two functions, and the only password
  prompts Wharf has. `RequestElevatedRun(program, args, env)` trusts
  mkcert's local certificate authority, which writes to the system trust
  store (`features/local-ssl.feature`: without it every browser warns), and
  installs Apache with a Linux package manager (§4b). `AllowLowPorts()` lets
  the front door take ports 80 and 443 (§4c); it prompts on Linux only, and
  only while the limit is still 1024. Three small adapters behind them (UAC /
  `osascript with administrator privileges` / `pkexec`) are one of the
  platform-specific surfaces of the codebase; process trees and the
  terminal PATH are the others (below).
- **IPC** — see §4a. One protocol and one client API across all three OS; the
  socket type differs on Windows only because the GUI's language cannot open a
  unix socket there.
- **Process management** — `os/exec` starts every service the same way on all
  three OS. Stopping one is where they differ: nginx, Apache and php-fpm are a
  master with workers that all hold the listening socket, so the whole tree
  must go. `internal/proctree` is the second platform-specific surface — a
  process group on macOS/Linux, a job object on Windows, which has no process
  groups and does not take children down with their parent. Before it, a stop
  on Windows killed the nginx master and left a worker holding port 80
  (`features/service-management.feature`, "Stopping a webserver stops its
  worker processes too"). It also answers whether a process is alive, which
  the daemon's parent watchdog asks: on Windows an exited process stays
  openable while anything holds a handle to it — the flutter tool, a
  debugger — so "can it be opened" kept the daemon waiting for an app that
  was gone; it must be asked whether the process has exited.
- **PHP in the terminal** — `bin/path` holds a `php` that runs the global
  default version, and "Use in terminal" puts that folder first on PATH
  (`features/php-terminal.feature`). `internal/shellpath` is the third
  platform-specific surface: a symbolic link and a marked block in the shell
  startup files (`.zshrc`, bash's, `.profile`, fish's `conf.d`) on macOS and
  Linux; a `php.cmd` and the `Path` of the user environment on Windows, where
  a symbolic link needs Developer Mode and no shell reads a startup file every
  editor also reads. *Added later*: before it, a terminal or an editor could
  not find the PHP Wharf serves with, because nothing Wharf installs is on
  PATH. It is off until the user turns it on, since it writes outside the
  Wharf folder; it needs no password, because those files and the user
  environment are the user's. Wharf changes them only when the switch changes
  or is on at start. PATH runs the first `php` it finds, so only one Wharf
  folder can be the terminal's: the one switched on (or started with it on)
  last. Turning it on replaces another folder's block; turning it off removes
  only the folder's own, so a second Wharf folder cannot take away the first
  one's. A start line without its end line is refused with the file named,
  never removed up to the end of the file — that deleted the user's own lines.
  Quitting leaves it in place; Reset takes it away. `bin/path` itself is never
  adopted as a PHP install. On Windows `php.cmd` passes its arguments through
  cmd, which re-reads `&` and `|` in an argument a program did not quote for
  it, and Ctrl+C asks "Terminate batch job"; a `php.exe` shim of Wharf's own
  would remove both, once packaging can ship one. Editors started from the Dock on macOS see it only if they ask
  a login shell for PATH, as VS Code does.
- **SSL** — `mkcert`, already cross-platform, used unmodified.
- **Distribution shell** — a portable root folder (`bin/`, `www/`, `config/`)
  is the shared internal model; only the outer package format differs
  (installer / DMG / AppImage), never the content or config format.

**Deliberately not unified** (documented, not solved): elevation-prompt UX
per OS, outer package format, Linux system-tray availability (GNOME
requires an extension; KDE/Xfce work natively), and what outlives a daemon that
is killed outright. On Windows its services die with it, because the job
holding each one closes with the daemon's handle; macOS and Linux have no
equivalent, so a force-killed daemon can leave a webserver behind.

## 4c. The front door

One webserver process serves every project on ports 80 and 443 — the active
one. A project pinned to the *other* webserver gets its own instance, which
listens on `127.0.0.1:<port>` only; the front door forwards its name there:

```
browser ──► nginx :80/:443 (active) ──► my-kirby-site   served directly
                                   └──► legacy-app      proxied to apache on 127.0.0.1:8081
```

- The front door listens on IPv4 and IPv6: macOS resolves `*.localhost` to
  `::1` first, and Safari uses it.
- TLS ends at the front door, with the project's certificate. The instance
  behind it speaks plain HTTP on loopback.
- The instance behind trusts the scheme and port the front door forwards —
  it is reachable from nowhere else — and hands them to PHP as `HTTPS` and
  `SERVER_PORT`. Kirby reads those, not forwarded headers, so without this
  every link it builds would carry `:8081`.
- Pinning a project to the active webserver is not an override in effect: it
  is served by the front door like any other, with no second process.
- A name no project has is refused by a default server, never answered by
  whichever project comes first.
- It answers this machine only. It has to listen on every address — macOS
  lets an unprivileged process bind a port below 1024 on the wildcard
  address alone — so nginx allows loopback and denies the rest, and Apache
  denies every client that is not loopback. Without that, anyone on the same
  network could reach a project by sending its name, and a project behind
  the front door would see the front door's own `127.0.0.1` as the client,
  which is what Kirby checks before it lets the Panel be installed
  (`features/service-management.feature`, "The front door answers this
  machine only").
- On Linux, only administrators may listen below port 1024 until
  `net.ipv4.ip_unprivileged_port_start` is lowered. Before the front door
  starts, Wharf lowers it to 80 behind one password prompt — now, and in
  `/etc/sysctl.d/60-wharf.conf` for every boot after — so the question comes
  once per machine. A sysctl rather than `setcap` on the binary: it holds
  for nginx and Apache alike, a distribution's Apache included, and
  survives every update of either. Declining leaves the project stopped,
  saying how to allow the ports (`features/service-management.feature`).
  macOS and Windows let any process listen there, so they never ask.
- It serves the *started* projects only. Starting one project adds it and
  restarts the front door; stopping one removes it and leaves every other
  started project running; the front door stops once none is left. Which
  projects are started is daemon state, not config: everything stops when
  Wharf quits, so there is nothing to remember across runs.

## 4a. IPC transport: the one place the plan did not survive contact

The original plan was `AF_UNIX` everywhere: `net.Listen("unix", path)` in Go
runs unmodified on Windows 10 1803+, so one code path looked sufficient.

It is not, and the reason is the *other* side. Dart's `dart:io` supports unix
domain sockets on **Linux, macOS and Android only** — the Windows kernel offers
them, the Flutter GUI's language does not expose them. A GUI that cannot
connect is not a working tool, so:

| OS | Transport | Authorisation |
|---|---|---|
| macOS, Linux | unix domain socket, mode `0600` | filesystem permissions |
| Windows | loopback TCP on an OS-assigned port | a random token |

Loopback is not authorisation on its own — any local process can connect — so
the TCP transport requires a token as its first call. A client has ten
seconds to present it, and a wrong one ends the connection, so no local
process can hold connections open or try one token after another. The token lives in
`data/wharf.endpoint`, written mode `0600`, which is also how *every* client on
*every* OS finds the daemon: read the endpoint file, connect to whatever it
names. No client hard-codes a socket path or a port, and nothing above the
transport differs — same protocol, same methods, same events, same errors,
verified by the same tests running against both transports.

The cost is honest: one extra concept (the endpoint file) and one extra
handshake on one OS, in exchange for a GUI that runs on all three.

A connection's requests are answered in the order they arrive, so starting a
service and then reading the state cannot race. The exceptions are the
actions that take minutes — downloading PHP, installing a webserver, setting
up SSL, looking up PHP releases. They run beside the other requests and are
answered whenever they finish, because the GUI sends every click on one
connection and would otherwise wait out a download before it could stop a
project. Each one guards itself against running twice.

## 4b. Binaries Wharf installs

Nothing is downloaded at first launch (`features/php-runtime.feature`): a PHP
already on the machine is adopted where it is. Downloads happen only when the
user asks — "Download" in the PHP picker, or turning SSL on without mkcert:

| Binary | Source | Trust |
|---|---|---|
| PHP, macOS/Linux | static-php-cli builds (`dl.static-php.dev`): single static `php-fpm` and `php`, every extension Kirby needs | HTTPS; no checksums are published |
| PHP, Windows | the official builds (`windows.php.net`), NTS x64 | SHA-256 from `releases.json` |
| mkcert | a pinned GitHub release | SHA-256 pinned in `internal/certs` |
| nginx, Linux/Windows | static builds from jirutka/nginx-binaries, newest version | SHA-1 from the index — the only checksum published |
| nginx, macOS | Homebrew (`brew install nginx`) | Homebrew's own |
| Apache, Windows | Apache Lounge, newest 2.4 Win64 build | SHA-256 published beside each zip |
| Apache, macOS | nothing to install — macOS ships `/usr/sbin/httpd` | — |
| Apache, Linux | the distribution's package (apt, dnf, zypper or pacman), behind one password prompt | the distribution's own |

Each download is staged beside its destination and renamed into place only
when complete, so a failure leaves no half-installed folder. The newest patch
of a PHP minor version is found from each source's index, not hard-coded.

Wharf itself is not updated from inside the app yet. Settings → General shows
the version `wharfd` was built as (`-X main.version`, from
`gui/pubspec.yaml` through `tool/version.sh` — see `dev/releasing.md`) —
the daemon publishes it, so the window and the tray cannot name different
ones — and a "Check for updates" button that stays disabled until there are
releases to check against. When it is built (`features/settings.feature`,
"Checking for updates on request", `@roadmap`), the rule above holds for it
too: it looks only when the user asks, never at start or in the background;
a download is saved only once it matches the release's checksums; and Wharf
never runs what it downloaded.

Neither nginx.org nor apache.org publishes portable builds, so webservers
come from the best source each platform has, and a copy already on the
machine is always adopted first — a Mac's own Apache means the first project
starts with no install at all (`features/webserver-install.feature`). The
exceptions are honest, not hidden: every macOS nginx build in the index
links Homebrew's libpcre2 and cannot start without Homebrew, so macOS nginx
*is* Homebrew's; and a Linux distribution's Apache package is the only sane
Apache on Linux, so "Install" runs the package manager behind a password
prompt. Debian's package starts and enables the system's own Apache on port
80, so the same prompt switches that service off again
(`systemctl disable --now`); Wharf runs the binary with its own config, as
it does every other install. A distribution with none of the four package
managers gets the command to run instead of an install that would fail.

One copy of each webserver serves every project. Each project's server
block is generated into its own file (`data/gen/<webserver>/<project>.conf`)
and the main config includes those files. Wharf also writes its own
`mime.types` and `fastcgi_params`, and finds Apache's modules wherever the
install keeps them, so the generated config works with any install.

## 5. Directory layout (the tool's own runtime folder, not the source repo)

```
wharf/
├── bin/
│   ├── php/            # versioned, one folder per version (downloaded or vendored)
│   ├── path/           # php → the global default's php (php.cmd on Windows);
│   │                   #   on PATH when "Use in terminal" is on
│   ├── nginx/          # nginx, when Wharf installed it
│   ├── apache/         # bin/httpd + modules/, when Wharf installed it
│   └── mkcert/         # downloaded when SSL is first turned on
├── www/                # user projects live here (Kirby-style: folder = project)
├── config/
│   ├── wharf.json      # single flat config file — see §6
│   ├── php.ini         # the user's own PHP settings, read by every PHP
│   │                   #   version after its own php.ini
│   ├── vhosts/         # per-project custom configs, used instead of the
│   │                   #   config template, e.g. my-site.nginx.conf
│   └── templates/      # config templates: the user's own, and changed
│                       #   built-in ones, e.g. laravel.nginx.conf
└── data/
    ├── wharf.endpoint  # where the daemon is listening — see §4a
    ├── log/wharfd.log  # the daemon's own log (per-service logs sit beside it)
    ├── wharf.sock      # unix transport only
    ├── gen/            # generated configs (overwritten on start): nginx.conf and
    │                   #   apache.conf include nginx/<project>.conf and
    │                   #   apache/<project>.conf; run/ holds pid files
    ├── log/            # per-service logs; projects/<name>/ holds one project's
    │                   #   access.log and error.log, PHP's errors included.
    │                   #   A log past 10 MB is moved to <name>.1 when its
    │                   #   service next starts, so none grows without end
    ├── certs/          # mkcert output, one pair per SSL project
    └── mailpit/        # roadmap
```

Nothing in `data/` is user-authored: it can be deleted whole and the daemon
regenerates it. `config/` and `www/` are the parts worth backing up.

## 6. Config file shape (draft)

```json
{
  "services": {
    "webserver": { "active": "nginx", "available": ["apache", "nginx"] },
    "php": { "version": "8.3", "available": ["8.1", "8.2", "8.3"] }
  },
  "projects": [
    { "name": "my-kirby-site", "ssl": true, "port": 8080 }
  ]
}
```

Keys are omitted rather than written as `null`: clearing a project's
`webserver_override` removes the key, so a person reading the file sees only
what actually deviates from the defaults.

Two keys were added during implementation, both because behaviour depends on
them surviving a restart:

- `projects[].port` — the loopback port of the project's own instance, used
  only while it is pinned to the webserver that is not active. The front
  door forwards to it, so it does not change between runs (§4c). It is the
  first port from 8080 up that no other project records and no other program
  listens on; if another program has taken it by the time the instance
  starts, the project moves to the next free port and the file records it.
- `projects[].hosts_entry` — *removed.* It recorded a hosts line from the
  `<name>.wharf` days; an old file loses the key on its next write.
- `appearance` — `"light"` or `"dark"`; absent means follow the system.
- `projects[].path` — appears only for a project whose folder is not in
  `www/`: the folder is added where it is (`features/project-folders.feature`).
- `services.php.paths` — appears only when the tool adopted a PHP already
  installed on the machine, mapping version to its folder
  (`features/php-runtime.feature`). A self-contained install writes no paths.
- `services.php.hidden` — appears only once the user hides a PHP found on the
  machine: its folder, which detection then passes over. Wharf never deletes
  a PHP it did not download; removing one it did deletes `bin/php/<version>`
  instead, so neither needs elevation on any OS
  (`features/php-runtime.feature`).
- `services.php.terminal` — `true` while "Use in terminal" puts `bin/path`
  on the user's PATH. A first start writes it, so it is on unless the user
  turns it off; `wharfd --terminal=false` leaves it off for a Wharf folder
  made only for tests, which must not take the PATH from the real one
  (`features/php-terminal.feature`).
- `projects[].template` — the config template whose rules the project's
  server block carries; absent means none, and nginx serves the folder as it
  serves any folder while Apache reads the project's `.htaccess`
  (`features/config-templates.feature`).

A project's `name` becomes a folder in `www/`, files in `config/vhosts/` and
`data/certs/`, and a `server_name` line, so a hand-edited one must be usable
as all of them: letters, digits, dots, hyphens and underscores, starting
with a letter or digit. A `path` must not hold a quote or a line break, which
no webserver config can name. A file that breaks either rule is refused on
load the way a file that does not parse is — a reload keeps the config in
use — and the message names the project and the fix.

A project's custom config is deliberately *not* a key in this file: it is
nginx or Apache syntax, which belongs in a file of its own that an editor
highlights, not in a JSON string. It lives in `config/vhosts/`, one file per
webserver, and is picked up on save (`features/app-configuration.feature`).

Config templates follow the same rule. Wharf's own are compiled into the
daemon; saving one writes `config/templates/<id>.<webserver>.conf`, which
replaces it until deleted ("Restore"), and a file there with a new id is a
template of the user's. A template is a whole nginx `server { }` block or
Apache `<VirtualHost>`, PHP handler included, so a recipe from a CMS's docs
reads the same in Wharf. Wharf fills in placeholders for what only it knows:
`{{listen}}`, `{{ssl}}`, `{{server_name}}`, `{{root}}`, `{{log_dir}}`,
`{{php}}` and `{{fastcgi}}`. A template without `{{listen}}`,
`{{ssl}}` or `{{server_name}}` is refused on save: those decide where a
project is reachable. A project's generated file holds its template once
per place it is reachable — ports 80 and 443 on the front door, its loopback
port behind it. What differs between those places and is not a placeholder
lives outside the template: nginx's `$wharf_port` and `$wharf_https`, which
Wharf's `fastcgi.conf` hands to PHP, and Apache's `ProxyFCGISetEnvIf` lines,
set once for the whole instance. A project without a template gets a plain
block: its folder, its logs and PHP. An earlier version inserted only a
template's rules into a block Wharf wrote itself; a recipe from the docs
could not be pasted as it was, and its PHP location could not be changed.

A custom config is a config template for one project: it starts as a copy of
the project's template for the webserver serving it, placeholders included,
and is used *instead of* the template while that webserver serves the
project. It is checked like a template — on save, and again on start, since
it can be edited anywhere — and a project whose file fails the check fails
alone rather than taking the front door down. Settings only ever offer the
serving webserver's file: an Apache file for a project nginx serves would do
nothing. An earlier version included a file of directives into the
template's block through a `{{custom}}` placeholder, with both webservers'
files offered side by side; users could not tell which one applied, and the
file could not change the template's own rules — only add to them.

PHP's own settings follow the same rule: `config/php.ini` is a php.ini, not a
JSON object. Every PHP process is started with `PHP_INI_SCAN_DIR` naming
`config/` after an empty entry, which keeps the scan directory PHP was built
with — so the file is read *after* PHP's own php.ini and its values win,
while an adopted Homebrew PHP still loads the extensions its own `conf.d`
enables (`features/php-settings.feature`).

Extensions are not in that file: one a version already loads makes PHP
warn on every start. A PHP found without a php.ini of its own is started
with `-d extension=…` for the list a downloaded build loads
(`features/php-runtime.feature`).

Reset deletes `config/` whole, not file by file: whatever the user put
there, the next start begins from nothing. The folders in `www/` are the
user's own work, so Reset keeps them unless the user ticks "Also delete the
projects in www/"; a folder added from elsewhere is never deleted. Nothing a
reset does needs administrator rights (`features/settings.feature`).

Roadmap keys (`database`, `mail`, `runtimes.node/go/python`) are specified
in `features/roadmap-services.feature` but intentionally absent from this
v1 shape.

## 7. How a roadmap service is added later

A database, Mailpit or another runtime must be an addition, never a change
to what v1 files and clients already rely on. The seams are there now:

| Layer | What a new service adds | Why nothing breaks |
|---|---|---|
| `wharf.json` | one key under `services` (`"database": {…}`), and an optional per-project key (`"database": "postgres"`) | both `omitempty`; a v1 file has neither, and `normalise()` fills defaults in |
| `internal/<service>` | detection and installing, like `internal/php` and `internal/webserver` | a package of its own |
| `internal/runtime` | one `…Spec` builder: binary, args, port, log path | the supervisor runs any spec; it knows no service by name |
| `core` | the user actions, one method each, and a field in the `Services` snapshot | the GUI parses every key with a default, so an older GUI ignores it |
| GUI | one `SettingsSection` entry and its page; a row in the project sheet | the navigation lists what the enum holds |

A project that *uses* a service — "this app needs PostgreSQL" — is a
per-project key like `php_version`, started by `StartProject` beside the
PHP backend. Nothing in the front door changes: a database is not served
by name.

## 8. Design-principle cross-reference

See `dev/design-principles.md` for how the Kirby "just files and folders"
philosophy constrains the above (flat config over database, folder-as-project,
no settings surfaced that aren't needed for v1).
