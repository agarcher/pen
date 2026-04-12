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

		// Warn about overlay data loss if the disk has non-trivial content.
		// A fresh ext4 on a 10G sparse disk uses ~66MB; anything above 100MB
		// indicates user data worth mentioning.
		const overlayWarnThreshold = 100 * 1024 * 1024 // 100 MB
		if usage, err := vm.OverlayDiskUsage(name); err == nil && usage > overlayWarnThreshold {
			fmt.Fprintf(cmd.ErrOrStderr(), "pen: overlay disk for %q has ~%dMB of data (will be permanently deleted)\n",
				name, usage/(1024*1024))
		}

		if err := vm.Delete(name); err != nil {
			return err
		}

		fmt.Fprintf(cmd.ErrOrStderr(), "pen: deleted %s\n", name)
		return nil
	},
}
