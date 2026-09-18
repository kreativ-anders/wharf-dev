# Wharf — working agreement

Local development environment manager. Native processes, no Docker.
Flutter GUI + Go daemon. PHP-only in v1, Kirby-inspired minimalism.

Working title **Wharf**: chosen so it can be search-and-replaced in one pass
once a name is locked in. Use it consistently — never introduce a second name.

---

## 1. The sync rule

**The Gherkin specs in [`features/`](features/) and the code in
[`daemon/`](daemon/) must never disagree. Neither one is allowed to change
alone.**

`features/*.feature` is the specification. Code implements it; tests prove it.
The rule is enforced by [`TestSpecCoverage`](daemon/internal/specsync/specsync_test.go),
which runs as part of `make test` and fails the build on any drift.

### How a test claims a scenario

Put the claim in a comment directly above the test. The scenario title must
match the `.feature` file exactly; a long title may wrap across comment lines.
Both languages count — the GUI implements these specs as much as the daemon
does, and one scenario may be claimed by several tests.

```go
// features/service-management.feature — "Switching the active webserver"
func TestSwitchingTheActiveWebserver(t *testing.T) { … }
```

```dart
// features/settings.feature — "Settings screen hides roadmap services in v1"
testWidgets('settings show only Webserver and PHP runtime', (tester) async { … });
```

### What the guard enforces

| Situation | Result |
|---|---|
| A `@v1` scenario has no test claiming it | **Fail** — implement it, or re-tag it `@roadmap` |
| A claim names a scenario that no longer exists | **Fail** — the scenario was renamed or deleted; update the claim |
| A test claims a `@roadmap` scenario | **Fail** — re-tag the scenario `@v1` first if it really is implemented |

Run `make spec` to print the coverage matrix.

### Which comes first

- **Changing behaviour** → edit the `.feature` file first, then the code, then
  the test claim. The spec is the record of intent; a diff that touches only
  Go is a diff that lost the intent.
- **New behaviour** → write the scenario, watch `make spec` show `✗ NO TEST`,
  then make it pass.
- **Behaviour that turns out to be wrong or out of scope** → change or re-tag
  the scenario, and say why in the commit. Do not delete a scenario to silence
  the guard.
- **Documents in `dev/`** are not covered by the guard. When a decision
  recorded there changes, update the document in the same commit and say so in
  the message. A code change without its document change is unfinished.

### Keeping the section-2 map current

Section 2 below is the file directory. Update it whenever a file or package is
added, removed or renamed — in the same commit. It is the map a new reader
starts from; a stale map is worse than none, and `internal/docsync` fails when
it goes stale.

### The prose guard

[`internal/docsync`](daemon/internal/docsync/) does for prose what the sync
guard does for specs, also inside `make test`: every file in the places §2
covers is named in §2; the landing page's FAQ and its JSON-LD ask the same
questions; every build of `wharfd` stamps its version; every path the release
workflow names exists; `tool/version.sh` computes versions correctly.

Only claims a machine can decide go there. **A prose rule that has been broken
twice becomes a test in `docsync`**, not just a corrected sentence.

---

## 2. File directory

