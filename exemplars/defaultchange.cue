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
				v1_0: _
				v0_0: {
					if v1_0.aunion == "bar" {
						aunion: "foo"
					}
					if v1_0.aunion != "bar" {
						aunion: v1_0.aunion
					}
				}
				lacunas: [
					thema.Lacuna & {
						condition: v1_0.aunion == "bar"
						sourceFields: [{
							path:  "aunion"
							value: v1_0.aunion
						}]
						targetFields: [{
							path:  "aunion"
							value: v0_0.aunion
						}]
						message: "aunion was the source default, \"bar\", and was changed to the target default, \"foo\""
						type:    thema.LacunaTypes.ChangedDefault
					},
				]
			}
			"0.0->1.0": {
				v0_0: _
				v1_0: {
					// FIXME lenses need more structure to allow disambiguating absence and presence in the instance
					if v0_0.aunion == "foo" {
						aunion: "bar"
					}
					if v0_0.aunion != "foo" {
						aunion: v0_0.aunion
					}
				}
				lacunas: [
					thema.Lacuna & {
						condition: v0_0.aunion == "foo"
						sourceFields: [{
							path:  "aunion"
							value: v0_0.aunion
						}]
						targetFields: [{
							path:  "aunion"
							value: v1_0.aunion
						}]
						message: "aunion was the source default, \"foo\", and was changed to the target default, \"bar\""
						type:    thema.LacunaTypes.ChangedDefault
					},
				]
			}
		}
	}
}
