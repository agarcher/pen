# Future: Read-Only Squashfs Sidecar for Heavy Image Content

**Status:** Future optimization. Do not design details until image bloat is an actual problem.
**Depends on:** Profiles, custom images, and overlay disks (see `profiles-and-overlay.md`) must be in place.

## The problem this solves

The pen initramfs is unpacked into tmpfs at boot, which means **every byte in the image consumes guest RAM for the life of the VM**. For the tooling we're currently targeting (claude binary, node, git, ripgrep, apk packages) this is fine — we're talking tens of megabytes.

But the ceiling is real. Profiles that want to bake in heavy content will hit it:
- Full JDK + Maven local repo
- CUDA toolkit
- Multi-gigabyte model weights
- Large language SDKs (Android NDK, Xcode command line equivalents)

Once profiles start exceeding a few hundred MB of baked content, the RAM cost becomes user-visible and the "just put it in the image" answer stops working.

## When to revisit

- When a user files a profile build that pushes the initrd past ~500MB and the resulting VM feels memory-starved.
- When a natural use case (ML tooling, JDK-based agents) appears that requires > 1GB of stable, read-only content.
- **Not** when someone just wants more space for mutable state — that belongs on the per-VM overlay disk, not here.

## Rough shape of the approach

Ship heavy, read-only content as a **squashfs image mounted from a virtio-blk device**, alongside the existing kernel/initrd. The initramfs remains small; the squashfs carries the bulk.

1. Extend the profile image format from `{vmlinuz, initrd}` to `{vmlinuz, initrd, sidecar.squashfs}` (sidecar optional).
2. During image build, the builder VM decides which files go in the initrd (small, required early) vs. the squashfs (large, accessed on demand). Probably driven by a profile field like `squashfs_paths = ["/opt/jdk", "/opt/models"]`.
3. At runtime, pen attaches the squashfs as a read-only virtio-blk device. Guest init mounts it at a fixed path (e.g. `/nix`-style or overlay-style) and the content is available without copying into RAM.
4. The image cache key now covers both the initrd and the squashfs.

## Key properties

- **Read-only.** This is not a replacement for the overlay disk. Anything mutable still goes on the per-VM overlay.
- **Content-addressed, shared across VMs.** Same semantics as custom initrds today — one squashfs per profile, reused by every VM built from it.
- **No per-VM storage cost beyond the overlay.** The squashfs lives in `~/.config/pen/images/profiles/<name>/sidecar.squashfs` alongside the initrd.
- **Cold reads only touch disk.** Linux page cache handles hot content naturally; RAM cost is proportional to the working set, not the image size.

## Known questions (to answer at design time, not now)

- **How does the profile author split content between initrd and squashfs?** Explicit path list, size threshold heuristic, or build-time tooling that analyzes what's large and moves it automatically?
- **Mount strategy:** plain mount at a fixed path, or overlay the squashfs into `/` so baked content appears in its "natural" location? Overlay is more magical but matches user expectations.
- **Multiple sidecars per profile?** Probably not for v1 — one sidecar per profile keeps the cache model simple.
- **Interaction with the per-VM overlayfs:** the overlay's lowerdir becomes a stack — initramfs root + squashfs content. Overlayfs supports this (multiple lower dirs), but ordering matters and failure modes multiply.
- **Build VM complexity:** the builder VM needs `mksquashfs`. Add to the base image's apk packages for building, or use a dedicated "builder image" variant?
- **Distribution:** per-profile squashfs files can be large. Does `pen image build` still run locally, or do we move to pre-built profile artifacts hosted on GitHub Releases?

## Why we're not doing this now

The two-layer model (immutable image + mutable per-VM disk) is sufficient for everything the current user story needs. Adding a third tier introduces real complexity — multi-lower overlayfs, image format versioning, build-time partitioning — and the RAM ceiling won't be hit until someone actually tries to bake something heavy. When that day comes, the plan above is the escape hatch; until then, keep the design simple.
