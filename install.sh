#!/bin/sh
set -e

# Check for gh CLI
if ! command -v gh >/dev/null 2>&1; then
  echo "Error: 'gh' CLI is required. Install it: https://cli.github.com"
  exit 1
fi

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
esac

INSTALL_DIR="$HOME/.local/bin"
mkdir -p "$INSTALL_DIR"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

echo "Downloading latest mirage for ${OS}/${ARCH}..."
gh release download --repo thenickygee/mirage --pattern "*${OS}_${ARCH}*" --dir "$TMPDIR"

ARCHIVE=$(find "$TMPDIR" -name "*.tar.gz" -o -name "*.zip" | head -1)
if [ -z "$ARCHIVE" ]; then
  echo "Error: no archive found"
  exit 1
fi

if echo "$ARCHIVE" | grep -q "\.tar\.gz$"; then
  tar xzf "$ARCHIVE" -C "$TMPDIR"
else
  unzip -o "$ARCHIVE" -d "$TMPDIR"
fi

mv "$TMPDIR/mirage" "$INSTALL_DIR/mirage"
chmod +x "$INSTALL_DIR/mirage"

echo "Installed mirage to $INSTALL_DIR/mirage"

# Check PATH
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "Note: Add $INSTALL_DIR to your PATH if it isn't already." ;;
esac
