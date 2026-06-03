// Package jsonreport's Writer accumulates probe / MTR results from a
// running CLI invocation and writes one or more JSON documents that
// validate against schema/pingtrace.schema.json.
//
// Filenames follow the same `{mode}[_TAG]_UTC<stamp>.json` convention
// used by csvexport, so a `--export` + `--json` run produces sibling
// files that are easy to correlate.
package jsonreport

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/skhell/pingtrace/internal/probe"
)

// Writer is the JSON sibling of csvexport.Writer. All public methods
// are safe for concurrent use.
type Writer struct {
	dir     string
	stamp   string
	tag     string
	version string

	mu sync.Mutex
	// probe-mode accumulation
	targets []Target
	results []Result
	// mtr-mode accumulation: one report per target
	mtrs []MtrReport
}

// New creates the export directory (if missing) and stamps every
// filename written through this Writer with the same UTC timestamp.
func New(dir, version string) (*Writer, error) {
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create export dir %s: %w", dir, err)
	}
	stamp := time.Now().UTC().Format("2006-01-02-15-04-05")
	return &Writer{dir: dir, stamp: stamp, version: version}, nil
}

// SetTag adds a sanitized infix between the mode name and the UTC
// timestamp in produced filenames (e.g. a CIDR scope).
func (w *Writer) SetTag(tag string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tag = sanitizeTag(tag)
}

// AddTarget records a target in the probe-mode document. Idempotent per value.
func (w *Writer) AddTarget(t Target) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, existing := range w.targets {
		if existing.Value == t.Value {
			return
		}
	}
	t.Source = NormalizeSource(t.Source)
	w.targets = append(w.targets, t)
}

// AppendPing accumulates one ping Result into the probe document.
func (w *Writer) AppendPing(target, source string, durationMs int64, r probe.PingResult) {
	res := FromPingResult(target, source, durationMs, r)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.results = append(w.results, res)
}

// AppendTrace accumulates one trace Result into the probe document.
func (w *Writer) AppendTrace(target, source string, durationMs int64, r probe.TraceResult) {
	res := FromTraceResult(target, source, durationMs, r)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.results = append(w.results, res)
}

// AppendMTR records an MtrReport for one MTR target. Callers pass the
// final snapshot, the planned cycle count (nil for "until Ctrl+C"),
// and the interval in milliseconds.
func (w *Writer) AppendMTR(target string, cyclesPlanned *int, intervalMs int64, command string, snap probe.MTRResult) {
	report := NewMtrReport(w.version, target, cyclesPlanned, intervalMs, command, FromMTRResult(snap), snap.Cycles, time.Now())
	w.mu.Lock()
	defer w.mu.Unlock()
	w.mtrs = append(w.mtrs, report)
}

// Close flushes the accumulated state to disk. `exported` lists the
// sibling CSV files (if any) for the probe document's `exportedFiles`
// section. Returns the JSON file paths written.
func (w *Writer) Close(exported []ExportedFile) ([]string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var paths []string

	if len(w.results) > 0 || len(w.targets) > 0 {
		report := NewProbeReport(w.version, w.targets, w.results, exported, time.Now())
		p, err := w.writeFile("probe", report)
		if err != nil {
			return paths, err
		}
		paths = append(paths, p)
	}

	for _, mr := range w.mtrs {
		name := "mtr"
		if mr.Target != "" {
			name = fmt.Sprintf("mtr_%s", sanitizeTag(mr.Target))
		}
		p, err := w.writeFile(name, mr)
		if err != nil {
			return paths, err
		}
		paths = append(paths, p)
	}

	return paths, nil
}

func (w *Writer) writeFile(name string, doc any) (string, error) {
	filename := fmt.Sprintf("%s_UTC%s.json", name, w.stamp)
	if w.tag != "" {
		filename = fmt.Sprintf("%s_%s_UTC%s.json", name, w.tag, w.stamp)
	}
	path := filepath.Join(w.dir, filename)
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// sanitizeTag strips characters that are unfriendly in filenames
// across macOS / Linux / Windows. Mirrored from csvexport for consistency.
func sanitizeTag(s string) string {
	if s == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		" ", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	out := replacer.Replace(s)
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}
