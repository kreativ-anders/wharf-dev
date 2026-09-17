# Glossary

The words Wharf uses for its own things. Use them in UI text, specs, docs,
code comments and identifiers alike. Two words for one thing read like two
things, and one word for two things hides a difference that matters.

A new concept gets a row here in the same commit that introduces it.

| Term | Means | Where it is defined |
|---|---|---|
| **Wharf folder** (root) | The portable folder holding `bin/`, `www/`, `config/`, `data/` — `~/Wharf` unless `WHARF_ROOT` names another | `dev/architecture.md` §5, `internal/layout` |
| **Project** | A folder Wharf serves at `http://<name>.localhost`. One in `www/` is found by name; one anywhere else is *added* and stays where it is, recorded as `path` | `features/project-folders.feature` |
| **Front door** | The active webserver on ports 80 and 443, serving every started project and forwarding those pinned to the other webserver | `dev/architecture.md` §4c |
| **Active webserver** | The one that runs the front door — `services.webserver.active` | `features/service-management.feature` |
| **Override** | A per-project setting that differs from the global one: PHP version, webserver, SSL. A project with a webserver override is *pinned* to that webserver | `features/app-configuration.feature` |
| **Adopt** | Use a PHP or webserver already on the machine, where it is, instead of installing one — recorded in `services.php.paths` | `features/php-runtime.feature`, `features/webserver-install.feature` |
| **Install** | Download or fetch a binary into `bin/`, only when the user asks | `dev/architecture.md` §4b |
| **Hide** / **Remove** (a PHP) | *Hide*: detection passes over a PHP found on the machine (`services.php.hidden`). *Remove*: delete a PHP Wharf downloaded. Wharf never deletes what it did not install | `features/php-runtime.feature` |
| **Use in terminal** | Put `bin/path`, whose `php` runs the global default version, first on the user's PATH — `services.php.terminal` | `features/php-terminal.feature` |
| **Recommended** (PHP) | The newest version in active support — what a first run selects | `gui/lib/models/state.dart` |
| **Stop all** | Stop every running project; Wharf stays open | `features/tray-actions.feature` |
| **Cast off** / **Quit** | Stop everything and quit — *Cast off* in the window, *Quit* in the tray. The only action about Wharf itself rather than a project | `features/single-application.feature` |
| **`@v1`** / **`@roadmap`** | Scenario tags: in this version / specified and deliberately not built | `CLAUDE.md` §1 |
| **Claim** | The comment above a test naming the scenario it proves | `CLAUDE.md` §1 |
| **Snapshot** | The full state the daemon sends after every change; the window and the tray render the same one | `daemon/internal/core/state.go` |
