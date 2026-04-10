# Future: VM Snapshots and Suspend/Resume

**Status:** Future optimization. Do not design details until we're committing to build it.
**Depends on:** Profiles, custom images, and overlay disks (see `profiles-and-overlay.md`) should be stable first.

## Goal

Make `pen shell` startup time effectively instant (sub-second) by skipping the boot path entirely on subsequent invocations. Boot once, capture a snapshot of the running VM's memory and device state, and restore from that snapshot on future shells.

## When to revisit

- When per-shell startup time becomes a user complaint *after* profiles + custom images are in place. The current boot path is already fast; the value of this feature is almost entirely about eliminating the residual few seconds, not about solving a current pain point.
- When `Code-Hex/vz/v3` and Apple Virtualization.framework have documented, stable save/restore APIs we can rely on.
- If competing tools (Lima, OrbStack, etc.) popularize sub-second VM startup and it becomes a baseline expectation.

## Rough shape of the approach

1. On first `pen shell` for a VM, boot normally, run profile setup if needed, and reach a "ready" state (user-visible prompt).
2. Immediately before attaching the console, pause the VM and write a snapshot (memory image + device state) to `~/.config/pen/vms/<name>/snapshot/`.
3. On subsequent `pen shell` invocations, if a valid snapshot exists, restore from it instead of booting.
4. Invalidate the snapshot on any change that would make it stale: profile image hash mismatch, overlay disk modified out-of-band, env var changes (snapshots freeze the environment), kernel/image upgrade.

## Known hard problems (to solve at design time, not now)

- **Stale state in the restored VM:** clock drift, expired DHCP leases, dangling network connections, cached DNS, `/tmp` contents from the snapshot moment. Need a post-restore hook to re-run clock sync, renew networking, clear volatile state.
- **Environment variable changes:** pen injects env vars at boot. Snapshots freeze those. Either re-inject post-restore (requires a host→guest channel beyond the current share-and-read model) or invalidate the snapshot whenever env changes (common case, erodes the speedup).
- **Snapshot size:** memory snapshots are roughly the size of allocated guest RAM. A 4GB VM = 4GB snapshot on disk per VM. Disk pressure, especially with many VMs.
- **Snapshot portability:** snapshots are tied to macOS version, CPU generation, vz version. User upgrades can invalidate every snapshot. Need a version tag and automatic invalidation.
- **`vz` API maturity:** confirm save/restore is supported, stable, and exposed by `Code-Hex/vz/v3` (not just Apple's private APIs). If not, this plan is blocked until the bindings catch up.
- **Interaction with overlay disks:** the snapshot must be consistent with the overlay disk's on-disk state at snapshot time. Restoring while the overlay has been modified since the snapshot was taken = corruption. Need a consistency check (overlay mtime or hash) and automatic invalidation.

## Non-obvious things to keep in mind

- Snapshots are **per-VM**, not per-profile. Each VM has its own memory state, overlay contents, and workspace path.
- The first shell is slower, not faster (boot + snapshot). The win only materializes on the 2nd+ shell.
- Users will expect `pen shell` to "just work" after a macOS upgrade. Graceful fallback to a fresh boot on snapshot-version mismatch is non-negotiable.

## Alternatives to consider before committing

- **Kexec-based fast reboot:** if the slowness is the firmware/boot phase, kexec within the guest could shave time without full snapshot machinery.
- **Keep VMs running in the background:** instead of snapshot/restore, keep the VM alive across `pen shell` exits and just reattach the console. Simpler, avoids all snapshot pitfalls, but uses memory continuously.

Decide between snapshot, backgrounding, and kexec at design time. They solve the same problem with very different tradeoffs.
