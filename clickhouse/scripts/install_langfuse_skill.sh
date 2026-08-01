#!/usr/bin/env bash
# Install Langfuse agent skill from github.com/langfuse/skills (Cursor / Claude Code).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
REPO="${ROOT}/.langfuse-skills-repo"
SKILL_DST="${ROOT}/.cursor/skills/langfuse"

if [[ ! -d "${REPO}/.git" ]]; then
  git clone --depth 1 https://github.com/langfuse/skills.git "$REPO"
else
  git -C "$REPO" pull --ff-only
fi

mkdir -p "${ROOT}/.cursor/skills"
rm -rf "$SKILL_DST"
ln -sf "../../.langfuse-skills-repo/skills/langfuse" "$SKILL_DST"
echo "Installed Langfuse skill → .cursor/skills/langfuse"
