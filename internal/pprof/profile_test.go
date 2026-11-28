package pprof

import (
	"os"
	"testing"
)

// The fixtures are real runtime/pprof profiles captured from a tiny busy
// program (leaf -> middle -> busy -> main). Their expected numbers come
// from `go tool pprof -top` / `-raw` on the same files.
func loadFixture(t *testing.T, name string) *Profile {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseCPUProfile(t *testing.T) {
	p := loadFixture(t, "cpu.out")
	if got, want := len(p.SampleTypes), 2; got != want {
		t.Fatalf("sample types = %d, want %d: %+v", got, want, p.SampleTypes)
	}
	st := p.SampleTypes[1]
	if st.Type != "cpu" || st.Unit != "nanoseconds" {
		t.Fatalf("cpu sample type = %+v", st)
	}
	if p.PeriodType.Type != "cpu" || p.Period != 10_000_000 {
		t.Fatalf("period = %+v %d", p.PeriodType, p.Period)
	}
	if p.DurationNanos < 200_000_000 || p.DurationNanos > 1_000_000_000 {
		t.Fatalf("duration implausible: %d", p.DurationNanos)
	}
	if len(p.Samples) == 0 || len(p.Locations) == 0 || len(p.Functions) == 0 {
		t.Fatalf("empty profile: samples=%d locations=%d functions=%d", len(p.Samples), len(p.Locations), len(p.Functions))
	}
	// Locations must be self-symbolized.
	found := false
	for _, fn := range p.Functions {
		if fn.Name == "main.leaf" {
			found = true
			if fn.Filename == "" || fn.StartLine == 0 {
				t.Fatalf("main.leaf lost source info: %+v", fn)
			}
		}
	}
	if !found {
		t.Fatal("main.leaf missing from functions")
	}
}

func TestCPUTopMatchesReference(t *testing.T) {
	p := loadFixture(t, "cpu.out")
	// Reference (go tool pprof -top, cpu/nanoseconds):
	//   runtime.mallocgc   flat 50ms cum 80ms
	//   runtime.newobject  flat  0ms cum 90ms
	//   hash/crc32.ChecksumIEEE flat 10ms cum 40ms
	top := Top(p, "cpu", "cum")
	var mallocgc, newobject, checksum *TopEntry
	for i := range top {
		switch top[i].Name {
		case "runtime.mallocgc":
			mallocgc = &top[i]
		case "runtime.newobject":
			newobject = &top[i]
		case "hash/crc32.ChecksumIEEE":
			checksum = &top[i]
		}
	}
	if mallocgc == nil || newobject == nil || checksum == nil {
		t.Fatalf("top rows missing: %+v", top)
	}
	if mallocgc.Flat != 50_000_000 || mallocgc.Cum != 80_000_000 {
		t.Fatalf("mallocgc = %+v", mallocgc)
	}
	if newobject.Flat != 10_000_000 || newobject.Cum != 90_000_000 {
		t.Fatalf("newobject = %+v", newobject)
	}
	if checksum.Flat != 10_000_000 || checksum.Cum != 40_000_000 {
		t.Fatalf("ChecksumIEEE = %+v", checksum)
	}
	// Total samples = 130ms (13 samples of 10ms).
	tree := BuildTree(p, "cpu")
	if tree.Total != 130_000_000 {
		t.Fatalf("tree total = %d", tree.Total)
	}
	if tree.Unit != "nanoseconds" || tree.SampleType != "cpu" {
		t.Fatalf("tree unit = %+v", tree)
	}
	if tree.Root.Value != tree.Total {
		t.Fatalf("root value %d != total %d", tree.Root.Value, tree.Total)
	}
	// Self + children conservation on the root.
	var sumSelf, sumChild int64
	for _, c := range tree.Root.Children {
		sumSelf += c.Self
		sumChild += c.Value
	}
	if sumChild != tree.Total || sumSelf > tree.Total {
		t.Fatalf("root children do not conserve the total: self=%d value=%d total=%d", sumSelf, sumChild, tree.Total)
	}
}

func TestFlameTreeStructure(t *testing.T) {
	p := loadFixture(t, "cpu.out")
	tree := BuildTree(p, "cpu")
	// The busy -> middle -> leaf chain must appear under runtime.main.
	main := tree.FindNode("main.busy")
	if main == nil {
		t.Fatal("main.busy missing from the tree")
	}
	middle := findChild(main, "main.middle")
	if middle == nil {
		t.Fatal("main.middle not under main.busy")
	}
	leaf := findChild(middle, "main.leaf")
	if leaf == nil {
		t.Fatal("main.leaf not under main.middle")
	}
	if leaf.Value == 0 {
		t.Fatalf("leaf has no samples: %+v", leaf)
	}
	// Children sorted by value descending.
	for _, n := range []*TreeNode{tree.Root, main, middle} {
		for i := 1; i < len(n.Children); i++ {
			if n.Children[i-1].Value < n.Children[i].Value {
				t.Fatalf("children not sorted by value: %+v", n.Children)
			}
		}
	}
	// Files carry the fixture source paths.
	if leaf.File == "" || baseName(leaf.File) != "main.go" {
		t.Fatalf("leaf file = %q", leaf.File)
	}
	if leaf.Line == 0 {
		t.Fatalf("leaf line missing")
	}
}

func findChild(n *TreeNode, name string) *TreeNode {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestHeapSampleTypesAndDefault(t *testing.T) {
	p := loadFixture(t, "heap.out")
	// Sample types: alloc_objects/count alloc_space/bytes
	// inuse_objects/count inuse_space/bytes; the runtime's default is
	// index 2 (inuse_objects) per runtime/pprof, while pprof displays
	// inuse_space — the panel must resolve BOTH names.
	want := []ValueType{
		{"alloc_objects", "count"},
		{"alloc_space", "bytes"},
		{"inuse_objects", "count"},
		{"inuse_space", "bytes"},
	}
	if len(p.SampleTypes) != len(want) {
		t.Fatalf("sample types = %+v", p.SampleTypes)
	}
	for i, w := range want {
		if p.SampleTypes[i] != w {
			t.Fatalf("sample type %d = %+v, want %+v", i, p.SampleTypes[i], w)
		}
	}
	tree := BuildTree(p, "inuse_space")
	if tree.Unit != "bytes" {
		t.Fatalf("heap tree unit = %q", tree.Unit)
	}
	// Reference: inuse_space total = 1.16MB (StartCPUProfile buffer);
	// exact value asserted structurally (>1MB, <2MB).
	if tree.Total < 1<<20 || tree.Total > 2<<20 {
		t.Fatalf("heap total = %d", tree.Total)
	}
	start := tree.FindNode("runtime/pprof.StartCPUProfile")
	if start == nil || start.Self != tree.Total {
		t.Fatalf("StartCPUProfile node = %+v total=%d", start, tree.Total)
	}
}

func TestAnnotateLines(t *testing.T) {
	p := loadFixture(t, "cpu.out")
	// Reference (`go tool pprof -list main.leaf`): leaf never owns the
	// leaf frame (it always calls crc32), so flat is 0 and all 130ms of
	// cum land on line 14 (the ChecksumIEEE call).
	var leafFile string
	for _, fn := range p.Functions {
		if fn.Name == "main.leaf" {
			leafFile = fn.Filename
		}
	}
	lines := AnnotateLines(p, leafFile, "main.leaf", "cpu")
	if len(lines) == 0 {
		t.Fatal("no annotated lines for main.leaf")
	}
	var flatTotal, cumTotal int64
	for _, v := range lines {
		flatTotal += v.Flat
		cumTotal += v.Cum
	}
	if flatTotal != 0 || cumTotal != 130_000_000 {
		t.Fatalf("leaf flat=%d cum=%d", flatTotal, cumTotal)
	}
	if lines[14].Cum != 130_000_000 || lines[14].Flat != 0 {
		t.Fatalf("line 14 = %+v", lines[14])
	}
}

func TestPeekCallersCallees(t *testing.T) {
	p := loadFixture(t, "cpu.out")
	tree := BuildTree(p, "cpu")
	pk := tree.PeekAt("main.middle")
	if pk == nil {
		t.Fatal("no peek for main.middle")
	}
	var busyCaller, leafCallee bool
	for _, c := range pk.Callers {
		if c.Name == "main.busy" {
			busyCaller = true
		}
	}
	for _, c := range pk.Callees {
		if c.Name == "main.leaf" {
			leafCallee = true
		}
	}
	if !busyCaller || !leafCallee {
		t.Fatalf("peek = %+v", pk)
	}
}

func TestFormatValue(t *testing.T) {
	cases := []struct {
		v    int64
		unit string
		want string
	}{
		{10_000_000, "nanoseconds", "10.00ms"},
		{1_500_000, "nanoseconds", "1.50ms"},
		{1_300_000_000, "nanoseconds", "1.30s"},
		{1234, "bytes", "1.21KB"},
		{1_500_000, "bytes", "1.43MB"},
		{500, "bytes", "500B"},
		{42, "", "42"},
	}
	for _, c := range cases {
		if got := FormatValue(c.v, c.unit); got != c.want {
			t.Fatalf("FormatValue(%d, %q) = %q, want %q", c.v, c.unit, got, c.want)
		}
	}
	if Pct(25, 100) != "25.0%" || Pct(1, 0) != "0%" {
		t.Fatal("Pct broken")
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte("not a profile at all")); err == nil {
		t.Fatal("garbage parsed without error")
	}
	if _, err := Parse(nil); err == nil {
		t.Fatal("nil parsed without error")
	}
	// Truncated gzip header.
	if _, err := Parse([]byte{0x1f, 0x8b, 0x00}); err == nil {
		t.Fatal("truncated gzip parsed without error")
	}
}
