package render

import (
	"fmt"
	"strconv"

	"github.com/skhell/pingtrace/internal/probe"
)

// Ping renders a ping result for one target.
func Ping(target string, r probe.PingResult, opts Options) {
	out := writer(opts)
	w := termWidth(opts)
	fmt.Fprintf(out, "PING %s  (%d/%d, loss %.1f%%, avg %.2f ms, min %.2f, max %.2f)\n",
		target, r.Received, r.Sent, r.LossPct, r.AvgMs, r.MinMs, r.MaxMs)
	if opts.Summary {
		return
	}
	cols := chooseColumns(PingAllColumns, pingEssentials, opts.Columns, w)
	rows := make([][]string, 0, len(r.Packets))
	for _, p := range r.Packets {
		rows = append(rows, pickPingRow(p, cols))
	}
	writeTable(out, cols, rows, opts, w)
}

func pickPingRow(p probe.PingReply, cols []string) []string {
	row := make([]string, len(cols))
	for i, c := range cols {
		switch c {
		case "seq":
			row[i] = strconv.Itoa(p.Seq)
		case "bytes":
			row[i] = strconv.Itoa(p.Bytes)
		case "ip":
			row[i] = p.IP
		case "ttl":
			row[i] = strconv.Itoa(p.TTL)
		case "time_ms":
			row[i] = fmt.Sprintf("%.2f", p.TimeMs)
		case "public_dns":
			row[i] = p.PublicDNS
		case "private_dns":
			row[i] = p.PrivateDNS
		case "org":
			row[i] = p.Org
		case "asn":
			row[i] = p.ASN
		case "location":
			row[i] = p.Location
		case "policy":
			row[i] = p.Policy
		case "net_type":
			row[i] = p.NetType
		case "status":
			row[i] = p.Status
		default:
			row[i] = ""
		}
	}
	return row
}

// Trace renders a trace result.
func Trace(target string, r probe.TraceResult, opts Options) {
	out := writer(opts)
	w := termWidth(opts)
	fmt.Fprintf(out, "TRACE %s  (%d hops)\n", target, len(r.Hops))
	if opts.Summary {
		return
	}
	cols := chooseColumns(TraceAllColumns, traceEssentials, opts.Columns, w)
	rows := make([][]string, 0, len(r.Hops))
	for _, h := range r.Hops {
		rows = append(rows, pickTraceRow(h, cols))
	}
	writeTable(out, cols, rows, opts, w)
}

func pickTraceRow(h probe.TraceHop, cols []string) []string {
	row := make([]string, len(cols))
	for i, c := range cols {
		switch c {
		case "hop":
			row[i] = strconv.Itoa(h.Hop)
		case "ip":
			if h.IP == "" {
				row[i] = "*"
			} else {
				row[i] = h.IP
			}
		case "host":
			row[i] = h.Host
		case "probe_1_ms":
			row[i] = formatProbe(h.Probe1Ms, h.Status)
		case "probe_2_ms":
			row[i] = formatProbe(h.Probe2Ms, h.Status)
		case "probe_3_ms":
			row[i] = formatProbe(h.Probe3Ms, h.Status)
		case "time_ms":
			row[i] = formatProbe(h.TimeMs, h.Status)
		case "public_dns":
			row[i] = h.PublicDNS
		case "private_dns":
			row[i] = h.PrivateDNS
		case "org":
			row[i] = h.Org
		case "asn":
			row[i] = h.ASN
		case "location":
			row[i] = h.Location
		case "policy":
			row[i] = h.Policy
		case "net_type":
			row[i] = h.NetType
		case "status":
			row[i] = h.Status
		default:
			row[i] = ""
		}
	}
	return row
}

func formatProbe(t float64, status string) string {
	if status != "ok" || t <= 0 {
		return "*"
	}
	return fmt.Sprintf("%.2f", t)
}

// MTR renders an MTR snapshot.
func MTR(target string, r probe.MTRResult, opts Options) {
	out := writer(opts)
	w := termWidth(opts)
	fmt.Fprintln(out, SectionHeading(fmt.Sprintf("MTR %s  (cycle %d)", target, r.Cycles), opts.NoColor))
	if opts.Summary {
		return
	}
	all := mtrEssentials
	cols := chooseColumns(all, mtrEssentials, opts.Columns, w)
	rows := make([][]string, 0, len(r.Hops))
	for _, h := range r.Hops {
		rows = append(rows, pickMTRRow(h, cols))
	}
	writeBorderedTable(out, cols, rows, opts)
}

func pickMTRRow(h probe.MTRStat, cols []string) []string {
	row := make([]string, len(cols))
	for i, c := range cols {
		switch c {
		case "hop":
			row[i] = strconv.Itoa(h.Hop)
		case "ip":
			if h.IP == "" {
				row[i] = "*"
			} else {
				row[i] = h.IP
			}
		case "loss":
			row[i] = fmt.Sprintf("%.1f%%", h.LossPc)
		case "snt":
			row[i] = strconv.Itoa(h.Snt)
		case "last":
			row[i] = fmt.Sprintf("%.1f", h.Last)
		case "avg":
			row[i] = fmt.Sprintf("%.1f", h.Avg)
		case "best":
			row[i] = fmt.Sprintf("%.1f", h.Best)
		case "wrst":
			row[i] = fmt.Sprintf("%.1f", h.Wrst)
		case "stdev":
			row[i] = fmt.Sprintf("%.1f", h.StDev)
		default:
			row[i] = ""
		}
	}
	return row
}
