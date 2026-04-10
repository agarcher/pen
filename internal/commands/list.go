package commands

import (
	"fmt"
	"text/tabwriter"

	"github.com/agarcher/pen/internal/vm"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(listCmd)
}

var listCmd = &cobra.Command{
	Use:     "list",
	Short:   "List VMs",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		vms, err := vm.List()
		if err != nil {
			return err
		}

		if len(vms) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "No VMs found.")
			return nil
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSTATUS\tCPUS\tMEMORY\tDIR")
		for _, v := range vms {
			fmt.Fprintf(w, "%s\t%s\t%d\t%dMB\t%s\n",
				v.Name, v.Status, v.CPUs, v.MemoryMB, v.Dir)
		}
		return w.Flush()
	},
}
