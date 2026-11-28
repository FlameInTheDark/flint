// Package vulncheck runs govulncheck and parses its JSON stream into a
// report the workspace's Vulnerabilities panel can render. It shells out to
// the govulncheck binary without a shell and keeps every decode failure
// recoverable: one malformed document must not lose the whole scan.
package vulncheck

import (
        "bufio"
        "context"
        "encoding/json"
        "fmt"
        "io"
        "os/exec"
        "sort"
        "strings"
)

// InstallHint is shown when govulncheck is not on PATH (the debug panel's
// Delve hint pattern).
const InstallHint = "govulncheck not found. Install: go install golang.org/x/vuln/cmd/govulncheck@latest"

// Config is the stream's first document (scanner metadata).
type Config struct {
        ProtocolVersion string `json:"protocol_version"`
        ScannerVersion  string `json:"scanner_version"`
        GoVersion       string `json:"go_version"`
        ScanLevel       string `json:"scan_level"`
        ScanMode        string `json:"scan_mode"`
}

// Progress is a human-readable scan step.
type Progress struct {
        Message string `json:"message"`
}

// OSV is one vulnerability database entry. Only the fields the panel
// renders are decoded; Details stays available for tooltips.
type OSV struct {
        ID         string   `json:"id"`
        Summary    string   `json:"summary"`
        Details    string   `json:"details"`
        Aliases    []string `json:"aliases"`
        References []struct {
                Type string `json:"type"`
                URL  string `json:"url"`
        } `json:"references"`
        DatabaseSpecific struct {
                URL string `json:"url"`
        } `json:"database_specific"`
        Affected []struct {
                Package struct {
                        Name string `json:"name"`
                } `json:"package"`
        } `json:"affected"`
}

// URL returns the advisory page (pkg.go.dev/vuln/ID).
func (o OSV) URL() string {
        if o.DatabaseSpecific.URL != "" {
                return o.DatabaseSpecific.URL
        }
        if o.ID != "" {
                return "https://pkg.go.dev/vuln/" + o.ID
        }
        return ""
}

// Position is a source location inside a trace frame.
type Position struct {
        Filename string `json:"filename"`
        Line     int    `json:"line"`
        Column   int    `json:"column"`
}

// Frame is one call-stack entry. A missing Position marks module-level
// findings (imported but the vulnerable symbol is not called).
type Frame struct {
        Module   string   `json:"module"`
        Version  string   `json:"version"`
        Package  string   `json:"package"`
        Function string   `json:"function"`
        Receiver string   `json:"receiver"`
        Position Position `json:"position"`
        hasPos   bool
}

type rawFrame struct {
        Module   string    `json:"module"`
        Version  string    `json:"version"`
        Package  string    `json:"package"`
        Function string    `json:"function"`
        Receiver string    `json:"receiver"`
        Position *Position `json:"position"`
}

// UnmarshalJSON adapts the stream's raw frame shape into Frame, keeping
// whether a position was present at all (stdlib module frames carry none).
func (fr *Frame) UnmarshalJSON(b []byte) error {
        var raw rawFrame
        if err := json.Unmarshal(b, &raw); err != nil {
                return err
        }
        fr.Module, fr.Version, fr.Package = raw.Module, raw.Version, raw.Package
        fr.Function, fr.Receiver = raw.Function, raw.Receiver
        if raw.Position != nil {
                fr.Position, fr.hasPos = *raw.Position, true
        }
        return nil
}

// Finding is one emitted trace for an OSV. The trace reads from the
// vulnerable symbol to the analyzed module's entry point; a single
// position-less frame means the module is imported but the symbol is not
// called.
type Finding struct {
        OSV          string
        FixedVersion string
        Trace        []Frame
}

// Called reports whether the vulnerable symbol is reachable from the
// analyzed module's code (a call stack with more than the symbol frame).
func (f Finding) Called() bool {
        if len(f.Trace) < 2 {
                return false
        }
        // A second frame with no position is a module-level trace too (some
        // streams emit `stdlib` pairs); require real positions.
        return f.Trace[0].hasPos
}

// Symbol renders a frame's symbol with its receiver, govulncheck style.
func (fr Frame) Symbol() string {
        name := fr.Function
        if fr.Receiver != "" {
                name = fr.Receiver + "." + name
        }
        return name
}

// HasPosition reports whether the frame carries a source location.
func (fr Frame) HasPosition() bool { return fr.hasPos && fr.Position.Filename != "" }

// Report aggregates one scan. Vulnerabilities the project's code calls are
// listed first (they need action), followed by imported-only ones.
type Report struct {
        Config   Config
        Modules  []string
        Called   []Group
        Imported []Group
}

// Group merges every finding emitted for one OSV.
type Group struct {
        OSV      OSV
        Fixed    string
        Called   []Finding // called traces, empty for imported-only groups
        Module   string    // affected module path (shown for imported-only rows)
        Imported bool      // true when the module is imported but not called
}

// URL returns the OSV advisory page (the doc's database_specific entry).
func (g Group) URL() string { return g.OSV.URL() }

