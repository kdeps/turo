#!/bin/sh
# turo — Point more. Token less.
# Single-line installer for macOS and Linux.
# Usage: curl -fsSL https://raw.githubusercontent.com/kdeps/turo/main/install.sh | sh

set -e

REPO="kdeps/turo"
VERSION="${TURO_VERSION:-0.1.0}"
INSTALL_DIR="${TURO_INSTALL_DIR:-$HOME/.turo/bin}"

case "$(uname -s)" in
  Darwin)  OS="darwin" ;;
  Linux)   OS="linux" ;;
  *)       echo "turo: unsupported OS"; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64)  ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)       echo "turo: unsupported architecture"; exit 1 ;;
esac

PLATFORM="${OS}_${ARCH}"
URL="https://github.com/${REPO}/releases/download/v${VERSION}/turo_${VERSION}_${PLATFORM}.tar.gz"

mkdir -p "$INSTALL_DIR"
echo "Installing turo ${VERSION} for ${PLATFORM}..."

curl -fsSL "$URL" | tar -xz -C "$INSTALL_DIR"
chmod +x "$INSTALL_DIR/turo"

echo "turo installed to $INSTALL_DIR/turo"
echo "Add to PATH: export PATH=\"$INSTALL_DIR:\$PATH\""

# Register the turo skill + /turo command with every detected coding agent.
# Requires npx (Node). Skipped silently when Node is unavailable.
if command -v npx >/dev/null 2>&1; then
  echo "Registering turo with detected agents..."
  npx -y turo --no-binary || echo "turo: agent registration skipped (run 'npx turo --no-binary' later)"
else
  echo "Node not found — register agents later with: npx turo --no-binary"
fi
