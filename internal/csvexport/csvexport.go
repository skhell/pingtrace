// Package csvexport writes pingtrace results to disk as CSV.
//
// Filenames follow {op}[_TAG]_UTC<YYYY-MM-DD-HH-MM-SS>.csv. When
// the run is scoped to one or more CIDRs the TAG is the sanitized
// CIDR list (e.g. ping_10.0.0.0_28_UTC...csv); otherwise the
// TAG segment is omitted. One file is produced per operation per
// run; subsequent targets / cycles append rows to the same file.
package csvexport

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skhell/pingtrace/internal/probe"
)

// Writer holds one or more open CSV files for the lifetime of a run.
// All public methods are safe for concurrent use; an internal mutex
// serializes writes so the bulk-mode worker pool can call us from
// many goroutines without corrupting rows.
type Writer struct {
	dir   string
	stamp string
	tag   string // optional filename infix (e.g. sanitized CIDR)
	files map[string]*csvFile
	mu    sync.Mutex
}

type csvFile struct {
	f        *os.File
	w        *csv.Writer
	headers  []string
	rowCount int
}

// New creates the export directory (if missing) and stamps every
// filename written through this Writer with the same UTC timestamp.
func New(dir string) (*Writer, error) {
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create export dir %s: %w", dir, err)
	}
	stamp := time.Now().UTC().Format("2006-01-02-15-04-05")
	return &Writer{dir: dir, stamp: stamp, files: map[string]*csvFile{}}, nil
}

// Close flushes and closes every file opened by this writer and
// returns the list of paths produced (for the final summary line).
func (w *Writer) Close() ([]string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	paths := make([]string, 0, len(w.files))
	var firstErr error
	for _, cf := range w.files {
		cf.w.Flush()
		if err := cf.w.Error(); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := cf.f.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		paths = append(paths, cf.f.Name())
	}
	return paths, firstErr
}

// FileInfo describes one CSV file written during this run.
type FileInfo struct {
	Operation string
	Path      string
	RowCount  int
}

// Files returns a snapshot of the files opened so far, along with how
// many data rows have been written to each (excluding the header).
// Safe to call after Close to feed `jsonreport.Writer`'s exportedFiles.
func (w *Writer) Files() []FileInfo {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]FileInfo, 0, len(w.files))
	for op, cf := range w.files {
		out = append(out, FileInfo{Operation: op, Path: cf.f.Name(), RowCount: cf.rowCount})
	}
	return out
}

func (w *Writer) open(op string, headers []string) (*csvFile, error) {
	if cf, ok := w.files[op]; ok {
		return cf, nil
	}
	name := fmt.Sprintf("%s_UTC%s.csv", op, w.stamp)
	if w.tag != "" {
		name = fmt.Sprintf("%s_%s_UTC%s.csv", op, w.tag, w.stamp)
	}
	path := filepath.Join(w.dir, name)
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", path, err)
	}
	cw := csv.NewWriter(f)
	if err := cw.Write(headers); err != nil {
		f.Close()
		return nil, err
	}
	cf := &csvFile{f: f, w: cw, headers: headers}
	w.files[op] = cf
	return cf, nil
}

// SetTag adds a sanitized infix between the operation name and the
// UTC timestamp in produced filenames (e.g. "10.0.0.0_28" so
// bulk CIDR runs are easy to spot in a reports/ folder). Pass an
// empty string to clear.
func (w *Writer) SetTag(tag string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tag = sanitizeTag(tag)
}

// sanitizeTag strips characters that are unfriendly in filenames
// across macOS / Linux / Windows.
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

// --- ping ------------------------------------------------------

// pingHeaders mirrors the CLI ping columns plus every enrichment
// field from ipinfo.io and PeeringDB. No truncation: CSV is for
// downstream analysis.
var pingHeaders = []string{
	"target", "seq", "bytes", "ip", "ttl", "time_ms",
	"public_dns", "private_dns", "hostname",
	"org", "asn", "location", "city", "region", "country", "loc",
	"net_type", "policy", "pdb_name", "traffic",
	"prefixes_v4", "prefixes_v6", "ixp_count",
	"status",
}

var pingSummaryHeaders = []string{
	"target", "sent", "received", "loss_pct", "avg_ms", "min_ms", "max_ms",
}

