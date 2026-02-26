#!/usr/bin/env bash
# install.sh — builds and installs context-bridge to /usr/local/bin/bridge
set -e

echo "Building context-bridge..."
go build -ldflags="-s -w" -o bridge ./cmd/bridge

echo "Installing to /usr/local/bin/bridge..."
sudo mv bridge /usr/local/bin/bridge
sudo chmod +x /usr/local/bin/bridge

echo ""
echo "context-bridge installed. Next steps:"
echo ""
echo "  1. Set your Anthropic API key:"
echo "     export ANTHROPIC_API_KEY=sk-ant-..."
echo ""
echo "  2. Make sure cmux is running (it sets CMUX_SOCKET_PATH automatically)."
echo ""
echo "  3. Start the daemon:"
echo "     bridge daemon"
echo ""
echo "  That's it. context-bridge will auto-inject context when you switch agents."
echo ""
echo "  Manual handoff:"
echo "     bridge handoff --to codex"
echo "     bridge handoff --to gemini --note 'focus on the API layer'"
echo ""
echo "  Check what's being tracked:"
echo "     bridge status"
