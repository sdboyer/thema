.PHONY: release release-check agents-md

THEMA_MODULE_VERSION := $(shell sed -n 's/^const ThemaModuleVersion = "\(.*\)"$$/\1/p' internal/util/modfile.go)

# Refuse a tag the release workflow must not publish: one not of the form
# vX.Y.Z or vX.Y.Z-rc.N, or whose version, without its rc suffix, is not
# ThemaModuleVersion. An rc of a version is tagged on the commit that sets it,
# so the release itself can be tagged on the commit its last rc rehearsed.
# The release workflow runs this too.
release-check:
	@test -n "$(tag)" || { echo "usage: make release-check tag=vX.Y.Z[-rc.N]"; exit 1; }
	@echo "$(tag)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$$' || { echo "tag $(tag) is neither vX.Y.Z nor vX.Y.Z-rc.N"; exit 1; }
	@base=$$(echo "$(tag)" | sed 's/-rc\.[0-9]*$$//'); test "$$base" = "$(THEMA_MODULE_VERSION)" || { echo "tag $(tag) is not of ThemaModuleVersion $(THEMA_MODULE_VERSION) in internal/util/modfile.go"; exit 1; }

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
	@base=$$(echo "$(tag)" | sed 's/-rc\.[0-9]*$$//'); \
	last=$$(git tag -l 'v*' | sed 's/-rc\.[0-9]*$$//' | sort -uV | tail -1); \
	if [ -n "$$last" ] && [ "$$last" != "$$base" ] && [ "$$(printf '%s\n' "$$last" "$$base" | sort -V | tail -1)" = "$$last" ]; then echo "$(tag) is older than $$last"; exit 1; fi; \
	if [ "$$base" != "$(tag)" ] && git rev-parse -q --verify "refs/tags/$$base" >/dev/null; then echo "$$base is released already, so $(tag) cannot rehearse it"; exit 1; fi
	@ci=$$(gh run list -R sdboyer/thema --workflow ci.yml --event push --commit "$$(git rev-parse HEAD)" --json status,conclusion --jq '.[0] | if . == null then "no CI run" else "\(.status) \(.conclusion)" end'); \
	test "$$ci" = "completed success" || { echo "CI on $$(git rev-parse --short HEAD) has not completed and passed: $$ci"; exit 1; }
	git tag -s "$(tag)" -m "thema $(tag)"
	git push origin "$(tag)"

# Regenerate the thresh-generated section of AGENTS.md, leaving the rest intact.
agents-md:
	@{ sed '/^<!-- BEGIN thresh explain/q' AGENTS.md; thresh explain --repo .; sed -n '/^<!-- END thresh explain -->/,$$p' AGENTS.md; } > AGENTS.md.tmp && mv AGENTS.md.tmp AGENTS.md
