package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/skhell/pingtrace/internal/csvexport"
	"github.com/skhell/pingtrace/internal/jsonreport"
	"github.com/skhell/pingtrace/internal/probe"
	"github.com/skhell/pingtrace/internal/render"
	"github.com/skhell/pingtrace/internal/target"
)

// bulkThreshold: at this many targets (or any CIDR-derived target)
// pingtrace switches from per-target tables to bulk mode.
const bulkThreshold = 5

// shouldUseBulk returns true when bulk mode should kick in for the
// resolved target list. Force-overridable via --tables.
func shouldUseBulk(targets []target.Target, force, disable bool) bool {
	if disable {
		return false
	}
	if force {
		return true
	}
	if len(targets) >= bulkThreshold {
		return true
	}
	for _, t := range targets {
		if t.Source == "cidr" {
			return true
		}
	}
	return false
}

// cidrFilenameTag returns the distinct CIDR(s) from the target list
// joined with "+" so CSV filenames can advertise their scope (e.g.
// "78.64.0.0/12" or "10.0.0.0/30+192.168.1.0/30"). Returns ""
// when no targets came from CIDR expansion.
func cidrFilenameTag(targets []target.Target) string {
	seen := map[string]struct{}{}
	order := []string{}
	for _, t := range targets {
		if t.Source != "cidr" || t.OriginalInput == "" {
			continue
		}
		if _, ok := seen[t.OriginalInput]; ok {
			continue
		}
		seen[t.OriginalInput] = struct{}{}
		order = append(order, t.OriginalInput)
	}
	if len(order) == 0 {
		return ""
	}
	// Cap at a handful to keep filenames readable.
	if len(order) > 3 {
		order = append(order[:3], fmt.Sprintf("and%dmore", len(order)-3))
	}
	return strings.Join(order, "+")
}

// bulkSample holds the result of one worker's probe.
type bulkSample struct {
	target string
	res    probe.PingResult
	err    error
}

// runBulk fans the target list out across a worker pool, drives a
// live progress bar, writes per-packet ping + per-hop trace rows
// to CSV (same format as single-target runs), and prints a brief
// leaderboard at the end.
func runBulk(ctx context.Context, targets []target.Target, f *rootFlags, opts render.Options, csvW *csvexport.Writer, jsonW *jsonreport.Writer, pingOpts probe.PingOptions, traceOpts probe.TraceOptions, enr enrichers) error {
	conc := f.concurrency
	if conc <= 0 {
		conc = 8
	}
	if conc > len(targets) {
		conc = len(targets)
	}

	total := len(targets)
	var done int64
	results := make([]bulkSample, 0, total)
	var mu sync.Mutex

	jobs := make(chan target.Target, total)
	for _, t := range targets {
		jobs <- t
	}
	close(jobs)

	bar := newBulkProgress(opts.Out, total, opts.NoColor)
	bar.Start()
	defer bar.Stop()

	var wg sync.WaitGroup
	for i := 0; i < conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobs {
				if ctx.Err() != nil {
					return
				}
				bar.SetCurrent(t.Value)

				var pingRes probe.PingResult
				if !f.noPing {
					pingStart := time.Now()
					pingRes, _ = probe.Ping(ctx, t.Value, pingOpts)
					for i := range pingRes.Packets {
						if pingRes.Packets[i].Status == "ok" {
							enrichReply(ctx, &pingRes.Packets[i], enr)
						}
					}
					pingDur := time.Since(pingStart).Milliseconds()
					if csvW != nil {
						_ = csvW.Ping(t.Value, pingRes)
					}
					if jsonW != nil {
						jsonW.AppendPing(t.Value, t.Source, pingDur, pingRes)
					}
				}

				if !f.noTrace {
					traceStart := time.Now()
					traceRes, _ := probe.Trace(ctx, t.Value, traceOpts)
					for i := range traceRes.Hops {
						if traceRes.Hops[i].Status == "ok" {
							enrichHop(ctx, &traceRes.Hops[i], enr)
						}
					}
					traceDur := time.Since(traceStart).Milliseconds()
					if csvW != nil {
						_ = csvW.Trace(t.Value, traceRes)
					}
					if jsonW != nil {
						jsonW.AppendTrace(t.Value, t.Source, traceDur, traceRes)
					}
				}

				mu.Lock()
				results = append(results, bulkSample{target: t.Value, res: pingRes})
				mu.Unlock()
				atomic.AddInt64(&done, 1)
				bar.SetDone(int(atomic.LoadInt64(&done)))
			}
		}()
	}
	wg.Wait()
	bar.Stop()

	sort.SliceStable(results, func(i, j int) bool {
		return results[i].res.AvgMs < results[j].res.AvgMs
	})

	printBulkSummary(opts.Out, results, total, opts.NoColor)
	return nil
}

