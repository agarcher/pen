# Security Audit: pen VM Tool

**Date**: 2026-04-12

Audit of the pen security model, focusing on secret exfiltration from the host and VM escape vectors.

## Critical: No Integrity Verification on Downloaded Images

**Location**: `internal/image/image.go` — `downloadFile()`

Images (kernel + initrd) are downloaded over HTTPS from GitHub Releases with no checksum or signature verification. An attacker who compromises the GitHub account, performs a MITM on a corporate proxy that terminates TLS, or poisons a CDN cache can serve a malicious kernel or initrd. Since the initrd *is* the root filesystem, this gives full code execution on the host's network with access to whatever `--dir` and `--env` the user passes.

**Fix**: Ship a checksums file signed with a key embedded in the binary. Verify before renaming the `.tmp` file to its final path.

## High: Shared Directory Path Not Validated

**Location**: `internal/commands/shell.go` (the `--dir` flag) → `internal/virt/apple.go:116`

The `--dir` flag value is passed straight to `vz.NewSharedDirectory()` with no symlink resolution or path validation. If the user (or a wrapper script) passes a symlink, the hypervisor follows it, potentially exposing unintended host directories to the guest. More importantly, if a malicious guest-side process can influence what path gets passed on the next boot (e.g., by writing a config file the host reads), this becomes a confused-deputy attack.

**Fix**: `filepath.EvalSymlinks()` + `filepath.Abs()` before passing to the hypervisor.

## High: Race Window in `.pen-env` — Guest Can Be Tricked Into Sourcing Attacker Content

**Location**: `images/alpine/build.sh:276-278`, `internal/envject/inject.go`

The host writes `.pen-env` to the shared directory *before boot*. The guest init copies it to `/run/pen-env`, deletes the original, then sources it with `.` (shell eval). The file format is `export KEY='value'`, but `.` executes arbitrary shell. If an attacker has write access to the shared directory on the host side (e.g., it's a team-shared workspace), they can race to replace `.pen-env` between the host's write and the guest's read. The `O_EXCL` on the host side prevents creation races, but doesn't protect the file after creation.

More subtly: the host writes `.pen-env` then starts the VM. There is no MAC/HMAC on the file. Any process with write access to the shared directory can modify it before the guest reads it.

**Fix**: Write `.pen-env` to a separate host-only directory that the guest mounts read-only (not the user's workspace). Or HMAC the file and verify in the guest.

## Medium: PID Reuse in `StopByPID()` Can Kill Wrong Process

**Location**: `internal/vm/state.go:283-303`

`StopByPID()` calls `IsRunning()`, then separately reads the PID from the lock file, then sends SIGTERM. Between the liveness check and the signal, the original process could exit and the PID could be reassigned to an unrelated process. The signal then kills the wrong process.

The flock-based `IsRunning()` check is correct on its own — the bug is that `StopByPID` doesn't hold the lock across the signal.

**Fix**: Attempt a non-blocking `LOCK_EX`. If it fails (lock is held), read the PID from the lock file while still holding the fd open, then signal. If the lock succeeds, no process is running — skip the signal.

## Medium: No Atomic Write for `vm.json`

**Location**: `internal/vm/state.go:82`

`os.WriteFile()` is not atomic. A crash mid-write corrupts `vm.json`, making the VM unmanageable (can't stop, delete, or list it). The image download code already does temp-file-then-rename — the same pattern should be used here.

## Medium: Overlay Disk Symlink Attack

**Location**: `internal/vm/overlay.go:46`

`os.Create(path)` doesn't use `O_NOFOLLOW`. If an attacker can pre-create a symlink at `~/.config/pen/vms/<name>/overlay.img` before `EnsureOverlay()` runs, the overlay file is created at the symlink target. This requires write access to `~/.config/pen/vms/`, which normally only the user has — but in shared environments or with misconfigured permissions, it's exploitable.

**Fix**: Use `O_EXCL|O_NOFOLLOW` flags when creating the overlay file, matching the pattern used in `envject/inject.go`.

## Low: DNS Queries Leak to 8.8.8.8

**Location**: `images/alpine/build.sh:359`

All guest DNS queries go to Google's `8.8.8.8`, hardcoded at image build time. In corporate environments this may violate policy or leak internal hostnames to an external resolver. The DHCP response from the NAT network likely includes a local DNS server that should be preferred.

## Design Strengths Worth Preserving

The security model is fundamentally sound in several areas:

- **Hypervisor isolation** via Virtualization.framework is the real security boundary, and it's Apple's to maintain.
- **Flock-based liveness** elegantly avoids most PID-reuse issues for the `IsRunning()` check.
- **Environment variable escaping** with single-quote wrapping is correct and prevents shell injection in the common case.
- **O_EXCL | O_NOFOLLOW** on `.pen-env` creation prevents the most obvious symlink/race attacks on the host side.
- **Overlayfs composition** correctly prevents guest writes from modifying the base rootfs.
- **Builder VM isolation** (no workspace, no overlay, destroyed after build) limits profile build blast radius.

## Priority Order for Fixes

1. Image integrity verification (critical — the entire trust chain is unauthenticated)
2. Shared directory symlink resolution (high)
3. Move `.pen-env` out of the user workspace into a read-only share (high)
4. Atomic PID check + signal in `StopByPID()` (medium)
5. Atomic `vm.json` writes (medium)
6. `O_NOFOLLOW` on overlay disk creation (medium)
7. Configurable DNS (low)
