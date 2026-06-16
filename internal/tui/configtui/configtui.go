// Package configtui implements a bubbletea-powered interactive
// editor for the pingtrace config file. It is launched from
// `pingtrace config` (with no subcommand) when stdout is a TTY.
package configtui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skhell/pingtrace/internal/config"
	"github.com/skhell/pingtrace/internal/iana"
)

type mode int

const (
	modeList mode = iota
	modeEdit
)

// row is a selectable item inside a tab: either an editable key or a
// triggered action (like the IANA database sync button).
type row struct {
	key    string
	action string
}

func (r row) isAction() bool { return r.action != "" }

// tabData groups the rows that belong to one config category.
type tabData struct {
	category string
	rows     []row
}

type syncDoneMsg struct{ err error }

type model struct {
	tabs       []tabData
	activeTab  int
	tabCursors map[int]int // remembered cursor position per tab index
	values     map[string]any
	mode       mode
	editing    string
	input      textinput.Model
	status     string
	err        string
	syncing    bool
	width      int
	height     int
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

// tabShortNames maps full category names to compact tab labels.
var tabShortNames = map[string]string{
	"Tokens & accounts":  "API",
	"DNS resolution":     "DNS",
	"Ping engine":        "Ping",
	"Traceroute engine":  "Trace",
	"MTR engine":         "MTR",
	"Port scan":          "Scan",
	"Color thresholds":   "Colors",
	"IANA port database": "IANA",
	"Other":              "Other",
}

func shortTabName(cat string) string {
	if s, ok := tabShortNames[cat]; ok {
		return s
	}
	if len(cat) > 8 {
		return cat[:8]
	}
	return cat
}

func buildTabs() []tabData {
	var tabs []tabData
	for _, g := range config.KeysByCategory() {
		cat := g[0].(string)
		keys := g[1].([]string)
		var rows []row
		for _, k := range keys {
			rows = append(rows, row{key: k})
		}
		if cat == "IANA port database" {
			rows = append(rows, row{action: "iana.sync"})
		}
		if len(rows) > 0 {
			tabs = append(tabs, tabData{category: cat, rows: rows})
		}
	}
	return tabs
}

func initialModel(eff map[string]any) model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 256
	ti.Width = 60
	return model{
		tabs:       buildTabs(),
		activeTab:  0,
		tabCursors: make(map[int]int),
		values:     eff,
		input:      ti,
	}
}

func (m model) currentRows() []row {
	if m.activeTab < 0 || m.activeTab >= len(m.tabs) {
		return nil
	}
	return m.tabs[m.activeTab].rows
}

func (m model) cursor() int {
	return m.tabCursors[m.activeTab]
}

func (m model) selectedRow() row {
	rows := m.currentRows()
	c := m.cursor()
	if c < 0 || c >= len(rows) {
		return row{}
	}
	return rows[c]
}

func (m model) selectedKey() string { return m.selectedRow().key }

func (m model) Init() tea.Cmd { return nil }

func (m model) clampCursor() int {
	rows := m.currentRows()
	c := m.tabCursors[m.activeTab]
	if c < 0 {
		return 0
	}
	if c >= len(rows) && len(rows) > 0 {
		return len(rows) - 1
	}
	return c
}

