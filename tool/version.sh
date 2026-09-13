#!/usr/bin/env bash
# Wharf's version, from its one source: the `version:` line of gui/pubspec.yaml
# (X.Y.Z+B). Flutter stamps the app bundle with it; the Makefile, the Windows
# CMake build and the release workflow stamp wharfd by asking this script. The
# same script runs locally and in CI, so a hand-cut release and a button-cut
# one cannot differ. See dev/releasing.md.
#
#   current                 1.2.3+4      the pubspec line
#   semver                  1.2.3
#   describe                1.2.3        on its release tag, clean
#                           1.2.3+3b2c1ff[.dirty]  anywhere else
#   next    <kind>          the version a bump would write (kind: patch,
#                           minor, major, or an exact X.Y.Z newer than now)
#   bump    <kind>          write it into the pubspec
#   release <kind>          bump, commit "🔖 Version …", tag vX.Y.Z; no push
#   check-tag <tag>         fail unless the tag is v<semver>
#   changelog <tag>         the CHANGELOG section for a tag
#   prepend-changelog <tag> write that section into CHANGELOG.md
#
# PUBSPEC and CHANGELOG override the file paths, for tests.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
pubspec="${PUBSPEC:-$root/gui/pubspec.yaml}"
changelog="${CHANGELOG:-$root/CHANGELOG.md}"

semver_re='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
full_re='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\+[0-9]+$'

die() {
	echo "version.sh: $*" >&2
	exit 1
}

git_() { git -C "$root" "$@"; }

current() {
	local v
	v="$(sed -n 's/^version:[[:space:]]*//p' "$pubspec" | tr -d '\r' | head -n 1)"
	[[ "$v" =~ $full_re ]] || die "$pubspec: version \"$v\" is not X.Y.Z+B"
	echo "$v"
}

semver() {
	local v
	v="$(current)"
	echo "${v%+*}"
}

build_number() {
	local v
	v="$(current)"
	echo "${v#*+}"
}

# The build number keeps counting across every bump: macOS refuses to treat a
# bundle as newer when CFBundleVersion went down.
next() {
	local want="${1:-}" cur major minor patch new
	[ -n "$want" ] || die "say how to bump: patch, minor, major or an exact X.Y.Z"
	cur="$(semver)"
	IFS=. read -r major minor patch <<<"$cur"
	case "$want" in
	major) new="$((major + 1)).0.0" ;;
	minor) new="$major.$((minor + 1)).0" ;;
	patch) new="$major.$minor.$((patch + 1))" ;;
	*)
		[[ "$want" =~ $semver_re ]] || die "\"$want\" is neither patch, minor, major nor a version X.Y.Z"
		newer "$want" "$cur" || die "$want is not newer than the current $cur"
		new="$want"
		;;
	esac
	echo "$new+$(($(build_number) + 1))"
}

# newer A B: is A a higher version than B?
newer() {
	local a b i
	IFS=. read -r -a a <<<"$1"
	IFS=. read -r -a b <<<"$2"
	for i in 0 1 2; do
		if ((a[i] > b[i])); then return 0; fi
		if ((a[i] < b[i])); then return 1; fi
	done
	return 1
}

write() {
	local v="$1" tmp
	[[ "$v" =~ $full_re ]] || die "refusing to write \"$v\": not X.Y.Z+B"
	tmp="$(mktemp)"
	sed "s/^version:.*/version: $v/" "$pubspec" >"$tmp"
	# cat, not mv: keeps the file's own permissions and inode.
	cat "$tmp" >"$pubspec"
	rm -f "$tmp"
}

bump() {
	local new
	new="$(next "${1:-}")"
	write "$new"
	echo "$new"
}

describe() {
	local v sha dirty=""
	v="$(semver)"
	if ! git_ rev-parse --git-dir >/dev/null 2>&1; then
		echo "$v"
		return
	fi
	if [ -n "$(git_ status --porcelain --untracked-files=no)" ]; then
		dirty=".dirty"
	fi
	if [ -z "$dirty" ] && git_ describe --exact-match --tags --match "v$v" HEAD >/dev/null 2>&1; then
		echo "$v"
		return
	fi
	sha="$(git_ rev-parse --short HEAD)"
	echo "$v+$sha$dirty"
}

release() {
	local new tag
	if [ -n "$(git_ status --porcelain --untracked-files=no)" ]; then
		die "the working tree has uncommitted changes — a release commit holds the version bump alone"
	fi
	new="$(next "${1:-}")"
	tag="v${new%+*}"
	if git_ rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
		die "tag $tag already exists"
	fi
	write "$new"
	git_ add "$pubspec"
	git_ commit -q -m "🔖 Version $new"
	git_ tag -a "$tag" -m "Wharf ${new%+*}"
	echo "$tag"
}

check_tag() {
	local tag="${1:-}" want
	want="v$(semver)"
	[ "$tag" = "$want" ] || die "tag \"$tag\" does not match gui/pubspec.yaml ($want). Tag the commit that carries the version, or cut the release with \`make release\`."
}

# Dated by the tagged commit, not by today: a section written late or re-run
# must still name the day the release was made.
section() {
	local tag="${1:-}" prev range
	git_ rev-parse -q --verify "refs/tags/$tag" >/dev/null || die "no tag \"$tag\""
	prev="$(git_ describe --tags --abbrev=0 --match 'v[0-9]*' "$tag^" 2>/dev/null || true)"
	if [ -n "$prev" ]; then range="$prev..$tag"; else range="$tag"; fi
	echo "## $tag - $(git_ log -1 --format=%cs "$tag")"
	echo
	# The release bookkeeping itself is not a change anyone needs to read about.
	git_ log "$range" --no-merges --format='- %s (%h)' | grep -v -E '^- (🔖 Version |📝 Changelog )' || true
	echo
}

prepend_changelog() {
	local tag="${1:-}" sec tmp n
	[ -n "$tag" ] || die "usage: version.sh prepend-changelog <tag>"
	if [ -f "$changelog" ] && grep -q "^## $tag " "$changelog"; then
		echo "version.sh: $changelog already has $tag" >&2
		return 0
	fi
	sec="$(mktemp)"
	tmp="$(mktemp)"
	section "$tag" >"$sec"
	[ -f "$changelog" ] || printf '# Changelog\n\n' >"$changelog"
	n="$(grep -n -m 1 '^## ' "$changelog" | cut -d: -f1 || true)"
	if [ -z "$n" ]; then
		cat "$changelog" "$sec" >"$tmp"
	else
		{
			head -n $((n - 1)) "$changelog"
			cat "$sec"
			tail -n +"$n" "$changelog"
		} >"$tmp"
	fi
	cat "$tmp" >"$changelog"
	rm -f "$sec" "$tmp"
}

cmd="${1:-}"
[ $# -gt 0 ] && shift
case "$cmd" in
current) current ;;
semver) semver ;;
describe) describe ;;
next) next "$@" ;;
bump) bump "$@" ;;
release) release "$@" ;;
check-tag) check_tag "$@" ;;
changelog) section "$@" ;;
prepend-changelog) prepend_changelog "$@" ;;
*) die "usage: version.sh current|semver|describe|next|bump|release|check-tag|changelog|prepend-changelog" ;;
esac
