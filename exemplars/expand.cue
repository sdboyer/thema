package exemplars

import "github.com/sdboyer/thema"

expand: {
	description: "A few schema in a single sequence, illustrating some simple expansions permitted by backwards compatibility rules."
	l: thema.Lineage & {
		schemas: {
			"0": "0": {
				schema: {
					init: string
				}
			}
			"0": "1": {
				schema: {
					init:      string
					optional?: int
				}
			}
			"0": "2": {
				schema: {
					init:         string
					optional?:    int
					withDefault?: *"foo" | "bar"
				}
			}
			"0": "3": {
				schema: {
					init:         string
					optional?:    int
					withDefault?: *"foo" | "bar" | "baz"
				}
			}
		}

		lenses: {
			"0.1->0.0": {
				v0_1: _
				v0_0: {
					init: v0_1.init
				}
			}
			"0.2->0.1": {
				v0_2: _
				v0_1: {
					init: v0_2.init
					if v0_2.optional != _|_ {
						optional: v0_2.optional
					}
				}
			}
			"0.3->0.2": {
				v0_3: _
				v0_2: {
					init: v0_3.init
					if v0_3.optional != _|_ {
						optional: v0_3.optional
					}
					if v0_3.withDefault != _|_ {
						// if the value is "baz" (not allowed by the to schema), then the to
						// schema's default value "foo" will be selected by Thema's #Translate
						withDefault: v0_3.withDefault
					}
				}
			}
		}
	}
}
