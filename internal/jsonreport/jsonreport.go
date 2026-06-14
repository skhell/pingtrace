// Package jsonreport marshals probe results into documents that validate
// against schema/pingtrace.schema.json. This package is the contract
// boundary between the Go CLI and the npm library: both implementations
// must produce documents that validate against the same schema.
package jsonreport

import (
	"fmt"
	"math"
	"time"

	"github.com/skhell/pingtrace/internal/probe"
)

// SchemaURL points at the published schema in the repo at the matching
// version tag. Update when bumping the schema.
const SchemaURL = "https://raw.githubusercontent.com/skhell/pingtrace/v1.1.0/schema/pingtrace.schema.json"

// Target mirrors the JSON Schema Target object.
type Target struct {
	Value         string `json:"value"`
	Source        string `json:"source"`
	OriginalInput string `json:"originalInput,omitempty"`
}

// Row is the row element common to ping and trace results. Fields not
// applicable to a given operation are omitted via `omitempty` and
// nullable pointer types.
type Row struct {
	// ping fields
	Seq    *int     `json:"seq,omitempty"`
	Bytes  *int     `json:"bytes,omitempty"`
	TTL    *int     `json:"ttl,omitempty"`
	TimeMs *float64 `json:"timeMs,omitempty"`

	// trace fields
	Hop      *int     `json:"hop,omitempty"`
	Hostname string   `json:"hostname,omitempty"`
	HostIp   string   `json:"hostIp,omitempty"`
	Probe1Ms *float64 `json:"probe1Ms,omitempty"`
	Probe2Ms *float64 `json:"probe2Ms,omitempty"`
	Probe3Ms *float64 `json:"probe3Ms,omitempty"`

	// shared: present on every row, mirrors the CSV source/target columns
	Source string `json:"source,omitempty"`
	Target string `json:"target,omitempty"`

	PublicDNS  string `json:"publicDns,omitempty"`
	PrivateDNS string `json:"privateDns,omitempty"`
	Org        string `json:"org,omitempty"`
	ASN        string `json:"asn,omitempty"`
	Location   string `json:"location,omitempty"`
	NetType    string `json:"netType,omitempty"`
	Policy     string `json:"policy,omitempty"`
	Status     string `json:"status"`
}

// Result is the per-target outcome in a ProbeReport.
type Result struct {
	Target     string `json:"target"`
	Source     string `json:"source"`
	Operation  string `json:"operation"`
	Status     string `json:"status"`
	Summary    string `json:"summary"`
	Command    string `json:"command"`
	DurationMs int64  `json:"durationMs"`
	Rows       []Row  `json:"rows"`
}

// ExportedFile mirrors the schema ExportedFile object (one entry per
// CSV file produced alongside the JSON document).
type ExportedFile struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
	RowCount  int    `json:"rowCount"`
}

// ProbeReport is the top-level "probe" document.
type ProbeReport struct {
	Schema           string         `json:"$schema"`
	PingtraceVersion string         `json:"pingtraceVersion"`
	GeneratedAt      string         `json:"generatedAt"`
	Mode             string         `json:"mode"`
	Targets          []Target       `json:"targets"`
	Results          []Result       `json:"results"`
	ExportedFiles    []ExportedFile `json:"exportedFiles,omitempty"`
}

// MtrHop mirrors the schema MtrHop object.
type MtrHop struct {
	Hop         int      `json:"hop"`
	IP          *string  `json:"ip"`
	IPs         []string `json:"ips,omitempty"`
	Host        string   `json:"host,omitempty"`
	Sent        int      `json:"sent"`
	Lost        int      `json:"lost"`
	LossPercent float64  `json:"lossPercent"`
	Last        *float64 `json:"last"`
	Best        *float64 `json:"best"`
	Worst       *float64 `json:"worst"`
	Avg         *float64 `json:"avg"`
	StDev       *float64 `json:"stdev"`
	PublicDNS   string   `json:"publicDns,omitempty"`
	PrivateDNS  string   `json:"privateDns,omitempty"`
	Org         string   `json:"org,omitempty"`
	ASN         string   `json:"asn,omitempty"`
	Location    string   `json:"location,omitempty"`
	NetType     string   `json:"netType,omitempty"`
	Policy      string   `json:"policy,omitempty"`
}

// MtrReport is the top-level "mtr" document.
type MtrReport struct {
	Schema           string   `json:"$schema"`
	PingtraceVersion string   `json:"pingtraceVersion"`
	GeneratedAt      string   `json:"generatedAt"`
	Mode             string   `json:"mode"`
	Target           string   `json:"target"`
	CyclesPlanned    *int     `json:"cyclesPlanned"`
	CyclesCompleted  int      `json:"cyclesCompleted"`
	IntervalMs       int64    `json:"intervalMs"`
	Command          string   `json:"command,omitempty"`
	Hops             []MtrHop `json:"hops"`
}

// NewProbeReport builds a ProbeReport with the canonical top-level fields
// populated. Callers supply the per-target results.
func NewProbeReport(version string, targets []Target, results []Result, exported []ExportedFile, generatedAt time.Time) ProbeReport {
	return ProbeReport{
		Schema:           SchemaURL,
		PingtraceVersion: version,
		GeneratedAt:      generatedAt.UTC().Format(time.RFC3339Nano),
		Mode:             "probe",
		Targets:          targets,
		Results:          results,
		ExportedFiles:    exported,
	}
}

