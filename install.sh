#!/bin/sh
# Installs the latest agent-squiggles release.
#
#   curl -fsSL https://raw.githubusercontent.com/wpkc0429/agent-squiggles/main/install.sh | sh
#
# Environment:
#   AGENT_SQUIGGLES_VERSION  release tag to install (default: latest)
#   BIN_DIR                  install directory (default: ~/.local/bin)
set -eu

repo="wpkc0429/agent-squiggles"
bin_dir="${BIN_DIR:-$HOME/.local/bin}"

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "agent-squiggles supports Linux and macOS (use WSL on Windows)." >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

tag="${AGENT_SQUIGGLES_VERSION:-}"
if [ -z "$tag" ]; then
  tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest" | sed 's|.*/tag/||')
fi
case "$tag" in
  v*) ;;
  *) echo "could not determine the latest release (got '$tag')" >&2; exit 1 ;;
esac
version="${tag#v}"
archive="agent-squiggles_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$repo/releases/download/$tag"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "Downloading agent-squiggles $tag ($os/$arch)…"
curl -fsSL "$base/$archive" -o "$tmp/$archive"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"

expected=$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1)
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$archive" | cut -d' ' -f1)
else
  actual=$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)
fi
if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
  echo "checksum mismatch for $archive" >&2
  exit 1
fi

tar -xzf "$tmp/$archive" -C "$tmp" agent-squiggles
mkdir -p "$bin_dir"
install -m 0755 "$tmp/agent-squiggles" "$bin_dir/agent-squiggles"
echo "Installed $bin_dir/agent-squiggles"

case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *) echo "Note: $bin_dir is not on your PATH." ;;
esac
echo "Next: run 'agent-squiggles install' to add the Codex hooks."
