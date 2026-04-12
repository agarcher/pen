# Usage Guide

This document provides detailed information about all `pen` commands and configuration options.

## Commands

### pen shell

Create a VM if it doesn't exist, start it, and attach an interactive console.

```bash
pen shell <name> [flags]
```

**Arguments:**

| Argument | Description |
|----------|-------------|
| `<name>` | VM name (used for state tracking and display) |

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--dir <path>` | `.` | Host directory to share into the VM at `/workspace` |
| `--cpus <n>` | Host CPU count | Number of virtual CPUs |
| `--memory <mb>` | `2048` | Memory in megabytes |
| `--env KEY=VALUE` | | Set an environment variable in the guest (repeatable) |
| `--env-from-host KEY` | | Pass an env var from the host environment (repeatable) |
| `--profile <name>` | | Use a named profile from `~/.config/pen/profiles/<name>.toml` |
| `--disk-size <size>` | `10G` | Overlay disk size (first boot only; ignored thereafter) |

**Behavior:**

- Refuses to start if a VM with the same name is already running (lock-based check)
- Saves VM state to `~/.config/pen/vms/<name>/vm.json`
- Acquires an exclusive lock for liveness detection by `pen list` and `pen stop`
- On first run, downloads Alpine Linux kernel + initrd if not cached locally
- Creates a persistent overlay disk (`overlay.img`) on first boot; all filesystem changes persist across reboots
- If `--profile` is specified, builds the profile's custom image if stale or missing
- Mounts the host directory at `/workspace` in the guest via virtio-fs (read-write)
- Attaches the terminal in raw mode to the guest console (hvc0)
- Cleans up on exit

**Guest environment:**

The guest boots a minimal Alpine Linux system with:

- Interactive shell at `/workspace` (the shared directory)
- NAT networking with outbound connectivity (DNS via 8.8.8.8)
- Injected environment variables available in the shell session

**Examples:**

```bash
# Basic usage — share current directory
pen shell dev --dir .

# Custom resources
pen shell heavy --dir . --cpus 8 --memory 4096

# With environment variables
pen shell agent --dir . \
  --env-from-host ANTHROPIC_API_KEY \
  --env-from-host GITHUB_TOKEN \
  --env DEBUG=1
```

---

### pen list

List all known VMs with their status.

```bash
pen list
```

**Aliases:** `ls`

**Example output:**

```
NAME     PROFILE  STATUS   CPUS  MEMORY  DIR
dev      claude   running  12    2048MB  /Users/me/src/project
agent    -        stopped  4     4096MB  /Users/me/src/other
```

**Status values:**

| Status | Meaning |
|--------|---------|
| `running` | The owning `pen shell` process holds the lock |
| `stopped` | No live process holds the lock |

---

### pen stop

Stop a running VM by sending SIGTERM to its owning process.

```bash
pen stop <name>
```

**Behavior:**

- Reads the PID from `~/.config/pen/vms/<name>/lock`
- Sends SIGTERM to the `pen shell` process
- The process handles the signal, stops the VM, and cleans up

**Example:**

```bash
pen stop dev
# pen: sent stop signal to dev
```

---

### pen delete

Delete a VM's persisted state.

```bash
pen delete <name>
```

**Aliases:** `rm`

**Behavior:**

- Refuses to delete a running VM (stop it first)
- Warns if the overlay disk has non-trivial data (>100MB actual usage)
- Removes the `~/.config/pen/vms/<name>/` directory including the overlay disk

**Example:**

```bash
pen delete dev
# pen: overlay disk for "dev" has ~245MB of data (will be permanently deleted)
# pen: deleted dev
```

---

### pen version

Print the version number.

```bash
pen version
```

**Example output:**

```
pen version 0.1.0
```

---

## Environment Injection

Environment variables are injected into the guest via a dotfile in the shared directory.

### Mechanism

1. Before boot, the host writes a `.pen-env` file to the shared directory
2. Guest init copies it to `/run/pen-env` (tmpfs) and deletes the original from the shared directory
3. The shell profile sources `/run/pen-env` so variables are available in the session
4. On exit, the host also cleans up `.pen-env` as a safety net

The variables exist briefly on disk in the shared directory between write and boot. After boot, they live only in guest tmpfs.

### Flags

| Flag | Description |
|------|-------------|
| `--env KEY=VALUE` | Set an explicit value. Repeatable. |
| `--env-from-host KEY` | Read the value from the host's environment at start time. Repeatable. The key name is resolved when the VM starts, not when the command is parsed. |

### Examples

```bash
# Forward API keys from host
pen shell agent --dir . \
  --env-from-host ANTHROPIC_API_KEY \
  --env-from-host GITHUB_TOKEN

