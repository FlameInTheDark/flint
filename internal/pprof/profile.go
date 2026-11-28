// Package pprof reads Go pprof profiles (the compressed protobuf format
// served by runtime/pprof's HTTP endpoints and written by -cpuprofile
// flags) and turns them into the views the Profiler panel renders: a flame
// tree, a top table, per-line annotated source and caller/callee lists.
// The parser is dependency-free on purpose: the project has no protobuf
// runtime, and the profile schema is a small, stable subset of protobuf
// (varints, fixed64s, length-delimited fields, packed repeated scalars).
package pprof

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ValueType names one sample value dimension ("cpu"/"nanoseconds",
// "inuse_space"/"bytes", ...).
type ValueType struct {
	Type string
	Unit string
}

// Label is one sample tag: either a string key/value pair (both string-
// table indexes until resolveStrings) or a numeric key/value pair.
type Label struct {
	KeyIdx int
	StrIdx int
	Num    int64
}

// Sample is one recorded sample: a leaf-to-root location stack, the values
// for each sample type, and any labels (e.g. goroutine tags).
type Sample struct {
	Locations []uint64 // leaf first, root last (profile.proto order)
	Values    []int64
	Labels    []Label
}

// Function is one symbol (after inline expansion, function granularity).
type Function struct {
	ID         uint64
	Name       string
	SystemName string
	Filename   string
	StartLine  int64
}

// Line is one source line of a location (inline frames included).
type Line struct {
	FunctionID uint64
	Line       int64
	Column     int64
}

// Location is one instruction address with its inline chain.
type Location struct {
	ID        uint64
	MappingID uint64
	Address   uint64
	Lines     []Line
}

// Mapping describes one loaded binary (for the binary name display).
type Mapping struct {
	ID      uint64
	File    string
	BuildID string
}

// Profile is the parsed protobuf profile with strings resolved.
type Profile struct {
	SampleTypes   []ValueType
	DefaultType   int
	PeriodType    ValueType
	Period        int64
	TimeNanos     int64
	DurationNanos int64
	Samples       []Sample
	Locations     []Location
	Functions     []Function
	Mappings      []Mapping
	StringTable   []string
}

// Parse decodes a gzipped (or raw) protobuf profile and resolves its
// string table.
func Parse(data []byte) (*Profile, error) {
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		raw, err := io.ReadAll(zr)
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		data = raw
	}
	p := &Profile{}
	if err := parseProfile(data, p); err != nil {
		return nil, err
	}
	if len(p.Samples) == 0 && len(p.Locations) == 0 && len(p.StringTable) == 0 {
		return nil, errors.New("pprof: not a pprof profile (no samples)")
	}
	p.resolveStrings()
	return p, nil
}

// ---------------------------------------------------------------------------
// Minimal protobuf reader (profile.proto subset only)
// ---------------------------------------------------------------------------

var errTruncated = errors.New("pprof: truncated profile")

type field struct {
	num  int
	wire int
	v    uint64 // varint / fixed64 little-endian
	data []byte // length-delimited
}

// readField reads the next protobuf field header and payload.
func readField(b []byte) (field, []byte, error) {
	if len(b) == 0 {
		return field{}, nil, errTruncated
	}
	key, n := binary.Uvarint(b)
	if n <= 0 {
		return field{}, nil, errors.New("pprof: bad field key")
	}
	b = b[n:]
	f := field{num: int(key >> 3), wire: int(key & 7)}
	switch f.wire {
	case 0: // varint
		v, n := binary.Uvarint(b)
		if n <= 0 {
			return field{}, nil, errTruncated
		}
		f.v, b = v, b[n:]
	case 1: // fixed64
		if len(b) < 8 {
			return field{}, nil, errTruncated
		}
		f.v, b = binary.LittleEndian.Uint64(b), b[8:]
	case 2: // length-delimited
		l, n := binary.Uvarint(b)
		if n <= 0 || uint64(len(b)-n) < l {
			return field{}, nil, errTruncated
		}
		f.data, b = b[n:n+int(l)], b[n+int(l):]
	case 5: // fixed32
		if len(b) < 4 {
			return field{}, nil, errTruncated
		}
		f.v, b = uint64(binary.LittleEndian.Uint32(b)), b[4:]
	default:
		return field{}, nil, fmt.Errorf("pprof: unsupported wire type %d", f.wire)
	}
	return f, b, nil
}

