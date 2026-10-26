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
				input: _
				result: {
					init: input.init
				}
			}
			"0.2->0.1": {
				input: _
				result: {
					init: input.init
					if input.optional != _|_ {
						optional: input.optional
					}
				}
			}
			"0.3->0.2": {
				input: _
				result: {
					init: input.init
					if input.optional != _|_ {
						optional: input.optional
					}
					if input.withDefault != _|_ {
						// if the value is "baz" (not allowed by the to schema), then the to
						// schema's default value "foo" will be selected by Thema's #Translate
						withDefault: input.withDefault
					}
				}
			}
		}
	}
}
