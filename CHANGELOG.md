# Changelog

Written by the release workflow, never by hand: each release adds the commit
subjects since the one before it, newest first (dev/releasing.md).

## v1.2.3 - 2026-11-24

- 🐛 Keep wharfctl add's flags for a project named by its www/ folder (#27) (fd6f3ef)
- 🐛 Leave go() and defer() to Kirby with the PHP Wharf downloads (#26) (3bbdf09)

## v1.2.2 - 2026-11-23

- 🐛 Say why a PHP version in use cannot be removed, before the click (#25) (bf2b695)
- 📝 Document which PHP extensions the downloaded build lacks (#24) (6e7974c)
- ➖ Drop the unused Cupertino Icons package (#22) (654548f)
- Bump url_launcher from 6.3.2 to 6.3.3 in /gui (#17) (b271dd6)

## v1.2.1 - 2026-11-23

- 🐛 Trust the certificate authority on macOS without an error (#20) (6ea17bc)
- ⬆️ Bump actions/setup-go from 6.5.0 to 7.1.0 (#16) (fc32001)
- ⬆️ Bump actions/upload-artifact from 7.0.1 to 7.0.2 (#15) (f82d0dc)
- ⬆️ Bump actions/download-artifact from 8.0.1 to 8.0.2 (#14) (4da6555)
- ⬆️ Bump softprops/action-gh-release from 3.0.3 to 3.0.4 (#13) (aead4a5)

## v1.1.1 - 2026-10-03


## v1.1.0 - 2026-09-28

- 👷 Add manual Wharf update checks (5eea763)
- 🧑‍💻 Show project QR before SSL cert steps (8b92958)
- ✨ Simplify network sharing dialog (75e495d)
- ✨ Add LAN project sharing with network CA (f29be25)
- ⚡️Render nginx front door as one block (2c6525c)
- 🧑‍💻 Load common extensions for adopted PHP (85b93cb)
- Add early-development notice on landing page (593b6d0)
- 🔍️ Polish docs metadata and demo markup (948bc33)
- ✈️ Launch custom domain and support page (28c1acf)
- ⚡️ Harden daemon installs and shutdown flow (2b73319)
- Create CNAME (0b7f702)
- 📝 Point the landing page at the v1 downloads (4adefff)

## v1.0.0 - 2026-09-21

- 📌 Update Flutter lockfile deps (014407c)
- 📦 Ship an AppImage and a Windows installer (c87d8c5)
- 🐛 fix PHP version handling and daemon shutdown process (0123494)
- 🐛 fix PHP detection (f796407)
- 🐛 fix Linux elevator privileges (8f219dc)
- 🐛 fix linux build (8855b5b)
- 🔧 Clean Flutter's build files when the checkout moves to another OS (71eedd2)
- 🐛 fix Linux build (e8bf461)
- 💄 Tighten landing page copy (d69e391)
- 💄 Add privacy and imprint pages, and trim the landing page (83a9491)
- 📄 License Wharf under MIT (13f85c5)
- ✨ Add projects from any folder with detected config templates (25053bd)
- ✨ Add config templates and custom editors (8577ea7)
- 🧱 Add PHP terminal PATH management (9731810)
- 🧑‍💻 Refactor daemon core and harden IPC (918e47f)
- 🚀 Add release pipeline and version tooling (8509900)
- ✅ Share e2e daemon binary resolver in GUI tests (3b2c1ff)
- 🧪 Add AXTree tooltip click regression tests (38cebc9)
- ✨ Add PHP removal and cast off flows (af6c9b3)
- 🏗️ Avoid occupied ports for own webserver instances (05ccc1a)
- 🚨 Keep failed tray starts out of next launch (f97a6fe)
- 🧑‍💻 Rewrite project names consistently (92fae04)
- 🦖 Kill full webserver process trees (b9fc109)
- ⚗️ Gracefully stop daemon on Windows (4420df0)
- 🐛 Update module path and build outputs (dc33a1c)
- 🔍️ Refresh landing page and SEO metadata (1138e8a)
- ♻️ Adopt .localhost front door and new settings (0af80b7)
- 🚸 Add project restart actions and icon refresh (d65718f)
- 🚧 Add appearance settings and webserver installs (0c60d9f)
- 🎉 Bootstrap Wharf daemon and desktop app (09ec1f1)
- Initial commit (fc77c80)

