#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="${HOME}/.local/bin"

echo "Building pixel-fleet (cs)..."

# Find Go
GO_BIN="go"
if [ -x "${HOME}/go-install/go/bin/go" ]; then
    GO_BIN="${HOME}/go-install/go/bin/go"
elif ! command -v go &>/dev/null; then
    echo "Error: Go is not installed."
    exit 1
fi

cd "$SCRIPT_DIR"
"$GO_BIN" build -o cs .

# Install
mkdir -p "$INSTALL_DIR"
cp cs "$INSTALL_DIR/cs"
chmod +x "$INSTALL_DIR/cs"

echo "Installed cs to ${INSTALL_DIR}/cs"

# Check if in PATH
if ! echo "$PATH" | grep -q "$INSTALL_DIR"; then
    echo ""
    echo "Add to your PATH if not already:"
    echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
fi

echo "Done. Run 'cs' to start."
