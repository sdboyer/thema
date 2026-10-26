package thema

import "list"

basic: #Lineage & {
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
					all:  42
					init: "some string"
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
		"0": "3": {
			schema: {
				init:        string
				optional?:   int32
				withDefault: *"foo" | "bar" | "baz"
			}
		}
		"1": "0": {
			schema: {
				renamed:     string
				optional?:   int32
				withDefault: "foo" | *"bar" | "baz"
			}
		}
		"1": "1": {
			schema: {
				renamed:     string
				optional?:   int32
				withDefault: "foo" | *"bar" | "baz" | "bing"
			}
		}
	}

	lenses: {
		"1.0->0.3": {
			input: _
			result: {
				init: input.renamed
				all:  input.all
				if (input.optional != _|_) {
					optional: input.optional
				}

				withDefault: input.withDefault
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
		"0.3->1.0": {
			input: _
			result: {
				renamed: input.init
				all:     input.all
				if (input.optional != _|_) {
					optional: input.optional
				}

				withDefault: input.withDefault
			}
		}
		"1.1->1.0": {
			input: _
			result: {
				renamed: input.renamed
				all:     input.all
				if (input.optional != _|_) {
					optional: input.optional
				}

				withDefault: input.withDefault
			}
		}
		"0.1->0.0": {
			input: _
			result: {
				init: input.init
				all:  input.all
			}
		}
	}
	_counts: [4, 2]
	_basis: [0, 4]
}

cmpsv: {
	cases: [...{input: [...#SyntacticVersion], out: [...#SyntacticVersion]}]
	cases: [
		{
			input: [[0, 0], [1, 0], [1, 1], [3, 2], [0, 1]]
			out: [[0, 0], [0, 1], [1, 0], [1, 1], [3, 2]]
		},

	]
	results: [ for case in cases {
		case.out & list.Sort(case.input, {
			x:    #SyntacticVersion
			y:    #SyntacticVersion
			less: (_cmpSV & {l: x, r: y}).out == -1
		})
	},
	]
}
