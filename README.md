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
- [`CLAUDE.md`](CLAUDE.md) — the sync rule and the file directory
- `daemon/` — the Go core daemon (`wharfd`) and its CLI (`wharfctl`) — see
  [`daemon/README.md`](daemon/README.md)
- `gui/` — the Flutter desktop app — see [`gui/README.md`](gui/README.md)

## Status

| Component | State |
|---|---|
| Core daemon | Implemented. Every `@v1` scenario has a test claiming it. |
| Flutter GUI | Implemented: project list, tray menu, settings, PHP version picker. Runs on macOS today; Linux and Windows shells are generated but unbuilt here. |
| `wharfctl` CLI | Implemented — a complete way to use Wharf without the GUI. |
| PHP runtime | Detected on the machine and adopted where it is, or downloaded from Settings with one click. |
| SSL | mkcert is downloaded (pinned, checksum-verified) and its authority trusted when a project first turns SSL on. |
| Webservers | Adopted where installed (a Mac's own Apache is used out of the box), or installed from Settings: nginx on Linux/Windows by download, on macOS via Homebrew; Apache on Windows by download. |
| Appearance | Kirby-plain light and dark themes, or follow the system. |
| Packaging (DMG / MSI / AppImage) | Not started. |

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

Then add a project: pick any folder with "Add folder…" — it stays where it is —
or put one in `~/Wharf/www/` and it appears in the app, ready to add.
Adding writes a real `/etc/hosts` entry, so it asks for your password once;
declining is fine — the project still works on its raw-port URL.

Quitting from the tray stops everything Wharf started. If the app is force-quit
or crashes, the daemon notices within a couple of seconds and stops itself.
If something looks wrong, the log is at `~/Wharf/data/log/wharfd.log`.

### Without the GUI

`wharfctl` is a complete front end on its own:

```sh
./build/wharfctl status
./build/wharfctl php                        # what PHP this machine has
./build/wharfctl new kirby my-kirby-site    # scaffold from the Kirby starter kit
```

### Everything else

```sh
make check   # both halves, the sync guard, the cross-compile
make spec    # the spec-to-test coverage matrix
make run     # the daemon alone, against a throwaway root
```

**Note:** on Linux, Apache comes from your distribution's package — Settings
names the command. Everything else installs itself on request.

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
