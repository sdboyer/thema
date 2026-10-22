.PHONY: gen-tag agents-md

gen-tag:
	git tag $(tag)
	git push origin $(tag)

# Regenerate the thresh-generated section of AGENTS.md, leaving the rest intact.
agents-md:
	@{ sed '/^<!-- BEGIN thresh explain/q' AGENTS.md; thresh explain --repo .; sed -n '/^<!-- END thresh explain -->/,$$p' AGENTS.md; } > AGENTS.md.tmp && mv AGENTS.md.tmp AGENTS.md
