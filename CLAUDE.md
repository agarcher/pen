# CLAUDE.md

This file provides guidance to Claude Code when working with this repository.

## Build & Test

```bash
make build              # Build pen (CGo + codesign with virtualization entitlement)
make build-guest-agent  # Cross-compile guest agent for linux/arm64
make test               # Run unit tests
make test-integration   # Run integration tests (requires macOS + Apple Silicon)
make lint               # Run go vet and golangci-lint
```

Run a single test:
```bash
go test -v -run TestName ./internal/commands/
```

## Package Structure

- **cmd/pen/main.go** - Entry point
- **internal/commands/** - Cobra command implementations
- **internal/virt/** - Hypervisor abstraction (Apple Virtualization.framework via `github.com/Code-Hex/vz/v3`)
- **internal/vm/** - VM lifecycle management (create, start, stop, delete, state tracking)
- **internal/image/** - VM image download, verification, and caching
- **internal/envject/** - Secure environment variable injection via vsock
- **internal/share/** - virtio-fs directory sharing configuration
- **internal/config/** - User configuration (`~/.config/pen/config.yaml`)
- **guest/** - Guest-side components (cross-compiled to linux/arm64)

## Key Design Patterns

**CGo Required**: This project uses Apple Virtualization.framework via `github.com/Code-Hex/vz/v3`, which requires CGo. The binary must be ad-hoc code signed with the `com.apple.security.virtualization` entitlement.

**Console, Not SSH**: Interactive shell via virtio console device -- no SSH overhead. Guest runs agetty with auto-login.

**Env Vars Never Touch Disk**: Environment variables are injected host-to-guest via vsock at boot time, written to tmpfs in the guest. Config stores key names only.

**Stdout vs Stderr**: Machine-readable output to stdout. Messages and errors to stderr via `cmd.PrintErrln()`.

**VM State**: Stored in `~/.config/pen/vms/<name>/`.

## Git Workflow

Always use `gh pr merge --merge`. Never squash or rebase on merge.
