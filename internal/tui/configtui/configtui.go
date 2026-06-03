// Package configtui implements a bubbletea-powered interactive
// editor for the pingtrace config file. It is launched from
// `pingtrace config` (with no subcommand) when stdout is a TTY.
package configtui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skhell/pingtrace/internal/config"
)

type mode int

const (
	modeList mode = iota
	modeEdit
)

// row is a flat display item: either a category header (key="") or
// an editable key row.
type row struct {
	header bool
	cat    string
	key    string
}

type model struct {
	rows    []row
	values  map[string]any
	cursor  int
	mode    mode
	editing string
	input   textinput.Model
	status  string
	err     string
	width   int
	height  int
}

// Run launches the interactive editor. Returns on quit or error.
func Run() error {
	eff, err := config.Effective()
	if err != nil {
		return err
	}
	m := initialModel(eff)
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func buildRows() []row {
	out := []row{}
	for _, g := range config.KeysByCategory() {
		cat := g[0].(string)
		keys := g[1].([]string)
		out = append(out, row{header: true, cat: cat})
		for _, k := range keys {
			out = append(out, row{key: k})
		}
	}
	return out
}

func initialModel(eff map[string]any) model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 256
	ti.Width = 60
	rows := buildRows()
	cursor := 0
	// start on first editable row
	for i, r := range rows {
		if !r.header {
			cursor = i
			break
		}
	}
	return model{
		rows:   rows,
		values: eff,
		cursor: cursor,
		input:  ti,
	}
}

func (m model) selectedKey() string {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return ""
	}
	r := m.rows[m.cursor]
	if r.header {
		return ""
	}
	return r.key
}

func (m model) Init() tea.Cmd { return nil }

func (m model) nextCursor(delta int) int {
	n := len(m.rows)
	if n == 0 {
		return m.cursor
	}
	i := m.cursor
	for steps := 0; steps < n; steps++ {
		i += delta
		if i < 0 || i >= n {
			return m.cursor
		}
		if !m.rows[i].header {
			return i
		}
	}
	return m.cursor
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.mode == modeEdit {
			switch msg.String() {
			case "esc":
				m.mode = modeList
				m.input.Blur()
				m.status = "edit cancelled"
				return m, nil
			case "enter":
				newVal := m.input.Value()
				if err := config.Set(m.editing, newVal); err != nil {
					m.err = err.Error()
				} else {
					m.err = ""
					m.status = "saved " + m.editing
					if eff, err := config.Effective(); err == nil {
						m.values = eff
					}
				}
				m.mode = modeList
				m.input.Blur()
				return m, nil
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.cursor = m.nextCursor(-1)
		case "down", "j":
			m.cursor = m.nextCursor(1)
		case "g":
			m.cursor = 0
			if m.rows[0].header {
				m.cursor = m.nextCursor(1)
			}
		case "G":
			m.cursor = len(m.rows) - 1
			if m.rows[m.cursor].header {
				m.cursor = m.nextCursor(-1)
			}
		case "enter", "e":
			k := m.selectedKey()
			if k == "" {
				return m, nil
			}
			m.editing = k
			cur := config.Format(m.values[k])
			if cur == "(unset)" {
				cur = ""
			}
			if config.SecretKeys[k] {
				cur = ""
			}
			m.input.SetValue(cur)
			m.input.CursorEnd()
			m.input.Focus()
			m.mode = modeEdit
			m.status = ""
			m.err = ""
		case "u":
			k := m.selectedKey()
			if k == "" {
				return m, nil
			}
			if err := config.Unset(k); err != nil {
				m.err = err.Error()
			} else {
				if eff, err := config.Effective(); err == nil {
					m.values = eff
				}
				m.status = "unset " + k
				m.err = ""
			}
		}
	}
	return m, nil
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	catStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")).MarginTop(1)
	keyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
	valStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	cursorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	hintStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	descStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)
	urlStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	editStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).BorderForeground(lipgloss.Color("212"))
	panelStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1).MarginTop(1)
)

func (m model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("pingtrace config"))
	b.WriteString("\n")

	maxKey := 0
	for _, r := range m.rows {
		if !r.header && len(r.key) > maxKey {
			maxKey = len(r.key)
		}
	}

	// Vertical paging: fit all rows minus a small footer reserve.
	visible := m.height - 10
	if visible < 8 {
		visible = 8
	}
	if visible > len(m.rows) {
		visible = len(m.rows)
	}
	start := 0
	if m.cursor >= visible {
		start = m.cursor - visible + 1
	}
	end := start + visible
	if end > len(m.rows) {
		end = len(m.rows)
	}

	for i := start; i < end; i++ {
		r := m.rows[i]
		if r.header {
			b.WriteString("\n" + catStyle.Render(r.cat) + "\n")
			continue
		}
		v := config.Format(config.Redact(r.key, m.values[r.key]))
		cursor := "  "
		var line string
		if i == m.cursor {
			cursor = cursorStyle.Render("❯ ")
			line = cursorStyle.Render(fmt.Sprintf("%-*s", maxKey, r.key)) + "  " + valStyle.Render(v)
		} else {
			line = keyStyle.Render(fmt.Sprintf("%-*s", maxKey, r.key)) + "  " + valStyle.Render(v)
		}
		b.WriteString("  " + cursor + line + "\n")
	}

	// Detail panel for the selected key
	k := m.selectedKey()
	if k != "" {
		meta := config.Meta()
		if md, ok := meta[k]; ok {
			detail := keyStyle.Render(k)
			if md.Description != "" {
				detail += "\n" + descStyle.Render(md.Description)
			}
			if md.Hint != "" {
				detail += "\n" + urlStyle.Render("↳ "+md.Hint)
			}
			b.WriteString(panelStyle.Render(detail) + "\n")
		}
	}

	if m.mode == modeEdit {
		b.WriteString(hintStyle.Render("editing ") + keyStyle.Render(m.editing) + "\n")
		b.WriteString(editStyle.Render(m.input.View()) + "\n")
		b.WriteString(hintStyle.Render("enter: save   esc: cancel"))
	} else {
		b.WriteString(hintStyle.Render("↑/↓ move   enter/e edit   u unset   q quit"))
	}

	if m.status != "" {
		b.WriteString("\n" + okStyle.Render(m.status))
	}
	if m.err != "" {
		b.WriteString("\n" + errStyle.Render("error: "+m.err))
	}
	return b.String()
}