```
wharf/
├── CLAUDE.md                  ← this file: the working agreement and the map — the only instruction file
├── README.md                  project overview and status
├── CONTRIBUTING.md            set-up, workflow, commit subjects — points here for the rules
├── CHANGELOG.md               written by the release workflow, never by hand
├── Makefile                   build, test, cross-compile, spec matrix, version and release
├── .github/workflows/
│   └── release.yml            THE ONLY WORKFLOW: gate → bump → build per OS → release
├── tool/
│   └── version.sh             the one place a version is computed, bumped, tagged, changelogged
├── packaging/macos/
│   └── build_dmg.sh           Wharf.app → signed, notarised DMG; same script locally and in CI
│
├── dev/                       developer documentation — decisions, not user docs
│   ├── architecture.md        components, cross-OS strategy, config shape
│   ├── design-principles.md   Kirby-inspired minimalism, applied
│   ├── glossary.md            the words Wharf uses for its own things
│   ├── releasing.md           one version source, cutting a release, what the workflow does
│   └── quality-audit.md       the recurring on-demand review: a11y, landing page, code
│
├── docs/                      the public landing page (GitHub Pages) — HTML + CSS, no JS
│   ├── index.html             content from features/; FAQ mirrored in its JSON-LD
│   ├── style.css              the app's Kirby-plain palette (gui/lib/theme.dart)
│   ├── icon.svg               favicon: the app mark on its tile
│   ├── apple-touch-icon.png   the app icon at 180 px
│   ├── og.png                 1200×630 link preview, drawn from the same mark
│   ├── robots.txt, sitemap.xml
│   └── .nojekyll              serve the files as-is, no Jekyll build
│
├── features/                  THE SPECIFICATION — Gherkin, one file per capability
│   ├── app-configuration.feature    per-project overrides, custom config
│   ├── config-templates.feature     nginx/Apache rules per kind of project: built in, edited, your own
│   ├── local-ssl.feature            mkcert installed and trusted by the SSL switch
│   ├── php-runtime.feature          PHP detection, first-run default, picker, download, remove/hide
│   ├── php-settings.feature         config/php.ini, read by every PHP version after its own
│   ├── php-terminal.feature         bin/path runs the default PHP; "Use in terminal" puts it on PATH
│   ├── pretty-urls.feature          <name>.localhost; the hosts file is never touched
│   ├── project-logs.feature         one log folder per project, opened from its settings
│   ├── project-folders.feature      "Add project…": what is missing first, folder picker, detection, opening folders
│   ├── roadmap-services.feature     @roadmap only — do not implement
│   ├── service-management.feature   webserver switching, port conflicts
│   ├── settings.feature             global settings surface
│   ├── single-application.feature   one app: starts and stops its own daemon
│   ├── tray-actions.feature         tray menu behaviour
│   └── webserver-install.feature    nginx/Apache: adopt, install, one config per project
│
├── daemon/                    the Go core daemon — see daemon/README.md
│   ├── go.mod                 module github.com/kreativ-anders/wharf-dev/daemon
│   ├── cmd/
│   │   ├── wharfd/            the daemon binary
│   │   └── wharfctl/          dev-only CLI client of a running daemon; not shipped
│   └── internal/
│       ├── certs/             mkcert: pinned download, local CA trust, per-project certs
│       ├── config/            wharf.json: load, atomic write, hand-edit repair
│       ├── configtemplate/    config templates: the built-in rules (builtin/*.conf) and config/templates/
│       ├── core/              THE BEHAVIOUR — one method per user action
│       │   ├── core.go        the daemon: construction, first run, settings, reset, shutdown
│       │   ├── projects.go    inspect a folder, add, remove, start, stop, restart, overrides
│       │   ├── php.go         PHP versions: detect, adopt, download, remove or hide, backends
│       │   ├── webserver.go   webservers: detect, install, switch the active one
│       │   ├── frontdoor.go   what the front door serves; own instances' ports
│       │   ├── userfiles.go   custom configs and php.ini: read, saved, applied on save
│       │   ├── configtemplates.go  config templates: listed, read, saved, created, deleted
│       │   ├── terminal.go    bin/path follows the default PHP; "Use in terminal" on and off
│       │   ├── errors.go      what the user got wrong, told apart from what the daemon did
│       │   ├── state.go       the snapshot the GUI renders
│       │   ├── api.go         IPC method wiring
│       │   ├── watch.go       reloads wharf.json and custom configs when hand-edited
│       │   └── *_test.go      one test file per feature file (see §1)
│       ├── docsync/           THE PROSE GUARD (§1): the map, the FAQ mirror, version stamping
│       ├── download/          HTTPS fetch, checksum, untar/unzip for PHP and mkcert
│       ├── elevate/           PLATFORM-SPECIFIC: elevation prompts (3 adapters)
│       ├── ipc/               newline-delimited JSON over a unix socket; loopback TCP + token on Windows
│       ├── layout/            the portable root: bin/ www/ config/ data/
│       ├── php/               release timeline, support status, detection, downloads
│       ├── proctree/          PLATFORM-SPECIFIC: a service and its workers, stopped as one; is a process alive
│       ├── project/           folder-as-project discovery, name rewriting, config template detection
│       ├── runtime/           config → process specs; the front door, one generated file per project
│       ├── shellpath/         PLATFORM-SPECIFIC: bin/path's php, and bin/path on the user's PATH (startup files / user environment)
│       ├── specsync/          THE SYNC GUARD (§1)
│       ├── supervisor/        process lifecycle, port waiting, state machine
│       ├── watchdog/          daemon exits when the app that started it is gone
│       └── webserver/         find nginx/Apache (Wharf's, Homebrew's, the OS's), install them
│
└── gui/                       the Flutter desktop app — see gui/README.md
    ├── pubspec.yaml           THE VERSION: `version: X.Y.Z+B` (dev/releasing.md)
    ├── dart_test.yaml          declares the e2e tag (excluded from the default run)
    ├── assets/tray/           tray icon: template_*.png (macOS), icon_*.png (Linux), icon.ico (Windows)
    ├── tool/draw_icons.py     draws the tray and app icons from one mark
    ├── lib/
    │   ├── main.dart          window, tray wiring, close-to-tray
    │   ├── daemon.dart        THE APP'S STATE — one snapshot from the daemon
    │   ├── daemon_launcher.dart  finds, starts and stops wharfd: one application
    │   ├── folders.dart       opening folders and files, the folder picker, the tray's add
    │   ├── project_name.dart  the daemon's name rewrite, mirrored for the "Add project…" name field
    │   ├── theme.dart         Kirby-plain light/dark palette, status mark (shape + colour)
    │   ├── tray.dart          the tray menu (features/tray-actions.feature)
    │   ├── ipc/
    │   │   ├── endpoint.dart  reads data/wharf.endpoint, opens the transport
    │   │   └── client.dart    newline-delimited JSON client
    │   ├── models/state.dart  the snapshot shape — a contract with core/state.go
    │   └── pages/
    │       ├── projects_page.dart  THE ONE PRIMARY VIEW; before the first project, what is missing
    │       ├── add_project.dart    "Add project…": folder picker, then the sheet proposing name, template, webserver
    │       ├── project_sheet.dart  per-project overrides, config template, custom config and logs, behind a tap
    │       ├── config_editor.dart  the line-numbered editor for config templates and custom configs, and "New template…"
    │       └── settings_page.dart  side navigation: General, Webserver (config templates), PHP (picker, php.ini), SSL
    ├── test/
    │   ├── state_test.dart      snapshot parsing
    │   ├── theme_test.dart      WCAG contrast of the palette, both themes
    │   ├── widgets_test.dart    what each view renders
    │   ├── add_project_test.dart  "Add project…" and the window before the first project
    │   ├── config_templates_test.dart  the config template list, editor and project picker; the custom config editor
    │   ├── accessibility_tree_test.dart  semantics updates replayed through the Windows engine's AXTree commit
    │   ├── project_name_test.dart  the name rewrite, against the daemon's own table
    │   ├── launcher_test.dart   binary search order, missing-binary message
    │   ├── launcher_e2e_test.dart  start / attach / quit against real wharfd (--tags e2e)
    │   ├── daemon_e2e_test.dart drives the real wharfd binary (--tags e2e)
    │   └── e2e_daemon.dart      the binary both e2e tests drive; refuses a build older than daemon/
    └── macos/ linux/ windows/   platform shells; macOS embeds wharfd via the
                                 "Embed wharfd" Xcode build phase, Windows via
                                 a post-build step in windows/runner/CMakeLists.txt
```

