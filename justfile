# The recipes are names for the scripts in scripts/. The release workflow runs
# scripts/release-check.sh itself, so publishing does not depend on just.

_default:
    @{{ just_executable() }} --list --justfile {{ quote(justfile()) }}

# Refuse a tag the release workflow must not publish
release-check tag:
    @{{ quote(justfile_directory() / "scripts/release-check.sh") }} {{ quote(tag) }}

# Check, tag and push a release
release tag:
    @{{ quote(justfile_directory() / "scripts/release.sh") }} {{ quote(tag) }}

# Regenerate AGENTS.md's thresh section
agents-md:
    @{{ quote(justfile_directory() / "scripts/agents-md.sh") }}
