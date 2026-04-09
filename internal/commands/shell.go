package commands

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/agarcher/pen/internal/image"
	"github.com/agarcher/pen/internal/virt"
	"github.com/agarcher/pen/internal/vm"
	"github.com/spf13/cobra"
)

var (
	shellDir  string
	shellCPUs uint
	shellMem  uint64
)

func init() {
	shellCmd.Flags().StringVar(&shellDir, "dir", ".", "Directory to share into the VM")
	shellCmd.Flags().UintVar(&shellCPUs, "cpus", uint(runtime.NumCPU()), "Number of CPUs")
	shellCmd.Flags().Uint64Var(&shellMem, "memory", 2048, "Memory in MB")
	rootCmd.AddCommand(shellCmd)
}

var shellCmd = &cobra.Command{
	Use:   "shell <name>",
	Short: "Create (if needed), start, and attach to a VM",
	Long: `Create a VM if it doesn't exist, start it if stopped, and attach an
interactive console. The specified directory is shared into the guest
at /workspace via virtio-fs.`,
	Args: cobra.ExactArgs(1),
	RunE: runShell,
}

func runShell(cmd *cobra.Command, args []string) error {
	name := args[0]

	imgs, err := image.Resolve()
	if err != nil {
		return err
	}

	dir, err := filepath.Abs(shellDir)
	if err != nil {
		return fmt.Errorf("resolving directory: %w", err)
	}

	hyp := virt.NewAppleHypervisor()
	if !hyp.Available() {
		return fmt.Errorf("Apple Virtualization.framework is not available on this system")
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "pen: booting %s (cpus=%d mem=%dMB dir=%s)\n", name, shellCPUs, shellMem, dir)

	v, err := hyp.CreateVM(virt.VMConfig{
		Name:       name,
		KernelPath: imgs.Kernel,
		InitrdPath: imgs.Initrd,
		CmdLine:    "console=hvc0",
		CPUs:       shellCPUs,
		MemoryMB:   shellMem,
		ShareDir:   dir,
		ShareTag:   "workspace",
	})
	if err != nil {
		return fmt.Errorf("creating VM: %w", err)
	}

	if err := v.Start(); err != nil {
		return fmt.Errorf("starting VM: %w", err)
	}

	reader, writer := v.Console()
	if err := vm.AttachConsole(reader, writer); err != nil {
		return fmt.Errorf("attaching console: %w", err)
	}

	return v.Stop()
}