// printBulkSummary prints a small bordered table of the fastest /
// most-lossy hosts so the user gets immediate insight at the end.
func printBulkSummary(out io.Writer, samples []bulkSample, total int, noColor bool) {
	if len(samples) == 0 {
		return
	}
	headers := []string{"target", "loss%", "avg ms", "min ms", "max ms", "org"}
	rows := make([][]string, 0, len(samples))
	reachable := 0
	for _, s := range samples {
		if s.res.Received > 0 {
			reachable++
		}
	}
	// Show up to 10 fastest reachable hosts, then up to 5 unreachable.
	shown := 0
	for _, s := range samples {
		if s.res.Received == 0 {
			continue
		}
		if shown >= 10 {
			break
		}
		first := firstOK(s.res)
		rows = append(rows, []string{
			s.target,
			fmt.Sprintf("%.1f", s.res.LossPct),
			fmt.Sprintf("%.2f", s.res.AvgMs),
			fmt.Sprintf("%.2f", s.res.MinMs),
			fmt.Sprintf("%.2f", s.res.MaxMs),
			truncate(first.Org, 28),
		})
		shown++
	}
	missCount := 0
	for _, s := range samples {
		if s.res.Received == 0 {
			if missCount < 5 {
				rows = append(rows, []string{s.target, "100.0", "-", "-", "-", "(no reply)"})
			}
			missCount++
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, render.SectionHeading(
		fmt.Sprintf("SUMMARY - %d/%d reachable", reachable, total),
		noColor,
	))
	render.WriteBorderedTable(out, headers, rows, noColor)
	if missCount > 5 {
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
		if noColor {
			dim = lipgloss.NewStyle()
		}
		fmt.Fprintln(out, dim.Render(fmt.Sprintf("  … and %d more unreachable hosts (see CSV)", missCount-5)))
	}
}

func firstOK(r probe.PingResult) probe.PingReply {
	for _, p := range r.Packets {
		if p.Status == "ok" {
			return p
		}
	}
	if len(r.Packets) > 0 {
		return r.Packets[0]
	}
	return probe.PingReply{}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// --- bulkProgress: animated single-line progress bar ----------

type bulkProgress struct {
	out      io.Writer
	total    int
	noColor  bool
	disabled bool

	doneN   int64
	current atomic.Value // string

	stop chan struct{}
	wait chan struct{}
	on   atomic.Bool
	mu   sync.Mutex

	start time.Time
}

func newBulkProgress(out io.Writer, total int, noColor bool) *bulkProgress {
	bp := &bulkProgress{
		out:      out,
		total:    total,
		noColor:  noColor,
		disabled: !isTTYWriter(out),
	}
	bp.current.Store("")
	return bp
}

func isTTYWriter(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		if fi, err := f.Stat(); err == nil {
			return fi.Mode()&os.ModeCharDevice != 0
		}
	}
	return render.IsTTY()
}

func (b *bulkProgress) Start() {
	if b.disabled || b.on.Swap(true) {
		return
	}
	b.start = time.Now()
	b.stop = make(chan struct{})
	b.wait = make(chan struct{})
	fmt.Fprint(b.out, "\033[?25l")
	b.paint()
	go b.loop()
}

func (b *bulkProgress) loop() {
	defer close(b.wait)
	t := time.NewTicker(120 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-b.stop:
			return
		case <-t.C:
			b.paint()
		}
	}
}

func (b *bulkProgress) SetDone(n int) {
	atomic.StoreInt64(&b.doneN, int64(n))
}

func (b *bulkProgress) SetCurrent(s string) {
	b.current.Store(s)
}

func (b *bulkProgress) paint() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.on.Load() {
		return
	}
	done := int(atomic.LoadInt64(&b.doneN))
	pct := 0.0
	if b.total > 0 {
		pct = float64(done) / float64(b.total) * 100
	}
	width := 28
	filled := int(float64(width) * pct / 100)
	if filled > width {
		filled = width
	}
	bar := "▓" + makeRepeat("▓", filled-1) + makeRepeat("░", width-filled)
	if filled <= 0 {
		bar = makeRepeat("░", width)
	}
	cur, _ := b.current.Load().(string)
	elapsed := time.Since(b.start).Truncate(100 * time.Millisecond)
	var line string
	if b.noColor {
		line = fmt.Sprintf("[%s] %d/%d  %.1f%%  current: %s  (%s)",
			bar, done, b.total, pct, cur, elapsed)
	} else {
		barStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
		num := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
		line = fmt.Sprintf("%s %s  %s  current: %s  %s",
			barStyle.Render("["+bar+"]"),
			num.Render(fmt.Sprintf("%d/%d", done, b.total)),
			num.Render(fmt.Sprintf("%.1f%%", pct)),
			cur,
			dim.Render("("+elapsed.String()+")"),
		)
	}
	fmt.Fprint(b.out, "\r\033[2K"+line)
}

func (b *bulkProgress) Stop() {
	if !b.on.Swap(false) {
		return
	}
	close(b.stop)
	<-b.wait
	fmt.Fprint(b.out, "\r\033[2K\033[?25h")
}

func makeRepeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, 0, n*len(s))
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
