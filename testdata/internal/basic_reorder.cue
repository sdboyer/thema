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
			v0_1: _
			v0_0: {
				init: v0_1.init
				all:  v0_1.all
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
		"1.0->0.3": {
			v1_0: _
			v0_3: {
				init: v1_0.init
				all:  v1_0.all
				if (v1_0.optional != _|_) {
					optional: v1_0.optional
				}

				withDefault: v1_0.withDefault
			}
		}
	}
	_counts: [4, 1]
	_basis: [0, 4]
}
