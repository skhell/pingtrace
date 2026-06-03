// Package mtrtui drives the live MTR view via bubbletea. The
// model receives MTRUpdate messages from the probe goroutine and
// redraws the table on each frame.
package mtrtui

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skhell/pingtrace/internal/probe"
	"github.com/skhell/pingtrace/internal/render"
)

type cycleMsg probe.MTRUpdate
type doneMsg struct{}

// Model is the bubbletea model for live MTR.
type Model struct {
	target  string
	snap    probe.MTRResult
	updates <-chan probe.MTRUpdate
	cycles  int
	done    bool
	opts    render.Options
	sp      spinner.Model
	start   time.Time
}

// NewProgram wires a tea.Program around the supplied channel.
func NewProgram(target string, cycles int, updates <-chan probe.MTRUpdate, opts render.Options) *tea.Program {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	if !opts.NoColor {
		sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	}
	m := Model{
		target:  target,
		updates: updates,
		cycles:  cycles,
		opts:    opts,
		sp:      sp,
		start:   time.Now(),
	}
	return tea.NewProgram(m, tea.WithAltScreen())
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(waitForUpdate(m.updates), m.sp.Tick)
}

func waitForUpdate(ch <-chan probe.MTRUpdate) tea.Cmd {
	return func() tea.Msg {
		u, ok := <-ch
		if !ok {
			return doneMsg{}
		}
		return cycleMsg(u)
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch t := msg.(type) {
	case tea.KeyMsg:
		switch t.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		}
	case cycleMsg:
		m.snap = probe.MTRUpdate(t).Snapshot
		if m.cycles > 0 && t.Cycle >= m.cycles {
			m.done = true
			return m, tea.Quit
		}
		return m, waitForUpdate(m.updates)
	case doneMsg:
		m.done = true
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.sp, cmd = m.sp.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) View() string {
	var buf bytes.Buffer
	opts := m.opts
	opts.Out = &buf

	if m.snap.Cycles == 0 || len(m.snap.Hops) == 0 {
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
		hint := lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("244"))
		fmt.Fprintln(&buf, render.SectionHeading(fmt.Sprintf("MTR %s", m.target), opts.NoColor))
		fmt.Fprintln(&buf)
		elapsed := time.Since(m.start).Truncate(100 * time.Millisecond)
		fmt.Fprintf(&buf, "  %s probing %s · waiting for first cycle %s\n",
			m.sp.View(), m.target, dim.Render("("+elapsed.String()+")"))
		fmt.Fprintln(&buf)
		fmt.Fprintln(&buf, hint.Render("  hop discovery can take several seconds - each TTL round-trips before the next starts"))
		fmt.Fprintln(&buf)
		fmt.Fprintln(&buf, "press q / Ctrl+C / ESC to stop")
		return buf.String()
	}

	render.MTR(m.target, m.snap, opts)
	fmt.Fprintln(&buf)
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	elapsed := time.Since(m.start).Truncate(100 * time.Millisecond)
	var status string
	if m.cycles > 0 {
		status = fmt.Sprintf("  %s cycle %d/%d · %d hops %s",
			m.sp.View(), m.snap.Cycles, m.cycles, len(m.snap.Hops),
			dim.Render("("+elapsed.String()+")"))
	} else {
		status = fmt.Sprintf("  %s cycle %d · %d hops %s",
			m.sp.View(), m.snap.Cycles, len(m.snap.Hops),
			dim.Render("("+elapsed.String()+")"))
	}
	fmt.Fprintln(&buf, status)
	fmt.Fprintln(&buf, "press q / Ctrl+C / ESC to stop")
	return buf.String()
}

// RunFallback drives the same channel without a TUI: prints a
// header line, then one summary line per cycle. Suitable for
// non-TTY / piped stdout.
func RunFallback(ctx context.Context, target string, updates <-chan probe.MTRUpdate, opts render.Options) probe.MTRResult {
	var last probe.MTRResult
	for {
		select {
		case <-ctx.Done():
			return last
		case u, ok := <-updates:
			if !ok {
				return last
			}
			last = u.Snapshot
			fmt.Fprintf(opts.Out, "[%s] cycle %d  hops=%d\n",
				time.Now().UTC().Format("15:04:05"), u.Cycle, len(last.Hops))
		}
	}
}

// Snapshot exposes the last MTR snapshot received by the model so
// the caller can print a persistent recap after the alt-screen
// program exits (otherwise the result is wiped when the terminal
// restores the main screen).
func (m Model) Snapshot() probe.MTRResult { return m.snap }
