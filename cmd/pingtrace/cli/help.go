package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// flagGroups maps each flag name (long form) to a help category.
// Flags missing here land in "Other".
var flagGroups = map[string]string{
	// Targets
	"file": "Targets",

	// Operations
	"no-ping":  "Operations",
	"no-trace": "Operations",
	"mtr":      "Operations",
	"cycles":   "Operations",
	"interval": "Operations",

	// Ping engine
	"count":         "Ping engine",
	"packet-size":   "Ping engine",
	"timeout":       "Ping engine",
	"ping-interval": "Ping engine",

	// Trace engine
	"max-hops":          "Trace engine",
	"queries":           "Trace engine",
	"wait":              "Trace engine",
	"trace-packet-size": "Trace engine",

	// Output
	"columns":  "Output",
	"summary":  "Output",
	"wide":     "Output",
	"no-color": "Output",

	// Export
	"export":      "Export",
	"json":        "Export",
	"tables":      "Bulk mode",
	"concurrency": "Bulk mode",

	// Misc (auto-added by cobra)
	"help":    "Misc",
	"version": "Misc",
}

var groupOrder = []string{
	"Targets",
	"Operations",
	"Ping engine",
	"Trace engine",
	"Output",
	"Export",
	"Bulk mode",
	"Misc",
	"Other",
}

var helpExamples = []struct {
	Cmd, Desc string
}{
	{"pingtrace 8.8.8.8", "ping + traceroute, single host"},
	{"pingtrace 8.8.8.8,1.1.1.1", "multiple hosts (comma-separated)"},
	{"pingtrace 10.0.0.0/30", "expand a small IPv4 CIDR"},
	{"pingtrace --file ./targets.csv", "read targets from CSV (first column)"},
	{"pingtrace 1.1.1.1 --mtr", "live MTR until Ctrl+C"},
	{"pingtrace 1.1.1.1 -m --cycles 10 --interval 2", "bounded MTR: 10 cycles, 2 s apart"},
	{"pingtrace 8.8.8.8 --no-trace", "ping only"},
	{"pingtrace 8.8.8.8 --no-ping", "trace only"},
	{"pingtrace 8.8.8.8 --export ./reports", "write CSV report to ./reports"},
	{"pingtrace 8.8.8.8 --export ./reports --json", "write CSV + JSON report (schema-validated) to ./reports"},
	{"pingtrace 8.8.8.8 --columns seq,ip,time_ms,status", "render only the listed columns"},
	{"pingtrace config", "interactive TUI to edit defaults & API tokens"},
	{"pingtrace 1.1.1.1 --summary", "skip the table; print only the one-line summary"},
	{"pingtrace completion zsh > _pingtrace", "generate a zsh completion script"},
}

