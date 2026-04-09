# Pen - A Playpen for AI Agents

## Context

Agentic coding tools (Claude Code, etc.) are most useful when running in "dangerous mode" (auto-accept all tool calls), but this is risky on bare metal. The goal is a tool that spins up the lightest possible Linux VM, shares the repo directory into it, injects carefully scoped credentials, and shells the user in. The agent runs inside the VM with full autonomy -- it literally cannot damage the host or exfiltrate secrets beyond what was explicitly injected.

## Decision: Standalone Tool (not a WT subcommand)

Three options were evaluated:

| Option | Verdict |
|--------|---------|
| **A: Standalone CLI** | **Chosen.** Clean separation, independent release cycle, no CGo contamination of wt |
| B: WT subcommand | Rejected. wt is pure Go, cross-compiles to Linux. Adding vz (CGo + Virtualization.framework) would break the build, require macOS-only CI, and need code signing for the entire wt binary. Conceptual mismatch -- VMs aren't worktrees. |
| C: Standalone + formal wt integration | Premature. wt's existing hook system already provides the integration point (`post_create` hook can call `pen`). No code changes to wt needed. Can evolve into this naturally. |

## VM Technology: Apple Virtualization.framework (`github.com/Code-Hex/vz`)

- Native Apple hypervisor, near-native performance, minimal memory overhead
- Excellent Go bindings (MIT licensed, `github.com/Code-Hex/vz/v3`)
- Supports virtio-fs (directory sharing), NAT networking, vsock (env injection), virtio console (TTY)
- Requires ad-hoc code signing with `com.apple.security.virtualization` entitlement
- macOS-only (Apple Silicon primary, Intel secondary). Linux support (QEMU/KVM) deferred to v0.3+

## Package Structure

```
github.com/agarcher/pen/
├── cmd/pen/main.go                    # Entry point
├── internal/
│   ├── commands/                       # Cobra commands (same pattern as wt)
│   │   ├── root.go                    # Root command, version injection
│   │   ├── shell.go                   # pen shell <name> --dir <path>
│   │   ├── list.go                    # pen list
│   │   ├── stop.go                    # pen stop <name>
│   │   ├── delete.go                  # pen delete <name>
│   │   └── image.go                   # pen image pull (future)
│   ├── vm/                            # VM lifecycle management
│   │   ├── manager.go                 # Create/start/stop/delete, state in ~/.config/pen/vms/<name>/
│   │   ├── config.go                  # Per-VM config (JSON)
│   │   ├── state.go                   # Running/stopped tracking
│   │   └── console.go                 # Raw TTY attachment via virtio console
│   ├── virt/                          # Virtualization abstraction
│   │   ├── virt.go                    # Hypervisor + VM interfaces
│   │   ├── apple.go                   # Apple Virtualization.framework impl
│   │   └── apple_config.go            # Boot loader, devices, networking
│   ├── image/                         # VM image management
│   │   ├── image.go                   # Download, verify, cache
│   │   └── cache.go                   # ~/.config/pen/images/
│   ├── envject/                       # Secure env var injection
│   │   ├── inject.go                  # Host-side vsock sender
│   │   └── agent.go                   # Protocol definition
│   ├── share/                         # Directory sharing
│   │   └── virtiofs.go                # virtio-fs configuration
│   └── config/                        # User configuration
│       └── config.go                  # ~/.config/pen/config.yaml
├── guest/                             # Guest-side components (cross-compiled to linux/arm64)
│   ├── agent/main.go                  # vsock receiver, writes env to /run/pen-env
│   └── init/init.sh                   # Minimal init: mount virtiofs, start agent, run getty
├── images/alpine/                     # Image build tooling
│   ├── build.sh                       # Builds kernel + rootfs from Alpine minirootfs
│   └── packages.txt                   # APK packages to include
├── entitlements/pen.entitlements       # com.apple.security.virtualization
├── Makefile
├── go.mod
└── CLAUDE.md
```

## Key Design Decisions

### 1. `pen shell` is the primary command

`pen shell myproject --dir .` creates the VM if it doesn't exist, starts it if stopped, and attaches the console. One command for the common case. Separate `pen create` deferred to v0.2.

### 2. Environment variables never touch disk

Env vars are passed host-to-guest via vsock at boot time. The config file stores only **key names** (not values) to pass from the host environment. Values resolved at start time, transmitted over vsock, written to tmpfs (`/run/pen-env`) in the guest.

```yaml
# ~/.config/pen/config.yaml
default_env:          # Key names only -- values come from host env at start time
  - ANTHROPIC_API_KEY
  - GITHUB_TOKEN
memory: 2G
cpus: 4
```

