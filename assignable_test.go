package thema

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

func TestAssignable(t *testing.T) {
	ctx := cuecontext.New()
	const (
		nestUnbounded = "nest.p: schema type int not subsumed by Go type int64"
		nestUnread    = "nest: schema has a default in a form assignability cannot check, may only correspond to interface{}/any"
	)

	var blah string
	tt := map[string]struct {
		// The raw CUE that will be checked for assignability to a Go type
		cue string
		// The Go type the raw CUE will be checked for assignability to
		T interface{}
		// What the refusal of an unassignable type says; empty if assignable
		refusal string
	}{
		"nonpointer": {
			T: struct{}{},
			cue: `typ: {}
			`,
		},
		"doublepointer": {
			T: struct{}{},
			cue: `typ: {}
			`,
		},
		"nonstruct": {
			T: blah,
			cue: `typ: _
			`,
			refusal: "must provide struct-kinded type, got *string",
		},
		"simpleMatch": {
			T: &struct {
				Foo string `json:"foo"`
			}{},
			cue: `typ: {
				foo: string
			}
			`,
		},
		"any": {
			T: &struct {
				AString any `json:"aString"`
				AnInt   any `json:"anInt"`
			}{},
			cue: `typ: {
				aString: string
				anInt: int32
			}
			`,
		},
		"not-any-union": {
			T: &struct {
				Hopeful struct {
					AString any `json:"aString"`
				} `json:"hopeful"`
			}{},
			cue: `typ: {
				hopeful: {
					aString: string
				} | {
					anInt: int32
				}
			}
			`,
			refusal: "hopeful: contains disjunction over struct types, but Go type is not any",
		},
		"stringEnumNoPointer": {
			T: struct {
				Foo string `json:"foo"`
			}{},
			cue: `typ: {
				foo: "foo" | "bar"
			}
			`,
		},
		"stringEnum": {
			T: &struct {
				Foo string `json:"foo"`
			}{},
			cue: `typ: {
				foo: "foo" | "bar"
			}
			`,
		},
		"multiEnum": {
			T: &struct {
				Foo string `json:"foo"`
			}{},
			cue: `typ: {
				foo: "foo" | 14
			}
			`,
			refusal: "foo: is kind (int|string) in schema, but kind string in Go type",
		},
		"extraSchField": {
			T: &struct {
				Foo string `json:"foo"`
			}{},
			cue: `typ: {
				foo: "foo" | "bar"
				bar: int
			}
			`,
			refusal: "bar: field present in schema, absent from Go type",
		},
		"extraGoField": {
			T: &struct {
				Foo string `json:"foo"`
				Bar int    `json:"bar"`
			}{},
			cue: `typ: {
				foo: "foo" | "bar"
			}
			`,
			refusal: "bar: field present in Go type, absent from schema",
		},
		"optionalField": {
			T: &struct {
				Foo string `json:"foo"`
			}{},
			cue: `typ: {
				foo?: string
			}
			`,
		},
		"optionalFieldOmit": {
			T: &struct {
				Foo string `json:"foo,omitempty"`
			}{},
			cue: `typ: {
				foo?: string
			}
			`,
		},
		"wrongType": {
			T: &struct {
				Foo string `json:"foo"`
			}{},
			cue: `typ: {
				foo: int
			}
			`,
			refusal: "foo: is kind int in schema, but kind string in Go type",
		},
		"convertMap": {
			T: &struct {
				Amap map[string]bool `json:"amap"`
			}{},
			cue: `typ: {
				amap: [string]: bool
			}
			`,
		},
		"mapPointer": {
			T: &struct {
				Amap *map[string]bool `json:"amap"`
			}{},
			cue: `typ: {
				amap: [string]: bool
			}
			`,
		},
		"nestedStruct": {
			T: &struct {
				Foo   string `json:"foo"`
				Nest1 struct {
					Inner string `json:"inner"`
					Nest2 struct {
						DoubleInner string `json:"doubleinner"`
					} `json:"nest2"`
				} `json:"nest1"`
			}{},
			cue: `typ: {
				foo: string
				nest1: {
					inner: string
					nest2: {
						doubleinner: string
					}
				}
			}
			`,
		},
		"nestedStructDefault": {
			T: &struct {
				Foo   string `json:"foo"`
				Nest1 struct {
					Inner string `json:"inner"`
				} `json:"nest1"`
			}{},
			cue: `typ: {
				foo: string
				nest1: {
					inner: string
				} | *{ inner: "foo" }
			}
			`,
		},
		"simpleSlice": {
			T: &struct {
				Slice []string `json:"slice"`
			}{},
			cue: `typ: {
				slice: [...string]
			}
			`,
		},
		"slicePointer": {
			T: &struct {
				Slice *[]string `json:"slice"`
			}{},
			cue: `typ: {
				slice: [...string]
			}
			`,
		},
		"sliceWithDefault": {
			T: &struct {
				Slice []string `json:"slice"`
			}{},
			cue: `typ: {
				slice: [...string] | *["foo", "bar"]
			}
			`,
		},
		"multiTypeList": {
			T: &struct {
				Slice []string `json:"slice"`
			}{},
			cue: `typ: {
				slice: [...string] | [...int] | [...bool] | *[1, 2, 3]
			}
			`,
			refusal: "slice: schema is a complex disjunction of list types, may only correspond to interface{}/any",
		},
		"closedList": {
			T: &struct {
				Slice []string `json:"slice"`
			}{},
			cue: `typ: {
				slice: []
			}
			`,
			refusal: "slice: list is closed in schema, Go type must be an array, not slice",
		},
		"closedStringList": {
			T: &struct {
				Slice []string `json:"slice"`
			}{},
			cue: `typ: {
				slice: [string]
			}
			`,
			refusal: "slice: list is closed in schema, Go type must be an array, not slice",
		},
		"arrayEmpty": {
			T: &struct {
				Arr [0]string `json:"arr"`
			}{},
			cue: `typ: {
				arr: []
			}
			`,
		},
		"arrayClosedList": {
			T: &struct {
				Arr [2]string `json:"arr"`
			}{},
			cue: `typ: {
				arr: [string, string]
			}
			`,
		},
		"simpleSliceMistype": {
			T: &struct {
				Slice []string `json:"slice"`
			}{},
			cue: `typ: {
				slice: [...int]
			}
			`,
			refusal: "slice.[_]: is kind int in schema, but kind string in Go type",
		},
		"listMinLen": {
			T: &struct {
				Slice []string `json:"slice"`
			}{},
			cue: `typ: {
				slice: [string, ...string]
			}
			`,
		},
		"listMinLenMultitype": {
			T: &struct {
				Slice []string `json:"slice"`
			}{},
			cue: `typ: {
				slice: [int, ...string]
			}
			`,
			refusal: "slice: schema is list of multiple types; not representable in Go",
		},
		"structInList": {
			T: &struct {
				Slice []struct {
					Listfield string `json:"listfield"`
				} `json:"slice"`
			}{},
			cue: `typ: {
				slice: [...{
					listfield: "foo" | "bar"
				}]
			}
			`,
		},
		"listInStruct": {
			T: &struct {
				Foo  string `json:"foo"`
				Nest struct {
					Slice []struct {
						Listfield string `json:"listfield"`
					} `json:"slice"`
				} `json:"nest"`
			}{},
			cue: `typ: {
				foo: string
				nest: {
					slice: [...{
						listfield: string
					}]
				}
			}
			`,
		},
		"listInStructDefault": {
			T: &struct {
				Foo  string `json:"foo"`
				Nest struct {
					Slice []string `json:"slice"`
				} `json:"nest"`
			}{},
			cue: `typ: {
				foo: string
				nest: {
					slice: [...string] | *["foo", "bar"]
				}
			}
			`,
		},
		"listInList": {
			T: &struct {
				Foo         string     `json:"foo"`
				DoubleSlice [][]string `json:"doubleslice"`
			}{},
			cue: `typ: {
				foo: string
				doubleslice: [...[...string]]
			}
			`,
		},
		"integerArch": {
			T: &struct {
				UintField uint `json:"uintField"`
				IntField  int  `json:"intField"`
			}{},
			cue: fmt.Sprintf(`typ: {
				uintField: uint%v
				intField: int%v
			}
			`, strconv.IntSize, strconv.IntSize),
		},
		// A Go int64 holds every value of a bounded CUE int, but not of an
		// unbounded one, whatever form the field takes (#304). A default is
		// one of the values a field may take, so it never stands for the rest.
		"intRegular": {
			T: &struct {
				P int64 `json:"p"`
			}{},
			cue: `typ: {
				p: int
			}
			`,
			refusal: "p: schema type int not subsumed by Go type int64",
		},
		"intRequired": {
			T: &struct {
				P int64 `json:"p"`
			}{},
			cue: `typ: {
				p!: int
			}
			`,
			refusal: "p!: schema type int not subsumed by Go type int64",
		},
		"intOptional": {
			T: &struct {
				P *int64 `json:"p,omitempty"`
			}{},
			cue: `typ: {
				p?: int
			}
			`,
			refusal: "p?: schema type int not subsumed by Go type int64",
		},
		"intDefault": {
			T: &struct {
				P int64 `json:"p"`
			}{},
			cue: `typ: {
				p: *3 | int
			}
			`,
			refusal: "p: schema type *3 | int not subsumed by Go type int64",
		},
		"intOptionalDefault": {
			T: &struct {
				P *int64 `json:"p,omitempty"`
			}{},
			cue: `typ: {
				p?: *3 | int
			}
			`,
			refusal: "p?: schema type *3 | int not subsumed by Go type int64",
		},
		"intDefaultInDisjunction": {
			T: &struct {
				P int64 `json:"p"`
			}{},
			cue: `typ: {
				p: *3 | 4 | int
			}
			`,
			refusal: "p: schema type *3 | 4 | int not subsumed by Go type int64",
		},
		"intDefaultRef": {
			T: &struct {
				X int64 `json:"x"`
				P int64 `json:"p"`
			}{},
			cue: `typ: {
				x: 3
				p: *x | int
			}
			`,
			refusal: "p: schema type *3 | int not subsumed by Go type int64",
		},
		"intDefaultNested": {
			T: &struct {
				Nest struct {
					P int64 `json:"p"`
				} `json:"nest"`
			}{},
			cue: `typ: {
				nest: {
					p: *3 | int
				}
			}
			`,
			refusal: "nest.p: schema type *3 | int not subsumed by Go type int64",
		},
		"intDefaultListElem": {
			T: &struct {
				L []int64 `json:"l"`
			}{},
			cue: `typ: {
				l: [...(*3 | int)]
			}
			`,
			refusal: "l.[_]: schema type *3 | int not subsumed by Go type int64",
		},
		"intDefaultListPrefix": {
			T: &struct {
				L []int64 `json:"l"`
			}{},
			cue: `typ: {
				l: [*3 | int, ...(*4 | int)]
			}
			`,
			refusal: "l.[_]: schema type *4 | int not subsumed by Go type int64",
		},
		"intDefaultArrayElem": {
			T: &struct {
				L [2]int64 `json:"l"`
			}{},
			cue: `typ: {
				l: [*3 | int, *4 | int]
			}
			`,
			refusal: "l.[_]: schema type *4 | int not subsumed by Go type int64",
		},
		"structDefault": {
			T: &struct {
				Nest struct {
					P int64 `json:"p"`
				} `json:"nest"`
			}{},
			cue: `typ: {
				nest: {p: int} | *{p: 3}
			}
			`,
			refusal: nestUnbounded,
		},
		"structDefaultRef": {
			T: &struct {
				Nest struct {
					P int64 `json:"p"`
				} `json:"nest"`
			}{},
			cue: `#S: {p: int}
			#D: {p: 3}
			typ: {
				nest: #S | *#D
			}
			`,
			refusal: nestUnbounded,
		},
		"structDefaultRefBounded": {
			T: &struct {
				Nest struct {
					P int64 `json:"p"`
				} `json:"nest"`
			}{},
			cue: `#S: {p: int64}
			#D: {p: 3}
			typ: {
				nest: #S | *#D
			}
			`,
		},
		"structDefaultOfDefinition": {
			T: &struct {
				Nest struct {
					P int64 `json:"p"`
				} `json:"nest"`
			}{},
			cue: `#S: {p: int} | *{p: 3}
			typ: {
				nest: #S
			}
			`,
			refusal: nestUnbounded,
		},
		"structDefaultConjunct": {
			T: &struct {
				Nest struct {
					P int64 `json:"p"`
				} `json:"nest"`
			}{},
			cue: `typ: {
				nest: {p: int} & ({p: int} | *{p: 3})
			}
			`,
			refusal: nestUnbounded,
		},
		"structDefaultConjunction": {
			T: &struct {
				Nest struct {
					P int64 `json:"p"`
					Q int64 `json:"q"`
				} `json:"nest"`
			}{},
			cue: `typ: {
				nest: ({p: int} & {q: int64}) | *{p: 3, q: 4}
			}
			`,
			refusal: nestUnbounded,
		},
		"structDefaultConjunctionBounded": {
			T: &struct {
				Nest struct {
					P int64 `json:"p"`
					Q int64 `json:"q"`
				} `json:"nest"`
			}{},
			cue: `typ: {
				nest: ({p: int64} & {q: int64}) | *{p: 3, q: 4}
			}
			`,
		},
		"structDefaultOverDisjunction": {
			T: &struct {
				Nest struct {
					P int64 `json:"p"`
				} `json:"nest"`
			}{},
			cue: `typ: {
				nest: {p: int64} & ({p: 3} | *{p: 4})
			}
			`,
			refusal: "nest: contains disjunction over struct types, but Go type is not any",
		},
		"structDefaultLet": {
			T: &struct {
				Nest struct {
					P int64 `json:"p"`
				} `json:"nest"`
			}{},
			cue: `let S = {p: int} | *{p: 4}
			typ: {
				nest: S
			}
			`,
			refusal: nestUnread,
		},
		"structDefaultLetDisjunction": {
			T: &struct {
				Nest struct {
					P int64 `json:"p"`
				} `json:"nest"`
			}{},
			cue: `let S = {p: int} | *{p: 4}
			typ: {
				nest: S | *{p: 4}
			}
			`,
			refusal: nestUnread,
		},
		// The type of a call cannot be read without the default, so this is
		// refused, though close({p: int64}) holds only int64 values.
		"structDefaultClose": {
			T: &struct {
				Nest struct {
					P int64 `json:"p"`
				} `json:"nest"`
			}{},
			cue: `typ: {
				nest: close({p: int64}) | *close({p: 3})
			}
			`,
			refusal: nestUnread,
		},
		"listDefaultLetSelector": {
			T: &struct {
				L []int64 `json:"l"`
			}{},
			cue: `let X = {l: [...int64]}
			typ: {
				l: X.l | *[1]
			}
			`,
			refusal: "l: schema has a default in a form assignability cannot check, may only correspond to interface{}/any",
		},
		"listOfDefinition": {
			T: &struct {
				L []int64 `json:"l"`
			}{},
			cue: `#L: [...int64]
			typ: {
				l: #L
			}
			`,
		},
		"listElemDefaultsDiffer": {
			T: &struct {
				L []string `json:"l"`
			}{},
			cue: `typ: {
				l: [*"a" | string, ...(*"b" | string)]
			}
			`,
		},
		"arrayElemDefaultsDiffer": {
			T: &struct {
				L [2]int64 `json:"l"`
			}{},
			cue: `typ: {
				l: [*3 | int64, *4 | int64]
			}
			`,
		},
		"listDefaultOtherType": {
			T: &struct {
				Slice []string `json:"slice"`
			}{},
			cue: `typ: {
				slice: [...string] | *[1]
			}
			`,
			refusal: "slice: schema is a complex disjunction of list types, may only correspond to interface{}/any",
		},
		"listDefaultTwoTypes": {
			T: &struct {
				L []int64 `json:"l"`
			}{},
			cue: `typ: {
				l: [...int64] | [...string] | *[1]
			}
			`,
			refusal: "l: schema is a complex disjunction of list types, may only correspond to interface{}/any",
		},
		"listDefaultUnbounded": {
			T: &struct {
				L []int64 `json:"l"`
			}{},
			cue: `typ: {
				l: [...int] | *[1]
			}
			`,
			refusal: "l.[_]: schema type int not subsumed by Go type int64",
		},
		"listDefaultRef": {
			T: &struct {
				L []int64 `json:"l"`
			}{},
			cue: `#L: [...int64]
			typ: {
				l: #L | *[1]
			}
			`,
		},
		"listDefaultOfDefinition": {
			T: &struct {
				L []int64 `json:"l"`
			}{},
			cue: `#L: [...int64] | *[1]
			typ: {
				l: #L
			}
			`,
		},
		"intBounded": {
			T: &struct {
				A int64 `json:"a"`
				B int64 `json:"b"`
				C int64 `json:"c"`
				D int64 `json:"d"`
			}{},
			cue: `typ: {
				a: int & >=0 & <=1000
				b: *3 | int & >=0 & <=1000
				c: int64
				d: *3 | int64
			}
			`,
		},
		"int32Bounded": {
			T: &struct {
				A int64  `json:"a"`
				B int64  `json:"b"`
				C *int64 `json:"c,omitempty"`
				D int64  `json:"d"`
			}{},
			cue: `typ: {
				a: int32
				b: *3 | int32
				c?: int32
				d: *3 | 4
			}
			`,
		},
		"intDefaultRefBounded": {
			T: &struct {
				X int64 `json:"x"`
				P int64 `json:"p"`
			}{},
			cue: `typ: {
				x: 3
				p: *x | int64
			}
			`,
		},
		"intDefaultNestedBounded": {
			T: &struct {
				Nest struct {
					P int64 `json:"p"`
				} `json:"nest"`
				L []int64 `json:"l"`
			}{},
			cue: `typ: {
				nest: {
					p: *3 | int64
				}
				l: [...(*3 | int64)]
			}
			`,
		},
		// Go's float64 encodes as number, so a default changes no float verdict:
		// number is accepted and float refused, with or without one.
		"floatNumber": {
			T: &struct {
				A float64 `json:"a"`
				B float64 `json:"b"`
				C float64 `json:"c"`
				D float64 `json:"d"`
			}{},
			cue: `typ: {
				a: number
				b: *3.0 | number
				c: float64
				d: *1.5 | float64
			}
			`,
		},
		"floatFloat": {
			T: &struct {
				A float64 `json:"a"`
			}{},
			cue: `typ: {
				a: float
			}
			`,
			refusal: "a: is kind float in schema, but kind number in Go type",
		},
		"floatFloatDefault": {
			T: &struct {
				A float64 `json:"a"`
			}{},
			cue: `typ: {
				a: *3.0 | float
			}
			`,
			refusal: "a: is kind float in schema, but kind number in Go type",
		},
		"or-null": {
			T: &struct {
				Both   *string `json:"both,omitempty"`
				NoOmit *string `json:"noOmit"`
				// This case is the ugly ambiguous one - is the user saying that an empty string
				// should be serialized as an absent field, but a nil pointer be serialized as
				// null? WAAAAAAAT
				Optional *string `json:"optional"`
			}{},
			cue: `typ: {
				both: string | null
				noOmit: string | null
				optional?: string | null
			}
			`,
		},
	}

	for name, tst := range tt {
		t.Run(name, func(t *testing.T) {
			f := func(def bool) func(t *testing.T) {
				return func(t *testing.T) {
					path, cuestr := "typ", tst.cue
					if def {
						cuestr = strings.Replace(cuestr, "typ", "#typ", 1)
						path = "#typ"
					}
					sch := ctx.CompileString(cuestr).LookupPath(cue.ParsePath(path))

					err := assignable(sch, tst.T)
					if tst.refusal != "" {
						if err == nil {
							t.Fatal("expected unassignable err")
						}
						if !strings.Contains(err.Error(), tst.refusal) {
							t.Fatalf("expected refusal %q, got:\n%s", tst.refusal, err)
						}
						return
					}

					if err != nil {
						t.Fatal(err)
					}
				}
			}
			t.Run("normal", f(false))
			t.Run("definition", f(true))
		})
	}
}

