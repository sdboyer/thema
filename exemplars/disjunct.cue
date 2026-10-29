package exemplars

import "github.com/sdboyer/thema"

disjunct: {
	description: "Changes over schemas with complex disjunctions."
	l: thema.Lineage & {
		schemas: {
			"0": "0": {
				schema: {
					rootfield: {
						branch:    1
						branchone: string
					} | {
						branch:    2
						branchtwo: string
					}
				}
			}
			"0": "1": {
				schema: {
					rootfield: {
						branch:    1
						branchone: string
					} | {
						branch:    2
						branchtwo: string
					} | {
						branch: 3
						branchthree: {
							branchthreeinner: int
						} | {
							branchthreeinner: string
						}
					}
				}
			}
		}

		lenses: {
			"0.1->0.0": {
				v0_1: _
				v0_0: {
					// Branches 1 and 2 are unchanged: copy them through.
					if v0_1.rootfield.branch == 1 {
						rootfield: v0_1.rootfield
					}
					if v0_1.rootfield.branch == 2 {
						rootfield: v0_1.rootfield
					}

					// Branch 3 has no correspondence in 0.0: fall back to branch 1
					// with a filler value, and say so with the lacuna below.
					if v0_1.rootfield.branch == 3 {
						rootfield: {
							branch:    1
							branchone: "wasthree"
						}
					}
				}
				lacunas: [
					thema.Lacuna & {
						condition: v0_1.rootfield.branch == 3
						sourceFields: [{
							path:  "rootfield"
							value: v0_1.rootfield
						}]
						targetFields: [{
							path:  "rootfield"
							value: v0_0.rootfield
						}]
						message: "rootfield was branch 3, which 0.0 does not have; branch 1 with branchone \"wasthree\" was substituted"
						type:    thema.LacunaTypes.LossyFieldMapping
					},
				]
			}
		}
	}
}
