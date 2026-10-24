package thema_example

import "github.com/sdboyer/thema"

lin: thema.Lineage & {
	name: "Ship"
	schemas: [{
		version: [0, 0]
		schema: {
			firstfield: string
		}
	}, {
		version: [1, 0]
		schema: {
			firstfield:  string
			secondfield: int
		}
	}]
	lenses: [{
		// 1.0 -> 0.0: drop secondfield.
		from:  [1, 0]
		to:    [0, 0]
		input: _
		result: {
			firstfield: input.firstfield
		}
		lacunas: [thema.Lacuna & {
			sourceFields: [{
				path:  "secondfield"
				value: input.secondfield
			}]
			message: "secondfield has no equivalent in 0.0 and was dropped"
			type:    thema.LacunaTypes.DroppedField
		}]
	}, {
		// 0.0 -> 1.0: secondfield is new and required, and 0.0 has no data for it,
		// so the lens fills in a placeholder and says so with a lacuna.
		from:  [0, 0]
		to:    [1, 0]
		input: _
		result: {
			firstfield:  input.firstfield
			secondfield: -1
		}
		lacunas: [thema.Lacuna & {
			targetFields: [{
				path:  "secondfield"
				value: result.secondfield
			}]
			message: "-1 used as a placeholder value - replace with a real value before persisting!"
			type:    thema.LacunaTypes.Placeholder
		}]
	}]
}
