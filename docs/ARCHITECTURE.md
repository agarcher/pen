# Architecture

This document describes the internal architecture of `pen` for developers who want to understand or contribute to the codebase.

## Overview

`pen` is a macOS CLI that creates lightweight Linux VMs using Apple's Virtualization.framework. It provides an isolated environment for running agentic coding tools with controlled access to host resources.

```
┌─────────────────────────────────────────────────────────────────┐
│                         Host (macOS)                            │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                     pen CLI (Go + CGo)                    │   │
│  │  ┌────────────┐  ┌────────────┐  ┌───────────────────┐  │   │
│  │  │  Commands   │  │   Image    │  │   Env Injection   │  │   │
│  │  │  - shell    │  │  - resolve │  │  - write .pen-env │  │   │
│  │  │  - list     │  │  - download│  │  - write .pen-    │  │   │
│  │  │  - stop     │  │  - build   │  │    setup          │  │   │
│  │  │  - delete   │  │  - hash    │  │  - cleanup        │  │   │
│  │  │  - profile  │  │  - cache   │  │                   │  │   │
│  │  │  - image    │  │            │  │                   │  │   │
│  │  └────────────┘  └────────────┘  └───────────────────┘  │   │
│  │  ┌────────────┐  ┌────────────┐                         │   │
│  │  │  VM State   │  │  Profile   │                        │   │
│  │  │  - save     │  │  - parse   │                        │   │
│  │  │  - load     │  │  - validate│                        │   │
│  │  │  - lock     │  │  - list    │                        │   │
│  │  │  - overlay  │  │            │                        │   │
│  │  └────────────┘  └────────────┘                         │   │
│  │  ┌──────────────────────────────────────────────────┐   │   │
│  │  │         Virtualization (vz/v3 → ObjC)            │   │   │
│  │  │  - Linux boot loader                             │   │   │
│  │  │  - virtio console (hvc0)                         │   │   │
│  │  │  - virtio-fs (workspace + build shares)          │   │   │
│  │  │  - virtio-blk (overlay disk)                     │   │   │
│  │  │  - virtio-net (NAT)                              │   │   │
│  │  │  - virtio-entropy                                │   │   │
│  │  └──────────────────────────────────────────────────┘   │   │
│  └──────────────────────────────────────────────────────────┘   │
│            │ stdin/stdout (pipes)    │ virtio-fs  │ virtio-blk  │
│            │                        │            │              │
├────────────┼────────────────────────┼────────────┼──────────────┤
│            ▼                        ▼            ▼              │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                   Guest (Alpine Linux)                    │   │
│  │                                                           │   │
│  │  /init (PID 1) — stage 1 (bare initramfs)               │   │
│  │    ├── mount proc, sys, devtmpfs, tmpfs, devpts          │   │
│  │    ├── modprobe virtio_blk, virtiofs, overlay, ext4      │   │
│  │    ├── ip link set eth0 up → udhcpc                      │   │
│  │    ├── mount -t ext4 /dev/vda /overlay                   │   │
│  │    ├── mount -t overlay (lower=/, upper=/overlay/upper)  │   │
│  │    └── chroot /newroot /init (stage 2)                   │   │
│  │                                                           │   │
│  │  /init — stage 2 (merged rootfs, overlayfs active)       │   │
│  │    ├── mount -t virtiofs workspace /workspace             │   │
│  │    ├── read .pen-env → /run/pen-env → delete original    │   │
│  │    ├── first-boot setup (if marker absent)               │   │
│  │    ├── /bin/sh -l  (on hvc0)                             │   │
│  │    └── sync && poweroff -f                               │   │
│  │                                                           │   │
│  │  /workspace ← virtio-fs shared directory (read-write)    │   │
│  │  /           ← overlayfs (initramfs + overlay disk)      │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

## Package Structure

```
pen/
├── cmd/pen/main.go           # Entry point, delegates to commands.Execute()
├── internal/
│   ├── commands/             # Cobra command implementations
│   │   ├── root.go          #   Root command, version injection
│   │   ├── shell.go         #   pen shell — boot + attach
│   │   ├── list.go          #   pen list — tabular VM listing
│   │   ├── stop.go          #   pen stop — SIGTERM to owner
│   │   ├── delete.go        #   pen delete — remove state
│   │   ├── profile.go       #   pen profile list/show
│   │   └── image.go         #   pen image build/list
│   ├── virt/                 # Hypervisor abstraction
│   │   ├── virt.go          #   VM and Hypervisor interfaces
│   │   └── apple.go         #   Apple Virtualization.framework impl
│   ├── vm/                   # VM lifecycle and state
│   │   ├── state.go         #   Save/load/list/delete, flock-based liveness
│   │   ├── overlay.go       #   Overlay disk creation and management
│   │   └── console.go       #   Raw terminal attachment
│   ├── image/                # VM image management
│   │   ├── image.go         #   Resolve, download, cache
│   │   ├── build.go         #   Profile image building via builder VM
│   │   └── hash.go          #   Content-addressable image cache keys
│   ├── profile/              # Profile parsing and validation
│   │   └── profile.go       #   TOML loading, listing, validation
│   └── envject/              # Environment and setup injection
│       └── inject.go        #   Write/cleanup .pen-env and .pen-setup
├── images/alpine/            # Image build tooling
│   └── build.sh             #   Downloads Alpine + builds initramfs
├── entitlements/             # macOS code signing
│   └── pen.entitlements      #   com.apple.security.virtualization
└── .github/workflows/        # CI/CD
    ├── ci.yml               #   Lint + test + build on push/PR
    └── release.yml          #   Binary + image release on tag
