#!/usr/bin/env bash
# Packs Wharf.app into a disk image and signs and notarizes it, as far as the
# credentials allow. One script for local and CI, so the build tested by hand is
# exactly the one a release ships. Ported from finanzgecko's
# packaging/macos/build_dmg.sh, where every workaround below was paid for once.
#
# Usage:
#   cd gui && flutter build macos --release && cd ..
#   NOTARY_PROFILE=wharf ./packaging/macos/build_dmg.sh          # uses gui/build/...
#   ./packaging/macos/build_dmg.sh /path/to/Wharf.app            # or a finished bundle
#
# Environment (all optional):
#   OUT_FILE             target file (default build/Wharf-<version>-mac.dmg,
#                        version from gui/pubspec.yaml via tool/version.sh)
#   SIGN_IDENTITY        default "Developer ID Application" — codesign resolves
#                        a substring as long as exactly one identity matches, so
#                        no name and no team ID live in the repo.
#   ENTITLEMENTS         default gui/macos/Runner/Release.entitlements
#   NOTARY_PROFILE       a profile stored with `xcrun notarytool store-credentials`
#                        (the local way). Any profile of the same team works.
#   APPLE_API_KEY_PATH   .p8 file   ┐ the CI way (App Store Connect API key);
#   APPLE_API_KEY_ID     key ID     ├ used only when NOTARY_PROFILE is unset
#   APPLE_API_ISSUER_ID  issuer ID  ┘
#   SKIP_NOTARIZE=1      sign only, do not notarize (a quick test run)
#   REQUIRE_NOTARIZED=1  fail instead of falling back to an unsigned or
#                        unnotarized image — set it for anything published.
#
# Without an identity or notarization credentials the script does NOT fail by
# default: it builds an unsigned image and says so loudly. A fork or an ad-hoc
# test build has no secrets, and a hard failure there would block the whole
# release chain. Such an image makes Gatekeeper refuse to open the app on any
# other Mac ("cannot be opened because the developer cannot be verified") —
# which is what REQUIRE_NOTARIZED=1 is for.
#
# The App Store is out of reach, and deliberately so: Wharf runs with the App
# Sandbox off (gui/README.md, "Platform notes"). Developer ID + notarization is
# the channel that remains, and it opens without a warning.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

APP="${1:-$REPO_ROOT/gui/build/macos/Build/Products/Release/Wharf.app}"
VERSION="$("$REPO_ROOT/tool/version.sh" semver)"
OUT_FILE="${OUT_FILE:-$REPO_ROOT/build/Wharf-$VERSION-mac.dmg}"
SIGN_IDENTITY="${SIGN_IDENTITY:-Developer ID Application}"
ENTITLEMENTS="${ENTITLEMENTS:-$REPO_ROOT/gui/macos/Runner/Release.entitlements}"
REQUIRE_NOTARIZED="${REQUIRE_NOTARIZED:-0}"

fail() {
  echo "Error: $*" >&2
  exit 1
}

# INFO: A missing credential is a warning for a test build and an error for a
# release; one function so the two cannot drift apart.
missing() {
  if [ "$REQUIRE_NOTARIZED" = 1 ]; then
    fail "$1 REQUIRE_NOTARIZED=1 refuses to build an image other Macs would reject."
  fi
  echo "WARNING: $1" >&2
  echo "         Other Macs will refuse to open this image's app." >&2
}

if [ ! -d "$APP" ]; then
  fail "$APP not found. Run 'cd gui && flutter build macos --release' first, or pass the path to a finished .app bundle."
fi
if [ "$REQUIRE_NOTARIZED" = 1 ] && [ "${SKIP_NOTARIZE:-0}" = 1 ]; then
  fail "REQUIRE_NOTARIZED=1 and SKIP_NOTARIZE=1 contradict each other."
fi

# ------------------------------------------------------ Work on a copy --
#
# WARNING: Do not optimise away: signing the bundle in gui/build/ in place breaks the
# NEXT `flutter build macos`. Since Sonoma, macOS's app-management protection
# forbids other processes from modifying a *signed* bundle, so Flutter can no
# longer copy its plugins in — a wall of "You don't have permission to save the
# file … in the folder Resources" and "xattr: Operation not permitted" that
# points at the repository's permissions when the cause is this signature.
WORK="$(mktemp -d)"
STAGING=""
cleanup() { rm -rf "$WORK" ${STAGING:+"$STAGING"}; }
trap cleanup EXIT

