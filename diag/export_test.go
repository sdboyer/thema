package diag

// ShapeInfo describes one entry of the shapes table to the external tests.
type ShapeInfo struct {
	Name, Msg string
	Kernel    bool
}

// Shapes lists the shapes table for the menagerie's coverage test, with the
// empty-disjunction header, which [From] matches on its own.
func Shapes() []ShapeInfo {
	out := make([]ShapeInfo, 0, len(shapes)+1)
	for _, s := range shapes {
		out = append(out, ShapeInfo{Name: s.name, Msg: s.msg, Kernel: s.kernel})
	}
	return append(out, ShapeInfo{Name: "disjunction-header", Msg: msgEmptyDisjunction})
}
