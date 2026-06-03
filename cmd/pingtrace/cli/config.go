package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"text/tabwriter"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/skhell/pingtrace/internal/config"
	"github.com/skhell/pingtrace/internal/render"
	configtui "github.com/skhell/pingtrace/internal/tui/configtui"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage persistent pingtrace settings (tokens, color thresholds).",
		Long: "Read and write the on-disk pingtrace configuration.\n\n" +
			"Run without arguments on a TTY to launch the interactive editor.\n\n" +
			"Values are addressed by dotted keys, for example:\n" +
			"  ipinfo.token, peeringdb.token,\n" +
			"  dns.public, dns.private, dns.timeout_ms,\n" +
			"  ping.{count,packet_size,timeout_ms,interval_ms},\n" +
			"  trace.{max_hops,queries,wait_ms,packet_size},\n" +
			"  thresholds.latency.{green,yellow,orange,red}_ms,\n" +
			"  thresholds.loss.{yellow,orange,red}_pct.\n\n" +
			"Environment variables of the form PINGTRACE_<UPPER_DOTTED>\n" +
			"override the file at read time.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !render.IsTTY() {
				return cmd.Help()
			}
			return configtui.Run()
		},
	}

	cmd.AddCommand(
		newConfigListCmd(),
		newConfigGetCmd(),
		newConfigSetCmd(),
		newConfigUnsetCmd(),
		newConfigPathCmd(),
		newConfigEditCmd(),
	)
	return cmd
}

func newConfigListCmd() *cobra.Command {
	var (
		showSecrets bool
		jsonOut     bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Print all effective config values grouped by category.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			eff, err := config.Effective()
			if err != nil {
				return err
			}

			if jsonOut {
				out := map[string]any{}
				for _, k := range config.SortedKeys() {
					v := eff[k]
					if !showSecrets {
						v = config.Redact(k, v)
					}
					out[k] = v
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			meta := config.Meta()
			groups := config.KeysByCategory()
			w := cmd.OutOrStdout()
			catStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
			keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
			descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)
			hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))

			for gi, g := range groups {
				cat := g[0].(string)
				keys := g[1].([]string)
				if gi > 0 {
					fmt.Fprintln(w)
				}
				fmt.Fprintln(w, catStyle.Render(cat))

				tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
				for _, k := range keys {
					v := eff[k]
					if !showSecrets {
						v = config.Redact(k, v)
					}
					desc := ""
					hint := ""
					if m, ok := meta[k]; ok {
						desc = m.Description
						hint = m.Hint
					}
					fmt.Fprintf(tw, "  %s\t%s\t%s\n",
						keyStyle.Render(k),
						config.Format(v),
						descStyle.Render(desc),
					)
					if hint != "" {
						fmt.Fprintf(tw, "  \t\t%s\n", hintStyle.Render("↳ "+hint))
					}
				}
				tw.Flush()
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&showSecrets, "show-secrets", false, "Print token values in clear text.")
	cmd.Flags().BoolVarP(&jsonOut, "json", "j", false, "Emit JSON instead of a table.")
	return cmd
}

func newConfigGetCmd() *cobra.Command {
	var showSecrets bool
	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Print the effective value for a single key.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			if !config.KnownKey(key) {
				return fmt.Errorf("unknown config key %q", key)
			}
			v, _, err := config.Get(key)
			if err != nil {
				return err
			}
			if !showSecrets {
				v = config.Redact(key, v)
			}
			fmt.Fprintln(cmd.OutOrStdout(), config.Format(v))
			return nil
		},
	}
	cmd.Flags().BoolVar(&showSecrets, "show-secrets", false, "Print token values in clear text.")
	return cmd
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Write a config value to disk.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := config.Set(args[0], args[1]); err != nil {
				return err
			}
			p, _ := config.Path()
			fmt.Fprintf(cmd.OutOrStdout(), "set %s (saved to %s)\n", args[0], p)
			return nil
		},
	}
}

func newConfigUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset <key>",
		Short: "Remove a key from the file (defaults / env still apply).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !config.KnownKey(args[0]) {
				return fmt.Errorf("unknown config key %q", args[0])
			}
			if err := config.Unset(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "unset %s\n", args[0])
			return nil
		},
	}
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the absolute path to the config file.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := config.Path()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), p)
			return nil
		},
	}
}

func newConfigEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open the config file in $EDITOR (or a sensible default).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := config.Path()
			if err != nil {
				return err
			}
			if _, statErr := os.Stat(p); os.IsNotExist(statErr) {
				if err := config.Save(map[string]any{}); err != nil {
					return err
				}
			}
			editor := os.Getenv("VISUAL")
			if editor == "" {
				editor = os.Getenv("EDITOR")
			}
			if editor == "" {
				if runtime.GOOS == "windows" {
					editor = "notepad"
				} else {
					editor = "vi"
				}
			}
			ed := exec.Command(editor, p)
			ed.Stdin = os.Stdin
			ed.Stdout = os.Stdout
			ed.Stderr = os.Stderr
			return ed.Run()
		},
	}
}
