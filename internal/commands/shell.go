package commands

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/agarcher/pen/internal/envject"
	"github.com/agarcher/pen/internal/image"
	"github.com/agarcher/pen/internal/virt"
	"github.com/agarcher/pen/internal/vm"
	"github.com/spf13/cobra"
)

var (
	shellDir     string
	shellCPUs    uint
	shellMem     uint64
	shellEnv     []string // KEY=VALUE pairs
	shellEnvHost []string // key names to pass from host env
)

func init() {
	shellCmd.Flags().StringVar(&shellDir, "dir", ".", "Directory to share into the VM")
	shellCmd.Flags().UintVar(&shellCPUs, "cpus", uint(runtime.NumCPU()), "Number of CPUs")
	shellCmd.Flags().Uint64Var(&shellMem, "memory", 2048, "Memory in MB")
	shellCmd.Flags().StringArrayVar(&shellEnv, "env", nil, "Set env var in guest (KEY=VALUE)")
	shellCmd.Flags().StringArrayVar(&shellEnvHost, "env-from-host", nil, "Pass env var from host to guest (KEY)")
	rootCmd.AddCommand(shellCmd)
}

var shellCmd = &cobra.Command{
	Use:   "shell <name>",
	Short: "Create (if needed), start, and attach to a VM",
	Long: `Create a VM if it doesn't exist, start it if stopped, and attach an
interactive console. The specified directory is shared into the guest
at /workspace via virtio-fs.

Environment variables can be injected into the guest:
  --env KEY=VALUE         Set an explicit value
  --env-from-host KEY     Pass a key's value from the host environment`,
	Args: cobra.ExactArgs(1),
	RunE: runShell,
}

func runShell(cmd *cobra.Command, args []string) error {
	name := args[0]

	// Check if this VM is already running in another process.
	if vm.IsRunning(name) {
		return fmt.Errorf("VM %q is already running (PID %d)", name, vm.ReadPID(name))
	}

	imgs, err := image.Resolve()
	if err != nil {
		return err
	}

	dir, err := filepath.Abs(shellDir)
	if err != nil {
		return fmt.Errorf("resolving directory: %w", err)
	}

	// Build env spec from flags.
	spec := &envject.EnvSpec{
		FromHost: shellEnvHost,
		Explicit: make(map[string]string),
	}
	for _, key := range shellEnvHost {
		if err := envject.ValidateName(key); err != nil {
			return fmt.Errorf("--env-from-host: %w", err)
		}
	}
	for _, e := range shellEnv {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			return fmt.Errorf("invalid --env value %q (expected KEY=VALUE)", e)
		}
		if err := envject.ValidateName(k); err != nil {
			return fmt.Errorf("--env: %w", err)
		}
		spec.Explicit[k] = v
	}

	// Write env file to shared dir before boot so guest init can read it.
	if !spec.IsEmpty() {
		if err := envject.WriteEnvFile(dir, spec); err != nil {
			return fmt.Errorf("writing env file: %w", err)
		}
		defer envject.CleanupEnvFile(dir)
	}

	// Persist VM state.
	state := &vm.VMState{
		Name:      name,
		Dir:       dir,
		CPUs:      shellCPUs,
		MemoryMB:  shellMem,
		CreatedAt: time.Now(),
	}
	if err := vm.Save(state); err != nil {
		return fmt.Errorf("saving VM state: %w", err)
	}

	// Write PID so other processes can detect this VM is running.
	if err := vm.WritePID(name); err != nil {
		return fmt.Errorf("writing PID file: %w", err)
	}
	defer vm.ClearPID(name)

	hyp := virt.NewAppleHypervisor()

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
