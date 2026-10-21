#!/bin/sh
# Installs empirio, which starts coding agents on EmpirioLabs AI models.
#
#   curl -fsSL https://empiriolabs.ai/install.sh | sh
#
# EMPIRIO_VERSION         install this version instead of the latest (for example 0.1.0)
# EMPIRIO_INSTALL_DIR     install here instead of ~/.local/bin
# EMPIRIO_NO_MODIFY_PATH  set to leave your shell profile unchanged
set -eu

releases="https://github.com/EmpirioLabs-AI/cli/releases"

say() { printf '%s\n' "$*" >&2; }
fail() { say "error: $*"; exit 1; }

fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl --proto '=https' --tlsv1.2 -fsSL "$1" -o "$2"
	elif command -v wget >/dev/null 2>&1; then
		wget --https-only -q "$1" -O "$2"
	else
		fail "curl or wget is required"
	fi
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d ' ' -f 1
	else
		fail "sha256sum or shasum is required to check the download"
	fi
}

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) fail "this installer supports macOS and Linux; on Windows run: irm https://empiriolabs.ai/install.ps1 | iex" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) fail "unsupported processor $(uname -m); empirio runs on amd64 and arm64" ;;
esac
# An Intel shell on Apple silicon still gets the native build.
if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
	arch=arm64
fi

if [ -n "${EMPIRIO_VERSION:-}" ]; then
	version="${EMPIRIO_VERSION#v}"
	case "$version" in
	*[!0-9A-Za-z.-]* | "") fail "EMPIRIO_VERSION must look like 0.1.0" ;;
	esac
	base="$releases/download/v$version"
else
	base="$releases/latest/download"
fi
asset="empirio_${os}_${arch}.tar.gz"
dir="${EMPIRIO_INSTALL_DIR:-$HOME/.local/bin}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
trap 'exit 1' INT TERM

say "Downloading empirio for $os/$arch..."
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS"
fetch "$base/SHA256SUMS.sig" "$tmp/SHA256SUMS.sig"
# The release key that signs SHA256SUMS. ssh-keygen, which macOS and Linux
# ship, checks the signature; without it only the checksum is checked.
printf '%s\n' 'release@empiriolabs.ai namespaces="empirio-release" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGBE/2Npm/ixxdwNX+ldPJnpzlh8sp/3T8nHD5LtoXKb' >"$tmp/allowed_signers"
if command -v ssh-keygen >/dev/null 2>&1; then
	if out="$(ssh-keygen -Y verify -f "$tmp/allowed_signers" -I release@empiriolabs.ai -n empirio-release -s "$tmp/SHA256SUMS.sig" <"$tmp/SHA256SUMS" 2>&1)"; then
		say "Release signature verified."
	else
		case "$out" in
		*"illegal option"* | *"unknown option"* | *"usage:"*) say "note: this ssh-keygen cannot check signatures, so only the checksum is checked" ;;
		*) fail "the release signature did not verify; nothing was installed" ;;
		esac
	fi
else
	say "note: ssh-keygen is not installed, so only the checksum is checked"
fi
fetch "$base/$asset" "$tmp/$asset"
expected="$(awk -v name="$asset" '{ n = $2; sub(/^\*/, "", n); if (n == name) print $1 }' "$tmp/SHA256SUMS")"
[ -n "$expected" ] || fail "$asset is not listed in SHA256SUMS; nothing was installed"
actual="$(sha256 "$tmp/$asset")"
[ "$(printf '%s' "$expected" | tr 'A-F' 'a-f')" = "$actual" ] || fail "checksum mismatch for $asset; nothing was installed"

tar -xzf "$tmp/$asset" -C "$tmp"
[ -f "$tmp/empirio" ] || fail "the download has no empirio program"
mkdir -p "$dir"
# Replace through a rename, so a running empirio keeps working.
cp "$tmp/empirio" "$dir/.empirio.new"
chmod 755 "$dir/.empirio.new"
mv -f "$dir/.empirio.new" "$dir/empirio"

installed="$("$dir/empirio" --version 2>/dev/null || true)"
say "Installed ${installed:-empirio} to $dir/empirio"

case ":$PATH:" in
*":$dir:"*) ;;
*)
	line="export PATH=\"$dir:\$PATH\""
	if [ -n "${EMPIRIO_NO_MODIFY_PATH:-}" ]; then
		say "Add $dir to your PATH to run empirio: $line"
	else
		case "$(basename "${SHELL:-sh}")" in
		zsh) profile="${ZDOTDIR:-$HOME}/.zshrc" ;;
		bash) if [ "$os" = darwin ]; then profile="$HOME/.bash_profile"; else profile="$HOME/.bashrc"; fi ;;
		fish)
			profile="${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d/empirio.fish"
			line="fish_add_path \"$dir\""
			mkdir -p "$(dirname "$profile")"
			;;
		*) profile="$HOME/.profile" ;;
		esac
		if ! grep -Fqs "$line" "$profile"; then
			printf '\n# Added by the EmpirioLabs AI installer\n%s\n' "$line" >>"$profile"
		fi
		say "Added $dir to your PATH in $profile. Open a new terminal, or run:"
		say "  $line"
	fi
	;;
esac

say ""
say "Run 'empirio' to sign in and start a coding agent."