func doSync(url string) tea.Cmd {
	return func() tea.Msg {
		return syncDoneMsg{iana.Sync(url)}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case syncDoneMsg:
		m.syncing = false
		if msg.err != nil {
			m.err = "sync failed: " + msg.err.Error()
			m.status = ""
		} else {
			m.status = "IANA database synced"
			m.err = ""
		}
		return m, nil

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
			c := m.cursor() - 1
			if c < 0 {
				c = 0
			}
			m.tabCursors[m.activeTab] = c

		case "down", "j":
			rows := m.currentRows()
			c := m.cursor() + 1
			if c >= len(rows) {
				c = len(rows) - 1
			}
			m.tabCursors[m.activeTab] = c

		case "left", "h":
			if m.activeTab > 0 {
				m.activeTab--
				m.tabCursors[m.activeTab] = m.clampCursor()
			}

		case "right", "l":
			if m.activeTab < len(m.tabs)-1 {
				m.activeTab++
				m.tabCursors[m.activeTab] = m.clampCursor()
			}

		case "g":
			m.tabCursors[m.activeTab] = 0

		case "G":
			rows := m.currentRows()
			if len(rows) > 0 {
				m.tabCursors[m.activeTab] = len(rows) - 1
			}

		case "enter", "e":
			r := m.selectedRow()
			if r.isAction() {
				return m.triggerAction(r.action)
			}
			k := r.key
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

func (m model) triggerAction(action string) (tea.Model, tea.Cmd) {
	switch action {
	case "iana.sync":
		if m.syncing {
			return m, nil
		}
		url, _ := m.values["iana.url"].(string)
		if url == "" {
			url = "https://www.iana.org/assignments/service-names-port-numbers/service-names-port-numbers.csv"
		}
		m.syncing = true
		m.status = "syncing IANA database..."
		m.err = ""
		return m, doSync(url)
	}
	return m, nil
}

var (
	titleStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	activeTabStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).Underline(true)
	inactiveTabStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	tabSepStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	keyStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
	valStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	cursorStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	hintStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	descStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)
	urlStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	okStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	editStyle        = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).BorderForeground(lipgloss.Color("212"))
	panelStyle       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1).MarginTop(1)
	actionStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	syncingStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)
	scrollStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

// renderTabBar renders the tab strip. When the total width exceeds the
// terminal width it shows only the tabs that fit around the active one,
// with < > scroll indicators on each side.
func (m model) renderTabBar() string {
	const sep = " | "
	sepR := tabSepStyle.Render(sep)

	type part struct {
		rendered string
		width    int // visible (ANSI-free) character count
	}
	parts := make([]part, len(m.tabs))
	totalW := 0
	for i, t := range m.tabs {
		label := shortTabName(t.category)
		var r string
		if i == m.activeTab {
			r = activeTabStyle.Render(label)
		} else {
			r = inactiveTabStyle.Render(label)
		}
		parts[i] = part{r, len(label)}
		totalW += len(label)
		if i > 0 {
			totalW += len(sep)
		}
	}

	// Fast path: everything fits.
	avail := m.width
	if avail <= 0 || totalW <= avail {
		strs := make([]string, len(parts))
		for i, p := range parts {
			strs[i] = p.rendered
		}
		return strings.Join(strs, sepR)
	}

	// Narrow terminal: show a window of tabs centred on the active one.
	// Reserve 4 chars for "< " / " >" indicators.
	budget := avail - 4
	start, end := m.activeTab, m.activeTab+1
	used := parts[m.activeTab].width
	for {
		grew := false
		if start > 0 {
			w := parts[start-1].width + len(sep)
			if used+w <= budget {
				start--
				used += w
				grew = true
			}
		}
		if end < len(parts) {
			w := parts[end].width + len(sep)
			if used+w <= budget {
				end++
				used += w
				grew = true
			}
		}
		if !grew {
			break
		}
	}

	strs := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		strs = append(strs, parts[i].rendered)
	}
	bar := strings.Join(strs, sepR)
	if start > 0 {
		bar = hintStyle.Render("< ") + bar
	} else {
		bar = "  " + bar
	}
	if end < len(parts) {
		bar = bar + hintStyle.Render(" >")
	}
	return bar
}

