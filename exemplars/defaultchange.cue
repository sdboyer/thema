package exemplars

import "github.com/sdboyer/thema"

defaultchange: {
	description: "The default value for a field is changed, entailing a new sequence."
	l: thema.Lineage & {
		schemas: {
			"0": "0": {
				schema: aunion: *"foo" | "bar" | "baz"
			}
			"1": "0": {
				schema: aunion: "foo" | *"bar" | "baz"
			}
		}

		lenses: {
			"1.0->0.0": {
				input: _
				result: {
					if input.aunion == "bar" {
						aunion: "foo"
					}
					if input.aunion != "bar" {
						aunion: input.aunion
					}
				}
				lacunas: [
					thema.Lacuna & {
						condition: input.aunion == "bar"
						sourceFields: [{
							path:  "aunion"
							value: input.aunion
						}]
						targetFields: [{
							path:  "aunion"
							value: result.aunion
						}]
						message: "aunion was the source default, \"bar\", and was changed to the target default, \"foo\""
						type:    thema.LacunaTypes.ChangedDefault
					},
				]
			}
			"0.0->1.0": {
				input: _
				result: {
					// FIXME lenses need more structure to allow disambiguating absence and presence in the instance
					if input.aunion == "foo" {
						aunion: "bar"
					}
					if input.aunion != "foo" {
						aunion: input.aunion
					}
				}
				lacunas: [
					thema.Lacuna & {
						condition: input.aunion == "foo"
						sourceFields: [{
							path:  "aunion"
							value: input.aunion
						}]
						targetFields: [{
							path:  "aunion"
							value: result.aunion
						}]
						message: "aunion was the source default, \"foo\", and was changed to the target default, \"bar\""
						type:    thema.LacunaTypes.ChangedDefault
					},
				]
			}
		}
	}
}
