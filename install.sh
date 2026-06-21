#!/bin/sh
# respondi.ng agent installer.
# Downloads the latest responding-agent release binary for this OS/arch and
# installs it to a bin directory on PATH.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/aldrys-labs/responding-agent/main/install.sh | sh
#
# Environment overrides:
#   RESPONDING_VERSION   release tag to install (default: latest)
#   RESPONDING_INSTALL_DIR  install directory (default: /usr/local/bin, or
#                           $HOME/.local/bin when not writable)
set -eu

REPO="aldrys-labs/responding-agent"
VERSION="${RESPONDING_VERSION:-latest}"

die() { echo "error: $*" >&2; exit 1; }

# Detect OS.
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux|darwin) ;;
  *) die "unsupported OS: $os (Windows: download the .exe from the releases page)" ;;
esac

# Detect architecture and map to Go's naming.
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) die "unsupported architecture: $arch" ;;
esac

# Resolve the version tag.
if [ "$VERSION" = "latest" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | head -n1 | cut -d '"' -f4)
  [ -n "$VERSION" ] || die "could not resolve the latest release (none published yet?)"
fi

asset="responding-agent_${os}_${arch}"
url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"

# Pick an install directory.
dir="${RESPONDING_INSTALL_DIR:-/usr/local/bin}"
if [ ! -w "$dir" ] 2>/dev/null; then
  dir="$HOME/.local/bin"
  mkdir -p "$dir"
fi

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
echo "Downloading responding-agent ${VERSION} (${os}/${arch})..."
curl -fSL "$url" -o "$tmp" || die "download failed: $url"
chmod +x "$tmp"
mv "$tmp" "$dir/responding-agent"
trap - EXIT

echo "Installed responding-agent to $dir/responding-agent"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "Note: $dir is not on your PATH. Add it to use 'responding-agent' directly." ;;
esac