ditto "$APP" "$WORK/Wharf.app"
APP="$WORK/Wharf.app"

MAIN_EXE="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$APP/Contents/Info.plist")"

# ------------------------------------------- One architecture set --
#
# WARNING: `flutter build macos --release` builds a universal app; the "Embed wharfd"
# build phase builds the daemon for the build machine only unless told
# otherwise. An arm64-only wharfd inside a universal app launches on an Intel
# Mac and then fails to start its daemon — an app that looks broken for a reason
# no user could find. Refuse to package that.
[ -x "$APP/Contents/MacOS/wharfd" ] ||
  fail "$APP has no Contents/MacOS/wharfd. The \"Embed wharfd\" build phase puts it there; is Go installed?"
app_archs="$(lipo -archs "$APP/Contents/MacOS/$MAIN_EXE" | tr ' ' '\n' | sort | xargs)"
daemon_archs="$(lipo -archs "$APP/Contents/MacOS/wharfd" | tr ' ' '\n' | sort | xargs)"
if [ "$app_archs" != "$daemon_archs" ]; then
  fail "the app is built for [$app_archs] but its wharfd for [$daemon_archs]. Build the daemon universal (DARWIN_UNIVERSAL=1, see the Makefile) and rebuild the app."
fi

# ---------------------------------------------------------- Signing --

CAN_SIGN=0
if security find-identity -v -p codesigning | grep -q "$SIGN_IDENTITY"; then
  CAN_SIGN=1
else
  missing "no identity '$SIGN_IDENTITY' in the keychain — the image stays UNSIGNED."
fi

# WARNING: Retries with a growing pause. Not decoration: --timestamp is one network call
# per signature to Apple's timestamp service, which now and then does not
# answer. codesign reports that as a meaningless "errSecInternalComponent" and
# gives up; the identical call succeeds seconds later.
retry() {
  local attempt=1 max=5
  until "$@"; do
    if [ "$attempt" -ge "$max" ]; then
      echo "Error: '$1' failed after $max attempts." >&2
      return 1
    fi
    echo "Attempt $attempt failed, retrying in $((attempt * 5))s ..." >&2
    sleep $((attempt * 5))
    attempt=$((attempt + 1))
  done
}

sign_one() {
  # INFO: --options runtime: the Hardened Runtime, required for notarization.
  # --timestamp: Apple's signed timestamp, so the signature outlives the
  # certificate's expiry.
  retry codesign --force --options runtime --timestamp --sign "$SIGN_IDENTITY" "$@"
}

if [ "$CAN_SIGN" = 1 ]; then
  # WARNING: Extended attributes (com.apple.quarantine, Finder metadata) make codesign
  # fail with "resource fork, Finder information, or similar detritus".
  xattr -cr "$APP"

  # WARNING: Inside out: every embedded library, then every framework as a whole, then
  # every helper executable, the bundle last. Deliberately not --deep — Apple
  # does not intend it for distribution, and it would hand the app's
  # entitlements to everything inside.
  if [ -d "$APP/Contents/Frameworks" ]; then
    while IFS= read -r -d '' lib; do
      sign_one "$lib"
    done < <(find "$APP/Contents/Frameworks" -type f -name "*.dylib" -print0)

    while IFS= read -r -d '' fw; do
      sign_one "$fw"
    done < <(find "$APP/Contents/Frameworks" -mindepth 1 -maxdepth 1 -print0)
  fi

  # WARNING: wharfd, and anything else beside the main executable. Notarization rejects
  # the whole app if one Mach-O inside is signed only ad hoc — which is what the
  # Go linker gives wharfd. No entitlements: the daemon needs none, and it
  # starts PHP and the webservers as processes of their own, which the Hardened
  # Runtime does not reach.
  while IFS= read -r -d '' exe; do
    [ "$(basename "$exe")" = "$MAIN_EXE" ] && continue
    sign_one "$exe"
  done < <(find "$APP/Contents/MacOS" -type f -perm -u+x -print0)

  # INFO: Only the outer bundle gets the entitlements (the sandbox stays off).
  sign_one --entitlements "$ENTITLEMENTS" "$APP"

  codesign --verify --strict --deep --verbose=2 "$APP"
  echo "App signed: $APP"
