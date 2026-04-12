package commands

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/agarcher/pen/internal/profile"
	"github.com/spf13/cobra"
)

func init() {
	profileCmd.AddCommand(profileListCmd)
	profileCmd.AddCommand(profileShowCmd)
	rootCmd.AddCommand(profileCmd)
}

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage VM profiles",
	Long: `A profile is a TOML file in ~/.config/pen/profiles/<name>.toml that
describes what to run on the first boot of a fresh VM (setup) and what
to bake into a custom image (packages, build — Phase 3).`,
}

var profileListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List available profiles",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		profiles, perFileErrs, err := profile.List()
		if err != nil {
			return err
		}

		if len(profiles) == 0 && len(perFileErrs) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "No profiles found in ~/.config/pen/profiles/.")
			return nil
		}

		if len(profiles) > 0 {
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tPACKAGES\tSETUP")
			for _, p := range profiles {
				setup := "no"
				if strings.TrimSpace(p.Setup) != "" {
					setup = "yes"
				}
				fmt.Fprintf(w, "%s\t%d\t%s\n", p.Name, len(p.Packages), setup)
			}
			if err := w.Flush(); err != nil {
				return err
			}
		}

		// Surface parse errors after the table so a broken profile
		// doesn't hide the healthy ones.
		for _, pe := range perFileErrs {
			fmt.Fprintf(cmd.ErrOrStderr(), "pen: warning: %v\n", pe)
		}
		return nil
	},
}

var profileShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show a profile's configuration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		p, err := profile.Load(name)
		if err != nil {
			return err
		}
		path, err := profile.Path(name)
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Profile:  %s\n", p.Name)
		fmt.Fprintf(out, "Path:     %s\n", path)
		fmt.Fprintln(out)

		fmt.Fprintln(out, "Setup script:")
		printBlock(out, p.Setup)
		fmt.Fprintln(out)

		fmt.Fprintln(out, "Packages (DEFERRED — Phase 3):")
		if len(p.Packages) == 0 {
			fmt.Fprintln(out, "  (none)")
		} else {
			for _, pkg := range p.Packages {
				fmt.Fprintf(out, "  %s\n", pkg)
			}
		}
		fmt.Fprintln(out)

		fmt.Fprintln(out, "Build script (DEFERRED — Phase 3):")
		printBlock(out, p.Build)
		fmt.Fprintln(out)

		diskSize := p.Disk.Size
		if diskSize == "" {
			diskSize = "(default)"
		}
		fmt.Fprintf(out, "Disk size (DEFERRED — Phase 3): %s\n", diskSize)
		return nil
	},
}

// printBlock renders a multi-line script indented by two spaces, or
// "(none)" if the block is empty after trimming whitespace.
func printBlock(w interface{ Write([]byte) (int, error) }, s string) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		fmt.Fprintln(w, "  (none)")
		return
	}
	for _, line := range strings.Split(trimmed, "\n") {
		fmt.Fprintf(w, "  %s\n", line)
	}
}
