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
						if input.withDefault != "baz" {
							withDefault: input.withDefault
						}
						if input.withDefault == "baz" {
							// 0.2 has no "baz"; its default stands in for it, and the
							// lacuna below says so.
							withDefault: "foo"
						}
					}
				}
				lacunas: [
					thema.Lacuna & {
						condition: (input.withDefault & "baz") != _|_
						sourceFields: [{
							path:  "withDefault"
							value: input.withDefault
						}]
						targetFields: [{
							path:  "withDefault"
							value: result.withDefault
						}]
						message: "withDefault was \"baz\", which 0.2 does not have; \"foo\", its default, was substituted"
						type:    thema.LacunaTypes.LossyFieldMapping
					},
				]
			}
		}
	}
}