// Analyze builds the aggregated report from parsed stream documents.
func Analyze(cfg Config, osvs map[string]OSV, findings []Finding, modules []string) Report {
        r := Report{Config: cfg, Modules: modules}
        byOSV := map[string][]Finding{}
        var ids []string
        for _, f := range findings {
                if f.OSV == "" {
                        continue
                }
                if _, seen := byOSV[f.OSV]; !seen {
                        ids = append(ids, f.OSV)
                }
                byOSV[f.OSV] = append(byOSV[f.OSV], f)
        }
        sort.Strings(ids)
        for _, id := range ids {
                fs := byOSV[id]
                g := Group{OSV: osvs[id], Fixed: fs[0].FixedVersion}
                for _, f := range fs {
                        if f.Called() {
                                g.Called = append(g.Called, f)
                        }
                }
                // The affected module for import-only rows: the first finding's
                // frame module (a scan emits per-module findings).
                for _, f := range fs {
                        if len(f.Trace) > 0 && f.Trace[0].Module != "" {
                                g.Module = f.Trace[0].Module
                                break
                        }
                }
                g.Imported = len(g.Called) == 0
                if g.Imported {
                        r.Imported = append(r.Imported, g)
                } else {
                        r.Called = append(r.Called, g)
                }
        }
        return r
}

// streamDoc is one JSON document of the govulncheck stream. SBOM stays raw
// (decoded in a second step for its module list).
type streamDoc struct {
        Config   *Config         `json:"config"`
        SBOM     json.RawMessage `json:"SBOM"`
        Progress *Progress       `json:"progress"`
        OSV      *OSV            `json:"osv"`
        Finding  *rawFinding     `json:"finding"`
}

type rawFinding struct {
        OSV          string  `json:"osv"`
        FixedVersion string  `json:"fixed_version"`
        Trace        []Frame `json:"trace"`
}

// sbomDoc describes the SBOM document's VALUE (the stream wrapper key is
// consumed by streamDoc, so this struct starts at "modules").
type sbomDoc struct {
        Modules []struct {
                Path string `json:"path"`
        } `json:"modules"`
}

// consume decodes every document of a stream into the report pieces.
func consume(r io.Reader, onDoc func(string)) (Config, map[string]OSV, []Finding, []string, error) {
        cfg := Config{}
        osvs := map[string]OSV{}
        var findings []Finding
        var modules []string
        dec := json.NewDecoder(bufio.NewReader(r))
        for {
                var doc streamDoc
                if err := dec.Decode(&doc); err != nil {
                        if err == io.EOF {
                                break
                        }
                        return cfg, osvs, findings, modules, err
                }
                switch {
                case doc.Config != nil:
                        cfg = *doc.Config
                        if onDoc != nil {
                                onDoc("Scan level: " + cfg.ScanLevel)
                        }
                case doc.OSV != nil:
                        if doc.OSV.ID != "" {
                                osvs[doc.OSV.ID] = *doc.OSV
                        }
                case doc.Finding != nil:
                        f := Finding{OSV: doc.Finding.OSV, FixedVersion: doc.Finding.FixedVersion, Trace: doc.Finding.Trace}
                        if f.OSV != "" {
                                findings = append(findings, f)
                        }
                case doc.Progress != nil:
                        if onDoc != nil && doc.Progress.Message != "" {
                                onDoc(doc.Progress.Message)
                        }
                case doc.SBOM != nil:
                        var sbom sbomDoc
                        if err := json.Unmarshal(doc.SBOM, &sbom); err == nil {
                                for _, m := range sbom.Modules {
                                        if m.Path != "" {
                                                modules = append(modules, m.Path)
                                        }
                                }
                        }
                }
        }
        return cfg, osvs, findings, modules, nil
}

// Parse decodes a captured govulncheck JSON stream (tests, saved scans).
func Parse(r io.Reader) (Report, error) {
        data, err := io.ReadAll(r)
        if err != nil {
                return Report{}, err
        }
        cfg, osvs, findings, modules, err := consume(strings.NewReader(string(data)), nil)
        if err != nil {
                return Report{}, err
        }
        return Analyze(cfg, osvs, findings, modules), nil
}

// Runner owns one cancellable govulncheck process.
type Runner struct {
        cancel context.CancelFunc
}

// NewRunner wraps a context cancel function (the scan's lifecycle owner).
func NewRunner(cancel context.CancelFunc) *Runner { return &Runner{cancel: cancel} }

// Running reports whether a scan is in flight.
func (r *Runner) Running() bool { return r != nil && r.cancel != nil }

// Stop cancels the in-flight scan (safe on nil).
func (r *Runner) Stop() {
        if r != nil && r.cancel != nil {
                r.cancel()
        }
}

// Scan runs `govulncheck -json ./...` in dir and streams progress through
// onDoc before returning the aggregated report. The process dies with ctx.
func Scan(ctx context.Context, dir string, onDoc func(string)) (Report, error) {
        path, err := exec.LookPath("govulncheck")
        if err != nil {
                return Report{}, fmt.Errorf("%s", InstallHint)
        }
        ctx, cancel := context.WithCancel(ctx)
        defer cancel()
        cmd := exec.CommandContext(ctx, path, "-json", "./...")
        cmd.Dir = dir
        cmd.Stderr = io.Discard
        scanCommandPlatform(cmd)
        stdout, err := cmd.StdoutPipe()
        if err != nil {
                return Report{}, err
        }
        if err := cmd.Start(); err != nil {
                return Report{}, err
        }
        cfg, osvs, findings, modules, parseErr := consume(stdout, onDoc)
        waitErr := cmd.Wait()
        if ctx.Err() != nil {
                return Report{}, ctx.Err()
        }
        if parseErr != nil {
                return Report{}, parseErr
        }
        if waitErr != nil {
                return Report{}, fmt.Errorf("govulncheck: %v", waitErr)
        }
        return Analyze(cfg, osvs, findings, modules), nil
}
