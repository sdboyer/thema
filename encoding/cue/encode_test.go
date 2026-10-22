package cue

import (
	"cuelang.org/go/cue/cuecontext"
	"github.com/sdboyer/thema"
)

var ctx = cuecontext.New()
var rt = thema.NewRuntime(ctx)

// TODO rewrite everything here on top of the corpus
