package load

import (
	"cuelang.org/go/cue/build"
	"cuelang.org/go/cue/load"
	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/util"
)

// composeDir and composeModule are where ComposedInstance loads a composed
// package from: a module of its own, presented to the loader at a virtual
// directory.
const (
	composeDir    = "/thema.compose"
	composeModule = "thema.compose/composed"
)

// ComposedInstance composes ps into one package named pkg, with
// [thema.Compose], and loads it on its own, in a module of its own, with
// thema's CUE module available to import. The placements' files may import
// thema and CUE's builtin packages, nothing else.
//
// To compose into a package of your own, or to import packages of your own
// module, load the files [thema.Compose] returns with your own load.Config
// instead, prepared with [Configure].
func ComposedInstance(pkg string, ps ...thema.Placement) (*build.Instance, error) {
	files, err := thema.Compose(pkg, ps...)
	if err != nil {
		return nil, err
	}
	cfg := &load.Config{
		Dir: composeDir,
		Overlay: map[string]load.Source{
			composeDir + "/cue.mod/module.cue": load.FromString(util.ModuleFile(composeModule)),
		},
	}
	for _, f := range files {
		cfg.Overlay[composeDir+"/"+f.Filename] = load.FromFile(f)
	}
	if err := Configure(cfg); err != nil {
		return nil, err
	}
	inst := load.Instances([]string{"."}, cfg)[0]
	if inst.Err != nil {
		return nil, diag.Convert(inst.Err, diag.Context{Op: diag.OpLoad, Generic: diag.E0609})
	}
	return inst, nil
}
