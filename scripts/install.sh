#!/bin/sh
# Install the git-ai-commit binary from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/cjonesy/git-ai-commit/main/scripts/install.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/cjonesy/git-ai-commit/main/scripts/install.sh | bash -s -- 0.1.0
#
# Installs the latest release, or the version given as the first argument, into
# $INSTALL_DIR (default /usr/local/bin), using sudo only if that directory isn't
# writable. The download is checked against the release's checksums.txt.

set -eu

repo=cjonesy/git-ai-commit

fail() {
	echo "error: $*" >&2
	exit 1
}

platform() {
	case $(uname -s) in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) fail "no prebuilt binary for $(uname -s); install with Go instead" ;;
	esac
	case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) fail "no prebuilt binary for $(uname -m); install with Go instead" ;;
	esac
	echo "${os}_${arch}"
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

# Everything runs from main, called on the last line, so a download cut off
# partway through executes nothing.
main() {
	version=${1:-}
	install_dir=${INSTALL_DIR:-/usr/local/bin}
	command -v curl >/dev/null 2>&1 || fail "curl is required"

	if [ -z "$version" ]; then
		base="https://github.com/$repo/releases/latest/download"
	else
		version=${version#v}
		echo "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || fail "'$1' isn't a version like 1.2.3"
		base="https://github.com/$repo/releases/download/v$version" # release tags have a v
	fi
	asset="git-ai-commit_$(platform)"

	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT

	echo "Downloading $asset from ${version:-the latest release}..."
	curl -fsSL -o "$tmp/$asset" "$base/$asset" || fail "couldn't download $base/$asset"
	curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || fail "couldn't download $base/checksums.txt"

	want=$(awk -v f="$asset" '$2 == f { print $1 }' "$tmp/checksums.txt")
	[ -n "$want" ] || fail "checksums.txt has no entry for $asset"
	[ "$(sha256 "$tmp/$asset")" = "$want" ] || fail "checksum mismatch for $asset; not installing"
	chmod 755 "$tmp/$asset"

	sudo=
	if ! mkdir -p "$install_dir" 2>/dev/null || [ ! -w "$install_dir" ]; then
		echo "Installing to $install_dir needs sudo."
		sudo=sudo
		$sudo mkdir -p "$install_dir"
	fi
	$sudo mv "$tmp/$asset" "$install_dir/git-ai-commit"

	echo "Installed $("$install_dir/git-ai-commit" --version) to $install_dir/git-ai-commit"
	case ":$PATH:" in
	*":$install_dir:"*) ;;
	*) echo "Add $install_dir to your PATH so git finds it as \`git ai-commit\`." ;;
	esac
}

main "$@"
