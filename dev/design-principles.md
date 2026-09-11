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
  status (running/stopped), URL. Nothing else visible by default.
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

## 4. Visual language (deferred)

Concrete typography/colour decisions are **not** part of this concept
package — visual design starts once the behaviour in `features/*.feature`
is agreed. The only fixed constraint carried forward: minimal chrome, high
whitespace-to-content ratio, no visual clutter competing with the
file-tree-like project list — consistent with Kirby's own text-forward,
low-ornamentation site design.
