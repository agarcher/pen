package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(stopCmd)
}

var stopCmd = &cobra.Command{
	Use:   "stop <name>",
	Short: "Stop a running VM",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintf(cmd.ErrOrStderr(), "pen: stop %s (not yet implemented)\n", args[0])
		return nil
	},
}