---

## 3. Standing constraints

These come from `dev/`. They are decisions, not preferences; changing one
means changing the document that records it.

- **No Docker, no VM.** Native host processes only.
- **One behaviour across Windows, macOS and Linux.** Prefer identical behaviour
  over the best per-platform solution. All platform branching lives in
  [`internal/elevate`](daemon/internal/elevate/) (elevation prompts),
  [`internal/proctree`](daemon/internal/proctree/) (process group / job object) and
  [`internal/shellpath`](daemon/internal/shellpath/) (shell startup files / user environment, link / `php.cmd`),
  plus one constant: the Windows `.exe` suffix. `make cross` proves it still builds
  everywhere.
- **PHP only in v1.** Node, Go, Python, MySQL, PostgreSQL and Mailpit are
  `@roadmap` — specified, deliberately unimplemented.
- **One flat config file**, hand-editable. No database, no scattered formats.
  Someone comfortable with a text editor must be able to skip the GUI entirely.
- **Folder = project.** A folder in `www/` is found by name; a folder anywhere
  else can be added and stays where it is, its location recorded as `path`.
- **Wharf installs what it needs.** PHP builds, webservers and mkcert are
  installed on request, never at first launch; what is already on the
  machine is adopted first. The source differs per platform
  (`dev/architecture.md` §4b); the user's action does not. Nothing requires
  placing a binary by hand.
- **Accessible by default.** WCAG AA contrast in both themes, status never
  by colour alone, every control labelled (`dev/design-principles.md` §4).
- **One primary view.** Progressive disclosure for per-project settings; no
  dashboards, no charts, no onboarding carousel.
- **The daemon owns all state.** The tray menu and the main window render the
  same snapshot, so they cannot disagree.
- **One application.** The user launches Wharf and nothing else. The app starts
  its own daemon and stops it on quit; the daemon is never something a user has
  to know about, start, or clean up after.

### Settled decisions

Each of these was worked through and written down, several after the first
plan failed in practice. **Do not reverse one — not even as a "cleanup" —
without discussing it first.** If the discussion changes it, the document
that records it changes in the same commit.

- `<name>.localhost`, no hosts-file lines, no DNS resolver of Wharf's own:
  the `.wharf` plan left Safari unable to find projects on macOS 26
  (`dev/architecture.md` §2).
