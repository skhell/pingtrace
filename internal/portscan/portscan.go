// Package portscan performs TCP connect scans against one or more ports on a host.
// It uses only stdlib net.Dial, no raw sockets, no root required,
// works identically on Linux, macOS, and Windows.
package portscan

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skhell/pingtrace/internal/iana"
)

// Result is the scan outcome for a single port.
type Result struct {
	Port      int
	Proto     string // always "tcp" for now
	Open      bool
	LatencyMs float64
	IANA      iana.Entry // full IANA record; zero value when port is not in registry
}

// ParsePorts parses a port specification into a sorted, deduplicated list.
// Accepts comma-separated ports (22,80,443), ranges (1-1024), or a mix.
// The empty string and the sentinel "default" both scan all 65535 ports.
func ParsePorts(spec string) ([]int, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "default" {
		out := make([]int, 65535)
		for i := range out {
			out[i] = i + 1
		}
		return out, nil
	}

	seen := make(map[int]struct{})
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.ContainsRune(part, '-') {
			lo, hi, err := parseRange(part)
			if err != nil {
				return nil, err
			}
			if hi-lo > 65534 {
				return nil, fmt.Errorf("port range %s exceeds 65535 ports", part)
			}
			for p := lo; p <= hi; p++ {
				seen[p] = struct{}{}
			}
		} else {
			p, err := strconv.Atoi(part)
			if err != nil || p < 1 || p > 65535 {
				return nil, fmt.Errorf("invalid port %q", part)
			}
			seen[p] = struct{}{}
		}
	}

	out := make([]int, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Ints(out)
	return out, nil
}

func parseRange(s string) (int, int, error) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid port range %q", s)
	}
	lo, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || lo < 1 || lo > 65535 {
		return 0, 0, fmt.Errorf("invalid port %q in range %q", parts[0], s)
	}
	hi, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || hi < 1 || hi > 65535 {
		return 0, 0, fmt.Errorf("invalid port %q in range %q", parts[1], s)
	}
	if lo > hi {
		return 0, 0, fmt.Errorf("range %q: start port > end port", s)
	}
	return lo, hi, nil
}

// Scan performs TCP connect probes on host:ports concurrently and returns
// results sorted by port number. db may be nil (IANA names will be empty).
// concurrency controls simultaneous dials per call; 0 defaults to 50.
func Scan(ctx context.Context, host string, ports []int, timeout time.Duration, concurrency int, db iana.DB) []Result {
	if len(ports) == 0 {
		return nil
	}
	if concurrency <= 0 {
		concurrency = 50
	}
	if concurrency > len(ports) {
		concurrency = len(ports)
	}

	jobs := make(chan int, len(ports))
	for _, p := range ports {
		jobs <- p
	}
	close(jobs)

	var mu sync.Mutex
	results := make([]Result, 0, len(ports))

	var wg sync.WaitGroup
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for port := range jobs {
				if ctx.Err() != nil {
					return
				}
				r := probePort(ctx, host, port, timeout)
				if db != nil {
					if e, ok := db.Lookup(port, "tcp"); ok {
						r.IANA = e
					}
				}
				mu.Lock()
				results = append(results, r)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool {
		return results[i].Port < results[j].Port
	})
	return results
}

func probePort(ctx context.Context, host string, port int, timeout time.Duration) Result {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	start := time.Now()
	conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", addr)
	ms := float64(time.Since(start).Microseconds()) / 1000.0
	if err != nil {
		return Result{Port: port, Proto: "tcp", Open: false, LatencyMs: ms}
	}
	conn.Close()
	return Result{Port: port, Proto: "tcp", Open: true, LatencyMs: ms}
}

// OpenOnly returns only the open results from a scan.
func OpenOnly(results []Result) []Result {
	out := make([]Result, 0, len(results))
	for _, r := range results {
		if r.Open {
			out = append(out, r)
		}
	}
	return out
}

// FormatCompact renders open ports as "22/ssh 443/https" for terminal display.
// Falls back to "N/tcp" when the port is not in the IANA registry.
func FormatCompact(results []Result) string {
	open := OpenOnly(results)
	if len(open) == 0 {
		return ""
	}
	parts := make([]string, 0, len(open))
	for _, r := range open {
		parts = append(parts, portLabel(r))
	}
	return strings.Join(parts, " ")
}

func portLabel(r Result) string {
	if r.IANA.ServiceName != "" {
		return fmt.Sprintf("%d/%s", r.Port, r.IANA.ServiceName)
	}
	return fmt.Sprintf("%d/tcp", r.Port)
}
