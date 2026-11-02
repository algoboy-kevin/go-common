package rowsink

// ResolveLayout returns the layout a component runs with: the zero Layout
// (both lists nil) means DefaultLayout(); anything else must pass Validate
// (a half-set or empty list is an error, never silently defaulted). The
// result owns fresh copies of both slices, so a caller mutating its config
// afterwards cannot change a running grid or sink.
func ResolveLayout(l Layout) (Layout, error) {
	if l.DepthBandsBps == nil && l.LadderEdgesBps == nil {
		l = DefaultLayout()
	}
	if err := l.Validate(); err != nil {
		return Layout{}, err
	}
	return Layout{DepthBandsBps: append([]float64(nil), l.DepthBandsBps...), LadderEdgesBps: append([]float64(nil), l.LadderEdgesBps...)}, nil
}

// selectedLayout implements the optional trailing layout argument of the CSV
// sink, readers and comparers: none or the zero Layout = DefaultLayout().
func selectedLayout(ls []Layout) Layout {
	if len(ls) == 0 || (ls[0].DepthBandsBps == nil && ls[0].LadderEdgesBps == nil) {
		return DefaultLayout()
	}
	return ls[0]
}
