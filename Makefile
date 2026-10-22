.PHONY: gen-release agents-md

THEMA_MODULE_VERSION := $(shell sed -n 's/^const ThemaModuleVersion = "\(.*\)"$$/\1/p' internal/util/modfile.go)

# Tag and push a release. The release workflow refuses a tag that differs from
# ThemaModuleVersion, so check here before anything is pushed.
gen-release:
	@test -n "$(tag)" || { echo "usage: make gen-release tag=vX.Y.Z"; exit 1; }
	@test "$(tag)" = "$(THEMA_MODULE_VERSION)" || { echo "tag $(tag) does not match ThemaModuleVersion $(THEMA_MODULE_VERSION) in internal/util/modfile.go"; exit 1; }
	git tag $(tag)
	git push origin $(tag)

# Regenerate the thresh-generated section of AGENTS.md, leaving the rest intact.
agents-md:
	@{ sed '/^<!-- BEGIN thresh explain/q' AGENTS.md; thresh explain --repo .; sed -n '/^<!-- END thresh explain -->/,$$p' AGENTS.md; } > AGENTS.md.tmp && mv AGENTS.md.tmp AGENTS.md
