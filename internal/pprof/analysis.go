// analysis.go turns parsed profiles into the Profiler panel's views: the
// flame tree (icicle layout data), the top table, per-line annotated
// source and caller/callee lists for the peek view.
package pprof

import (
	"sort"
	"strconv"
	"strings"
)

// TreeNode is one flame-graph frame. Children are sorted by cumulative
// value (descending) so the widest arm renders first — the classic icicle
// layout. Value carries the selected sample index's sum; Self excludes
// children.
type TreeNode struct {
	Name     string
	File     string
	Line     int64 // deepest known source line (for the jump)
	FuncID   uint64
	Value    int64
	Self     int64
	Children []*TreeNode
}

// Tree is the aggregated flame tree for one sample type.
type Tree struct {
	Root       *TreeNode // synthetic total node
	Total      int64
	SampleType string
	Unit       string
	Period     int64
	Duration   int64
	SampleN    int
}

// lineOf returns the deepest inline source function and line for a
// location id.
func (p *Profile) lineOf(locID uint64) (Function, int64, bool) {
	for i := range p.Locations {
		if p.Locations[i].ID == locID {
			if len(p.Locations[i].Lines) == 0 {
				return Function{}, 0, false
			}
			line := p.Locations[i].Lines[0]
			for j := range p.Functions {
				if p.Functions[j].ID == line.FunctionID {
					return p.Functions[j], line.Line, true
				}
			}
			return Function{}, line.Line, true
		}
	}
	return Function{}, 0, false
}

// inlineChain resolves every line of a location (the innermost frame
// first, matching pprof's inline expansion) with its source line.
func (p *Profile) inlineChain(locID uint64) []resolvedLine {
	for i := range p.Locations {
		if p.Locations[i].ID != locID {
			continue
		}
		out := make([]resolvedLine, 0, len(p.Locations[i].Lines))
		for _, l := range p.Locations[i].Lines {
			rl := resolvedLine{Line: l.Line}
			for j := range p.Functions {
				if p.Functions[j].ID == l.FunctionID {
					rl.Fn = p.Functions[j]
					break
				}
			}
			out = append(out, rl)
		}
		return out
	}
	return nil
}

type resolvedLine struct {
	Fn   Function
	Line int64
}

// sampleIndexOf resolves a sample type name to its value column.
func sampleIndexOf(p *Profile, name string) int {
	if name == "" && p.DefaultType >= 0 && p.DefaultType < len(p.SampleTypes) {
		return p.DefaultType
	}
	for i, st := range p.SampleTypes {
		if st.Type == name {
			return i
		}
	}
	if p.DefaultType >= 0 && p.DefaultType < len(p.SampleTypes) {
		return p.DefaultType
	}
	return 0
}

// BuildTree aggregates the profile into a flame tree for the named sample
// type ("" = profile default). Stacks merge root-first with consecutive
// recursion collapsed (the flame-graph convention).
func BuildTree(p *Profile, sampleType string) *Tree {
	idx := sampleIndexOf(p, sampleType)
	name, unit := "", ""
	if idx < len(p.SampleTypes) {
		name, unit = p.SampleTypes[idx].Type, p.SampleTypes[idx].Unit
	}
	t := &Tree{
		Root:       &TreeNode{Name: "root"},
		SampleType: name,
		Unit:       unit,
		Period:     p.Period,
		Duration:   p.DurationNanos,
		SampleN:    len(p.Samples),
	}
	root := t.Root
	for si := range p.Samples {
		s := &p.Samples[si]
		var v int64
		if idx >= 0 && idx < len(s.Values) {
			v = s.Values[idx]
		}
		if v == 0 {
			continue
		}
		t.Total += v
		// leaf-first -> root-first path, collapsing consecutive recursion
		var path []uint64
		prev := uint64(0)
		for li := len(s.Locations) - 1; li >= 0; li-- {
			id := s.Locations[li]
			if id == 0 || id == prev {
				continue
			}
			prev = id
			path = append(path, id)
		}
		node := root
		node.Value += v
		for _, locID := range path {
			fn, line, _ := p.lineOf(locID)
			frameName := fn.Name
			if frameName == "" {
				frameName = "@" + strconv.FormatUint(locID, 10)
			}
			var child *TreeNode
			for _, c := range node.Children {
				if c.Name == frameName {
					child = c
					break
				}
			}
			if child == nil {
				child = &TreeNode{Name: frameName, File: fn.Filename, FuncID: locID}
				node.Children = append(node.Children, child)
			}
			if line > 0 {
				child.Line = line
			}
			child.Value += v
			node = child
		}
		node.Self += v
	}
	sortTree(root)
	return t
}

