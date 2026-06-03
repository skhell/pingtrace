package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:       "completion [bash|zsh|fish|powershell]",
		Short:     "Generate shell completion (bash, zsh, fish, powershell).",
		Long:      completionLong,
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletionV2(os.Stdout, true)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			}
			return fmt.Errorf("unknown shell: %s", args[0])
		},
	}
	return cmd
}

const completionLong = `Print a shell completion script for pingtrace.

Once installed, completion makes flag and subcommand names tab-
complete in your shell.

Quick install:

  bash:        source <(pingtrace completion bash)
  zsh:         pingtrace completion zsh > "${fpath[1]}/_pingtrace"
  fish:        pingtrace completion fish | source
  powershell:  pingtrace completion powershell | Out-String | Invoke-Expression

For permanent installation, redirect the output to the appropriate
completion directory for your shell.`
