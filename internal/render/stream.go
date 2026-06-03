package render

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"

	"github.com/skhell/pingtrace/internal/probe"
)

var (
	pingWidthHints = map[string]int{
		"seq": 4, "bytes": 5, "ip": 15, "ttl": 4, "time_ms": 8,
		"public_dns": 22, "org": 20, "asn": 8, "location": 16,
		"net_type": 10, "policy": 11, "status": 7,
	}
	traceWidthHints = map[string]int{
		"hop": 3, "host": 22, "ip": 15,
		"probe_1_ms": 10, "probe_2_ms": 10, "probe_3_ms": 10,
		"public_dns": 22, "org": 20, "asn": 8, "location": 16,
		"net_type": 10, "policy": 11, "status": 7,
	}
)

// ClearScreen wipes the terminal and homes the cursor when stdout
// is a TTY. No-op on pipes/redirects so logs stay clean.
func ClearScreen() {
	if !IsTTY() {
		return
	}
	fmt.Fprint(os.Stdout, "\033[2J\033[H")
}

// ShowCursor unconditionally restores the cursor. Used as a defer
// safety net so an interrupt mid-stream never leaves the cursor
// hidden.
func ShowCursor() {
	if !IsTTY() {
		return
	}
	fmt.Fprint(os.Stdout, "\033[?25h")
}

// StreamTable prints a bordered table header up front, then accepts
// rows one at a time via AddRow. Column widths are fixed so each row
// can be flushed immediately (auto-fit-to-content would require
// buffering every row first, defeating streaming).
type StreamTable struct {
	out         io.Writer
	headers     []string
	widths      []int
	noColor     bool
	closed      bool
	border      lipgloss.Border
	borderFG    lipgloss.Color
	interactive bool // TTY: keep bottom border visible via cursor redraw
	hasBottom   bool // true once the bottom border has been printed at least once

	// status footer: an animated line beneath the bottom border that
	// shows what we're waiting on. Painted by a ticker goroutine and
	// also re-painted on every AddRow.
	statusMu      sync.Mutex
	statusLabel   atomic.Value // string
	statusFrames  []string
	statusFrame   int
	statusStart   time.Time
	statusOn      bool
	statusStop    chan struct{}
	statusDone    chan struct{}
	statusVisible bool // a status line is currently on screen
	frameStyle    lipgloss.Style
	dimStyle      lipgloss.Style
}

// NewStreamTable prints the top border + header + separator and
// returns a writer ready to receive rows.
func NewStreamTable(out io.Writer, headers []string, widths []int, noColor bool) *StreamTable {
	if len(widths) != len(headers) {
		widths = make([]int, len(headers))
		for i, h := range headers {
			widths[i] = max(len(h), 6)
		}
	}
	for i, h := range headers {
		if len(h) > widths[i] {
			widths[i] = len(h)
		}
	}
	st := &StreamTable{
		out:         out,
		headers:     headers,
		widths:      widths,
		noColor:     noColor,
		border:      lipgloss.RoundedBorder(),
		borderFG:    lipgloss.Color("240"),
		interactive: IsTTY() && !noColor,
	}
	if st.interactive {
		fmt.Fprint(out, "\033[?25l") // hide cursor; restored by Close
	}
	st.printTop()
	st.printHeader()
	st.printMid()
	if st.interactive {
		st.printBottom()
		st.hasBottom = true
	}
	return st
}

