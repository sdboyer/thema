.PHONY: release release-check agents-md

THEMA_MODULE_VERSION := $(shell sed -n 's/^const ThemaModuleVersion = "\(.*\)"$$/\1/p' internal/util/modfile.go)

# Refuse a tag the release workflow must not publish: one not of the form
# vX.Y.Z, or other than ThemaModuleVersion. The release workflow runs this
# too.
release-check:
	@test -n "$(tag)" || { echo "usage: make release-check tag=vX.Y.Z"; exit 1; }
	@echo "$(tag)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "tag $(tag) is not vX.Y.Z"; exit 1; }
	@test "$(tag)" = "$(THEMA_MODULE_VERSION)" || { echo "tag $(tag) is not ThemaModuleVersion $(THEMA_MODULE_VERSION) in internal/util/modfile.go"; exit 1; }

# Tag a release and push the tag, which starts the release workflow. Pushing a
# tag publishes the Go module for good, so this first refuses anything the
# workflow cannot finish: a tag release-check refuses, a commit other than
# origin's main with CI passed on it, or a version older than one tagged
# already.
release: release-check
	@git fetch -q origin main --tags
	@test "$$(git rev-parse --abbrev-ref HEAD)" = main || { echo "not on main"; exit 1; }
	@test -z "$$(git status --porcelain)" || { echo "the working tree has changes"; exit 1; }
	@test "$$(git rev-parse HEAD)" = "$$(git rev-parse origin/main)" || { echo "HEAD is not origin/main"; exit 1; }
	@! git rev-parse -q --verify "refs/tags/$(tag)" >/dev/null || { echo "$(tag) is tagged already"; exit 1; }
	@last=$$(git tag -l 'v*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$$' | sort -V | tail -1); \
	if [ -n "$$last" ] && [ "$$(printf '%s\n' "$$last" "$(tag)" | sort -V | tail -1)" = "$$last" ]; then echo "$(tag) is older than $$last"; exit 1; fi
	@ci=$$(gh run list -R sdboyer/thema --workflow ci.yml --event push --commit "$$(git rev-parse HEAD)" --json status,conclusion --jq '.[0] | if . == null then "no CI run" else "\(.status) \(.conclusion)" end'); \
	test "$$ci" = "completed success" || { echo "CI on $$(git rev-parse --short HEAD) has not completed and passed: $$ci"; exit 1; }
	git tag -s "$(tag)" -m "thema $(tag)"
	git push origin "$(tag)"

# Regenerate the thresh-generated section of AGENTS.md, leaving the rest intact.
agents-md:
	@{ sed '/^<!-- BEGIN thresh explain/q' AGENTS.md; thresh explain --repo .; sed -n '/^<!-- END thresh explain -->/,$$p' AGENTS.md; } > AGENTS.md.tmp && mv AGENTS.md.tmp AGENTS.md
