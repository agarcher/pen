# pen - A Playpen for AI Agents

[![CI](https://github.com/agarcher/pen/actions/workflows/ci.yml/badge.svg)](https://github.com/agarcher/pen/actions/workflows/ci.yml)
[![Release](https://github.com/agarcher/pen/actions/workflows/release.yml/badge.svg)](https://github.com/agarcher/pen/actions/workflows/release.yml)

A CLI that spins up lightweight Linux VMs to sandbox agentic coding tools. The agent runs inside the VM with full autonomy — it cannot damage the host or exfiltrate secrets beyond what was explicitly injected.

## Why?

Agentic coding tools (Claude Code, etc.) are most useful when running in "dangerous mode" (auto-accept all tool calls), but this is risky on bare metal. `pen` gives each agent a disposable Linux VM with:

- **Full isolation** — the guest cannot modify the host filesystem outside the shared directory
- **Scoped credentials** — only the env vars you explicitly pass are available in the guest
- **Near-native performance** — Apple Virtualization.framework with virtio devices
- **Zero config networking** — NAT provides outbound connectivity for API calls and git

## Installation

### From Source

```bash
git clone https://github.com/agarcher/pen.git
cd pen
make build
make install  # Installs to /usr/local/bin
```

<details>
<summary>Download Binary</summary>

Download the latest release from the [releases page](https://github.com/agarcher/pen/releases). The binary must be ad-hoc code signed on your machine:

```bash
codesign --force --entitlements entitlements/pen.entitlements -s - pen
```

</details>

**Requirements:** macOS 13+ (Ventura or later), Apple Virtualization.framework

## Quick Start

```bash
# Boot a VM sharing the current directory
pen shell myproject --dir .

# You're now inside an Alpine Linux VM at /workspace
# The host directory is mounted read-write via virtio-fs
ls /workspace

# Exit the VM (Ctrl-D or exit)
exit
```

On first run, `pen` automatically downloads a minimal Alpine Linux image (~15MB kernel + initrd).

## Commands

| Command | Description | Details |
|---------|-------------|---------|
| `pen shell <name>` | Create, start, and attach to a VM | [docs](docs/USAGE.md#pen-shell) |
| `pen list` | List all VMs with status | [docs](docs/USAGE.md#pen-list) |
| `pen stop <name>` | Stop a running VM | [docs](docs/USAGE.md#pen-stop) |
| `pen delete <name>` | Delete a VM and its state | [docs](docs/USAGE.md#pen-delete) |
| `pen version` | Print version number | [docs](docs/USAGE.md#pen-version) |

See the [Usage Guide](docs/USAGE.md) for detailed command documentation.

## Environment Variables

Pass credentials and configuration into the guest without them ever touching host disk beyond a brief window during boot:

```bash
# Explicit value
pen shell myproject --dir . --env OPENAI_API_KEY=sk-...

# Forward from host environment
pen shell myproject --dir . --env-from-host ANTHROPIC_API_KEY --env-from-host GITHUB_TOKEN
```

Inside the guest, the variables are available in the shell session:

```bash
echo $ANTHROPIC_API_KEY  # Shows the injected value
```

See [Environment Injection](docs/USAGE.md#environment-injection) for details.

## wt Integration

Users of [wt](https://github.com/agarcher/wt) (git worktree manager) can add a hook to `.wt.yaml` to automatically boot a VM for each new worktree:

```yaml
hooks:
  post_create:
    - script: pen shell ${WT_NAME} --dir ${WT_PATH}
```

## Contributing

See [Architecture](docs/ARCHITECTURE.md) for an overview of the codebase.

```bash
make build    # Build pen (CGo + codesign)
make test     # Run tests
make lint     # Run linters
make image    # Build VM image locally
```

## License

MIT
