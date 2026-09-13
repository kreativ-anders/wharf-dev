# Wharf GUI

The Flutter desktop app: a tray icon, one window, one list of projects.

## Run it

It is one application: launching it starts the daemon, quitting stops it.

```sh
make gui        # from the repo root: launches the app against a throwaway root
make gui-test   # unit and widget tests
make gui-e2e    # drives the real wharfd binary end to end
```

### How the app gets its daemon

[`daemon_launcher.dart`](lib/daemon_launcher.dart) does it, in this order:

1. **Already running?** If `data/wharf.endpoint` names a daemon that answers,
   attach to it — and leave it running on quit, because the app did not start it.
2. **Otherwise find `wharfd`:** `WHARFD_BIN` if set, then next to the app's own
   executable (where the macOS build embeds it), then the repository's `build/`
   folder, so `flutter run` from a checkout works too.
3. **Start it** with `--parent-pid <app pid>`, wait for it to answer, connect.
4. **On quit**, stop it — gracefully, so it stops its webservers first.

If the app never gets to step 4 — a crash, a force quit — the daemon sees its
parent is gone within two seconds and stops itself
(`daemon/internal/watchdog`).

If `wharfd` cannot be found, the window says where it looked and to run
`make build`, and keeps checking — building it is enough to recover.

### Embedding on each platform

- **macOS** — an Xcode build phase, *Embed wharfd*, runs `make build`, copies
  the binary into `Wharf.app/Contents/MacOS/` and signs it with Xcode's
  identity. Release and Profile builds are universal, so the phase builds the
  daemon for Apple Silicon and Intel too (`DARWIN_UNIVERSAL=1`); an arm64-only
  daemon in a universal app could not start on an Intel Mac. It needs Go on the
  build machine.
- **Windows** — a post-build step in `windows/runner/CMakeLists.txt` runs
  `go build` and drops `wharfd.exe` next to `wharf_gui.exe` on every build, so
  `flutter run -d windows` always launches the daemon built from the working
  tree. It stamps the version from `VERSION`, else pubspec's. Needs Go on
  `PATH` at CMake configure time.
- **Linux** — not wired into CMake yet. The release workflow copies `wharfd`
  next to the app executable in the bundle, where the launcher finds it.

Every path stamps `wharfd` with the version from `gui/pubspec.yaml`
(`dev/releasing.md`).

## How it talks to the daemon

The daemon writes `<root>/data/wharf.endpoint` on start; the GUI reads it and
connects to whatever it names — a unix socket on macOS and Linux, loopback TCP
with a token on Windows, where `dart:io` has no unix sockets. See
`dev/architecture.md` §4a. Nothing above the transport differs.

Every action returns a full state snapshot, and the daemon pushes one on any
change. So:

- **The GUI owns no state.** [`daemon.dart`](lib/daemon.dart) holds one
  snapshot and re-renders. There is no local model to keep in step, no polling,
  and no way for the tray menu and the window to disagree.
- **If a view needs data, it goes in the snapshot** — not into a local
  calculation that the tray menu would have to repeat.

## Structure

| File | Role |
|---|---|
| [`lib/main.dart`](lib/main.dart) | Window setup, tray wiring, close-to-tray |
| [`lib/daemon.dart`](lib/daemon.dart) | The app's whole state; one method per user action |
| [`lib/daemon_launcher.dart`](lib/daemon_launcher.dart) | Finds, starts and stops the daemon — the reason this is one app |
| [`lib/ipc/`](lib/ipc/) | Endpoint file, transport, newline-delimited JSON client |
| [`lib/models/state.dart`](lib/models/state.dart) | The snapshot shape — a contract with `daemon/internal/core/state.go` |
| [`lib/pages/projects_page.dart`](lib/pages/projects_page.dart) | The one primary view |
| [`lib/pages/project_sheet.dart`](lib/pages/project_sheet.dart) | Per-project overrides, behind a tap |
| [`lib/pages/settings_page.dart`](lib/pages/settings_page.dart) | Webserver and the PHP version picker |
| [`lib/tray.dart`](lib/tray.dart) | The tray menu |

## Design constraints it is holding to

From `dev/design-principles.md`:

- **One primary view.** Name, status, URL. Nothing else by default.
- **Progressive disclosure.** Per-project settings live behind a tap on the
  project or its settings button, never on the list itself.
- **No dashboard-itis.** No charts, no telemetry, no onboarding.
- **Tray-first.** Start, stop, restart and open are in the tray; the window is
  optional. Both offer the same actions, from `Project.actions`.
- **Kirby-plain, and accessible.** Black on white or white on near-black,
  colour only for status, WCAG AA contrast enforced by `test/theme_test.dart`
  (`dev/design-principles.md` §4).
- **Settings shows only Appearance, Webserver, PHP and SSL.** Database and mail are `@roadmap`
  and are not rendered at all — a widget test asserts their absence.

## Platform notes

- **The macOS App Sandbox is off** (`macos/Runner/*.entitlements`). Wharf reads
  the daemon's endpoint file, connects to a unix socket under the user's root
  folder, opens a folder picker on any folder, and opens folders in the file
  manager. A sandboxed container reaches
  none of that. This is a developer tool distributed outside the App Store:
  signed with Developer ID and notarized, so it opens without a warning
  ([`packaging/macos/build_dmg.sh`](../packaging/macos/build_dmg.sh)).
- **Linux tray** availability differs: KDE and Xfce work natively, GNOME needs
  an AppIndicator extension. Documented in `dev/architecture.md` §4, not
  solved.
- **Windows** uses the TCP transport and the `.ico` tray icon.
- **Icons** are drawn by [`tool/draw_icons.py`](tool/draw_icons.py): a
  template image for the macOS menu bar, the same mark on a dark tile for
  Windows and Linux bars, and the app icon.

## Not done yet

- No MSI or AppImage yet — `dev/architecture.md` §4 leaves the outer package
  format per-OS. macOS has its disk image: see `dev/releasing.md`.