// int64FromUvarint reinterprets a varint as the proto int64 two's
// complement (plain varint, NOT zigzag: profile.proto uses int64).
func int64FromUvarint(v uint64) int64 { return int64(v) }

func parseProfile(b []byte, p *Profile) error {
	for len(b) > 0 {
		f, rest, err := readField(b)
		if err != nil {
			return err
		}
		b = rest
		switch f.num {
		case 1: // sample_type
			if vt, err := parseValueType(f.data); err == nil {
				p.SampleTypes = append(p.SampleTypes, vt)
			}
		case 2: // sample
			if s, err := parseSample(f.data); err == nil {
				p.Samples = append(p.Samples, s)
			} else {
				return err
			}
		case 3: // mapping
			if m, err := parseMapping(f.data); err == nil {
				p.Mappings = append(p.Mappings, m)
			}
		case 4: // location
			if l, err := parseLocation(f.data); err == nil {
				p.Locations = append(p.Locations, l)
			}
		case 5: // function
			if fn, err := parseFunction(f.data); err == nil {
				p.Functions = append(p.Functions, fn)
			}
		case 6: // string_table
			p.StringTable = append(p.StringTable, string(f.data))
		case 9:
			p.TimeNanos = int64FromUvarint(f.v)
		case 10:
			p.DurationNanos = int64FromUvarint(f.v)
		case 11: // period_type
			if vt, err := parseValueType(f.data); err == nil {
				p.PeriodType = vt
			}
		case 12:
			p.Period = int64FromUvarint(f.v)
		case 14:
			p.DefaultType = int(f.v)
		}
	}
	return nil
}

// sub-message parsers keep string-table INDEXES; resolveStrings swaps them
// for the real strings afterwards.

func parseValueType(b []byte) (ValueType, error) {
	vt := ValueType{}
	for len(b) > 0 {
		f, rest, err := readField(b)
		if err != nil {
			return vt, err
		}
		b = rest
		switch f.num {
		case 1:
			vt.Type = idxString(int(f.v))
		case 2:
			vt.Unit = idxString(int(f.v))
		}
	}
	return vt, nil
}

// idxString encodes a string-table index as a string placeholder without
// allocating per call (resolveStrings maps it back). Indexes are < table
// size, so a single marker byte plus the value is unambiguous: real
// strings never pass through here.
func idxString(i int) string {
	return string([]byte{0, byte(i), byte(i >> 8), byte(i >> 16), byte(i >> 24)})
}

func (p *Profile) str(idx string) string {
	if len(idx) == 5 && idx[0] == 0 {
		i := int(uint32(idx[1]) | uint32(idx[2])<<8 | uint32(idx[3])<<16 | uint32(idx[4])<<24)
		if i >= 0 && i < len(p.StringTable) {
			return p.StringTable[i]
		}
		return ""
	}
	return idx
}

