#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
npm install --no-fund --no-audit
echo "Run: npm run dev  →  http://localhost:3032"
