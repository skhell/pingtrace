package render

import (
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
)

// Progress is an inline loading indicator that uses the bubbles
// spinner *frames* but drives them with a plain ticker writing
// ANSI directly to stdout.
type Progress struct {
	out      io.Writer
	label    atomic.Value // string
	noColor  bool
	disabled bool

	stop chan struct{}
	done chan struct{}
	on   atomic.Bool

	frames     []string
	frameStyle lipgloss.Style
	dimStyle   lipgloss.Style

	start time.Time
}

// NewProgress builds a Progress for the given output writer.
// On non-TTY writers Start is a no-op.
func NewProgress(out io.Writer, label string, noColor bool) *Progress {
	p := &Progress{
		out:      out,
		noColor:  noColor,
		disabled: !writerIsTTY(out),
	}
	p.label.Store(label)
	sp := spinner.Dot
	p.frames = sp.Frames
	if !noColor {
		p.frameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
		p.dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	}
	return p
}

func writerIsTTY(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		if fi, err := f.Stat(); err == nil {
			if fi.Mode()&os.ModeCharDevice != 0 {
				return true
			}
		}
		return false
	}
	// Fall back to checking os.Stdout for unwrapped writers (e.g.
	// cobra's bytes.Buffer in tests). In that case we still want
	// the spinner when stdout is a real terminal.
	return IsTTY()
}

// Start begins the animation and paints the first frame
// synchronously so the user sees feedback even if Stop is called
// on the very next millisecond.
func (p *Progress) Start() {
	if p.disabled || p.on.Swap(true) {
		return
	}
	p.start = time.Now()
	p.stop = make(chan struct{})
	p.done = make(chan struct{})
	fmt.Fprint(p.out, "\033[?25l")
	p.paint(0)
	go p.loop()
}

func (p *Progress) loop() {
	defer close(p.done)
	t := time.NewTicker(80 * time.Millisecond)
	defer t.Stop()
	frame := 1
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			p.paint(frame)
			frame++
		}
	}
}

func (p *Progress) paint(frame int) {
	ch := p.frames[frame%len(p.frames)]
	label := p.currentLabel()
	elapsed := time.Since(p.start).Truncate(100 * time.Millisecond)
	var line string
	if p.noColor {
		line = fmt.Sprintf("%s %s (%s)", ch, label, elapsed)
	} else {
		line = p.frameStyle.Render(ch) + " " + label + " " + p.dimStyle.Render("("+elapsed.String()+")")
	}
	fmt.Fprint(p.out, "\r\033[2K"+line)
}

// Update changes the spinner label. The new label is picked up on
// the next tick (or immediately if you call Update twice).
func (p *Progress) Update(label string) {
	p.label.Store(label)
}

func (p *Progress) currentLabel() string {
	if v := p.label.Load(); v != nil {
		return v.(string)
	}
	return ""
}

// Stop terminates the goroutine, erases the inline line, and
// waits for the goroutine to exit so the next caller can draw
// without fighting for the cursor.
func (p *Progress) Stop() {
	if !p.on.Swap(false) {
		return
	}
	close(p.stop)
	<-p.done
	// Erase the spinner line. Cursor is restored by StreamTable.Close
	// (or here if no table follows, to avoid leaving the cursor hidden).
	fmt.Fprint(p.out, "\r\033[2K\033[?25h")
}
