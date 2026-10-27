package thema_example

import "github.com/sdboyer/thema"

lin: thema.Lineage & {
	name: "Ship"
	schemas: {
		"0": "0": {
			schema: {
				firstfield: string
			}
		}
		"1": "0": {
			schema: {
				firstfield:  string
				secondfield: int
			}
		}
	}
	lenses: {
		"1.0->0.0": {
			// 1.0 -> 0.0: drop secondfield.
			v1_0: _
			v0_0: {
				firstfield: v1_0.firstfield
			}
			lacunas: [thema.Lacuna & {
				sourceFields: [{
					path:  "secondfield"
					value: v1_0.secondfield
				}]
				message: "secondfield has no equivalent in 0.0 and was dropped"
				type:    thema.LacunaTypes.DroppedField
			}]
		}
		"0.0->1.0": {
			// 0.0 -> 1.0: secondfield is new and required, and 0.0 has no data for it,
			// so the lens fills in a placeholder and says so with a lacuna.
			v0_0: _
			v1_0: {
				firstfield:  v0_0.firstfield
				secondfield: -1
			}
			lacunas: [thema.Lacuna & {
				targetFields: [{
					path:  "secondfield"
					value: v1_0.secondfield
				}]
				message: "-1 used as a placeholder value - replace with a real value before persisting!"
				type:    thema.LacunaTypes.Placeholder
			}]
		}
	}
}
