package thema_example

import "github.com/sdboyer/thema"

lin: thema.Lineage
lin: name: "Ship"
lin: seqs: [
	{
		schemas: [
			{
				firstfield: string
			}, // 0.0
		]
	},
	{
		schemas: [
			{
				firstfield:  string
				secondfield: int
			}, // 1.0
		]

		lens: forward: {
			from: seqs[0].schemas[0]
			to:   seqs[1].schemas[0]
			rel: {
				firstfield:  from.firstfield
				secondfield: -1
			}
			lacunas: [
				thema.Lacuna & {
					targetFields: [{
						path:  "secondfield"
						value: to.secondfield
					}]
					message: "-1 used as a placeholder value - replace with a real value before persisting!"
					type:    thema.LacunaTypes.Placeholder
				},
			]
			translated: to & rel
		}
		lens: reverse: {
			from: seqs[1].schemas[0]
			to:   seqs[0].schemas[0]
			rel: {
				// Map the first field back
				firstfield: from.firstfield
			}
			translated: to & rel
		}
	},
]
