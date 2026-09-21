# Wharf — Concept & Documentation (v0.1, working title)

> Local development environment manager. Native processes, no Docker.
> Flutter GUI + native background daemon. One-click GUI, inspired by Laragon's
> simplicity and Kirby's "just files and folders" minimalism.

The concept came first: architecture, scope and behaviour were fixed in
`dev/` and `features/` before any code was written. Both halves now exist:
the Go daemon and the Flutter desktop app.

**The specs and the code are kept in sync mechanically** — every `@v1`
scenario must be claimed by a test, in either language, and a build fails if
one is renamed, deleted or left unimplemented. See [CLAUDE.md](CLAUDE.md) §1.

## Contents

- `dev/architecture.md` — component overview, cross-OS strategy, directory layout
- `dev/design-principles.md` — Kirby-inspired minimalism, applied to this tool
- `features/*.feature` — Gherkin behaviour specs, one file per capability
- `docs/` — the public landing page (static HTML + CSS, served by GitHub Pages)
- [`CLAUDE.md`](CLAUDE.md) — the working agreement: the sync rule, the file
  directory, the settled decisions
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — getting set up, commits
- `dev/releasing.md` — versions and how a release is cut;
  [`CHANGELOG.md`](CHANGELOG.md) is written by each release
- `daemon/` — the Go core daemon (`wharfd`) and a developer CLI (`wharfctl`) — see
  [`daemon/README.md`](daemon/README.md)
- `gui/` — the Flutter desktop app — see [`gui/README.md`](gui/README.md)

## Status

| Component | State |
|---|---|
| Core daemon | Implemented. Every `@v1` scenario has a test claiming it. |
| Flutter GUI | Implemented: project list, tray menu, settings, PHP version picker. Runs on macOS today; Linux and Windows shells are generated but unbuilt here. |
| `wharfctl` CLI | Developer tool: drives a running daemon from the terminal. Not shipped in releases. |
| PHP runtime | Detected on the machine and adopted where it is, or downloaded from Settings with one click. |
| SSL | mkcert is downloaded (pinned, checksum-verified) and its authority trusted when a project first turns SSL on. |
| Webservers | Adopted where installed (a Mac's own Apache is used out of the box), or installed from Settings: nginx on Linux/Windows by download, on macOS via Homebrew; Apache on Windows by download, on Linux with the distribution's package manager. |
| Config templates | nginx and Apache rules for Kirby, Laravel, WordPress, Statamic, Symfony, Craft CMS and Drupal built in; each project picks one, and Settings → Webserver edits them or adds your own. |
| Appearance | Kirby-plain light and dark themes, or follow the system. |
| Updates | Settings → General shows the running version. "Check for updates" is there but disabled until releases exist; its behaviour is specified as `@roadmap`. |
| Releases | A release workflow builds and publishes all three platforms from one version in `gui/pubspec.yaml` (`dev/releasing.md`). Nothing is published yet. |
| Packaging | macOS: a DMG, signed and notarised when the credentials are there. Linux: an AppImage. Windows: an Inno Setup installer, not yet code-signed (SmartScreen warns). |

## Running it

**One application.** Launch Wharf and it works — it starts its background
daemon itself, and stops it again when you quit. There is nothing else to
start and no order to get right (`features/single-application.feature`).

### From the repository

```sh
make gui
```

Builds everything and launches the app against a throwaway root
(`build/dev/root`) with elevation disabled — no password prompt, and the real
`/etc/hosts` is never written.

Or open the built app directly; it uses `~/Wharf`:

```sh
cd gui && flutter build macos
open build/macos/Build/Products/Release/Wharf.app
```

The macOS build embeds `wharfd` inside `Wharf.app`, so the app is
self-contained. Building needs Go installed (`brew install go`).

Then add a project: "Add project…" asks for a folder — it stays where it is —
and proposes a name, the config template it detects (Kirby, Laravel,
WordPress, …) and a webserver; "Add" starts it. A folder put in
`~/Wharf/www/` appears in the app, ready to add the same way. Before the
first project the window says whether PHP and a webserver are there, and
installs what is missing when asked.
Adding a project writes nothing outside the Wharf folder and asks for no
password: every project is `http://<name>.localhost`, which the OS resolves by
itself.

Quitting from the tray — or "Cast off" in the window — stops everything Wharf
started. If the app is force-quit
or crashes, the daemon notices within a couple of seconds and stops itself.
If something looks wrong, the log is at `~/Wharf/data/log/wharfd.log`.

### From the terminal

`wharfctl` talks to a running daemon — the app's, or one from `make run` — and
never starts one. It is for development and debugging, not part of a release:

```sh
./build/wharfctl status
./build/wharfctl php                        # what PHP this machine has
./build/wharfctl inspect ~/Code/site        # what "Add project…" would propose
```

### Everything else

```sh
make check   # both halves, the sync guard, the cross-compile
make spec    # the spec-to-test coverage matrix
make run     # the daemon alone, against a throwaway root
```

**Note:** on Linux, Wharf asks for your password once before the first
project starts, to use ports 80 and 443, and again when it installs Apache
from your distribution's package.

## Scope statement (v1)

- **PHP-first.** The only fully specified runtime for v1 is PHP (mirrors
  Kirby's own PHP-only philosophy — no multi-language surface to maintain
  before the core tool is proven).
- Node.js, Go, Python, MySQL, PostgreSQL and Mailpit are **roadmap items**,
  specified at the level of scenarios but tagged `@roadmap` and not part of
  the v1 build.
- Cross-OS: Windows, macOS, Linux — one behaviour, one codebase, minimal
  platform branching (see `dev/architecture.md` §4).
- No Docker, no VM layer — native host processes only.

## Naming

Working title **Wharf**. Not finalised — placeholder used consistently
across these documents so it can be search-and-replaced once a name is
locked in.

## Licence

MIT — see [LICENSE](LICENSE).
