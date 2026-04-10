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

**Behavior:**

- Refuses to start if a VM with the same name is already running (PID-based check)
- Saves VM state to `~/.config/pen/vms/<name>/vm.json`
- Writes a PID file for liveness detection by `pen list` and `pen stop`
- On first run, downloads Alpine Linux kernel + initrd if not cached locally
- Mounts the host directory at `/workspace` in the guest via virtio-fs (read-write)
- Attaches the terminal in raw mode to the guest console (hvc0)
- Cleans up PID file on exit

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
NAME     STATUS   CPUS  MEMORY  DIR
dev      running  12    2048MB  /Users/me/src/project
agent    stopped  4     4096MB  /Users/me/src/other
```

**Status values:**

| Status | Meaning |
|--------|---------|
| `running` | The owning `pen shell` process is alive (verified via signal 0) |
| `stopped` | No live process found; stale PID files are auto-cleaned |

---

### pen stop

Stop a running VM by sending SIGTERM to its owning process.

```bash
pen stop <name>
```

**Behavior:**

- Looks up the PID from `~/.config/pen/vms/<name>/pid`
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
- Removes the `~/.config/pen/vms/<name>/` directory

**Example:**

```bash
pen delete dev
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

## VM Images

### Local Cache

Images are stored in `~/.config/pen/images/`:

```
~/.config/pen/images/
├── vmlinuz    # Alpine linux-virt kernel
└── initrd     # Compressed initramfs with Alpine minirootfs + init script
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
├── vm.json    # VM configuration (name, dir, cpus, memory, created_at)
└── pid        # PID of the owning pen shell process (present when running)
```

Liveness is checked by sending signal 0 to the recorded PID. Stale PID files (from crashed processes) are automatically cleaned up when `pen list` runs.