// Ping writes per-packet rows for one target.
func (w *Writer) Ping(target string, r probe.PingResult) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	cf, err := w.open("ping", pingHeaders)
	if err != nil {
		return err
	}
	for _, p := range r.Packets {
		row := []string{
			target, strconv.Itoa(p.Seq), strconv.Itoa(p.Bytes),
			p.IP, strconv.Itoa(p.TTL), fmt.Sprintf("%.3f", p.TimeMs),
			p.PublicDNS, p.PrivateDNS, p.Hostname,
			p.Org, p.ASN, p.Location, p.City, p.Region, p.Country, p.Loc,
			p.NetType, p.Policy, p.PdbName, p.Traffic,
			intOrEmpty(p.Prefixes4), intOrEmpty(p.Prefixes6), intOrEmpty(p.IXPCount),
			p.Status,
		}
		if err := cf.w.Write(row); err != nil {
			return err
		}
		cf.rowCount++
	}
	cf.w.Flush()
	return cf.w.Error()
}

// PingSummary writes one summary row per target instead of per-packet.
func (w *Writer) PingSummary(target string, r probe.PingResult) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	cf, err := w.open("ping", pingSummaryHeaders)
	if err != nil {
		return err
	}
	if err := cf.w.Write([]string{
		target, strconv.Itoa(r.Sent), strconv.Itoa(r.Received),
		fmt.Sprintf("%.2f", r.LossPct), fmt.Sprintf("%.3f", r.AvgMs),
		fmt.Sprintf("%.3f", r.MinMs), fmt.Sprintf("%.3f", r.MaxMs),
	}); err != nil {
		return err
	}
	cf.rowCount++
	cf.w.Flush()
	return cf.w.Error()
}

func intOrEmpty(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// --- trace -----------------------------------------------------

var traceHeaders = []string{
	"target", "hop", "host", "ip",
	"probe_1_ms", "probe_2_ms", "probe_3_ms",
	"public_dns", "private_dns", "hostname",
	"org", "asn", "location", "city", "region", "country", "loc",
	"net_type", "policy", "pdb_name", "traffic",
	"prefixes_v4", "prefixes_v6", "ixp_count",
	"status",
}

// Trace writes per-hop rows for one target.
func (w *Writer) Trace(target string, r probe.TraceResult) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	cf, err := w.open("trace", traceHeaders)
	if err != nil {
		return err
	}
	for _, h := range r.Hops {
		row := []string{
			target, strconv.Itoa(h.Hop), h.Host, h.IP,
			fmtProbe(h.Probe1Ms), fmtProbe(h.Probe2Ms), fmtProbe(h.Probe3Ms),
			h.PublicDNS, h.PrivateDNS, h.Hostname,
			h.Org, h.ASN, h.Location, h.City, h.Region, h.Country, h.Loc,
			h.NetType, h.Policy, h.PdbName, h.Traffic,
			intOrEmpty(h.Prefixes4), intOrEmpty(h.Prefixes6), intOrEmpty(h.IXPCount),
			h.Status,
		}
		if err := cf.w.Write(row); err != nil {
			return err
		}
		cf.rowCount++
	}
	cf.w.Flush()
	return cf.w.Error()
}

func fmtProbe(ms float64) string {
	if ms <= 0 {
		return ""
	}
	return fmt.Sprintf("%.3f", ms)
}

// --- mtr -------------------------------------------------------

var mtrHeaders = []string{
	"target", "cycle", "hop", "ip", "loss_pct", "snt",
	"last_ms", "avg_ms", "best_ms", "wrst_ms", "stdev_ms",
}

// MTRCycle appends one row per hop for a single MTR cycle.
func (w *Writer) MTRCycle(target string, cycle int, snap probe.MTRResult) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	cf, err := w.open("mtr", mtrHeaders)
	if err != nil {
		return err
	}
	for _, h := range snap.Hops {
		row := []string{
			target, strconv.Itoa(cycle), strconv.Itoa(h.Hop), h.IP,
			fmt.Sprintf("%.2f", h.LossPc), strconv.Itoa(h.Snt),
			fmt.Sprintf("%.3f", h.Last), fmt.Sprintf("%.3f", h.Avg),
			fmt.Sprintf("%.3f", h.Best), fmt.Sprintf("%.3f", h.Wrst),
			fmt.Sprintf("%.3f", h.StDev),
		}
		if err := cf.w.Write(row); err != nil {
			return err
		}
		cf.rowCount++
	}
	cf.w.Flush()
	return cf.w.Error()
}
