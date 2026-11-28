// fetch.go loads profiles from files and running servers. The runtime's
// /debug/pprof endpoints answer with the same protobuf format the parser
// reads, so a capture is one HTTP GET (cpu profiles run for ?seconds=N).
package pprof

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// ProfileKind is one runtime/pprof endpoint the Profiler panel offers.
type ProfileKind struct {
	// Name is the endpoint ("profile", "heap", ...).
	Name string
	// Title is the panel label ("CPU", "Heap", ...).
	Title string
	// Timed marks kinds the server collects over ?seconds=N.
	Timed bool
}

// Kinds lists the profile types in panel order (the runtime/pprof set).
var Kinds = []ProfileKind{
	{"profile", "CPU", true},
	{"heap", "Heap", false},
	{"allocs", "Allocs", false},
	{"goroutine", "Goroutines", false},
	{"block", "Block", false},
	{"mutex", "Mutex", false},
	{"threadcreate", "Threads", false},
}

// KindByName resolves the endpoint name ("" = cpu).
func KindByName(name string) ProfileKind {
	for _, k := range Kinds {
		if k.Name == name {
			return k
		}
	}
	return Kinds[0]
}

// NormalizeSource builds the fetch URL for a server source: it accepts
// "localhost:8080", "http://localhost:8080" or a full /debug/pprof URL.
func NormalizeSource(host string, kind ProfileKind, seconds int) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	u := strings.TrimRight(host, "/")
	if strings.Contains(u, "/debug/pprof/") {
		// Full URL: append seconds for timed kinds when missing.
		if kind.Timed && seconds > 0 && !strings.Contains(u, "seconds=") {
			u += separator(u) + "seconds=" + strconv.FormatInt(int64(seconds), 10)
		}
		return u
	}
	u += "/debug/pprof/" + kind.Name
	if kind.Timed && seconds > 0 {
		u += "?seconds=" + strconv.FormatInt(int64(seconds), 10)
	}
	return u
}

func separator(u string) string {
	if strings.Contains(u, "?") {
		return "&"
	}
	return "?"
}

// FetchURL downloads a profile over HTTP (the capture path). Timed CPU
// captures need a deadline longer than the collection window; callers
// pass it through ctx.
func FetchURL(ctx context.Context, fetchURL string) ([]byte, error) {
	if _, err := url.Parse(fetchURL); err != nil {
		return nil, err
	}
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", fetchURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return nil, err
	}
	return data, nil
}

// FetchFile reads a profile from disk (the open path).
func FetchFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// CaptureTimeout is the context deadline for a live capture: the
// collection window plus symbolization/transfer margin.
func CaptureTimeout(seconds int) time.Duration {
	return time.Duration(seconds)*time.Second + 30*time.Second
}
