#!/usr/bin/env bash
# Built Linux bundle (wharf_gui + data/ + lib/ + wharfd) → one executable
# AppImage. A port of finanzgecko's packaging/linux/build_appimage.sh; see
# dev/releasing.md §5.
#
#   OUT_FILE=Wharf-1.0.0-linux-x64.AppImage ./packaging/linux/build_appimage.sh [bundle]
#
# bundle defaults to gui/build/linux/x64/release/bundle. Needs appimagetool on
# PATH, or downloads a pinned one.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
bundle="${1:-$root/gui/build/linux/x64/release/bundle}"
out="${OUT_FILE:-$root/Wharf-linux-x64.AppImage}"
app_id="dev.wharf.wharf_gui"

[ -x "$bundle/wharf_gui" ] || {
	echo "build_appimage.sh: $bundle/wharf_gui not found — run 'flutter build linux --release' in gui/ first." >&2
	exit 1
}
# WARNING: The app looks for wharfd next to its own executable (gui/lib/daemon_launcher.dart).
# An AppImage without it starts and then reports a missing daemon on every machine.
[ -x "$bundle/wharfd" ] || {
	echo "build_appimage.sh: $bundle/wharfd not found — run 'make build' and copy build/wharfd into the bundle." >&2
	exit 1
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
appdir="$work/Wharf.AppDir"

# INFO: The bundle stays together under usr/bin: Flutter finds data/ and lib/ relative
# to the executable.
mkdir -p "$appdir/usr/bin"
cp -r "$bundle/." "$appdir/usr/bin/"

cat >"$appdir/AppRun" <<'EOF'
#!/usr/bin/env bash
here="$(dirname "$(readlink -f "$0")")"
exec "$here/usr/bin/wharf_gui" "$@"
EOF
chmod +x "$appdir/AppRun"

# INFO: The entry the repository ships for the app id, so a Wayland taskbar
# matches the window to it (gui/linux/dev.wharf.wharf_gui.desktop).
cp "$root/gui/linux/$app_id.desktop" "$appdir/$app_id.desktop"
cp "$root/gui/macos/Runner/Assets.xcassets/AppIcon.appiconset/app_icon_512.png" "$appdir/$app_id.png"

# WARNING: Pinned and checksum-verified, not the `continuous` tag: `continuous` is
# rebuilt at any time, so a tagged build here could change without a commit.
# finanzgecko saw `continuous` and 1.9.1 with the same size and different sums.
# An appimagetool already on PATH is the local developer's and is not pinned;
# CI has none and always takes the download.
appimagetool_version="1.9.1"
appimagetool_sha256="ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0"
if command -v appimagetool >/dev/null 2>&1; then
	appimagetool="appimagetool"
else
	appimagetool="$work/appimagetool"
	curl -fsSL -o "$appimagetool" \
		"https://github.com/AppImage/appimagetool/releases/download/$appimagetool_version/appimagetool-x86_64.AppImage"
	echo "$appimagetool_sha256  $appimagetool" | sha256sum -c -
	chmod +x "$appimagetool"
fi

# INFO: Extract-and-run sidesteps FUSE, which CI runners often lack.
export APPIMAGE_EXTRACT_AND_RUN=1
export ARCH=x86_64
"$appimagetool" "$appdir" "$out"

echo "$out"
