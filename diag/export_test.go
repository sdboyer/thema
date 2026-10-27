package diag

// ShapeInfo describes one entry of the shapes table to the external tests.
type ShapeInfo struct{ Name, Msg string }

// Shapes lists the shapes table for the menagerie's coverage test.
func Shapes() []ShapeInfo {
	out := make([]ShapeInfo, len(shapes))
	for i, s := range shapes {
		out[i] = ShapeInfo{Name: s.name, Msg: s.msg}
	}
	return out
}
