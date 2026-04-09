package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	// Version is set at build time via ldflags.
	Version = "dev"
)

var rootCmd = &cobra.Command{
	Use:   "pen",
	Short: "A playpen for AI agents",
	Long: `pen spins up lightweight Linux VMs using Apple Virtualization.framework
to sandbox agentic coding tools. The agent runs inside the VM with full
autonomy — it cannot damage the host or exfiltrate secrets beyond what
was explicitly injected.`,
	SilenceUsage: true,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(cmd.OutOrStdout(), "pen version", Version)
	},
}
