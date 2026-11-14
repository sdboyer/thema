# The recipes are names for the scripts in scripts/. CI runs the scripts
# directly, so the release workflow does not depend on just.

_default:
    @just --list

# Refuse a tag the release workflow must not publish
release-check tag:
    @scripts/release-check.sh {{ quote(tag) }}

# Check, tag and push a release
release tag:
    @scripts/release.sh {{ quote(tag) }}

# Regenerate AGENTS.md's thresh section
agents-md:
    @scripts/agents-md.sh