func (s *StreamTable) borderStyle() lipgloss.Style {
	if s.noColor {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(s.borderFG)
}

func (s *StreamTable) printTop() {
	bs := s.borderStyle()
	var b strings.Builder
	b.WriteString(s.border.TopLeft)
	for i, w := range s.widths {
		b.WriteString(strings.Repeat(s.border.Top, w+2))
		if i < len(s.widths)-1 {
			b.WriteString(s.border.MiddleTop)
		}
	}
	b.WriteString(s.border.TopRight)
	fmt.Fprintln(s.out, bs.Render(b.String()))
}

func (s *StreamTable) printMid() {
	bs := s.borderStyle()
	var b strings.Builder
	b.WriteString(s.border.MiddleLeft)
	for i, w := range s.widths {
		b.WriteString(strings.Repeat(s.border.Top, w+2))
		if i < len(s.widths)-1 {
			b.WriteString(s.border.Middle)
		}
	}
	b.WriteString(s.border.MiddleRight)
	fmt.Fprintln(s.out, bs.Render(b.String()))
}

func (s *StreamTable) printBottom() {
	bs := s.borderStyle()
	var b strings.Builder
	b.WriteString(s.border.BottomLeft)
	for i, w := range s.widths {
		b.WriteString(strings.Repeat(s.border.Bottom, w+2))
		if i < len(s.widths)-1 {
			b.WriteString(s.border.MiddleBottom)
		}
	}
	b.WriteString(s.border.BottomRight)
	fmt.Fprintln(s.out, bs.Render(b.String()))
}

func (s *StreamTable) printHeader() {
	hStyle := headerStyle(s.noColor)
	bs := s.borderStyle()
	sep := bs.Render(s.border.Left)
	fmt.Fprint(s.out, sep)
	for i, h := range s.headers {
		fmt.Fprint(s.out, " "+hStyle.Render(padTrunc(h, s.widths[i]))+" ")
		if i < len(s.headers)-1 {
			fmt.Fprint(s.out, bs.Render(s.border.Left))
		}
	}
	fmt.Fprintln(s.out, bs.Render(s.border.Right))
}

// AddRow writes one bordered row immediately. In interactive mode
// it overwrites the always-present bottom border (and the animated
// status footer if any), prints the row, re-prints the bottom
// border, and re-prints a fresh status frame so the table never
// looks "unfinished" while data is streaming in.
func (s *StreamTable) AddRow(row []string) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	bs := s.borderStyle()
	if s.interactive && s.hasBottom {
		// Erase status line (if visible) and bottom border.
		if s.statusVisible {
			fmt.Fprint(s.out, "\033[1A\r\033[2K")
			s.statusVisible = false
		}
		fmt.Fprint(s.out, "\033[1A\r\033[2K")
	}
	sep := bs.Render(s.border.Left)
	fmt.Fprint(s.out, sep)
	for i := 0; i < len(s.headers); i++ {
		var c string
		if i < len(row) {
			c = row[i]
		}
		cell := padTrunc(c, s.widths[i])
		if s.headers[i] == "status" {
			cell = statusStyle(c, s.noColor).Render(cell)
		}
		fmt.Fprint(s.out, " "+cell+" ")
		if i < len(s.headers)-1 {
			fmt.Fprint(s.out, bs.Render(s.border.Left))
		}
	}
	fmt.Fprintln(s.out, bs.Render(s.border.Right))
	if s.interactive {
		s.printBottom()
		s.hasBottom = true
		if s.statusOn {
			s.paintStatusLocked()
		}
	}
	if f, ok := s.out.(interface{ Sync() error }); ok {
		_ = f.Sync()
	}
}

// Close prints the bottom border if it hasn't been printed yet,
// stops the status spinner, removes the status line, and restores
// the cursor in interactive mode. Safe to call more than once.
func (s *StreamTable) Close() {
	if s.closed {
		return
	}
	s.closed = true
	s.stopStatus()
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	if s.interactive && s.statusVisible {
		fmt.Fprint(s.out, "\033[1A\r\033[2K")
		s.statusVisible = false
	}
	if !s.hasBottom {
		s.printBottom()
	}
	if s.interactive {
		fmt.Fprint(s.out, "\033[?25h")
	}
}

// StartStatus enables an animated single-line footer below the
// bottom border. The footer reads <spinner> <label> (<elapsed>).
// Call SetStatus to change the label as work progresses.
// No-op on non-interactive writers.
func (s *StreamTable) StartStatus(label string) {
	if !s.interactive || s.statusOn {
		return
	}
	sp := spinner.Dot
	s.statusFrames = sp.Frames
	if !s.noColor {
		s.frameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
		s.dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	}
	s.statusLabel.Store(label)
	s.statusStart = time.Now()
	s.statusOn = true
	s.statusStop = make(chan struct{})
	s.statusDone = make(chan struct{})
	s.statusMu.Lock()
	s.paintStatusLocked()
	s.statusMu.Unlock()
	go s.statusLoop()
}

// SetStatus updates the footer label. Picked up on the next tick.
func (s *StreamTable) SetStatus(label string) {
	s.statusLabel.Store(label)
}

func (s *StreamTable) stopStatus() {
	if !s.statusOn {
		return
	}
	s.statusOn = false
	close(s.statusStop)
	<-s.statusDone
}

func (s *StreamTable) statusLoop() {
	defer close(s.statusDone)
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-s.statusStop:
			return
		case <-t.C:
			s.statusMu.Lock()
			s.statusFrame++
			s.paintStatusLocked()
			s.statusMu.Unlock()
		}
	}
}

// paintStatusLocked draws or refreshes the status footer line.
// Caller MUST hold statusMu.
func (s *StreamTable) paintStatusLocked() {
	if !s.statusOn {
		return
	}
	ch := s.statusFrames[s.statusFrame%len(s.statusFrames)]
	label, _ := s.statusLabel.Load().(string)
	elapsed := time.Since(s.statusStart).Truncate(100 * time.Millisecond)
	var line string
	if s.noColor {
		line = fmt.Sprintf("%s %s (%s)", ch, label, elapsed)
	} else {
		line = s.frameStyle.Render(ch) + " " + label + " " + s.dimStyle.Render("("+elapsed.String()+")")
	}
	if s.statusVisible {
		fmt.Fprint(s.out, "\033[1A\r\033[2K"+line+"\n")
	} else {
		fmt.Fprint(s.out, line+"\n")
		s.statusVisible = true
	}
}

// --- ping streaming -------------------------------------------

