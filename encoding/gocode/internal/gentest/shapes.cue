package gentest

import "github.com/sdboyer/thema"

// shapes exercises the schema constructs a lineage imported from an OpenAPI
// document typically holds.
shapes: thema.Lineage & {
	name: "shapes"
	schemas: {
		"0": "0": {
			schema: {
				// id identifies the resource.
				id: string & =~"^[a-f0-9]{32}$"
				name!:   string
				count:   int & >=1 & <=100
				ratio?:  number
				enabled: *true | bool
				status:  *"active" | "pending" | "deleted"
				mode?:   null | "fast" | "slow"
				comment: null | string
				created_on?: string
				"x-trace-id"?: string
				owner: {
					email: string
					role?: "admin" | "member"
				}
				parent: null | {
					id: string
				}
				tags?: [...string]
				routes: [...{
					pattern: string
					zone_id?: string
					kind:    "a" | "b"
				}]
				metadata?: [string]: _
				labels: [string]: string
				limits: [string]: {
					max: int64
				}
				meta: #Meta
				audit?: {#Timestamps, by: string}

				#Meta: {
					version: int32
					modified_on?: null | string
				}
				#Timestamps: {
					created: string
					...
				}
			}
		}
	}
	lenses: {}
}
