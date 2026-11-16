// Copyright 2020 CUE Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package jsonschema is the decoder of cuelang.org/go/encoding/jsonschema at
// cuelang.org/go v0.18.0-rc.3, forked so that it keeps a property's x-
// keywords: they are written as an attribute on the property's field,
// @openapi(...), where upstream skips them (extensions.go, and the hooks
// marked thema in upstream's files). And NewComment writes U+FEFF as U+2060
// WORD JOINER, as CUE's scanner refuses U+FEFF in a comment. Only the
// decoding files are copied, with structbuilder under internal/. The other
// changes replace what Go forbids importing from another module (fork.go):
// the experiments of the current language version are hard-coded, and Config
// has no TargetLanguageVersion; NewComment is copied from
// cuelang.org/go/internal; a [cue.Value]'s own Context replaces
// internal/value's; and the go:generate directives, whose generators are not
// copied, are dropped. fork.go also holds the one table the decoder uses from
// upstream's encoder files.
//
// Package jsonschema converts JSON Schema to CUE.
//
// JSON Schema data is presented in CUE, so any of the supported encodings
// that can represent JSON Schema data can be used as a source.
package jsonschema