func sectionStyle(noColor bool) lipgloss.Style {
	if noColor {
		return lipgloss.NewStyle().Bold(true)
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
}

// SectionHeading returns the styled "PING target" / "TRACE target"
// heading rendered as a single line.
func SectionHeading(label string, noColor bool) string {
	return sectionStyle(noColor).Render(label)
}

// PingStreamHeader prints the ping section heading and the column
// header row, returning a StreamTable ready to receive replies.
//
// Deprecated: callers that want a spinner should print SectionHeading
// themselves and call NewPingStreamTable lazily on the first row.
func PingStreamHeader(out io.Writer, target string, opts Options) *StreamTable {
	fmt.Fprintln(out, SectionHeading("PING "+target, opts.NoColor))
	return NewPingStreamTable(out, opts)
}

// NewPingStreamTable builds a streaming table sized for the ping
// columns. Call after the section heading has been printed.
func NewPingStreamTable(out io.Writer, opts Options) *StreamTable {
	cols := chooseColumnsByWidth(PingAllColumns, pingEssentials, opts.Columns, pingWidthHints, streamWidth(opts), opts.Wide)
	widths := mapWidths(cols, pingWidthHints)
	return NewStreamTable(out, cols, widths, opts.NoColor)
}

// PingStreamRow renders a single reply.
func PingStreamRow(st *StreamTable, p probe.PingReply) {
	st.AddRow(pickPingRow(p, st.headers))
}

// PingStreamFooter closes the table and prints the aggregated
// summary line beneath it.
func PingStreamFooter(out io.Writer, r probe.PingResult) {
	fmt.Fprintf(out, "  → %d/%d, loss %.1f%%, avg %.2f ms, min %.2f, max %.2f\n",
		r.Received, r.Sent, r.LossPct, r.AvgMs, r.MinMs, r.MaxMs)
}

// --- trace streaming ------------------------------------------

func TraceStreamHeader(out io.Writer, target string, opts Options) *StreamTable {
	fmt.Fprintln(out, SectionHeading("TRACE "+target, opts.NoColor))
	return NewTraceStreamTable(out, opts)
}

// NewTraceStreamTable builds a streaming table sized for the
// traceroute columns. Call after the section heading has been
// printed.
func NewTraceStreamTable(out io.Writer, opts Options) *StreamTable {
	cols := chooseColumnsByWidth(TraceAllColumns, traceEssentials, opts.Columns, traceWidthHints, streamWidth(opts), opts.Wide)
	widths := mapWidths(cols, traceWidthHints)
	return NewStreamTable(out, cols, widths, opts.NoColor)
}

func TraceStreamRow(st *StreamTable, h probe.TraceHop) {
	st.AddRow(pickTraceRow(h, st.headers))
}

func TraceStreamFooter(out io.Writer, r probe.TraceResult) {
	hops := len(r.Hops)
	if hops == 0 {
		fmt.Fprintln(out, "  → 0 hops")
		return
	}

	timeouts := 0
	var lastOK *probe.TraceHop
	for i := range r.Hops {
		h := &r.Hops[i]
		if h.Status == "timeout" || h.IP == "" {
			timeouts++
			continue
		}
		lastOK = h
	}

	final := "unreachable"
	rttPart := ""
	if lastOK != nil {
		final = lastOK.IP
		if lastOK.PublicDNS != "" {
			final = lastOK.PublicDNS + " (" + lastOK.IP + ")"
		} else if lastOK.Hostname != "" {
			final = lastOK.Hostname + " (" + lastOK.IP + ")"
		}
		best := lastOK.Probe1Ms
		for _, v := range []float64{lastOK.Probe2Ms, lastOK.Probe3Ms} {
			if v > 0 && (best == 0 || v < best) {
				best = v
			}
		}
		if best > 0 {
			rttPart = fmt.Sprintf(", %.2f ms", best)
		}
	}

	loss := ""
	if timeouts > 0 {
		loss = fmt.Sprintf(", %d/%d timeouts", timeouts, hops)
	}

	fmt.Fprintf(out, "  → %d hops, final %s%s%s\n", hops, final, rttPart, loss)
}

// --- helpers --------------------------------------------------

func streamWidth(opts Options) int {
	if opts.Wide {
		return 0
	}
	return termWidth(opts)
}

func mapWidths(cols []string, table map[string]int) []int {
	out := make([]int, len(cols))
	for i, c := range cols {
		if w, ok := table[c]; ok {
			out[i] = w
		} else {
			out[i] = max(len(c), 6)
		}
	}
	return out
}

// chooseColumnsByWidth drops lowest-priority columns until the
// rendered width (cells + padding + borders) fits termW. Returns the
// full set when termW <= 0 (e.g. piped) or wide=true.
func chooseColumnsByWidth(all, essentials, requested []string, widths map[string]int, termW int, wide bool) []string {
	if len(requested) > 0 {
		return normalizeCols(requested)
	}
	cols := append([]string{}, all...)
	if wide || termW <= 0 {
		return cols
	}
	for renderedWidth(cols, widths) > termW {
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

// renderedWidth = sum(width+2 padding) + (n+1) border columns.
func renderedWidth(cols []string, widths map[string]int) int {
	if len(cols) == 0 {
		return 0
	}
	total := len(cols) + 1
	for _, c := range cols {
		w, ok := widths[c]
		if !ok {
			w = max(len(c), 6)
		}
		total += w + 2
	}
	return total
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