- One front door on ports 80/443. A project pinned to the other webserver is
  proxied to its own loopback instance, so every URL has the same shape (§2, §4c).
- IPC is a unix socket on macOS/Linux and loopback TCP with a token on Windows,
  found through `data/wharf.endpoint`, because Dart has no unix sockets on
  Windows (§4a).
- Custom webserver directives and PHP settings are files (`config/vhosts/`,
  `config/php.ini`), not JSON keys (§6).
- The app stops the daemon by asking over IPC, never by a signal: on Windows a
  signal is `TerminateProcess` (`daemon/README.md`).
- The macOS sandbox stays off (§4 below).
- The update check, once built, looks only when asked and never runs what it
  downloaded (`features/settings.feature`, `dev/architecture.md` §4b).
- One version source, `gui/pubspec.yaml`; one GitHub workflow, and no push or
  PR CI (`dev/releasing.md`).

**When the code and a document disagree, ask or reconcile both — don't
guess.** A document that no longer matches was usually forgotten in the last
change. That does not prove the code is right.

---

## 4. Conventions

- **Comments explain why, never what.** Where a decision came from a spec, name
  it: `(service-management.feature, "Port conflict on switch")`. A comment
  that explains *how Wharf works* belongs in `dev/` and shrinks to a pointer
  (`see dev/architecture.md §4c`). Keep the comments that name a concrete
  failure or say why something is deliberately *not* done.
- **Every explanatory comment carries a tag.** `INFO:` says why a line is
  the way it is; `WARNING:` names what breaks if it is changed — a concrete
  failure, a hazard, something deliberately not done; `TODO(topic):` is work
  still due. Doc comments on declarations — Go's above a package, type, func
  or field, Dart's `///` — stay untagged, and so do spec claims (§1) and tool
  directives (`//go:build`, `// ignore:`).
- **A `TODO` names the condition that makes it due** ("once packaging exists").
  One without a condition is a wish; delete it.
- **Errors name the fix.** "PHP 8.2 is not installed (expected …/bin/php/8.2)",
  not "exec: no such file".
- **A declined elevation prompt is a normal outcome**, never an error dialog:
  the action finishes without what the prompt would have added — SSL works
  with a browser warning.
- **Use the glossary's words** ([`dev/glossary.md`](dev/glossary.md)) in UI
  text, specs, docs and identifiers. A new concept gets a row in the same
  commit that introduces it.
- **Tests fake the outside world** — processes, ports, mkcert, the
  network — so the suite never prompts for a password, binds a real port, or
  needs a vendored binary.
- Run `make fmt vet test` before considering anything done; `make race` before
  touching the supervisor or the IPC layer; `make gui-test` after touching the
  GUI.
- **The GUI owns no state.** Every action asks the daemon and re-renders the
  snapshot it gets back. If a view needs data, add it to the snapshot rather
  than deriving it locally, or the tray and the window will drift apart.
- **The macOS sandbox stays off** (`gui/macos/Runner/*.entitlements`). Wharf
  manages the user's own projects and processes; a container reaches none of
  them.
- **Commit subjects are a gitmoji and an imperative English sentence.** They
  go into `CHANGELOG.md` word for word at release, so write them for a reader
  of release notes. The body says why.
- **This is the only instruction file.** Point a new tool at it; never copy
  these rules into a second file (AGENTS.md, a Copilot file, CONTRIBUTING.md)
  where they can drift.

---

## 5. Commands

```sh
make test      # daemon tests, including both guards
make spec      # print the spec-to-test coverage matrix
make race      # the daemon suite under the race detector
make cross     # build the daemon for macOS, Linux and Windows (arm64 + amd64)
make run       # run the daemon against a throwaway root, no password prompt

make gui-test  # GUI unit and widget tests
make gui-e2e   # GUI driving the real daemon binary (needs `make build` first)
make gui       # build and launch the desktop app against a throwaway root
make check     # everything above that is not interactive

make version                 # the version this checkout builds as
make release BUMP=minor      # bump gui/pubspec.yaml, commit, tag — never pushes
make dmg                     # release Wharf.app in a DMG (macOS)
```

---

## 6. Versions and releases

- `gui/pubspec.yaml` holds the only version number (`X.Y.Z+B`), and
  `tool/version.sh` is the only thing that computes one. Never hard-code a
  version anywhere else.
- A release is `make release BUMP=…` followed by a push of the commit and the
  tag, or the button in the Release workflow. Do not tag by hand without
  bumping: the gate refuses a tag that disagrees with the pubspec.
- `CHANGELOG.md` and the GitHub release notes are generated from commit
  subjects. Never edit the changelog by hand.
- The details, and why the workflow is shaped the way it is:
  [`dev/releasing.md`](dev/releasing.md).