# Mix explicit and forwarded
pen shell agent --dir . \
  --env-from-host ANTHROPIC_API_KEY \
  --env NODE_ENV=development \
  --env DEBUG=1

# Verify inside the guest
echo $ANTHROPIC_API_KEY
```

---

## Profiles & Custom Images

A profile is a TOML file at `~/.config/pen/profiles/<name>.toml` that declares two layers of customization:

| Layer | Mutability | Scope | What lives here |
|---|---|---|---|
| **Custom image** (initrd per profile) | Immutable, content-addressed | Shared across all VMs using the profile | apk packages, binaries, language runtimes |
| **Overlay disk** (overlay.img per VM) | Read/write, persistent | One VM | node_modules, caches, runtime installs, project state |

### Profile config

```toml
# ~/.config/pen/profiles/claude.toml

# Packages baked into the custom image.
packages = ["nodejs", "npm", "git", "ripgrep"]

# Commands run at image-build time (baked into the image).
build = """
npm install -g @anthropic-ai/claude-code
rm -rf /var/cache/apk/*
"""

# Commands run on first boot of a fresh VM (on the overlay disk).
# Runs exactly once; ignored for existing VMs if this section changes.
setup = """
mkdir -p /root/.claude
"""

# Overlay disk config (optional).
[disk]
size = "10G"     # default
```

### Cache invalidation

- Image cache key = sha256 of `packages` + `build` + base initrd content.
- `setup` and `disk` do **not** affect the image cache.
- Stale images are rebuilt automatically on `pen shell --profile`.

---

### pen profile list

List available profiles.

```bash
pen profile list
```

**Aliases:** `ls`

**Example output:**

```text
NAME     PACKAGES  SETUP
claude   4         yes
minimal  0         no
```

---

### pen profile show

Show a profile's configuration and image build status.

```bash
pen profile show <name>
```

**Example:**

```bash
pen profile show claude
```

---

### pen image build

Build (or rebuild) a custom image for a profile.

```bash
pen image build <profile>
```

Boots a builder VM that installs packages, runs the build script, and repacks the rootfs into a cached initrd. Build progress streams to stderr.

---

### pen image list

List all built images (base and profile) with sizes and ages.

```bash
pen image list
```

**Example output:**

```text
NAME     TYPE     SIZE    AGE
vmlinuz  base     14.2M   5d ago
initrd   base     28.7M   5d ago
claude   profile  95.3M   2h ago
```

---

## VM Images

### Local Cache

Images are stored in `~/.config/pen/images/`:

```
~/.config/pen/images/
├── vmlinuz                    # Alpine linux-virt kernel (shared by all images)
├── initrd                     # Base initramfs
└── profiles/
    └── <profile-name>/
        ├── initrd             # Custom initrd built from profile
        └── build.hash         # sha256 of image-affecting profile fields
```

### Auto-Download

On first run, if no local images exist, `pen` downloads them from the `images-latest` GitHub Release tag. The download is architecture-aware (x86_64 or aarch64).

### Building Locally

To build images from source (useful for customization or offline use):

```bash
make image
```

This downloads the Alpine minirootfs and kernel, configures the guest init script, and builds a compressed initramfs. The result is installed directly to `~/.config/pen/images/`.

### Guest Init

The initramfs includes a custom `/init` script that:

1. Mounts essential filesystems (proc, sys, devtmpfs, tmpfs, devpts)
2. Sets hostname to `pen`
3. Brings up loopback and eth0 (DHCP via udhcpc)
4. Mounts the shared directory at `/workspace` via virtio-fs
5. Reads and applies injected environment variables
6. Launches an interactive shell

---

## VM State

Per-VM state is stored in `~/.config/pen/vms/<name>/`:

```
~/.config/pen/vms/dev/
├── vm.json      # VM configuration (name, dir, cpus, memory, profile, setup_hash, created_at)
├── lock         # flock-based liveness (PID written inside)
└── overlay.img  # ext4 sparse file, persistent overlay disk
```

Liveness is checked via non-blocking `flock(2)` on the lock file. The OS releases the lock automatically on process exit, making this reliable even after crashes.
