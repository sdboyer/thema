#!/usr/bin/env bash
# Tag a release and push the tag, which starts the release workflow. Pushing a
# tag publishes the Go module for good, so this first refuses anything the
# workflow cannot finish: a tag release-check.sh refuses, a commit that is not
# origin's main or whose CI has not passed, or a version older than one tagged
# already.
set -euo pipefail
# An exported CDPATH would redirect a relative cd.
CDPATH='' cd "$(dirname "$0")/.."

die() {
	echo "$*" >&2
	exit 1
}

[[ $# -eq 1 && -n $1 ]] || die "usage: $0 vX.Y.Z"
tag=$1

scripts/release-check.sh "$tag"

git fetch -q origin main --tags
[[ $(git rev-parse --abbrev-ref HEAD) == main ]] || die "not on main"
[[ -z $(git status --porcelain) ]] || die "the working tree has changes"
[[ $(git rev-parse HEAD) == "$(git rev-parse origin/main)" ]] || die "HEAD is not origin/main"
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
	die "$tag is tagged already"
fi

# Before the first release there is no vX.Y.Z tag, and grep exits 1.
last=$(git tag -l 'v*' | { grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' || true; } | sort -V | tail -1)
if [[ -n $last && $(printf '%s\n' "$last" "$tag" | sort -V | tail -1) == "$last" ]]; then
	die "$tag is older than $last"
fi

ci=$(gh run list -R sdboyer/thema --workflow ci.yml --event push --commit "$(git rev-parse HEAD)" \
	--json status,conclusion --jq '.[0] | if . == null then "no CI run" else "\(.status) \(.conclusion)" end') ||
	die "could not ask GitHub for CI's result on $(git rev-parse --short HEAD)"
[[ $ci == "completed success" ]] || die "CI on $(git rev-parse --short HEAD) has not completed and passed: $ci"

set -x
git tag -s "$tag" -m "thema $tag"
git push origin "$tag"
