package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/agarcher/pen/internal/image"
	"github.com/agarcher/pen/internal/profile"
	"github.com/agarcher/pen/internal/virt"
	"github.com/spf13/cobra"
)

func init() {
	imageCmd.AddCommand(imageBuildCmd)
	imageCmd.AddCommand(imageListCmd)
	rootCmd.AddCommand(imageCmd)
}

var imageCmd = &cobra.Command{
	Use:   "image",
	Short: "Manage VM images",
	Long: `Custom images are built per profile by booting a builder VM that
installs packages, runs the build script, and repacks the rootfs into
a cached initrd. The kernel is shared across all images.`,
}

var imageBuildCmd = &cobra.Command{
	Use:   "build <profile>",
	Short: "Build a custom image for a profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		prof, err := profile.Load(name)
		if err != nil {
			return err
		}
		if !prof.NeedsImageBuild() {
			fmt.Fprintf(cmd.ErrOrStderr(), "pen: profile %q has no packages or build script — nothing to build\n", name)
			return nil
		}

		basePaths, err := image.Resolve()
		if err != nil {
			return err
		}

		hyp := virt.NewAppleHypervisor()
		return image.Build(hyp, name, prof.Packages, prof.Build, basePaths, cmd.ErrOrStderr())
	},
}

var imageListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List built images",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		imgDir, err := image.Dir()
		if err != nil {
			return err
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tTYPE\tSIZE\tAGE")

		// Base image.
		for _, name := range []string{"vmlinuz", "initrd"} {
			path := filepath.Join(imgDir, name)
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "%s\tbase\t%s\t%s\n", name, formatSize(info.Size()), formatAge(info.ModTime()))
		}

		// Profile images.
		profilesDir := filepath.Join(imgDir, "profiles")
		entries, err := os.ReadDir(profilesDir)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("reading profiles image dir: %w", err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			initrdPath := filepath.Join(profilesDir, e.Name(), "initrd")
			info, err := os.Stat(initrdPath)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "%s\tprofile\t%s\t%s\n", e.Name(), formatSize(info.Size()), formatAge(info.ModTime()))
		}

		return w.Flush()
	},
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1fK", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
