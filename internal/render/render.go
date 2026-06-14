// Package render produces human-readable terminal output for
// pingtrace results: ping tables, trace tables, MTR tables, and
// per-target summaries.
//
// The renderer is width-aware. When the terminal is narrower than
// the natural column widths, lowest-priority enrichment columns are
// dropped in this exact order before truncation kicks in:
//
//	policy, net_type, private_dns, location, asn, org, public_dns
//
// The essentials (`seq` / `hop`, `ip`, time, `status`) are always
// preserved. With --wide the auto-fit pass is skipped.
package render

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Options drives every rendering call. Zero-value is "auto" mode.
type Options struct {
	Columns        []string  // explicit column whitelist (user --columns flag)
	DefaultColumns []string  // caller-filtered default set; replaces PingAllColumns/TraceAllColumns when set
	Summary        bool      // one line per target
	Wide           bool      // disable auto-fit
	NoColor        bool
	Out            io.Writer
	Width          int // 0 = autodetect from terminal
}

// DropPriority lists enrichment columns from lowest to highest
// importance. Columns earlier in the slice are removed first when
// space is tight.
var DropPriority = []string{
	"policy", "net_type", "private_dns", "location", "asn", "org", "public_dns",
	"source", "target",
}

var pingEssentials = []string{"seq", "time_ms", "status"}
var traceEssentials = []string{"hop", "host_ip", "probe_1_ms", "status"}
var mtrEssentials = []string{"hop", "ip", "loss", "snt", "last", "avg", "best", "wrst", "stdev"}

// PingAllColumns is the full default ping column set in display order.
var PingAllColumns = []string{
	"seq", "bytes", "source", "target", "ttl", "time_ms",
	"public_dns", "private_dns", "org", "asn", "location", "net_type", "policy", "status",
}

// TraceAllColumns is the full default trace column set in display order.
var TraceAllColumns = []string{
	"hop", "source", "target", "hostname", "host_ip", "probe_1_ms", "probe_2_ms", "probe_3_ms",
	"public_dns", "private_dns", "org", "asn", "location", "net_type", "policy", "status",
}

func writer(opts Options) io.Writer {
	if opts.Out != nil {
		return opts.Out
	}
	return os.Stdout
}

func termWidth(opts Options) int {
	if opts.Width > 0 {
		return opts.Width
	}
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 100
}

// IsTTY reports whether stdout is an interactive terminal.
func IsTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// --- column helpers --------------------------------------------

func normalizeCols(in []string) []string {
	out := make([]string, 0, len(in))
	for _, c := range in {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		out = append(out, camelToSnake(c))
	}
	return out
}

func camelToSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + 32)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func chooseColumns(all, essentials, requested []string, width int) []string {
	if len(requested) > 0 {
		return normalizeCols(requested)
	}
	cols := append([]string{}, all...)
	if width <= 0 {
		return cols
	}
	for naturalWidth(cols) > width {
		dropped := false
		for _, low := range DropPriority {
			if !contains(essentials, low) && contains(cols, low) {
				cols = remove(cols, low)
				dropped = true
				break
			}
		}
		if !dropped {
			break
		}
	}
	return cols
}