fi

# ---------------------------------------------------- Notarization --

NOTARY_ARGS=()
if [ -n "${NOTARY_PROFILE:-}" ]; then
  NOTARY_ARGS=(--keychain-profile "$NOTARY_PROFILE")
elif [ -n "${APPLE_API_KEY_PATH:-}" ] && [ -n "${APPLE_API_KEY_ID:-}" ] && [ -n "${APPLE_API_ISSUER_ID:-}" ]; then
  NOTARY_ARGS=(--key "$APPLE_API_KEY_PATH" --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_ISSUER_ID")
fi

CAN_NOTARIZE=0
if [ "$CAN_SIGN" = 1 ] && [ "${SKIP_NOTARIZE:-0}" != 1 ]; then
  if [ ${#NOTARY_ARGS[@]} -gt 0 ]; then
    CAN_NOTARIZE=1
  else
    missing "no notarization credentials (NOTARY_PROFILE or APPLE_API_KEY_*) — signed, but NOT notarized."
  fi
fi

notarize() {
  # INFO: --wait blocks until Apple is done (usually 1–5 minutes). Without it the
  # submission ID would have to be polled, and a rejection would no longer sit
  # next to the build that caused it.
  if ! xcrun notarytool submit "$1" "${NOTARY_ARGS[@]}" --wait --output-format json >"$WORK/notary.json"; then
    cat "$WORK/notary.json" >&2
    fail "notarization submission failed."
  fi
  cat "$WORK/notary.json"
  if ! grep -q '"status" *: *"Accepted"' "$WORK/notary.json"; then
    # INFO: The log names the exact file and reason Apple rejected.
    id="$(sed -n 's/.*"id" *: *"\([^"]*\)".*/\1/p' "$WORK/notary.json" | head -n 1)"
    [ -n "$id" ] && xcrun notarytool log "$id" "${NOTARY_ARGS[@]}" >&2 || true
    fail "Apple did not accept $1 (log above)."
  fi
}

# WARNING: Notarized twice on purpose: the ticket is stapled into the app AND into the
# image. Stapling only the image is not enough — once the app is dragged out,
# it carries no ticket of its own and Gatekeeper has to ask Apple online on
# first launch, exactly what the image is meant to spare.
if [ "$CAN_NOTARIZE" = 1 ]; then
  ZIP="$WORK/Wharf.zip"
  # WARNING: ditto, not zip: keeps resource forks and extended attributes.
  ditto -c -k --sequesterRsrc --keepParent "$APP" "$ZIP"
  notarize "$ZIP"
  xcrun stapler staple "$APP"
  rm -f "$ZIP"
  spctl -a -t exec -vv "$APP"
  echo "App notarized and stapled."
fi

# ------------------------------------------------------- Disk image --

STAGING="$(mktemp -d)" # removed by cleanup() above

ditto "$APP" "$STAGING/Wharf.app"
ln -s /Applications "$STAGING/Applications"

mkdir -p "$(dirname "$OUT_FILE")"
rm -f "$OUT_FILE"
hdiutil create -volname Wharf -srcfolder "$STAGING" -ov -format UDZO "$OUT_FILE"

if [ "$CAN_SIGN" = 1 ]; then
  # INFO: No --options runtime: the Hardened Runtime belongs to a running program,
  # and the image is only its container.
  retry codesign --force --timestamp --sign "$SIGN_IDENTITY" "$OUT_FILE"
fi

if [ "$CAN_NOTARIZE" = 1 ]; then
  notarize "$OUT_FILE"
  xcrun stapler staple "$OUT_FILE"

  # INFO: The check that counts: "source=Notarized Developer ID" means Gatekeeper
  # opens the image on any Mac without asking.
  spctl -a -t open --context context:primary-signature -vv "$OUT_FILE"
fi

if [ "$CAN_NOTARIZE" = 1 ]; then
  echo "Disk image built, signed and notarized: $OUT_FILE"
elif [ "$CAN_SIGN" = 1 ]; then
  echo "Disk image built and signed, NOT notarized — for this Mac only: $OUT_FILE"
else
  echo "Disk image built, UNSIGNED — for this Mac only: $OUT_FILE"
fi