// NewMtrReport builds an MtrReport.
func NewMtrReport(version, target string, cyclesPlanned *int, intervalMs int64, command string, hops []MtrHop, completed int, generatedAt time.Time) MtrReport {
	return MtrReport{
		Schema:           SchemaURL,
		PingtraceVersion: version,
		GeneratedAt:      generatedAt.UTC().Format(time.RFC3339Nano),
		Mode:             "mtr",
		Target:           target,
		CyclesPlanned:    cyclesPlanned,
		CyclesCompleted:  completed,
		IntervalMs:       intervalMs,
		Command:          command,
		Hops:             hops,
	}
}

// NormalizeSource maps internal target sources to the schema enum
// ("argument" | "csv" | "cidr"). The internal value "file" is the
// CLI's name for what the schema labels "csv".
func NormalizeSource(s string) string {
	if s == "file" {
		return "csv"
	}
	return s
}

// FromPingResult converts a probe.PingResult into a schema Result.
// `source` is the target's provenance ("argument" | "file" | "cidr");
// it is mapped to the schema enum automatically.
func FromPingResult(target, source string, durationMs int64, p probe.PingResult) Result {
	rows := make([]Row, 0, len(p.Packets))
	for _, pkt := range p.Packets {
		rows = append(rows, pingRow(pkt))
	}
	return Result{
		Target:     target,
		Source:     NormalizeSource(source),
		Operation:  "ping",
		Status:     "completed",
		Summary:    fmt.Sprintf("loss %.1f%%, avg %.3f ms", p.LossPct, p.AvgMs),
		Command:    p.Command,
		DurationMs: durationMs,
		Rows:       rows,
	}
}

// FromTraceResult converts a probe.TraceResult into a schema Result.
func FromTraceResult(target, source string, durationMs int64, t probe.TraceResult) Result {
	rows := make([]Row, 0, len(t.Hops))
	for _, h := range t.Hops {
		rows = append(rows, traceRow(h))
	}
	return Result{
		Target:     target,
		Source:     NormalizeSource(source),
		Operation:  "trace",
		Status:     "completed",
		Summary:    fmt.Sprintf("%d hops", len(t.Hops)),
		Command:    t.Command,
		DurationMs: durationMs,
		Rows:       rows,
	}
}

// FromMTRResult converts a probe.MTRResult snapshot into schema MtrHops.
func FromMTRResult(r probe.MTRResult) []MtrHop {
	out := make([]MtrHop, 0, len(r.Hops))
	for _, h := range r.Hops {
		out = append(out, mtrHop(h))
	}
	return out
}

func pingRow(p probe.PingReply) Row {
	seq := p.Seq
	r := Row{
		Seq:        &seq,
		Source:     p.Source,
		Target:     p.Target,
		Status:     p.Status,
		PublicDNS:  p.PublicDNS,
		PrivateDNS: p.PrivateDNS,
		Org:        p.Org,
		ASN:        p.ASN,
		Location:   p.Location,
		NetType:    p.NetType,
		Policy:     p.Policy,
	}
	if p.Status == "ok" {
		bytes := p.Bytes
		ttl := p.TTL
		t := p.TimeMs
		r.Bytes = &bytes
		r.TTL = &ttl
		r.TimeMs = &t
	}
	return r
}

func traceRow(h probe.TraceHop) Row {
	hop := h.Hop
	r := Row{
		Hop:        &hop,
		Source:     h.Source,
		Target:     h.Target,
		Hostname:   h.Host,
		HostIp:     h.IP,
		Status:     h.Status,
		PublicDNS:  h.PublicDNS,
		PrivateDNS: h.PrivateDNS,
		Org:        h.Org,
		ASN:        h.ASN,
		Location:   h.Location,
		NetType:    h.NetType,
		Policy:     h.Policy,
	}
	if h.Probe1Ms > 0 {
		v := h.Probe1Ms
		r.Probe1Ms = &v
	}
	if h.Probe2Ms > 0 {
		v := h.Probe2Ms
		r.Probe2Ms = &v
	}
	if h.Probe3Ms > 0 {
		v := h.Probe3Ms
		r.Probe3Ms = &v
	}
	return r
}

func mtrHop(s probe.MTRStat) MtrHop {
	lost := s.Snt - int(math.Round(float64(s.Snt)*(100-s.LossPc)/100))
	if lost < 0 {
		lost = 0
	}
	h := MtrHop{
		Hop:         s.Hop,
		Sent:        s.Snt,
		Lost:        lost,
		LossPercent: s.LossPc,
	}
	if s.IP != "" {
		ip := s.IP
		h.IP = &ip
	}
	if s.Snt-lost > 0 {
		last := s.Last
		avg := s.Avg
		best := s.Best
		worst := s.Wrst
		h.Last = &last
		h.Avg = &avg
		h.Best = &best
		h.Worst = &worst
		if s.StDev > 0 || s.Snt-lost >= 2 {
			sd := s.StDev
			h.StDev = &sd
		}
	}
	return h
}
