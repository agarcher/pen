package commands

import (
	"fmt"

	"github.com/agarcher/pen/internal/vm"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(deleteCmd)
}

var deleteCmd = &cobra.Command{
	Use:     "delete <name>",
	Short:   "Delete a VM and its state",
	Aliases: []string{"rm"},
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		if err := vm.Delete(name); err != nil {
			return err
		}

		fmt.Fprintf(cmd.ErrOrStderr(), "pen: deleted %s\n", name)
		return nil
	},
}
