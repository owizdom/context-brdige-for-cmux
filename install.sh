#!/usr/bin/env bash
set -euo pipefail

echo "Building mnemo..."
go build -o mnemo .

INSTALL_DIR="/usr/local/bin"

if [ -w "$INSTALL_DIR" ]; then
    mv mnemo "$INSTALL_DIR/mnemo"
else
    echo "Installing to $INSTALL_DIR (requires sudo)..."
    sudo mv mnemo "$INSTALL_DIR/mnemo"
fi

echo "Installed: $(mnemo version)"
echo ""
echo "Next steps:"
echo "  1. Open tmux or cmux"
echo "  2. export ANTHROPIC_API_KEY=sk-ant-...  (optional)"
echo "  3. mnemo daemon"