func styledHelp(cmd *cobra.Command, _ []string) {
	out := cmd.OutOrStdout()
	noColor := false
	if v, _ := cmd.Flags().GetBool("no-color"); v {
		noColor = true
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	section := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	flagS := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	exCmd := lipgloss.NewStyle().Foreground(lipgloss.Color("114"))

	if noColor {
		title = lipgloss.NewStyle().Bold(true)
		section = lipgloss.NewStyle().Bold(true)
		dim = lipgloss.NewStyle()
		flagS = lipgloss.NewStyle()
		exCmd = lipgloss.NewStyle()
	}

	// Header
	fmt.Fprintln(out, title.Render("pingtrace")+dim.Render("  -  "+cmd.Short))
	fmt.Fprintln(out)
	if cmd.Long != "" && cmd == cmd.Root() {
		for _, ln := range strings.Split(strings.TrimSpace(cmd.Long), "\n") {
			fmt.Fprintln(out, "  "+ln)
		}
		fmt.Fprintln(out)
	}

	// Usage line
	fmt.Fprintln(out, section.Render("USAGE"))
	fmt.Fprintln(out, "  "+cmd.UseLine())
	fmt.Fprintln(out)

	// Subcommands (root only)
	if cmd == cmd.Root() {
		subs := visibleSubs(cmd)
		if len(subs) > 0 {
			fmt.Fprintln(out, section.Render("COMMANDS"))
			pad := longestName(subs)
			for _, c := range subs {
				name := c.Name() + strings.Repeat(" ", pad-len(c.Name()))
				fmt.Fprintf(out, "  %s  %s\n", flagS.Render(name), c.Short)
			}
			fmt.Fprintln(out)
		}
	}

	// Flags grouped
	groups := collectFlagGroups(cmd)
	for _, g := range groupOrder {
		flags := groups[g]
		if len(flags) == 0 {
			continue
		}
		fmt.Fprintln(out, section.Render(strings.ToUpper(g)))
		writeFlagGroup(out, flags, flagS, dim)
		fmt.Fprintln(out)
	}

	// Examples (root only)
	if cmd == cmd.Root() {
		fmt.Fprintln(out, section.Render("EXAMPLES"))
		pad := 0
		for _, e := range helpExamples {
			if len(e.Cmd) > pad {
				pad = len(e.Cmd)
			}
		}
		for _, e := range helpExamples {
			cmdStr := e.Cmd + strings.Repeat(" ", pad-len(e.Cmd))
			fmt.Fprintf(out, "  %s  %s\n", exCmd.Render(cmdStr), dim.Render(e.Desc))
		}
		fmt.Fprintln(out)
		fmt.Fprintln(out, dim.Render("  Tip: run `pingtrace config` for an interactive setup (DNS, ipinfo, PeeringDB, thresholds)."))
		fmt.Fprintln(out)
	}
}

func visibleSubs(cmd *cobra.Command) []*cobra.Command {
	out := make([]*cobra.Command, 0)
	for _, c := range cmd.Commands() {
		if c.Hidden || !c.IsAvailableCommand() {
			continue
		}
		if c.Name() == "help" {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

func longestName(cs []*cobra.Command) int {
	n := 0
	for _, c := range cs {
		if len(c.Name()) > n {
			n = len(c.Name())
		}
	}
	return n
}

func collectFlagGroups(cmd *cobra.Command) map[string][]*pflag.Flag {
	groups := map[string][]*pflag.Flag{}
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		g, ok := flagGroups[f.Name]
		if !ok {
			g = "Other"
		}
		groups[g] = append(groups[g], f)
	})
	// Add inherited help/version
	cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		g, ok := flagGroups[f.Name]
		if !ok {
			g = "Other"
		}
		groups[g] = append(groups[g], f)
	})
	for _, list := range groups {
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	}
	return groups
}

func writeFlagGroup(out io.Writer, flags []*pflag.Flag, flagS, dim lipgloss.Style) {
	labels := make([]string, len(flags))
	maxLen := 0
	for i, f := range flags {
		lbl := flagLabel(f)
		labels[i] = lbl
		if len(lbl) > maxLen {
			maxLen = len(lbl)
		}
	}
	for i, f := range flags {
		pad := strings.Repeat(" ", maxLen-len(labels[i]))
		usage := strings.TrimSpace(f.Usage)
		def := defaultHint(f)
		if def != "" {
			usage = usage + " " + dim.Render(def)
		}
		fmt.Fprintf(out, "  %s%s  %s\n", flagS.Render(labels[i]), pad, usage)
	}
}

func flagLabel(f *pflag.Flag) string {
	var b strings.Builder
	if f.Shorthand != "" {
		b.WriteString("-")
		b.WriteString(f.Shorthand)
		b.WriteString(", --")
	} else {
		b.WriteString("    --")
	}
	b.WriteString(f.Name)
	if f.Value.Type() != "bool" {
		b.WriteString(" ")
		b.WriteString(strings.ToUpper(f.Value.Type()))
	}
	return b.String()
}

func defaultHint(f *pflag.Flag) string {
	if f.DefValue == "" || f.DefValue == "false" || f.DefValue == "0" || f.DefValue == "[]" {
		return ""
	}
	return "(default " + f.DefValue + ")"
}
