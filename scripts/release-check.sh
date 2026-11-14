#!/usr/bin/env bash
# Refuse a tag the release workflow must not publish: one not of the form
# vX.Y.Z, or other than ThemaModuleVersion. The release workflow runs this
# too.
set -euo pipefail
# An exported CDPATH would redirect a relative cd.
CDPATH='' cd "$(dirname "$0")/.."

die() {
	echo "$*" >&2
	exit 1
}

[[ $# -eq 1 && -n $1 ]] || die "usage: $0 vX.Y.Z"
tag=$1

version=$(sed -n 's/^const ThemaModuleVersion = "\(.*\)"$/\1/p' internal/util/modfile.go)
[[ -n $version ]] || die "found no ThemaModuleVersion in internal/util/modfile.go"

[[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "tag $tag is not vX.Y.Z"
[[ $tag == "$version" ]] || die "tag $tag is not ThemaModuleVersion $version in internal/util/modfile.go"
