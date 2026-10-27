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
				v1_0: _
				v0_0: {
					// Preserving precise original form is a non-goal of thema in general.
					boolish: v1_0.properbool
				}
				lacunas: []
			}
			"0.0->1.0": {
				v0_0: _
				v1_0: {
					if ((v0_0.boolish & string) != _|_) {
						properbool: v0_0.boolish == "true"
					}
					if ((v0_0.boolish & bool) != _|_) {
						properbool: v0_0.boolish
					}
				}
				lacunas: [
					thema.Lacuna & {
						condition: ((v0_0.boolish & string) != _|_) && ((v0_0.boolish & ("true" | "false")) == _|_)
						sourceFields: [{
							path:  "boolish"
							value: v0_0.boolish
						}]
						targetFields: [{
							path:  "properbool"
							value: v1_0.properbool
						}]
						message: "boolish was a string but neither \"true\" nor \"false\"; fallback to treating as false"
						type:    thema.LacunaTypes.LossyFieldMapping
					},
				]
			}
		}
	}
}
