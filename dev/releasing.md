# Versions and releases

How Wharf is versioned, how a release is cut, and why the pipeline is shaped
the way it is. The machinery is [`tool/version.sh`](../tool/version.sh) and
[`.github/workflows/release.yml`](../.github/workflows/release.yml); much of
its shape comes from finanzgecko's release pipeline, lessons included.

## 1. One version, one source

`gui/pubspec.yaml` holds it: `version: X.Y.Z+B`.

- **X.Y.Z** is the version people see. Flutter stamps the app bundle with it
  (`CFBundleShortVersionString`, the Windows file version).
- **B** is the build number. It goes up by one on every bump and never goes
  back, because macOS will not treat a bundle as newer when `CFBundleVersion`
  went down.

Nothing else holds a version number. `wharfd` is stamped at build time
(`-ldflags "-X main.version=…"`) with what `tool/version.sh describe` prints:

| Build | Stamped as | Settings shows |
|---|---|---|
| On its release tag `vX.Y.Z`, no local changes | `1.2.0` | Wharf 1.2.0 |
| Any other commit | `1.2.0+3b2c1ff` | Wharf 1.2.0+3b2c1ff |
| With uncommitted changes | `1.2.0+3b2c1ff.dirty` | Wharf 1.2.0+3b2c1ff.dirty |

The suffix is semver *build metadata*, which is ignored when versions are
compared: a development build between 1.2.0 and 1.3.0 is still 1.2.0, and says
which commit it came from. Every build path stamps the daemon: the Makefile,
the Xcode "Embed wharfd" phase (through `make build`), and the Windows CMake
target (from `VERSION`, else pubspec's X.Y.Z). `internal/docsync` fails if a
new way of building wharfd forgets to.

## 2. Cutting a release

Releases are cut from `main`, with the tree clean and `make check` green.
There are two equivalent ways to do it.

**A — by hand**

```sh
make release BUMP=minor           # or patch, major, or an exact 2.0.0
git push --atomic origin HEAD:main v1.3.0
```

`make release` edits gui/pubspec.yaml, commits `🔖 Version 1.3.0+9` and
creates the annotated tag `v1.3.0`. It pushes nothing. The tag push starts the
workflow. `make bump BUMP=…` only edits the file; `make version` prints what
this checkout builds as.

**B — by button**

Actions → *Release* → *Run workflow*, with *bump* set to patch, minor or major
(or an exact *version*). The workflow runs the same `tool/version.sh release`,
pushes the commit and the tag, then builds and releases in the same run.
It has to be the same run: a push made with `GITHUB_TOKEN` never starts
another workflow.

*Run workflow* with *bump* set to `none` is a **test build**. All three
platforms are built and attached to the run as artifacts, and nothing is
tagged or released.

## 3. What the workflow does

```
gate ──► bump (button only) ──► build: macOS · Linux · Windows ──► release
```

| Job | Does | Why it is shaped like this |
|---|---|---|
| `gate` | On a tag push, checks the tag equals `v` + pubspec's version; then `gofmt`, `make check cross` | Nothing is bumped or built on a red tree. A tag that disagrees with the pubspec would ship files named after one version and an app that reports another |
| `bump` | `tool/version.sh release`, then `git push --atomic` | Atomic, so a tag never lands without its version commit. The version input goes through `env`, never pasted into the script |
| `build` | One leg per OS: `flutter build` (which embeds a stamped wharfd), checks that the embedded daemon reports the right version and, on macOS, is universal; then packages it | A platform is only built on its own OS. The check exists because a wrong daemon inside a right app is invisible until someone runs it |
| `release` | Prepends the section to `CHANGELOG.md` and pushes it to `main`; writes `SHA256SUMS`; publishes the GitHub release with the changelog section and the checksums as its notes | One job after all builds, so there is never a release missing a platform |

Rules the pipeline keeps, each learned the hard way in finanzgecko:

- **Every job after `bump` starts its `if:` with `!cancelled() &&`.** `bump`
  is skipped on a tag push and on a test build, and GitHub skips every job
  that `needs` a skipped one unless the condition contains a status-check
  function. Without it, only the button path ever builds, and the run still
  shows green.
- **Flutter is pinned** (`FLUTTER_VERSION` in the workflow), and Go is pinned
  by `daemon/go.mod`. A floating toolchain changes what a tag builds without a
  commit. Raise the pin on purpose, after `make check` passes locally.
- **One versioned file per platform**, and no unversioned copy beside it.
  The file-name suffixes (`-mac.dmg`, `-linux-x64.tar.gz`,
  `-windows-x64.zip`) are what the update check will look for
  (`features/settings.feature`, "Checking for updates on request"), so they
  are renamed together or not at all.
- **The VC++ runtime DLLs ship next to the Windows exe.** Flutter's template
  links them dynamically. finanzgecko's winget validation VM, which lacked
  them, failed at start with `STATUS_DLL_NOT_FOUND`.
- **`SHA256SUMS` proves integrity, not authenticity.** It is unsigned, and
  the release notes say only what it proves.
- **The changelog is dated by the tagged commit**, not by the day the job
  ran.

## 4. CHANGELOG.md

Written by the `release` job and never by hand. Each section is every commit
subject since the previous tag, merges and the release bookkeeping itself
(`🔖 Version …`, `📝 Changelog …`) left out. The commit subjects *are* the
release notes, which is why CONTRIBUTING.md asks for gitmoji + an imperative
sentence written for a reader.

## 5. The macOS DMG

`packaging/macos/build_dmg.sh` turns a built `Wharf.app` into
`Wharf-<version>-mac.dmg`. The same script runs locally (`make dmg`) and on the
macOS release leg, so the build someone tested by hand and the one CI ships
cannot differ. It signs inside out (frameworks, then the embedded `wharfd`,
then the bundle, with the sandbox-off entitlements), notarises and staples
both the app and the DMG, and checks that `wharfd` has the same architectures
as the app. Without an identity or credentials — a fork, a test build — it
builds an unsigned DMG instead of failing. A real release from
`kreativ-anders/wharf-dev` sets `REQUIRE_NOTARIZED=1` instead, and fails. An
unsigned DMG on a release page would be an app Gatekeeper refuses on every
other Mac.

Repository secrets for a signed, notarised DMG (without them, the unsigned
fallback applies):

| Secret | What |
|---|---|
| `MACOS_CERT_P12_BASE64` | The *Developer ID Application* certificate and key, `.p12`, base64 |
| `MACOS_CERT_PASSWORD` | Its password |
| `APPLE_API_KEY_P8_BASE64` | App Store Connect API key (`.p8`), base64 — for notarisation |
| `APPLE_API_KEY_ID`, `APPLE_API_ISSUER_ID` | That key's ids |

The certificate goes into a temporary keychain that is deleted at the end of
the job, including after a failure.

## 6. Before the first release

- **Workflow permissions:** Settings → Actions → General → *Read and write*.
  The workflow pushes the version commit, the tag and `CHANGELOG.md` to
  `main`. If `main` is protected, allow `github-actions[bot]` to push to it.
- **The version to start from:** gui/pubspec.yaml says `1.0.0+1`. For a
  first release below 1.0, set it by hand once (`version: 0.1.0+1`).
  `tool/version.sh` only ever moves forward.
- **Linux and Windows ship as a tarball and a zip** for now. An AppImage and
  an installer replace them when packaging exists (README, "Status"); the
  steps before *Package* in the workflow stay as they are.
- The update check stays `@roadmap` until there is a release to check
  against; its rules are already fixed in `features/settings.feature`.
