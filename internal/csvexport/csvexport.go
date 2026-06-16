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

	"github.com/skhell/pingtrace/internal/portscan"
	"github.com/skhell/pingtrace/internal/probe"
)

// Writer holds one or more open CSV files for the lifetime of a run.
// All public methods are safe for concurrent use; an internal mutex
// serializes writes so the bulk-mode worker pool can call us from
// many goroutines without corrupting rows.
type Writer struct {
	dir     string
	stamp   string
	tag     string // legacy infix, superseded by src/to when both are set
	src     string // local outbound IP
	to      string // sanitized target description
	compact bool   // omit all-empty columns
	files   map[string]*csvFile
	mu      sync.Mutex
}

type csvFile struct {
	f         *os.File
	w         *csv.Writer
	headers   []string
	rowCount  int
	activeIdx []int // non-nil in compact mode: indices into the full header slice
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

// openCompact is like open but determines the active column set from
// rows on the first call, then reuses that set for the lifetime of
// the file. Caller must hold w.mu.
func (w *Writer) openCompact(op string, allHeaders []string, rows [][]string) (*csvFile, error) {
	if cf, ok := w.files[op]; ok {
		return cf, nil
	}
	idx := nonEmptyIdx(rows)
	if idx == nil {
		idx = make([]int, len(allHeaders))
		for i := range idx {
			idx[i] = i
		}
	}
	cf, err := w.open(op, filterHeaders(allHeaders, idx))
	if err != nil {
		return nil, err
	}
	cf.activeIdx = idx
	return cf, nil
}

func (w *Writer) open(op string, headers []string) (*csvFile, error) {
	if cf, ok := w.files[op]; ok {
		return cf, nil
	}
	var name string
	switch {
	case w.src != "" && w.to != "":
		name = fmt.Sprintf("%s_from_%s_to_%s_UTC%s.csv", op, w.src, w.to, w.stamp)
	case w.tag != "":
		name = fmt.Sprintf("%s_%s_UTC%s.csv", op, w.tag, w.stamp)
	default:
		name = fmt.Sprintf("%s_UTC%s.csv", op, w.stamp)
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

// SetFromTo sets the source IP and target description used in filenames.
// When both are non-empty the filename becomes:
//
//	<op>_from_<src>_to_<to>_UTC<stamp>.csv
//
// Call before the first write.
func (w *Writer) SetFromTo(src, to string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.src = sanitizeTag(src)
	w.to = sanitizeTag(to)
}

// SetCompact enables compact mode: columns that are entirely empty
// across all rows of a target are omitted from the CSV output.
// The column set is locked in on the first write to each file and
// held for all subsequent targets in the same run.
func (w *Writer) SetCompact(v bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.compact = v
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

// --- compact helpers -------------------------------------------

// nonEmptyIdx returns the indices of columns that have at least one
// non-empty value across all rows.
func nonEmptyIdx(rows [][]string) []int {
	if len(rows) == 0 {
		return nil
	}
	width := len(rows[0])
	seen := make([]bool, width)
	for _, r := range rows {
		for i, v := range r {
			if i < width && v != "" {
				seen[i] = true
			}
		}
	}
	idx := make([]int, 0, width)
	for i, ok := range seen {
		if ok {
			idx = append(idx, i)
		}
	}
	return idx
}

func filterHeaders(all []string, idx []int) []string {
	out := make([]string, len(idx))
	for i, j := range idx {
		out[i] = all[j]
	}
	return out
}

func filterRow(row []string, idx []int) []string {
	out := make([]string, len(idx))
	for i, j := range idx {
		if j < len(row) {
			out[i] = row[j]
		}
	}
	return out
}

// --- ping ------------------------------------------------------

// pingHeaders mirrors the CLI ping columns plus every enrichment
// field from ipinfo.io and PeeringDB. No truncation: CSV is for
// downstream analysis.
var pingHeaders = []string{
	"seq", "bytes", "source", "target", "ttl", "time_ms",
	"public_dns", "private_dns", "ipinfo_hostname",
	"org", "asn", "location", "city", "region", "country", "loc",
	"net_type", "policy", "pdb_name", "traffic",
	"prefixes_v4", "prefixes_v6", "ixp_count",
	"status",
}

var pingSummaryHeaders = []string{
	"target", "sent", "received", "loss_pct", "avg_ms", "min_ms", "max_ms",
}

func buildPingRow(p probe.PingReply, fallbackTarget string) []string {
	tgt := p.Target
	if tgt == "" {
		tgt = fallbackTarget
	}
	return []string{
		strconv.Itoa(p.Seq), strconv.Itoa(p.Bytes),
		p.Source, tgt, strconv.Itoa(p.TTL), fmt.Sprintf("%.3f", p.TimeMs),
		p.PublicDNS, p.PrivateDNS, p.Hostname,
		p.Org, p.ASN, p.Location, p.City, p.Region, p.Country, p.Loc,
		p.NetType, p.Policy, p.PdbName, p.Traffic,
		intOrEmpty(p.Prefixes4), intOrEmpty(p.Prefixes6), intOrEmpty(p.IXPCount),
		p.Status,
	}
}

// Ping writes per-packet rows for one target.
func (w *Writer) Ping(target string, r probe.PingResult) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	rows := make([][]string, 0, len(r.Packets))
	for _, p := range r.Packets {
		rows = append(rows, buildPingRow(p, target))
	}

	var cf *csvFile
	var err error
	if w.compact {
		cf, err = w.openCompact("ping", pingHeaders, rows)
	} else {
		cf, err = w.open("ping", pingHeaders)
	}
	if err != nil {
		return err
	}

	for _, row := range rows {
		out := row
		if cf.activeIdx != nil {
			out = filterRow(row, cf.activeIdx)
		}
		if err := cf.w.Write(out); err != nil {
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
	"hop", "source", "target", "hostname", "host_ip",
	"probe_1_ms", "probe_2_ms", "probe_3_ms",
	"public_dns", "private_dns", "ipinfo_hostname",
	"org", "asn", "location", "city", "region", "country", "loc",
	"net_type", "policy", "pdb_name", "traffic",
	"prefixes_v4", "prefixes_v6", "ixp_count",
	"status",
}

func buildTraceRow(h probe.TraceHop, fallbackTarget string) []string {
	tgt := h.Target
	if tgt == "" {
		tgt = fallbackTarget
	}
	return []string{
		strconv.Itoa(h.Hop), h.Source, tgt, h.Host, h.IP,
		fmtProbe(h.Probe1Ms), fmtProbe(h.Probe2Ms), fmtProbe(h.Probe3Ms),
		h.PublicDNS, h.PrivateDNS, h.Hostname,
		h.Org, h.ASN, h.Location, h.City, h.Region, h.Country, h.Loc,
		h.NetType, h.Policy, h.PdbName, h.Traffic,
		intOrEmpty(h.Prefixes4), intOrEmpty(h.Prefixes6), intOrEmpty(h.IXPCount),
		h.Status,
	}
}

// Trace writes per-hop rows for one target.
func (w *Writer) Trace(target string, r probe.TraceResult) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	rows := make([][]string, 0, len(r.Hops))
	for _, h := range r.Hops {
		rows = append(rows, buildTraceRow(h, target))
	}

	var cf *csvFile
	var err error
	if w.compact {
		cf, err = w.openCompact("trace", traceHeaders, rows)
	} else {
		cf, err = w.open("trace", traceHeaders)
	}
	if err != nil {
		return err
	}

	for _, row := range rows {
		out := row
		if cf.activeIdx != nil {
			out = filterRow(row, cf.activeIdx)
		}
		if err := cf.w.Write(out); err != nil {
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

// --- port scan ------------------------------------------------------

// scanHeaders mirrors the IANA CSV column names exactly to avoid confusion, with source and
// target prepended so rows are attributable to a probe origin and destination.
var scanHeaders = []string{
	"source", "target",
	"Service Name", "Port Number", "Transport Protocol", "Description",
	"Assignee", "Contact", "Registration Date", "Modification Date",
	"Reference", "Service Code", "Unauthorized Use Reported", "Assignment Notes",
}

// Scan writes one row per open port to scan_*.csv. Closed ports are omitted.
// source is the local outbound IP; target is the probe destination.
func (w *Writer) Scan(source, target string, results []portscan.Result) error {
	open := portscan.OpenOnly(results)
	if len(open) == 0 {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	rows := make([][]string, 0, len(open))
	for _, r := range open {
		rows = append(rows, []string{
			source, target,
			r.IANA.ServiceName, r.IANA.PortNumber, r.IANA.TransportProtocol,
			r.IANA.Description, r.IANA.Assignee, r.IANA.Contact,
			r.IANA.RegistrationDate, r.IANA.ModificationDate,
			r.IANA.Reference, r.IANA.ServiceCode,
			r.IANA.UnauthorizedUseReported, r.IANA.AssignmentNotes,
		})
	}

	var cf *csvFile
	var err error
	if w.compact {
		cf, err = w.openCompact("scan", scanHeaders, rows)
	} else {
		cf, err = w.open("scan", scanHeaders)
	}
	if err != nil {
		return err
	}

	for _, row := range rows {
		out := row
		if cf.activeIdx != nil {
			out = filterRow(row, cf.activeIdx)
		}
		if err := cf.w.Write(out); err != nil {
			return err
		}
		cf.rowCount++
	}
	cf.w.Flush()
	return cf.w.Error()
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
