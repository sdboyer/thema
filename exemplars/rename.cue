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
				input: _
				result: {
					before:    input.after
					unchanged: input.unchanged
				}
				lacunas: []
			}
			"0.0->1.0": {
				input: _
				result: {
					after:     input.before
					unchanged: input.unchanged
				}
				lacunas: []
			}
		}
	}
}
