package exemplars

import "github.com/sdboyer/thema"

narrowing: {
	description: "Lineage that narrows a sloppily-specified boolean/string-ish type to a proper boolean over a single breaking change."
	l: thema.Lineage & {
		schemas: {
			"0": "0": {
				schema: boolish: "true" | "false" | bool | string
			}
			"1": "0": {
				schema: properbool: bool
			}
		}

		lenses: {
			"1.0->0.0": {
				input: _
				result: {
					// Preserving precise original form is a non-goal of thema in general.
					boolish: input.properbool
				}
				lacunas: []
			}
			"0.0->1.0": {
				input: _
				result: {
					if ((input.boolish & string) != _|_) {
						properbool: input.boolish == "true"
					}
					if ((input.boolish & bool) != _|_) {
						properbool: input.boolish
					}
				}
				lacunas: [
					thema.Lacuna & {
						condition: ((input.boolish & string) != _|_) && ((input.boolish & ("true" | "false")) == _|_)
						sourceFields: [{
							path:  "boolish"
							value: input.boolish
						}]
						targetFields: [{
							path:  "properbool"
							value: result.properbool
						}]
						message: "boolish was a string but neither \"true\" nor \"false\"; fallback to treating as false"
						type:    thema.LacunaTypes.LossyFieldMapping
					},
				]
			}
		}
	}
}