func (m model) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("pingtrace config"))
	b.WriteString("\n\n")
	b.WriteString(m.renderTabBar())
	b.WriteString("\n\n")

	rows := m.currentRows()
	cursor := m.cursor()

	maxKey := 0
	for _, r := range rows {
		if !r.isAction() && len(r.key) > maxKey {
			maxKey = len(r.key)
		}
	}

	// Overhead: title(1) + blank(1) + tabbar(1) + blank(1) +
	//           detail panel(~4) + hints(1) + status/err(1) = ~10
	// Add 3 more in edit mode for the input block.
	overhead := 10
	if m.mode == modeEdit {
		overhead += 3
	}
	visible := m.height - overhead
	if visible < 4 {
		visible = 4
	}
	if visible > len(rows) {
		visible = len(rows)
	}

	start := 0
	if cursor >= visible {
		start = cursor - visible + 1
	}
	end := start + visible
	if end > len(rows) {
		end = len(rows)
	}

	for i := start; i < end; i++ {
		r := rows[i]
		selected := i == cursor
		cur := "  "
		if selected {
			cur = cursorStyle.Render("> ")
		}

		if r.isAction() {
			b.WriteString("  " + cur + m.renderActionRow(r, selected) + "\n")
			continue
		}

		v := config.Format(config.Redact(r.key, m.values[r.key]))
		var line string
		if selected {
			line = cursorStyle.Render(fmt.Sprintf("%-*s", maxKey, r.key)) + "  " + valStyle.Render(v)
		} else {
			line = keyStyle.Render(fmt.Sprintf("%-*s", maxKey, r.key)) + "  " + valStyle.Render(v)
		}
		b.WriteString("  " + cur + line + "\n")
	}

	// Scroll position indicator when the list overflows.
	if len(rows) > visible {
		b.WriteString(scrollStyle.Render(fmt.Sprintf("  %d-%d / %d", start+1, end, len(rows))) + "\n")
	}

	// Detail panel for the selected row.
	r := m.selectedRow()
	if r.isAction() {
		b.WriteString(panelStyle.Render(m.renderActionDetail(r)) + "\n")
	} else if k := r.key; k != "" {
		meta := config.Meta()
		if md, ok := meta[k]; ok {
			detail := keyStyle.Render(k)
			if md.Description != "" {
				detail += "\n" + descStyle.Render(md.Description)
			}
			if md.Hint != "" {
				detail += "\n" + urlStyle.Render("-> "+md.Hint)
			}
			b.WriteString(panelStyle.Render(detail) + "\n")
		}
	}

	if m.mode == modeEdit {
		b.WriteString(hintStyle.Render("editing ") + keyStyle.Render(m.editing) + "\n")
		b.WriteString(editStyle.Render(m.input.View()) + "\n")
		b.WriteString(hintStyle.Render("enter: save   esc: cancel"))
	} else {
		b.WriteString(hintStyle.Render("up/down move   left/right tab   enter/e edit   u unset   q quit"))
	}

	if m.status != "" {
		b.WriteString("\n" + okStyle.Render(m.status))
	}
	if m.err != "" {
		b.WriteString("\n" + errStyle.Render("error: "+m.err))
	}
	return b.String()
}

func (m model) renderActionRow(r row, selected bool) string {
	switch r.action {
	case "iana.sync":
		var label string
		if m.syncing {
			label = syncingStyle.Render("[syncing...]")
		} else if selected {
			label = actionStyle.Render("[sync now]")
		} else {
			label = hintStyle.Render("[sync now]")
		}
		t, ok := iana.LastSyncTime()
		if ok {
			label += "  " + hintStyle.Render("last synced: "+t.Format("2006-01-02 15:04 UTC"))
		} else {
			label += "  " + hintStyle.Render("no local cache - press enter to download")
		}
		return label
	}
	return r.action
}

func (m model) renderActionDetail(r row) string {
	switch r.action {
	case "iana.sync":
		t, ok := iana.LastSyncTime()
		detail := actionStyle.Render("IANA port database sync")
		if ok {
			age := time.Since(t)
			days := int(age.Hours() / 24)
			detail += "\n" + descStyle.Render(fmt.Sprintf("Cache: %s (%d days old)", t.Format("2006-01-02 15:04 UTC"), days))
		} else {
			detail += "\n" + descStyle.Render("No cache - binary uses bundled snapshot.")
		}
		detail += "\n" + urlStyle.Render("-> press enter to download the latest CSV from IANA")
		return detail
	}
	return ""
}
