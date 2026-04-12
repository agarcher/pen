package commands

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/agarcher/pen/internal/envject"
	"github.com/agarcher/pen/internal/image"
	"github.com/agarcher/pen/internal/profile"
	"github.com/agarcher/pen/internal/virt"
	"github.com/agarcher/pen/internal/vm"
	"github.com/spf13/cobra"
)

var (
	shellDir      string
	shellCPUs     uint
	shellMem      uint64
	shellEnv      []string // KEY=VALUE pairs
	shellEnvHost  []string // key names to pass from host env
	shellDiskSize string
	shellProfile  string
)

func init() {
	shellCmd.Flags().StringVar(&shellDir, "dir", ".", "Directory to share into the VM")
	shellCmd.Flags().UintVar(&shellCPUs, "cpus", uint(runtime.NumCPU()), "Number of CPUs")
	shellCmd.Flags().Uint64Var(&shellMem, "memory", 2048, "Memory in MB")
	shellCmd.Flags().StringArrayVar(&shellEnv, "env", nil, "Set env var in guest (KEY=VALUE)")
	shellCmd.Flags().StringArrayVar(&shellEnvHost, "env-from-host", nil, "Pass env var from host to guest (KEY)")
	shellCmd.Flags().StringVar(&shellDiskSize, "disk-size", "10G", "Overlay disk size (first boot only; ignored thereafter)")
	shellCmd.Flags().StringVar(&shellProfile, "profile", "", "Named profile from ~/.config/pen/profiles/<name>.toml")
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

	// Resolve the effective profile before persisting state. Mismatch
	// rules are strict: we refuse to attach a profile to a profile-less
	// VM or switch between profiles, because the overlay has already
	// been shaped by whatever setup ran (or didn't) the first time.
	// Delete and recreate is the escape hatch.
	var prof *profile.Profile
	var prior *vm.VMState
	effectiveProfile := shellProfile
	if vm.Exists(name) {
		var err error
		prior, err = vm.Load(name)
		if err != nil {
			return fmt.Errorf("loading existing VM state: %w", err)
		}
		switch {
		case prior.Profile == "" && shellProfile == "":
			// no profile either way — nothing to do.
		case prior.Profile == "" && shellProfile != "":
			return fmt.Errorf("VM %q was created without a profile; cannot attach profile %q (delete and recreate if needed)", name, shellProfile)
		case prior.Profile != "" && shellProfile == "":
			fmt.Fprintf(cmd.ErrOrStderr(), "pen: using profile %q (from vm.json)\n", prior.Profile)
			effectiveProfile = prior.Profile
		case prior.Profile != "" && shellProfile != "" && prior.Profile != shellProfile:
			return fmt.Errorf("VM %q was created with profile %q; cannot switch to %q (delete and recreate if needed)", name, prior.Profile, shellProfile)
		}
	}
	if effectiveProfile != "" {
		p, err := profile.Load(effectiveProfile)
		if err != nil {
			return fmt.Errorf("loading profile: %w", err)
		}
		prof = p
	}

	// Persist VM state. Preserve CreatedAt for existing VMs.
	createdAt := time.Now()
	if prior != nil {
		createdAt = prior.CreatedAt
	}
	state := &vm.VMState{
		Name:      name,
		Dir:       dir,
		CPUs:      shellCPUs,
		MemoryMB:  shellMem,
		Profile:   effectiveProfile,
		CreatedAt: createdAt,
	}
	if err := vm.Save(state); err != nil {
		return fmt.Errorf("saving VM state: %w", err)
	}

	// Acquire exclusive lock for the lifetime of this VM. Fails fast if
	// another pen shell is already running this VM. Released on return
	// (and automatically by the OS if we crash).
	lock, err := vm.AcquireLock(name)
	if err != nil {
		return err
	}
	defer lock.Release()

	// Write env file to shared dir before boot so guest init can read it.
	if !spec.IsEmpty() {
		if err := envject.WriteEnvFile(dir, spec); err != nil {
			return fmt.Errorf("writing env file: %w", err)
		}
		defer envject.CleanupEnvFile(dir)
	}

	// Write the profile's first-boot setup script alongside .pen-env.
	// The guest init copies it to tmpfs, deletes the original, and runs
	// it exactly once per fresh VM (gated by /var/lib/pen/setup-done on
	// the overlay). The defer is belt-and-braces: the guest removes the
	// original on boot, but a crash before stage 2 would otherwise leave
	// .pen-setup lingering in the workspace.
	if prof != nil && strings.TrimSpace(prof.Setup) != "" {
		if err := envject.WriteSetupFile(dir, prof.Setup); err != nil {
			return fmt.Errorf("writing setup file: %w", err)
		}
		defer envject.CleanupSetupFile(dir)
	}

	// Ensure the per-VM overlay disk exists. Sized only on first creation;
	// the --disk-size flag is silently ignored on subsequent boots since the
	// file already has its final size.
	diskSize, err := vm.ParseDiskSize(shellDiskSize)
	if err != nil {
		return fmt.Errorf("--disk-size: %w", err)
	}
	overlay, err := vm.EnsureOverlay(name, diskSize)
	if err != nil {
		return err
	}

	hyp := virt.NewAppleHypervisor()

	fmt.Fprintf(cmd.ErrOrStderr(), "pen: booting %s (cpus=%d mem=%dMB dir=%s)\n", name, shellCPUs, shellMem, dir)

	v, err := hyp.CreateVM(virt.VMConfig{
		Name:       name,
		KernelPath: imgs.Kernel,
		InitrdPath: imgs.Initrd,
		CmdLine:    "console=hvc0",
		CPUs:       shellCPUs,
		MemoryMB:   shellMem,
		Shares: []virt.Share{
			{HostPath: dir, Tag: "workspace"},
		},
		Disks: []virt.Disk{
			{ImagePath: overlay},
		},
	})
	if err != nil {
		return fmt.Errorf("creating VM: %w", err)
	}

	if err := v.Start(); err != nil {
		return fmt.Errorf("starting VM: %w", err)
	}

	// Install signal handler for graceful shutdown. SIGTERM (sent by
	// `pen stop`) and SIGINT trigger an ACPI request-stop on the VM,
	// giving the guest kernel a chance to sync filesystems before halting.
	// Without this, the Go runtime would terminate this process on
	// SIGTERM and the VM would be killed mid-flight.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)
	go func() {
		if _, ok := <-sigCh; !ok {
			return
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "\npen: stopping VM...")
		if err := v.Stop(); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "pen: stop request failed:", err)
		}
	}()

	// Attach the console; returns when the guest closes its output
	// (either user typed exit, or the signal handler triggered v.Stop).
	reader, writer := v.Console()
	if err := vm.AttachConsole(reader, writer); err != nil {
		return fmt.Errorf("attaching console: %w", err)
	}

	// Ensure the VM is fully stopped before we release the lock and exit.
	// Stop is idempotent; Wait blocks until the state machine settles.
	v.Stop()
	return v.Wait()
}
