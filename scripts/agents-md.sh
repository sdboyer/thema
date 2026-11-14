#!/usr/bin/env bash
# Regenerate the thresh-generated section of AGENTS.md, leaving the rest intact.
set -euo pipefail
cd "$(dirname "$0")/.."

trap 'rm -f AGENTS.md.tmp' EXIT
{
	sed '/^<!-- BEGIN thresh explain/q' AGENTS.md
	thresh explain --repo .
	sed -n '/^<!-- END thresh explain -->/,$p' AGENTS.md
} >AGENTS.md.tmp
mv AGENTS.md.tmp AGENTS.md