func naturalWidth(cols []string) int {
	w := 0
	for _, c := range cols {
		w += len(c) + 3
	}
	return w
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// RemoveColumn removes one column name from a slice, returning a new slice.
func RemoveColumn(s []string, v string) []string { return remove(s, v) }

func remove(s []string, v string) []string {
	out := make([]string, 0, len(s))
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// --- styles ----------------------------------------------------

func headerStyle(noColor bool) lipgloss.Style {
	if noColor {
		return lipgloss.NewStyle().Bold(true)
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("33"))
}

func statusStyle(status string, noColor bool) lipgloss.Style {
	if noColor {
		return lipgloss.NewStyle()
	}
	switch status {
	case "ok":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	case "timeout":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	}
}

// WriteBorderedTable renders a static table with rounded borders.
// Exposed for callers outside the render package that have already
// prepared headers + rows (e.g. the bulk-mode summary).
func WriteBorderedTable(out io.Writer, headers []string, rows [][]string, noColor bool) {
	writeBorderedTable(out, headers, rows, Options{NoColor: noColor, Out: out})
}

// writeBorderedTable renders a static table (all rows known up
// front) with rounded lipgloss borders, matching the look of the
// streaming tables. Column widths are sized to content.
func writeBorderedTable(out io.Writer, headers []string, rows [][]string, opts Options) {
	colW := make([]int, len(headers))
	for i, h := range headers {
		colW[i] = len(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i >= len(colW) {
				continue
			}
			if len(c) > colW[i] {
				colW[i] = len(c)
			}
		}
	}

	border := lipgloss.RoundedBorder()
	var bs lipgloss.Style
	if !opts.NoColor {
		bs = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	}
	hStyle := headerStyle(opts.NoColor)

	line := func(left, fill, mid, right string) {
		var b strings.Builder
		b.WriteString(left)
		for i, w := range colW {
			b.WriteString(strings.Repeat(fill, w+2))
			if i < len(colW)-1 {
				b.WriteString(mid)
			}
		}
		b.WriteString(right)
		fmt.Fprintln(out, bs.Render(b.String()))
	}

	line(border.TopLeft, border.Top, border.MiddleTop, border.TopRight)

	sep := bs.Render(border.Left)
	fmt.Fprint(out, sep)
	for i, h := range headers {
		fmt.Fprint(out, " "+hStyle.Render(padTrunc(h, colW[i]))+" ")
		if i < len(headers)-1 {
			fmt.Fprint(out, sep)
		}
	}
	fmt.Fprintln(out, bs.Render(border.Right))

	line(border.MiddleLeft, border.Top, border.Middle, border.MiddleRight)

	for _, r := range rows {
		fmt.Fprint(out, sep)
		for i := 0; i < len(headers); i++ {
			var c string
			if i < len(r) {
				c = r[i]
			}
			cell := padTrunc(c, colW[i])
			if headers[i] == "status" {
				cell = statusStyle(c, opts.NoColor).Render(cell)
			}
			fmt.Fprint(out, " "+cell+" ")
			if i < len(headers)-1 {
				fmt.Fprint(out, sep)
			}
		}
		fmt.Fprintln(out, bs.Render(border.Right))
	}

	line(border.BottomLeft, border.Bottom, border.MiddleBottom, border.BottomRight)
}

func writeTable(out io.Writer, headers []string, rows [][]string, opts Options, width int) {
	colW := make([]int, len(headers))
	for i, h := range headers {
		colW[i] = len(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i >= len(colW) {
				continue
			}
			if len(c) > colW[i] {
				colW[i] = len(c)
			}
		}
	}

	if !opts.Wide && width > 0 {
		total := 0
		for _, w := range colW {
			total += w + 2
		}
		if total > width {
			over := total - width
			for over > 0 {
				maxI := 0
				for i, w := range colW {
					if !isEssential(headers[i]) && w > colW[maxI] {
						maxI = i
					}
				}
				if colW[maxI] <= 6 {
					break
				}
				colW[maxI]--
				over--
			}
		}
	}

	hStyle := headerStyle(opts.NoColor)
	for i, h := range headers {
		fmt.Fprint(out, hStyle.Render(padTrunc(h, colW[i])))
		if i < len(headers)-1 {
			fmt.Fprint(out, "  ")
		}
	}
	fmt.Fprintln(out)
	for _, r := range rows {
		for i, c := range r {
			if i >= len(colW) {
				continue
			}
			cell := padTrunc(c, colW[i])
			if headers[i] == "status" {
				cell = statusStyle(c, opts.NoColor).Render(cell)
			}
			fmt.Fprint(out, cell)
			if i < len(headers)-1 {
				fmt.Fprint(out, "  ")
			}
		}
		fmt.Fprintln(out)
	}
}

func isEssential(h string) bool {
	switch h {
	case "seq", "hop", "host_ip", "time_ms", "probe_1_ms", "status",
		"loss", "snt", "last", "avg", "best", "wrst", "stdev":
		return true
	}
	return false
}

func padTrunc(s string, w int) string {
	if len(s) > w {
		if w <= 1 {
			return s[:w]
		}
		return s[:w-1] + "…"
	}
	return s + strings.Repeat(" ", w-len(s))
}
