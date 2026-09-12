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
  the scenario. Do not delete a scenario to silence the guard.
- **`dev/architecture.md` and `dev/design-principles.md`** are not covered by
  the guard. When a decision recorded there changes, update the document in the
  same commit and say so in the message.

### Keeping the section-2 map current

Section 2 below is the file directory. Update it whenever a file or package is
added, removed or renamed — in the same commit. It is the map a new reader
starts from; a stale map is worse than none.

---

## 2. File directory

```
wharf/
├── CLAUDE.md                  ← this file: the sync rule and the map
├── README.md                  project overview and status
├── Makefile                   build, test, cross-compile, spec matrix
│
├── dev/                       developer documentation — decisions, not user docs
│   ├── architecture.md        components, cross-OS strategy, config shape
│   └── design-principles.md   Kirby-inspired minimalism, applied
│
├── docs/                      the public landing page (GitHub Pages) — HTML + CSS, no JS
│   ├── index.html
│   ├── style.css
│   └── .nojekyll              serve the files as-is, no Jekyll build
│
├── features/                  THE SPECIFICATION — Gherkin, one file per capability
│   ├── app-configuration.feature    per-project overrides, custom webserver config
│   ├── local-ssl.feature            mkcert installed and trusted by the SSL switch
│   ├── php-runtime.feature          PHP detection, first-run default, picker, download
│   ├── php-settings.feature         config/php.ini, read by every PHP version after its own
│   ├── pretty-urls.feature          hosts-file entries and elevation
│   ├── project-logs.feature         one log folder per project, opened from its settings
│   ├── project-folders.feature      folders from anywhere, opening them
│   ├── quick-app-php.feature        Kirby scaffolding
│   ├── roadmap-services.feature     @roadmap only — do not implement
│   ├── service-management.feature   webserver switching, port conflicts
│   ├── settings.feature             global settings surface
│   ├── single-application.feature   one app: starts and stops its own daemon
│   ├── tray-actions.feature         tray menu behaviour
│   └── webserver-install.feature    nginx/Apache: adopt, install, one config per project
│
├── daemon/                    the Go core daemon — see daemon/README.md
    ├── go.mod                 module github.com/manuel-steinberg/wharf/daemon
    ├── cmd/
    │   ├── wharfd/            the daemon binary
    │   └── wharfctl/          CLI client; the front end until the GUI exists
    └── internal/
        ├── certs/             mkcert: pinned download, local CA trust, per-project certs
        ├── config/            wharf.json: load, atomic write, hand-edit repair
        ├── core/              THE BEHAVIOUR — one method per user action
        │   ├── core.go        projects, services, settings, scaffolding
        │   ├── state.go       the snapshot the GUI renders
        │   ├── api.go         IPC method wiring
        │   ├── watch.go       reloads wharf.json and custom configs when hand-edited
        │   └── *_test.go      one test file per feature file (see §1)
        ├── download/          HTTPS fetch, checksum, untar/unzip for PHP and mkcert
        ├── elevate/           THE ONLY PLATFORM-SPECIFIC CODE (3 adapters)
        ├── hostsfile/         removes hosts lines left from the old <name>.wharf scheme
        ├── ipc/               newline-delimited JSON over AF_UNIX
        ├── layout/            the portable root: bin/ www/ config/ data/
        ├── php/               release timeline, support status, detection, downloads
        ├── project/           folder-as-project discovery, template scaffolding
        ├── runtime/           config → process specs; the front door, one generated file per project
│       ├── specsync/          THE SYNC GUARD (§1)
│       ├── supervisor/        process lifecycle, port waiting, state machine
│       ├── watchdog/          daemon exits when the app that started it is gone
│       └── webserver/         find nginx/Apache (Wharf's, Homebrew's, the OS's), install them
│
└── gui/                       the Flutter desktop app — see gui/README.md
    ├── pubspec.yaml
    ├── dart_test.yaml          declares the e2e tag (excluded from the default run)
    ├── assets/tray/           tray icon: template_*.png (macOS), icon_*.png (Linux), icon.ico (Windows)
    ├── tool/draw_icons.py     draws the tray and app icons from one mark
    ├── lib/
    │   ├── main.dart          window, tray wiring, close-to-tray
    │   ├── daemon.dart        THE APP'S STATE — one snapshot from the daemon
    │   ├── daemon_launcher.dart  finds, starts and stops wharfd: one application
    │   ├── folders.dart       opening folders and files, the "Add folder…" picker
    │   ├── theme.dart         Kirby-plain light/dark palette, status mark (shape + colour)
    │   ├── tray.dart          the tray menu (features/tray-actions.feature)
    │   ├── ipc/
    │   │   ├── endpoint.dart  reads data/wharf.endpoint, opens the transport
    │   │   └── client.dart    newline-delimited JSON client
    │   ├── models/state.dart  the snapshot shape — a contract with core/state.go
    │   └── pages/
    │       ├── projects_page.dart  THE ONE PRIMARY VIEW
    │       ├── project_sheet.dart  per-project overrides and logs, behind a tap
    │       └── settings_page.dart  side navigation: General, Webserver, PHP (picker, php.ini), SSL
    ├── test/
    │   ├── state_test.dart      snapshot parsing
    │   ├── theme_test.dart      WCAG contrast of the palette, both themes
    │   ├── widgets_test.dart    what each view renders
    │   ├── launcher_test.dart   binary search order, missing-binary message
    │   ├── launcher_e2e_test.dart  start / attach / quit against real wharfd (--tags e2e)
    │   └── daemon_e2e_test.dart drives the real wharfd binary (--tags e2e)
    └── macos/ linux/ windows/   platform shells; macOS embeds wharfd via the
                                 "Embed wharfd" Xcode build phase
```

---

## 3. Standing constraints

These come from `dev/`. They are decisions, not preferences; changing one
means changing the document that records it.

- **No Docker, no VM.** Native host processes only.
- **One behaviour across Windows, macOS and Linux.** Prefer identical behaviour
  over the best per-platform solution. All platform branching lives in
  [`internal/elevate`](daemon/internal/elevate/), plus two constants: the hosts
  file path and the Windows `.exe` suffix. `make cross` proves it still builds
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

---

## 4. Conventions

- **Comments explain why, never what.** Where a decision came from a spec, name
  it: `(service-management.feature, "Port conflict on switch")`.
- **Errors name the fix.** "PHP 8.2 is not installed (expected …/bin/php/8.2)",
  not "exec: no such file".
- **A declined elevation prompt is a normal outcome**, never an error dialog:
  fall back to the raw-port URL.
- **Tests fake the outside world** — processes, ports, hosts file, mkcert, the
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

---

## 5. Commands

```sh
make test      # daemon tests, including the sync guard
make spec      # print the spec-to-test coverage matrix
make race      # the daemon suite under the race detector
make cross     # build the daemon for macOS, Linux and Windows (arm64 + amd64)
make run       # run the daemon against a throwaway root, no password prompt

make gui-test  # GUI unit and widget tests
make gui-e2e   # GUI driving the real daemon binary (needs `make build` first)
make gui       # build and launch the desktop app against a throwaway root
make check     # everything above that is not interactive
```
