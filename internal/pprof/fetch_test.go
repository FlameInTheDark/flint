package pprof

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeSource(t *testing.T) {
	cases := []struct {
		host, kind string
		seconds    int
		want       string
	}{
		{"localhost:8080", "profile", 10, "http://localhost:8080/debug/pprof/profile?seconds=10"},
		{"localhost:8080", "heap", 0, "http://localhost:8080/debug/pprof/heap"},
		{"http://localhost:8080/metrics/", "profile", 5, "http://localhost:8080/metrics/debug/pprof/profile?seconds=5"},
		{"http://h/debug/pprof/heap?gc=1", "heap", 3, "http://h/debug/pprof/heap?gc=1"},
		{"", "cpu", 10, ""},
	}
	for _, c := range cases {
		if got := NormalizeSource(c.host, KindByName(c.kind), c.seconds); got != c.want {
			t.Fatalf("NormalizeSource(%q,%q,%d) = %q, want %q", c.host, c.kind, c.seconds, got, c.want)
		}
	}
}

func TestFetchURLAndParse(t *testing.T) {
	data, err := os.ReadFile("testdata/cpu.out")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	raw, err := FetchURL(nil, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Samples) == 0 {
		t.Fatal("fetched profile empty")
	}
	// Error status surfaces the body.
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte("no pprof here"))
	}))
	defer errSrv.Close()
	if _, err := FetchURL(nil, errSrv.URL); err == nil || !contains(err.Error(), "no pprof here") {
		t.Fatalf("expected status error with body, got %v", err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && index(s, sub) >= 0)
}

func index(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestFetchFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.out")
	data, _ := os.ReadFile("testdata/heap.out")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	raw, err := FetchFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.SampleTypes[3].Type != "inuse_space" {
		t.Fatalf("heap sample types = %+v", p.SampleTypes)
	}
}

func TestCaptureTimeout(t *testing.T) {
	if CaptureTimeout(10) <= 10*time.Second {
		t.Fatal("capture timeout has no margin")
	}
}
