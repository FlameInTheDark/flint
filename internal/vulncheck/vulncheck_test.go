package vulncheck

import (
	"strings"
	"testing"
)

// fixture is a trimmed govulncheck stream: one OSV with a called finding
// (trace ends in the analyzed module), one import-only OSV, and scanner
// metadata. Shapes follow real govulncheck v1.1.0 output byte for byte.
const fixture = `{
  "config": {
    "protocol_version": "v1.0.0",
    "scanner_name": "govulncheck",
    "scanner_version": "v1.1.0",
    "db": "https://vuln.go.dev",
    "go_version": "go1.23.4",
    "scan_level": "symbol",
    "scan_mode": "source"
  }
}
{
  "SBOM": {
    "go_version": "go1.23.4",
    "modules": [
      { "path": "example.test/app" },
      { "path": "golang.org/x/crypto", "version": "v0.17.0" }
    ],
    "roots": [ "example.test/app" ]
  }
}
{
  "progress": { "message": "Fetching vulnerabilities from the database..." }
}
{
  "osv": {
    "schema_version": "1.3.1",
    "id": "GO-2024-3321",
    "aliases": ["CVE-2024-45337", "GHSA-v778-237x-gjrc"],
    "summary": "Misuse of ServerConfig.PublicKeyCallback may cause authorization bypass in golang.org/x/crypto",
    "details": "An application that uses a custom PublicKeyCallback...",
    "affected": [
      {
        "package": { "name": "golang.org/x/crypto", "ecosystem": "Go" },
        "ranges": [ { "type": "SEMVER", "events": [ { "introduced": "0" }, { "fixed": "0.31.0" } ] } ],
        "ecosystem_specific": {
          "imports": [
            { "path": "golang.org/x/crypto/ssh", "symbols": ["NewServerConn", "ServerConfig.PublicKeyCallback"] }
          ]
        }
      }
    ],
    "references": [
      { "type": "REPORT", "url": "https://go.dev/issue/70779" }
    ],
    "database_specific": { "url": "https://pkg.go.dev/vuln/GO-2024-3321" }
  }
}
{
  "osv": {
    "schema_version": "1.3.1",
    "id": "GO-2025-4006",
    "summary": "Crypto/x509: verify panics on odd-sized RSA modulus",
    "affected": [
      { "package": { "name": "stdlib", "ecosystem": "Go" } }
    ],
    "database_specific": { "url": "https://pkg.go.dev/vuln/GO-2025-4006" }
  }
}
{
  "finding": {
    "osv": "GO-2024-3321",
    "fixed_version": "v0.31.0",
    "trace": [
      {
        "module": "golang.org/x/crypto",
        "version": "v0.17.0",
        "package": "golang.org/x/crypto/ssh",
        "function": "serverAuthenticate",
        "receiver": "*connection",
        "position": { "filename": "ssh/server.go", "offset": 9832, "line": 434, "column": 26 }
      },
      {
        "module": "golang.org/x/crypto",
        "version": "v0.17.0",
        "package": "golang.org/x/crypto/ssh",
        "function": "NewServerConn",
        "position": { "filename": "ssh/server.go", "offset": 2956, "line": 204, "column": 21 }
      },
      {
        "module": "example.test/app",
        "package": "example.test/app/serve",
        "function": "Serve",
        "position": { "filename": "serve/serve.go", "offset": 310, "line": 19, "column": 27 }
      }
    ]
  }
}
{
  "finding": {
    "osv": "GO-2024-3321",
    "fixed_version": "v0.31.0",
    "trace": [ { "module": "golang.org/x/crypto", "version": "v0.17.0" } ]
  }
}
{
  "finding": {
    "osv": "GO-2025-4006",
    "fixed_version": "v1.24.8",
    "trace": [ { "module": "stdlib", "version": "go1.23.4" } ]
  }
}
`