// TestBindTypeDefaultedInt checks the assignability rule through BindType, on
// the schema of a bound lineage: a default does not stand for the values a
// field may take (#304).
func TestBindTypeDefaultedInt(t *testing.T) {
	type P struct {
		P int64 `json:"p"`
	}
	for field, invalid := range map[string]bool{
		"p: *3 | int":            true,
		"p: *3 | int64":          false,
		"p: int64":               false,
		"let S = *3 | int\np: S": true,
	} {
		t.Run(field, func(t *testing.T) {
			lin := testLin(fmt.Sprintf(`name: "defaulted"
schemas: "0": "0": schema: {%s}
`, field))
			_, err := BindType(lin.First(), &P{})
			if invalid && (err == nil || !strings.Contains(err.Error(), "not subsumed by Go type int64")) {
				t.Fatalf("expected BindType to refuse the int as unbounded, got: %v", err)
			}
			if !invalid && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNoDeepPointer(t *testing.T) {
	typ := &struct{}{}
	assignerr := assignable(cue.Value{}, &typ)
	if assignerr == nil {
		t.Fatal("expected error when passing pointer with more than one level of indirection")
	}
	if !errors.Is(assignerr, ErrPointerDepth) {
		t.Fatal("unexpected error received when passing pointer with more than one level of indirection")
	}
}
