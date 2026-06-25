#!/bin/bash
set -e

SKILL_DIR="$HOME/.claude/skills/mcp-me"
CONFIG_DIR="$HOME/.claude/.mcpme"
CONFIG_FILE="$CONFIG_DIR/collections.json"

echo "Installing mcp-me skill for Claude Code..."

mkdir -p "$SKILL_DIR"
cp "$(dirname "$0")/skills/mcp-me/SKILL.md" "$SKILL_DIR/SKILL.md"

if [ ! -f "$CONFIG_FILE" ]; then
  mkdir -p "$CONFIG_DIR"
  echo '{"version":"1","api_key":"","collections":[]}' > "$CONFIG_FILE"
  echo "Created $CONFIG_FILE — paste your API key into the api_key field."
fi

echo "Done. Register the account MCP then restart Claude Code:"
echo ""
echo "  claude mcp add mcp-me --transport http https://mcp-me-production.up.railway.app/v1/mcp --header \"Authorization: Bearer <api_key>\""
