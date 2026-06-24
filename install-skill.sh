#!/bin/bash
set -e

SKILL_DIR="$HOME/.claude/skills/mcp-me"

echo "Installing mcp-me skill for Claude Code..."

mkdir -p "$SKILL_DIR"
cp "$(dirname "$0")/skills/mcp-me/SKILL.md" "$SKILL_DIR/SKILL.md"

echo "Done. Restart Claude Code and use /mcp-me to activate."