func parseSample(b []byte) (Sample, error) {
	s := Sample{}
	for len(b) > 0 {
		f, rest, err := readField(b)
		if err != nil {
			return s, err
		}
		b = rest
		switch f.num {
		case 1: // location_id: packed (wire 2) or individual varints (wire 0)
			if f.wire == 2 {
				s.Locations = append(s.Locations, unpackUint64s(f.data)...)
			} else {
				s.Locations = append(s.Locations, f.v)
			}
		case 2: // value: the runtime writer emits int64s unpacked (wire 0)
			if f.wire == 2 {
				s.Values = append(s.Values, unpackInt64s(f.data)...)
			} else {
				s.Values = append(s.Values, int64FromUvarint(f.v))
			}
		case 3: // label
			lab := Label{}
			rest2 := f.data
			for len(rest2) > 0 {
				lf, lrest, err := readField(rest2)
				if err != nil {
					return s, err
				}
				rest2 = lrest
				switch lf.num {
				case 1:
					lab.KeyIdx = int(lf.v)
				case 2:
					lab.StrIdx = int(lf.v)
				case 3:
					lab.Num = int64FromUvarint(lf.v)
				}
			}
			s.Labels = append(s.Labels, lab)
		}
	}
	return s, nil
}

func parseMapping(b []byte) (Mapping, error) {
	m := Mapping{}
	for len(b) > 0 {
		f, rest, err := readField(b)
		if err != nil {
			return m, err
		}
		b = rest
		switch f.num {
		case 1:
			m.ID = f.v
		case 5:
			m.File = idxString(int(f.v))
		case 6:
			m.BuildID = idxString(int(f.v))
		}
	}
	return m, nil
}

func parseLocation(b []byte) (Location, error) {
	l := Location{}
	for len(b) > 0 {
		f, rest, err := readField(b)
		if err != nil {
			return l, err
		}
		b = rest
		switch f.num {
		case 1:
			l.ID = f.v
		case 2:
			l.MappingID = f.v
		case 3:
			l.Address = f.v
		case 4: // line
			var line Line
			rest2 := f.data
			for len(rest2) > 0 {
				lf, lrest, err := readField(rest2)
				if err != nil {
					return l, err
				}
				rest2 = lrest
				switch lf.num {
				case 1:
					line.FunctionID = lf.v
				case 2:
					line.Line = int64FromUvarint(lf.v)
				case 3:
					line.Column = int64FromUvarint(lf.v)
				}
			}
			l.Lines = append(l.Lines, line)
		}
	}
	return l, nil
}

func parseFunction(b []byte) (Function, error) {
	fn := Function{}
	for len(b) > 0 {
		f, rest, err := readField(b)
		if err != nil {
			return fn, err
		}
		b = rest
		switch f.num {
		case 1:
			fn.ID = f.v
		case 2:
			fn.Name = idxString(int(f.v))
		case 3:
			fn.SystemName = idxString(int(f.v))
		case 4:
			fn.Filename = idxString(int(f.v))
		case 5:
			fn.StartLine = int64FromUvarint(f.v)
		}
	}
	return fn, nil
}

func unpackUint64s(b []byte) []uint64 {
	out := []uint64{}
	for len(b) > 0 {
		v, n := binary.Uvarint(b)
		if n <= 0 {
			break
		}
		out = append(out, v)
		b = b[n:]
	}
	return out
}

func unpackInt64s(b []byte) []int64 {
	out := []int64{}
	for _, v := range unpackUint64s(b) {
		out = append(out, int64FromUvarint(v))
	}
	return out
}

// resolveStrings swaps the index placeholders for real strings.
func (p *Profile) resolveStrings() {
	for i := range p.SampleTypes {
		p.SampleTypes[i].Type = p.str(p.SampleTypes[i].Type)
		p.SampleTypes[i].Unit = p.str(p.SampleTypes[i].Unit)
	}
	p.PeriodType.Type = p.str(p.PeriodType.Type)
	p.PeriodType.Unit = p.str(p.PeriodType.Unit)
	for i := range p.Mappings {
		p.Mappings[i].File = p.str(p.Mappings[i].File)
		p.Mappings[i].BuildID = p.str(p.Mappings[i].BuildID)
	}
	for i := range p.Functions {
		p.Functions[i].Name = p.str(p.Functions[i].Name)
		p.Functions[i].SystemName = p.str(p.Functions[i].SystemName)
		p.Functions[i].Filename = p.str(p.Functions[i].Filename)
	}
}
