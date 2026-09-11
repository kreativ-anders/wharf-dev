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
- No wildcard DNS resolver (`*.wharf.test`) — v1 uses hosts-file entries per
  project, identical logic on all three OS. Wildcard resolution is a
  possible v2 addition, not a v1 dependency.
- No chained webservers (nginx as a reverse proxy in front of Apache). The
  pattern survives on shared hosting — Plesk and cPanel use it so `.htaccess`
  keeps working behind nginx — but its reasons are production ones: offloading
  static files and connections from Apache's process model. Locally there is
  no load to offload, and with PHP-FPM both servers talk to PHP directly. The
  one case it serves, a project that needs `.htaccess`, is covered by choosing
  Apache for that project (per-project webserver override), which costs no
  second process and no second config to keep in step.

## 3. Components

| Component | Responsibility | Technology |
|---|---|---|
| GUI | Tray icon, project list, settings, one-click actions | Flutter (desktop target) |
| Core daemon | Process orchestration, config state, hosts/elevation adapter | Go, single cross-compiled binary |
| IPC | GUI ↔ daemon transport | Unix domain socket on macOS/Linux; loopback TCP + token on Windows (see §4a) |
| Service registry | Which services exist, which is active, per-project overrides | Single config file (JSON/TOML) read by daemon, watched for changes |
| Elevation adapter | The **only** platform-branching code path | 3 thin implementations behind one interface (see §4) |
| Runtime binaries | PHP (v1); Nginx/Apache; mkcert; roadmap: Node, Go, Python, MySQL, PostgreSQL, Mailpit | Per-OS portable builds under `bin/` — see §4b |

## 4. Cross-OS strategy: one code path, minimal branching

Every design decision favours identical behaviour across OS over the most
"elegant" per-platform solution:

- **Pretty URLs** — hosts-file entry (`C:\Windows\System32\drivers\etc\hosts`
  vs `/etc/hosts`), same logic, different path constant only. No macOS
  `/etc/resolver` trick, no Linux `dnsmasq` wildcard — deliberately, to keep
  behaviour identical rather than "best per OS."
- **Elevation** — one interface with two functions:
  `RequestElevatedWrite(path, content)` for the hosts file, and
  `RequestElevatedRun(program, args, env)` for trusting mkcert's local
  certificate authority, which writes to the system trust store. All call
  sites use only these. Three small adapters behind them (UAC / `osascript
  with administrator privileges` / `pkexec`) are the entire platform-specific
  surface of the whole codebase. The second function was added with
  `features/local-ssl.feature`: without it the authority is never trusted and
  every browser warns.
- **IPC** — see §4a. One protocol and one client API across all three OS; the
  socket type differs on Windows only because the GUI's language cannot open a
  unix socket there.
- **Process management** — `os/exec` behaves identically on all three OS;
  no adaptation needed.
- **SSL** — `mkcert`, already cross-platform, used unmodified.
- **Distribution shell** — a portable root folder (`bin/`, `www/`, `config/`)
  is the shared internal model; only the outer package format differs
  (installer / DMG / AppImage), never the content or config format.

**Deliberately not unified** (documented, not solved): elevation-prompt UX
per OS, outer package format, and Linux system-tray availability (GNOME
requires an extension; KDE/Xfce work natively).

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
the TCP transport requires a token as its first call. The token lives in
`data/wharf.endpoint`, written mode `0600`, which is also how *every* client on
*every* OS finds the daemon: read the endpoint file, connect to whatever it
names. No client hard-codes a socket path or a port, and nothing above the
transport differs — same protocol, same methods, same events, same errors,
verified by the same tests running against both transports.

The cost is honest: one extra concept (the endpoint file) and one extra
handshake on one OS, in exchange for a GUI that runs on all three.

## 4b. Binaries Wharf installs

Nothing is downloaded at first launch (`features/php-runtime.feature`): a PHP
already on the machine is adopted where it is. Downloads happen only when the
user asks — "Download" in the PHP picker, or turning SSL on without mkcert:

| Binary | Source | Trust |
|---|---|---|
| PHP, macOS/Linux | static-php-cli builds (`dl.static-php.dev`): single static `php-fpm` and `php`, every extension Kirby needs | HTTPS; no checksums are published |
| PHP, Windows | the official builds (`windows.php.net`), NTS x64 | SHA-256 from `releases.json` |
| mkcert | a pinned GitHub release | SHA-256 pinned in `internal/certs` |

Each download is staged beside its destination and renamed into place only
when complete, so a failure leaves no half-installed folder. The newest patch
of a PHP minor version is found from each source's index, not hard-coded.

Nginx and Apache are not downloaded: neither publishes portable builds for
all three OS. Settings shows where each binary is expected instead.

## 5. Directory layout (the tool's own runtime folder, not the source repo)

```
wharf/
├── bin/
│   ├── php/            # versioned, one folder per version (downloaded or vendored)
│   ├── nginx/
│   ├── apache/
│   └── mkcert/         # downloaded when SSL is first turned on
├── www/                # user projects live here (Kirby-style: folder = project)
├── config/
│   ├── wharf.json      # single flat config file — see §6
│   └── vhosts/         # per-project custom webserver directives, e.g.
│                       #   my-site.nginx.conf, my-site.apache.conf
└── data/
    ├── wharf.endpoint  # where the daemon is listening — see §4a
    ├── log/wharfd.log  # the daemon's own log (per-service logs sit beside it)
    ├── wharf.sock      # unix transport only
    ├── gen/            # generated nginx/apache/php-fpm configs (overwritten on start)
    ├── log/            # per-service logs
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
    { "name": "my-kirby-site", "ssl": true, "port": 8080, "hosts_entry": true }
  ]
}
```

Keys are omitted rather than written as `null`: clearing a project's
`webserver_override` removes the key, so a person reading the file sees only
what actually deviates from the defaults.

Two keys were added during implementation, both because behaviour depends on
them surviving a restart:

- `projects[].port` — the project's own raw port. It is what the fallback URL
  points at when the elevation prompt is declined, so it must not change
  between runs (`features/pretty-urls.feature`).
- `projects[].hosts_entry` — whether a hosts line exists. Removing a project
  must delete exactly the entry that was written, and elevation may have been
  declined when it was added.
- `projects[].path` — appears only for a project whose folder is not in
  `www/`: the folder is added where it is (`features/project-folders.feature`).
- `services.php.paths` — appears only when the tool adopted a PHP already
  installed on the machine, mapping version to its folder
  (`features/php-runtime.feature`). A self-contained install writes no paths.

Custom webserver directives are deliberately *not* keys in this file: they
are nginx or Apache syntax, which belongs in a file of its own that an editor
highlights, not in a JSON string. They live in `config/vhosts/`, are included
into the project's server block only while that webserver serves it, and are
picked up on save (`features/app-configuration.feature`).

Roadmap keys (`database`, `mail`, `runtimes.node/go/python`) are specified
in `features/roadmap-services.feature` but intentionally absent from this
v1 shape.

## 7. Design-principle cross-reference

See `dev/design-principles.md` for how the Kirby "just files and folders"
philosophy constrains the above (flat config over database, folder-as-project,
no settings surfaced that aren't needed for v1).