func TestParseFixtureAggregatesGroups(t *testing.T) {
	report, err := Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if report.Config.ScanLevel != "symbol" || report.Config.ScannerVersion != "v1.1.0" {
		t.Fatalf("config lost: %+v", report.Config)
	}
	if len(report.Modules) != 2 || report.Modules[0] != "example.test/app" {
		t.Fatalf("SBOM modules lost: %v", report.Modules)
	}
	if len(report.Called) != 1 || report.Called[0].OSV.ID != "GO-2024-3321" {
		t.Fatalf("called groups = %+v", report.Called)
	}
	g := report.Called[0]
	if g.Fixed != "v0.31.0" {
		t.Fatalf("fixed version = %q", g.Fixed)
	}
	if len(g.Called) != 1 {
		t.Fatalf("called findings = %d, want the symbol trace only", len(g.Called))
	}
	trace := g.Called[0].Trace
	if len(trace) != 3 {
		t.Fatalf("trace frames = %d, want 3", len(trace))
	}
	if want := "serverAuthenticate"; trace[0].Symbol() != "*connection."+want {
		t.Fatalf("symbol frame = %q", trace[0].Symbol())
	}
	if !trace[0].HasPosition() || trace[0].Position.Line != 434 {
		t.Fatalf("symbol position = %+v", trace[0].Position)
	}
	if !trace[len(trace)-1].HasPosition() || trace[len(trace)-1].Position.Filename != "serve/serve.go" {
		t.Fatalf("entry frame = %+v", trace[len(trace)-1])
	}
	if len(report.Imported) != 1 || report.Imported[0].OSV.ID != "GO-2025-4006" {
		t.Fatalf("imported groups = %+v", report.Imported)
	}
	imp := report.Imported[0]
	if !imp.Imported || len(imp.Called) != 0 {
		t.Fatalf("imported group wrongly carries called traces: %+v", imp)
	}
	if imp.Module != "stdlib" || imp.Fixed != "v1.24.8" {
		t.Fatalf("imported module/fixed = %q/%q", imp.Module, imp.Fixed)
	}
	if g.URL() != "https://pkg.go.dev/vuln/GO-2024-3321" {
		t.Fatalf("advisory URL = %q", g.URL())
	}
	if got := report.Imported[0].URL(); got != "https://pkg.go.dev/vuln/GO-2025-4006" {
		t.Fatalf("imported advisory URL = %q", got)
	}
}

func TestParseToleratesMissingOSVAndGarbageTail(t *testing.T) {
	// A finding whose OSV was never emitted still aggregates (the panel
	// shows the id); a malformed document aborts with an error instead of
	// returning a half-silent report.
	broken := strings.Replace(fixture, `"summary": "Misuse of ServerConfig.PublicKeyCallback may cause authorization bypass in golang.org/x/crypto",`, `"summary": ,`, 1)
	if _, err := Parse(strings.NewReader(broken)); err == nil {
		t.Fatal("malformed stream parsed without error")
	}
}

func TestAnalyzeSortsAndDeduplicates(t *testing.T) {
	osvs := map[string]OSV{
		"GO-2024-3321": {ID: "GO-2024-3321", Summary: "bypass"},
		"GO-2025-4006": {ID: "GO-2025-4006", Summary: "panic"},
	}
	findings := []Finding{
		{OSV: "GO-2025-4006", FixedVersion: "v1.24.8", Trace: []Frame{{Module: "stdlib"}}},
		{OSV: "GO-2024-3321", FixedVersion: "v0.31.0", Trace: []Frame{
			{Module: "golang.org/x/crypto", Package: "golang.org/x/crypto/ssh", Function: "serverAuthenticate", hasPos: true, Position: Position{Filename: "ssh/server.go", Line: 1}},
			{Module: "example.test/app", Function: "Serve", hasPos: true, Position: Position{Filename: "serve.go", Line: 2}},
		}},
		// Second trace for the same OSV (another entry point).
		{OSV: "GO-2024-3321", FixedVersion: "v0.31.0", Trace: []Frame{
			{Module: "golang.org/x/crypto", Package: "golang.org/x/crypto/ssh", Function: "NewServerConn", hasPos: true, Position: Position{Filename: "ssh/server.go", Line: 9}},
			{Module: "example.test/app", Function: "main", hasPos: true, Position: Position{Filename: "main.go", Line: 3}},
		}},
	}
	r := Analyze(Config{}, osvs, findings, []string{"example.test/app"})
	if len(r.Called) != 1 || len(r.Called[0].Called) != 2 {
		t.Fatalf("called groups/findings = %+v", r.Called)
	}
	if r.Called[0].Called[0].Trace[0].Function != "serverAuthenticate" {
		t.Fatalf("traces reordered: %+v", r.Called[0].Called[0].Trace)
	}
	if len(r.Imported) != 1 {
		t.Fatalf("imported = %+v", r.Imported)
	}
	// Module-level 1-frame traces never count as called even with a version.
	findings2 := []Finding{{OSV: "GO-2025-4006", Trace: []Frame{{Module: "stdlib", Version: "go1.23.4"}}}}
	r2 := Analyze(Config{}, osvs, findings2, nil)
	if len(r2.Imported) != 1 || len(r2.Called) != 0 {
		t.Fatalf("version-only frame counted as called: %+v", r2)
	}
}

func TestFrameWithoutPositionIsModuleLevel(t *testing.T) {
	var f Finding
	f.Trace = []Frame{{Module: "stdlib"}}
	if f.Called() {
		t.Fatal("position-less single frame reported as called")
	}
}
