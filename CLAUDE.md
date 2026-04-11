# CLAUDE.md

This file provides guidance to Claude Code when working with this repository.

## Build & Test

```bash
make build        # Build pen (CGo + codesign with virtualization entitlement)
make test         # Run unit tests
make test-integration  # Run integration tests (requires macOS)
make lint         # Run go vet and golangci-lint
make image        # Build VM image locally (~/.config/pen/images/)
make image-release  # Build VM image as release artifact (build/)
```

Run a single test:
```bash
go test -v -run TestName ./internal/commands/
```

## Package Structure

- **cmd/pen/main.go** - Entry point
- **internal/commands/** - Cobra command implementations
- **internal/virt/** - Hypervisor abstraction (Apple Virtualization.framework via `github.com/Code-Hex/vz/v3`)
- **internal/vm/** - VM lifecycle management (state tracking in `~/.config/pen/vms/<name>/`, PID-based liveness)
- **internal/image/** - VM image resolution with auto-download from GitHub Releases
- **internal/envject/** - Environment variable injection via shared directory dotfile
- **images/alpine/** - Alpine Linux image build script
- **.github/workflows/** - CI (test/lint on push) and Release (binary + images on tag)

## Key Design Patterns

**CGo Required**: This project uses Apple Virtualization.framework via `github.com/Code-Hex/vz/v3`, which requires CGo. The binary must be ad-hoc code signed with the `com.apple.security.virtualization` entitlement.

**Console, Not SSH**: Interactive shell via virtio console device -- no SSH overhead. Guest init runs `/bin/sh -l` on hvc0.

**Env Injection via Shared Dir**: Host writes `.pen-env` dotfile to shared directory before boot. Guest init copies to `/run/pen-env` (tmpfs), deletes original, sources into shell.

**Auto-Download Images**: On first run, if no local images exist, pen downloads kernel + initrd from the `images-latest` GitHub Release tag.

**Stdout vs Stderr**: Machine-readable output (list table) to stdout. Messages and errors to stderr.

**VM State**: Per-VM JSON in `~/.config/pen/vms/<name>/vm.json`. PID file for liveness checks (signal 0).

## Git Workflow

Always use `gh pr merge --merge`. Never squash or rebase on merge.

## Testing

Run `make test-integration` before marking any task complete. CI can't run it (see ARCHITECTURE.md § CI/CD). Unit tests alone don't prove a boot-path change works.
