package commands

import (
	"fmt"

	"github.com/agarcher/pen/internal/vm"
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
		name := args[0]

		if !vm.Exists(name) {
			return fmt.Errorf("VM %q not found", name)
		}

		if err := vm.StopByPID(name); err != nil {
			return err
		}

		fmt.Fprintf(cmd.ErrOrStderr(), "pen: sent stop signal to %s\n", name)
		return nil
	},
}