// sortTree orders children by value descending (ties by name), recursively.
func sortTree(n *TreeNode) {
	sort.SliceStable(n.Children, func(i, j int) bool {
		if n.Children[i].Value != n.Children[j].Value {
			return n.Children[i].Value > n.Children[j].Value
		}
		return n.Children[i].Name < n.Children[j].Name
	})
	for _, c := range n.Children {
		sortTree(c)
	}
}

// TopEntry is one row of the top table (Flat = own value, Cum = stack total).
type TopEntry struct {
	Name string
	File string
	Line int64
	Flat int64
	Cum  int64
}

// Top computes the function-level flat/cum table straight from the sample
// stacks (pprof semantics: a sample counts toward a function's cum once,
// inline frames included, and toward flat only through the leaf frame's
// innermost function). column is "flat" or "cum".
func Top(p *Profile, sampleType, column string) []TopEntry {
	idx := sampleIndexOf(p, sampleType)
	m := map[string]*TopEntry{}
	for si := range p.Samples {
		s := &p.Samples[si]
		var v int64
		if idx >= 0 && idx < len(s.Values) {
			v = s.Values[idx]
		}
		if v == 0 {
			continue
		}
		seen := map[string]bool{}
		for li, locID := range s.Locations { // leaf first
			chain := p.inlineChain(locID)
			for ci, rl := range chain {
				name := rl.Fn.Name
				if name == "" {
					name = "@" + strconv.FormatUint(locID, 10)
				}
				if seen[name] {
					continue
				}
				seen[name] = true
				e := m[name]
				if e == nil {
					e = &TopEntry{Name: name, File: rl.Fn.Filename, Line: rl.Line}
					m[name] = e
				}
				e.Cum += v
				if li == 0 && ci == 0 {
					e.Flat += v
				}
			}
		}
	}
	out := make([]TopEntry, 0, len(m))
	for _, e := range m {
		out = append(out, *e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if column == "flat" {
			if out[i].Flat != out[j].Flat {
				return out[i].Flat > out[j].Flat
			}
		} else {
			if out[i].Cum != out[j].Cum {
				return out[i].Cum > out[j].Cum
			}
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// SourceLine is one annotated source line for the source view.
type SourceLine struct {
	Line int
	Text string
	Flat int64
	Cum  int64
}

// FuncSource is the source view model: a function plus per-line values.
type FuncSource struct {
	Name  string
	File  string
	Lines []SourceLine
	Flat  int64
	Cum   int64
}

// AnnotateLines computes per-line flat/cum values for a function from the
// raw stacks (the same semantics as `go tool pprof -list`): flat lands on
// the leaf frame's innermost line, cum on every line the function's
// frames occupy (once per sample per line).
func AnnotateLines(p *Profile, fileName, funcName, sampleType string) map[int]SourceLineValue {
	idx := sampleIndexOf(p, sampleType)
	flat, cum := map[int]int64{}, map[int]int64{}
	for si := range p.Samples {
		s := &p.Samples[si]
		var v int64
		if idx >= 0 && idx < len(s.Values) {
			v = s.Values[idx]
		}
		if v == 0 {
			continue
		}
		seenLine := map[int]bool{}
		for li, locID := range s.Locations { // leaf first
			chain := p.inlineChain(locID)
			for ci, rl := range chain {
				if rl.Fn.Name != funcName {
					continue
				}
				if fileName != "" && rl.Fn.Filename != "" && !sameBase(rl.Fn.Filename, fileName) {
					continue
				}
				ln := int(rl.Line)
				if li == 0 && ci == 0 {
					flat[ln] += v
				}
				if !seenLine[ln] {
					cum[ln] += v
					seenLine[ln] = true
				}
			}
		}
	}
	out := map[int]SourceLineValue{}
	for line := range cum {
		out[line] = SourceLineValue{Line: line, Flat: flat[line], Cum: cum[line]}
	}
	return out
}

// SourceLineValue is one line's aggregated values.
type SourceLineValue struct {
	Line int
	Flat int64
	Cum  int64
}

func sameBase(a, b string) bool { return baseName(a) == baseName(b) }

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Peek is the callers/callees view for one function.
type Peek struct {
	Function string
	Callers  []TopEntry
	Callees  []TopEntry
}

// PeekAt computes the callers (parents) and callees (children) of a
// function in the flame tree, aggregated across every occurrence. Entry
// Flat carries the shared (caller->callee) value.
func (t *Tree) PeekAt(funcName string) *Peek {
	pk := &Peek{Function: funcName}
	parents, children := map[string]*TopEntry{}, map[string]*TopEntry{}
	entry := func(m map[string]*TopEntry, name, file string, line int64, v int64) {
		e := m[name]
		if e == nil {
			e = &TopEntry{Name: name, File: file, Line: line}
			m[name] = e
		}
		e.Flat += v
	}
	var walk func(n *TreeNode)
	walk = func(n *TreeNode) {
		for _, c := range n.Children {
			if n != t.Root {
				if c.Name == funcName {
					entry(parents, n.Name, n.File, n.Line, c.Value)
				}
				if n.Name == funcName {
					entry(children, c.Name, c.File, c.Line, c.Value)
				}
			}
			walk(c)
		}
	}
	walk(t.Root)
	for _, e := range parents {
		pk.Callers = append(pk.Callers, *e)
	}
	for _, e := range children {
		pk.Callees = append(pk.Callees, *e)
	}
	sort.SliceStable(pk.Callers, func(i, j int) bool { return pk.Callers[i].Flat > pk.Callers[j].Flat })
	sort.SliceStable(pk.Callees, func(i, j int) bool { return pk.Callees[i].Flat > pk.Callees[j].Flat })
	return pk
}

// FindNode locates the highest-value tree node for a function name.
func (t *Tree) FindNode(funcName string) *TreeNode {
	var best *TreeNode
	var walk func(n *TreeNode)
	walk = func(n *TreeNode) {
		if n.Name == funcName && (best == nil || n.Value > best.Value) {
			best = n
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(t.Root)
	return best
}

// FormatValue renders a value with its unit for panel display.
func FormatValue(v int64, unit string) string {
	switch unit {
	case "nanoseconds":
		if v >= 1e9 {
			return trim1(float64(v)/1e9) + "s"
		}
		if v >= 1e6 {
			return trim1(float64(v)/1e6) + "ms"
		}
		if v >= 1e3 {
			return trim1(float64(v)/1e3) + "µs"
		}
		return strconv.FormatInt(v, 10) + "ns"
	case "milliseconds":
		if v >= 1e3 {
			return trim1(float64(v)/1e3) + "s"
		}
		return strconv.FormatInt(v, 10) + "ms"
	case "bytes":
		if v >= 1<<20 {
			return trim1(float64(v)/(1<<20)) + "MB"
		}
		if v >= 1<<10 {
			return trim1(float64(v)/(1<<10)) + "KB"
		}
		return strconv.FormatInt(v, 10) + "B"
	default:
		return strconv.FormatInt(v, 10)
	}
}

func trim1(f float64) string {
	return strconv.FormatFloat(f, 'f', 2, 64)
}

// ShortFunc trims a symbol's package path for one row (the tooltip shows
// the full name).
func ShortFunc(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// Pct formats a ratio (0..1) as a percent string.
func Pct(part, total int64) string {
	if total == 0 {
		return "0%"
	}
	return strconv.FormatFloat(float64(part)/float64(total)*100, 'f', 1, 64) + "%"
}
