package exemplars

import "github.com/sdboyer/thema"

rename: {
	description: "A field is renamed - a breaking change, necessitating a new sequence."
	l: thema.Lineage & {
		schemas: {
			"0": "0": {
				schema: {
					before:    string
					unchanged: string
				}
			}
			"1": "0": {
				schema: {
					after:     string
					unchanged: string
				}
			}
		}

		lenses: {
			"1.0->0.0": {
				v1_0: _
				v0_0: {
					before:    v1_0.after
					unchanged: v1_0.unchanged
				}
				lacunas: []
			}
			"0.0->1.0": {
				v0_0: _
				v1_0: {
					after:     v0_0.before
					unchanged: v0_0.unchanged
				}
				lacunas: []
			}
		}
	}
}
