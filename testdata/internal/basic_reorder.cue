package thema

basicreorder: #Lineage & {
	name: "basic"
	joinSchema: {
		all: int32
	}
	schemas: {
		"0": "0": {
			schema: {
				init: string
			}
			examples: {
				simple: {
					init: "foo"
				}
			}
		}
		"0": "1": {
			schema: {
				init:      string
				optional?: int32
			}
		}
		"0": "2": {
			schema: {
				init:        string
				optional?:   int32
				withDefault: *"foo" | "bar"
			}
		}
		"1": "0": {
			schema: {
				init:        string
				optional?:   int32
				withDefault: "foo" | *"bar" | "baz"
			}
		}
		"0": "3": {
			schema: {
				init:        string
				optional?:   int32
				withDefault: *"foo" | "bar" | "baz"
			}
		}
	}

	lenses: {
		"0.1->0.0": {
			input: _
			result: {
				init: input.init
				all:  input.all
			}
		}
		"0.2->0.1": {
			input: _
			result: {
				init: input.init
				all:  input.all
				if (input.optional != _|_) {
					optional: input.optional
				}
			}
		}
		"0.3->0.2": {
			input: _
			result: {
				init: input.init
				all:  input.all
				if (input.optional != _|_) {
					optional: input.optional
				}

				withDefault: input.withDefault // TODO does this actually work
			}
		}
		"1.0->0.3": {
			input: _
			result: {
				init: input.init
				all:  input.all
				if (input.optional != _|_) {
					optional: input.optional
				}

				withDefault: input.withDefault
			}
		}
	}
	_counts: [4, 1]
	_basis: [0, 4]
}