CLI overrides: `--env KEY=VALUE`, `--env-from-host KEY`, `--env-file .env`

### 3. NAT networking (zero config)

Apple Virtualization.framework provides NAT via `vz.NATNetworkDeviceConfiguration`. Guest gets outbound connectivity (API calls, git push/pull) with no bridging or root access. Guest runs `udhcpc` for DHCP.

### 4. virtio-fs for directory sharing

Host repo directory mounted at `/workspace` in the guest. Read-write by default. The guest init script mounts it: `mount -t virtiofs workspace /workspace`.

### 5. Console, not SSH

Interactive shell via `vz.VirtioConsoleDeviceConfiguration` -- direct console attachment without SSH overhead. Guest runs `agetty` on the virtio console with auto-login.

## Build System

```makefile
build:
	CGO_ENABLED=1 go build $(LDFLAGS) -o $(BUILD_DIR)/pen ./cmd/pen
	codesign --entitlements entitlements/pen.entitlements -s - $(BUILD_DIR)/pen

build-guest-agent:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o $(BUILD_DIR)/pen-agent ./guest/agent

test:
	go test -v ./...

test-integration:  # Local only, needs macOS + Apple Silicon
	go test -v -tags integration ./...
```

CI requires `macos-latest` runner (CGo + Virtualization.framework headers). Ad-hoc signing (`codesign -s -`) works without Apple Developer cert.

## VM Image Strategy

**Alpine Linux mini rootfs** (~50-80MB total with kernel):
- `vmlinuz` -- Alpine's `linux-virt` kernel (already has all virtio drivers)
- `rootfs.ext4` -- Alpine minirootfs + packages (bash, git, curl, openssh-client, nodejs)
- Guest agent binary baked in
- Downloaded from GitHub Releases on first `pen shell`

## Build Order (Implementation Phases)

### Phase 1: Scaffolding
- Go module, Cobra root command, Makefile with CGo + codesign
- Entitlements file, CLAUDE.md, basic CI

### Phase 2: Boot a VM
- `internal/virt/apple.go` -- minimal vz config: kernel + rootfs + console
- Verify we can boot Alpine and see console output
- This is the hardest part -- get it working before building anything else

### Phase 3: Interactive console
- `internal/vm/console.go` -- raw terminal mode, bidirectional stdin/stdout copy
- `pen shell` command that boots and attaches
- Verify interactive shell works (type commands, see output)

### Phase 4: Directory sharing
- Add virtio-fs to VM config
- Guest init script mounts at `/workspace`
- Verify host files visible in guest, changes propagate both ways

### Phase 5: Networking
- Add NAT network device
- Guest DHCP config
- Verify `curl` works from guest

### Phase 6: Environment injection
- Guest agent (cross-compiled Go binary)
- vsock host listener + guest client
- Verify env vars appear in guest shell session

### Phase 7: VM lifecycle management
- State tracking in `~/.config/pen/vms/<name>/`
- `pen list`, `pen stop`, `pen delete`
- Config file `~/.config/pen/config.yaml`

### Phase 8: Image distribution
- Build script for Alpine rootfs + kernel
- GitHub Release pipeline
- Auto-download on first run

## Verification

- **Phase 2:** `pen shell test --dir .` boots VM, prints kernel messages to console
- **Phase 3:** Interactive shell -- can type `ls`, `whoami`, get output
- **Phase 4:** `ls /workspace` in guest shows host repo files; `touch /workspace/test` creates file on host
- **Phase 5:** `curl https://api.github.com` works from guest
- **Phase 6:** `echo $ANTHROPIC_API_KEY` in guest shows the injected value
- **Phase 7:** `pen list` shows running VMs; `pen stop test && pen list` shows stopped; `pen delete test` removes it
- **Phase 8:** Fresh machine with no cached images, `pen shell test --dir .` downloads image and boots

## WT Integration (no code changes to wt)

Users who want both tools add a hook to `.wt.yaml`:
```yaml
hooks:
  post_create:
    - script: pen shell ${WT_NAME} --dir ${WT_PATH}
```

## Reference: wt Patterns to Reuse

The wt codebase (github.com/agarcher/wt) provides reference implementations for:
- **Cobra command structure**: `internal/commands/root.go` -- self-registering commands via `init()`, version injection via ldflags
- **Config loading**: `internal/config/config.go` -- YAML config with defaults, `internal/config/resolve.go` -- layered resolution
- **User config**: `internal/userconfig/userconfig.go` -- XDG-style `~/.config/` location
- **Makefile**: Cross-platform build targets, version from VERSION file or git describe
- **Output conventions**: stderr for messages (`cmd.Println()`), stdout for machine-readable output
