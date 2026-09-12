# Design Principles — Kirby-inspired minimalism

Reference: [getkirby.com](https://getkirby.com). Kirby's own pitch is "just
files and folders" — content lives in plain text files, folders are pages,
no database, no admin complexity beyond what's needed. This tool borrows
the same philosophy for a different domain (local dev environments instead
of content).

## 1. Why Kirby as the reference, not a generic "clean UI"

- Kirby is PHP-only by design — no framework-juggling, no polyglot surface.
  This tool mirrors that: PHP is the only fully specified runtime in v1
  (see `architecture.md` §2).
- Kirby's data model is a flat file tree, not a database. This tool's
  config is a single flat file (`config/wharf.json`), not a database or
  multiple scattered config formats.
- Kirby's folder = page maps directly onto this tool's folder = project.
  Adding a project is "put a folder in `www/`," or "pick a folder" for one
  that already lives elsewhere — never a wizard with steps.

## 2. Concrete constraints this places on the GUI

- **One primary view.** A list of projects (folders), each showing: name,
  status (running/stopped), URL — and, while a project runs, the webserver
  and PHP versions serving it. Nothing else visible by default.
- **Progressive disclosure.** Per-project overrides (PHP version, webserver
  override, SSL) exist but are not shown until the user opens that project's
  detail — never a wall of settings up front.
- **No dashboard-itis.** No charts, no telemetry widgets, no onboarding
  carousel. Kirby's own marketing surface is text- and file-tree-first, not
  graph-first — same restraint applies here.
- **Tray-first interaction.** Start/stop/open actions belong in the tray
  menu (see `features/tray-actions.feature`), not buried in a full window
  that must be opened for a two-second action.
- **Config is human-readable and hand-editable.** A user comfortable with
  text files should be able to edit `wharf.json` directly instead of using
  the GUI at all — same spirit as Kirby's content `.txt` files being
  editable outside the Panel.

## 3. What this explicitly rules out for v1

- No plugin marketplace UI (Kirby has one; out of scope until the core tool
  is proven).
- No theming system.
- No multi-window layouts — one window, one tray menu.

## 4. Visual language

Kirby-plain, taken from getkirby.com's own palette (`gui/lib/theme.dart`):

- **Black on white, white on near-black.** Pure greys for structure
  (borders, secondary text); primary buttons are ink-filled, not a brand
  colour. There is no brand colour.
- **Colour only where it means something**: a project's status, a PHP
  version's support status, and what a project action does — Start green,
  Stop red (as is "Stop all"), Restart blue, each on a light tint of its own
  colour. Kirby's hues (green 80°, orange 28°, red 0°, blue 210°), with
  lightness picked for contrast rather than taste. *Changed from* status
  only: grey action icons all looked alike, and the stop-everything control
  read as plain text.
- **Fixed places for actions.** A project row's actions always stand in one
  order — Open, Restart, Settings, Start or Stop — and one a project does not
  offer leaves its place empty, so the columns line up down the list.
- **Settings are pages, not one long scroll**: a navigation on the left
  (General, Webserver, PHP, SSL), one page at a time. A future service is
  one more page there, not a longer list.
- **The platform's system font**, as getkirby.com uses: San Francisco,
  Segoe UI, the Linux desktop's default.
- **Light, dark or system**, chosen in Settings and stored in `wharf.json`
  like every other setting.

Accessibility is part of the palette, not an afterthought
(`gui/test/theme_test.dart` enforces it):

- Text reaches WCAG AA, 4.5:1; status marks and control outlines 3:1 — in
  both themes, on both the page and surface colours.
- Colour never carries meaning alone: status is also a shape (filled dot,
  ring, half ring, square) and a word for screen readers.
- Every icon-only control has a label naming its target ("Start
  my-kirby-site", not "Start"); a project row reads as one item; notices
  are announced; the keyboard reaches everything (⌘, / ⌘O / ⌘N).
- Minimal chrome, high whitespace-to-content ratio, nothing competing with
  the file-tree-like project list.