```

## Key Design Patterns

### CGo + Code Signing

Apple Virtualization.framework is accessed via `github.com/Code-Hex/vz/v3`, which uses CGo to call into Objective-C. The compiled binary must be ad-hoc code signed with the `com.apple.security.virtualization` entitlement, or macOS will kill the process.

```bash
codesign --force --entitlements entitlements/pen.entitlements -s - build/pen
```

This is handled automatically by `make build`.

### Hypervisor Abstraction

The `virt` package defines interfaces (`VM`, `Hypervisor`) with the Apple implementation in `apple.go`. This leaves room for future backends (e.g., QEMU/KVM on Linux) without changing the command layer.

```go
type VM interface {
    Start() error
    Stop() error
    Wait() error
    Console() (io.Reader, io.Writer)
}

type Hypervisor interface {
    Available() bool
    CreateVM(cfg VMConfig) (VM, error)
}
```

### Console Attachment

The interactive shell uses OS pipes for bidirectional I/O between the host terminal and the guest's virtio console (hvc0):

```
Host stdin  →  pipe  →  [FileHandleSerialPortAttachment]  →  Guest hvc0 input
Guest hvc0 output  →  [FileHandleSerialPortAttachment]  →  pipe  →  Host stdout
```

The terminal is set to raw mode (`term.MakeRaw`) so keystrokes are forwarded directly. The original terminal state is restored on exit.

### Environment Injection

Env vars are passed through the shared directory rather than vsock (the Alpine `linux-virt` kernel does not include AF_VSOCK support):

1. **Host:** writes `.pen-env` (shell-sourceable `export` statements) to the shared directory
2. **Guest init:** copies to `/run/pen-env` (tmpfs), deletes the original
3. **Guest profile:** sources `/run/pen-env` on shell login
4. **Host exit:** `defer` cleanup removes `.pen-env` as a safety net

Values are single-quote escaped to prevent shell injection.

### Lock-Based Liveness

VMs run in-process (the `pen shell` command blocks while the VM is active). Liveness is tracked via `flock(2)` on a lock file:

- `pen shell` acquires an exclusive non-blocking flock on `~/.config/pen/vms/<name>/lock` and writes its PID
- `pen list` checks liveness by attempting to acquire the same lock — success means the VM is stopped
- `pen stop` reads the PID from the lock file and sends `SIGTERM`
- The OS releases flock locks automatically on process exit, even after a crash — no stale PID cleanup needed

### Overlay Disk & Overlayfs

Each VM has a persistent ext4 overlay disk at `~/.config/pen/vms/<name>/overlay.img`:

- **Host:** creates a sparse file via `os.Truncate` (default 10G; actual footprint grows only as the guest writes)
- **Guest init (stage 1):** formats the disk on first boot (`mkfs.ext4 /dev/vda`), mounts it, composes overlayfs over the initramfs rootfs, then chroots into the merged view
- **Result:** `/` appears writable; all mutations land on the overlay disk and persist across reboots
- `/workspace` bypasses the overlay — it remains a direct virtio-fs host share

### Profile-Driven Images

Profiles declare two layers of customization:

1. **Custom image** (`packages` + `build`): stable tools baked into an immutable initrd, shared across all VMs using the profile. Built by a temporary builder VM that installs packages, runs the build script, and repacks the rootfs via `cpio | gzip`. Cache key = sha256(sorted packages + build script + base initrd).
2. **First-boot setup** (`setup`): per-VM initialization run once against the overlay disk (gated by a marker at `/var/lib/pen/setup-done`). Changes to `setup` do not affect existing VMs.

### Image Auto-Download

On first run, if `~/.config/pen/images/vmlinuz` and `initrd` don't exist, pen downloads them from the `images-latest` GitHub Release tag:

```
https://github.com/agarcher/pen/releases/download/images-latest/vmlinuz-{arch}
https://github.com/agarcher/pen/releases/download/images-latest/initrd-{arch}
```

Downloads use temp files (`.tmp` suffix) and atomic rename to avoid partial files.

### Version Injection

Version is set at build time via ldflags, falling back through VERSION file, git describe, then "dev":

```makefile
VERSION ?= $(shell cat VERSION 2>/dev/null || git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X github.com/agarcher/pen/internal/commands.Version=$(VERSION)"
```

### Output Conventions

- **stdout:** machine-readable output (`pen list` table)
- **stderr:** messages, progress, errors (`pen: booting...`, `pen: deleted...`)

Commands use `cmd.OutOrStdout()` for stdout and `cmd.ErrOrStderr()` for stderr, following Cobra conventions.

## Design Decisions

### Why Apple Virtualization.framework?

- Native hypervisor with near-native performance and minimal memory overhead
- Excellent Go bindings via `github.com/Code-Hex/vz/v3` (MIT licensed)
- Supports all needed virtio devices: console, fs, net, entropy
- Ad-hoc code signing works without an Apple Developer certificate
- Tradeoff: macOS-only. Linux support (QEMU/KVM) can be added behind the `Hypervisor` interface.

### Why Console Instead of SSH?

- Zero latency — direct pipe attachment, no TCP overhead
- No key management or authentication
- No sshd process in the guest
- Simpler init (no need to generate host keys or configure PAM)
- Tradeoff: no multiplexing (one session per VM). Acceptable for the primary use case of running a single agent.

### Why Alpine Linux?

- Minimal image size (~15MB kernel + initrd combined)
- Fast boot (~0.4s to shell prompt)
- `linux-virt` kernel has all virtio drivers built in
- BusyBox provides essential tools (sh, ip, udhcpc, mount, cpio)
- Tradeoff: no systemd, no package manager in the initramfs. Packages can be added at image build time.

### Why Shared Directory for Env Injection?

The plan originally called for vsock-based injection, but the Alpine `linux-virt` kernel doesn't include `AF_VSOCK` support (`CONFIG_VSOCKETS` is not enabled). The shared directory approach is simpler and equally effective:

- No kernel module dependencies
- No guest agent binary needed
- Works with the same virtio-fs mount already configured for workspace sharing
- Brief disk exposure (milliseconds between write and guest deletion) is acceptable for the threat model (the shared directory is already trusted)

### Why flock Instead of a Daemon?

- No background process to manage or crash
- Each `pen shell` is a self-contained process
- `flock(2)` is released automatically on process exit (even crashes) — no stale-PID problem
- Tradeoff: no out-of-band VM management (can't detach/reattach). This matches the intended workflow where the agent owns the terminal.

## CI/CD

### CI Workflow (`.github/workflows/ci.yml`)

Runs on every push and PR to `main`:
- `make lint` — go vet + golangci-lint
- `make test` — unit tests
- `make build` — compile + codesign

Uses `macos-latest` runner (required for CGo + Virtualization.framework headers).

**`make test-integration` does not run in CI.** GitHub's hosted macOS runners are themselves Anka VMs, and Apple VZ refuses nested virtualization, so `vz.NewVirtualMachine` fails on every hosted runner. GitHub closed this as "not planned" in [actions/runner-images#13505](https://github.com/actions/runner-images/issues/13505). Integration tests must be run locally on a real Mac before merging.

### Release Workflow (`.github/workflows/release.yml`)

Triggered by pushing a `v*` tag:

1. **Build binaries** — `pen-darwin-amd64` (macos-13) and `pen-darwin-arm64` (macos-latest)
2. **Build images** — `vmlinuz-{arch}` + `initrd-{arch}` for x86_64 and aarch64 (ubuntu runner)
3. **Publish** — creates a versioned GitHub Release with all artifacts
4. **Update images-latest** — floating tag for auto-download on first run
