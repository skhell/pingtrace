package render

import (
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Spinner shows a single-line "working..." indicator that updates
// in place via carriage return. It is meant for the short period
// between starting an operation and the first row arriving, so the
// user knows pingtrace is doing something during DNS resolution or
// other startup delays.
//
// On non-TTY writers Start is a no-op so logs stay clean.
type Spinner struct {
	out     io.Writer
	label   string
	noColor bool
	stop    chan struct{}
	done    chan struct{}
	running atomic.Bool
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// NewSpinner builds a spinner without starting it.
func NewSpinner(out io.Writer, label string, noColor bool) *Spinner {
	return &Spinner{out: out, label: label, noColor: noColor}
}

// Start launches the animation goroutine. No-op when stdout is
// not a TTY.
func (s *Spinner) Start() {
	if !IsTTY() {
		return
	}
	if s.running.Swap(true) {
		return
	}
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	go s.loop()
}

func (s *Spinner) loop() {
	defer close(s.done)
	t := time.NewTicker(80 * time.Millisecond)
	defer t.Stop()
	frame := 0
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	for {
		select {
		case <-s.stop:
			fmt.Fprint(s.out, "\r\033[2K")
			return
		case <-t.C:
			ch := spinnerFrames[frame%len(spinnerFrames)]
			frame++
			line := ch + " " + s.label
			if !s.noColor {
				line = style.Render(line)
			}
			fmt.Fprint(s.out, "\r\033[2K"+line)
		}
	}
}

// Stop erases the spinner line and waits for the goroutine to exit.
// Safe to call even if Start was a no-op.
func (s *Spinner) Stop() {
	if !s.running.Swap(false) {
		return
	}
	close(s.stop)
	<-s.done
}
