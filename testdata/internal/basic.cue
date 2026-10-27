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
			v1_0: _
			v0_3: {
				init: v1_0.renamed
				all:  v1_0.all
				if (v1_0.optional != _|_) {
					optional: v1_0.optional
				}

				withDefault: v1_0.withDefault
			}
		}
		"0.2->0.1": {
			v0_2: _
			v0_1: {
				init: v0_2.init
				all:  v0_2.all
				if (v0_2.optional != _|_) {
					optional: v0_2.optional
				}
			}
		}
		"0.3->0.2": {
			v0_3: _
			v0_2: {
				init: v0_3.init
				all:  v0_3.all
				if (v0_3.optional != _|_) {
					optional: v0_3.optional
				}

				withDefault: v0_3.withDefault // TODO does this actually work
			}
		}
		"0.3->1.0": {
			v0_3: _
			v1_0: {
				renamed: v0_3.init
				all:     v0_3.all
				if (v0_3.optional != _|_) {
					optional: v0_3.optional
				}

				withDefault: v0_3.withDefault
			}
		}
		"1.1->1.0": {
			v1_1: _
			v1_0: {
				renamed: v1_1.renamed
				all:     v1_1.all
				if (v1_1.optional != _|_) {
					optional: v1_1.optional
				}

				withDefault: v1_1.withDefault
			}
		}
		"0.1->0.0": {
			v0_1: _
			v0_0: {
				init: v0_1.init
				all:  v0_1.all
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
