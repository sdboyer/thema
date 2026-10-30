// Package provisional connects the development bind path of the authoring
// layer (load.DevelopmentLineage) to thema's bind, without making a
// provisional version part of thema's own API.
//
// thema's core states rules about lineages, never about a workflow (#59): a
// lineage that BindLineage returns is complete, and translates without
// missing a lens. A lineage's tip, the version being worked on, is a workflow
// concept of the authoring form, and so is the one bind path that admits it.
// That path needs bind to tolerate one thing, lenses absent from the edge
// into the newest version, and translation to say so when it needs one. It
// asks for that through [Option], which only the authoring layer calls.
package provisional

// Option returns a thema.BindOption, as any, that binds a lineage whose
// newest version, v, is provisional: the lenses its edge from its predecessor
// would need may be absent, and translating across one that is fails with
// E1002. Package thema sets it at init.
var Option func(v [2]uint) any
