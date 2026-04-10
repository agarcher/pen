# Profiles, Custom Images, and Per-VM Overlay Disks

**Status:** Ready to implement
**Scope:** Combines first-boot setup (#4), custom images (#1), and per-VM overlay disks (#2) into a single coherent feature.

## Goal

Make `pen shell` fast even when the user needs non-trivial tooling (claude code, language runtimes, project dependencies) inside the VM. Setup cost should be paid **at most once per profile** for stable tools and **at most once per VM** for project state — never on every `pen shell` invocation.

## Design

Two persistence layers, with a clean line between them:

| Layer | Mutability | Scope | What lives here |
|---|---|---|---|
| **Custom image** (`initrd` built per profile) | Immutable, content-addressed | Shared across all VMs using the profile | apk packages, `claude` binary, language runtimes, anything stable |
| **Overlay disk** (`overlay.img` per VM) | Read/write, persistent across reboots | One VM | `node_modules`, pip caches, runtime `apk add`s, `~/.claude/`, project state |

At runtime, the overlay disk is composed over the initramfs rootfs via **overlayfs**, so `/` appears writable and all mutations land on the disk. The workspace virtio-fs share at `/workspace` bypasses the overlay and remains a direct host share.

A **profile** is a TOML file that declares both sides: what to bake into the image, and what to run on first boot of a fresh VM against the overlay disk.

### First-boot setup behavior (decided)

When a profile's `setup` script changes after VMs have already been created from it:
- Existing VMs are **not** re-run — their marker file on the overlay disk prevents it.
- New VMs get the new setup.
- A status message tells the user "profile setup changed; existing VMs will not re-run it."

This is "Option 1" from prior design discussion: simple, predictable, sacrifices convenience for safety.

### Marker file location (decided)

The first-boot-setup-complete marker lives at **`/var/lib/pen/setup-done`** — a normal file inside the merged rootfs view that physically lands on the overlay disk's `upper/` directory via normal overlayfs copy-up.

This was chosen over the alternative of placing the marker at the raw disk root (e.g. `/overlay/.pen-setup-done`, a sibling of `upper/` and `work/`) for one deciding reason: **it lets the init script fully hide `/overlay` after pivot_root**, so no post-pivot code path needs raw access to the disk. The setup hook then runs in a plain rootfs environment and can check/touch the marker with normal shell operations, with no mount gymnastics and no error handling across the pivot boundary.

The tradeoff accepted: the marker lives in a user-visible location and could theoretically be clobbered by a misbehaving setup script or `rm -rf /var/lib`. This is a remote risk that would break other things first, and is worth the simpler init sequence paid on every boot.

## File and directory layout

```
~/.config/pen/
├── images/
│   ├── vmlinuz                          # base kernel (unchanged location)
│   ├── initrd                           # base initrd (unchanged location)
│   └── profiles/
│       └── <profile-name>/
│           ├── initrd                   # custom initrd built from profile
│           └── build.hash               # sha256 of image-affecting profile fields
├── profiles/
│   └── <profile-name>.toml              # user-authored profile config
└── vms/
    └── <vm-name>/
        ├── vm.json                      # existing
        ├── pen.lock                     # existing
        ├── pid                          # existing
        └── overlay.img                  # new: ext4 sparse file, per-VM persistent disk
```

**Kernel is shared** across all images (base and profile-built). Only `initrd` differs per profile. This keeps the cache simple.

## Profile config schema

```toml
# ~/.config/pen/profiles/claude.toml

# Alpine packages installed at image-build time. Baked into the immutable initrd.
packages = ["nodejs", "npm", "git", "ripgrep"]

# Commands run at image-build time, as root, with network, inside the builder VM.
# Output is baked into the immutable initrd. Must be idempotent (will run again on rebuild).
build = """
npm install -g @anthropic-ai/claude-code
rm -rf /var/cache/apk/*
"""

# Commands run on first boot of a fresh VM, against the overlay disk.
# Runs exactly once per VM; ignored for existing VMs if this section changes later.
setup = """
mkdir -p /root/.claude
"""

# Overlay disk config (per-VM). Optional.
[disk]
size = "10G"     # default
```

**Hashing rules for cache invalidation:**
- Image cache key = sha256 of `packages` + `build` + base kernel/initrd version.
- `setup` and `disk` do **not** affect the image cache — they only affect fresh-VM behavior.
- Stored in `images/profiles/<name>/build.hash`; mismatch triggers automatic rebuild on next `pen shell --profile`.

## CLI surface

**New commands:**
- `pen profile list` — list profiles in `~/.config/pen/profiles/`.
- `pen profile show <name>` — print parsed profile, image build status (hash match?), and list of VMs using it.
- `pen image build <profile>` — explicitly (re)build the custom image for a profile.
- `pen image list` — list built images (base + all profile images) with sizes and ages.

**New flags on `pen shell`:**
- `--profile <name>` — use a named profile. Implies an image build if the cached image is stale or missing.
- `--disk-size <size>` — override default overlay disk size. **Only honored on first boot of a new VM**; ignored thereafter (the disk file already exists and is sized).

**Changes to existing commands:**
- `pen delete <name>` — also removes `overlay.img`. Warn about data loss if the disk is non-empty.
- `pen list` — add a "PROFILE" column showing which profile each VM was created with (stored in `vm.json`).

## Guest init changes

The existing init script (in `images/alpine/build.sh`) runs as PID 1 and mounts essentials, then execs `/bin/sh -l`. The new flow adds an overlayfs stage and a first-boot hook.

### New init flow

```
1. Mount proc, sys, dev, tmp, run, devpts                    (unchanged)
2. Detect overlay disk at /dev/vda
   - If absent: skip to step 4 (ephemeral mode, back-compat)
   - If present and unformatted: mkfs.ext4 /dev/vda
3. Mount /dev/vda at /overlay; mkdir /overlay/upper /overlay/work
4. If overlay present:
   a. mkdir /newroot
   b. mount -t overlay overlay \
        -o lowerdir=/,upperdir=/overlay/upper,workdir=/overlay/work /newroot
   c. Move /proc, /sys, /dev, /run into /newroot
   d. pivot_root /newroot /newroot/.oldroot
   e. umount /.oldroot/overlay; umount /.oldroot; rmdir /.oldroot
   (after this point, /overlay is no longer visible — the marker lives inside
    the merged rootfs view and no post-pivot raw-disk access is needed)
5. Bring up loopback, eth0/DHCP                              (unchanged)
6. Mount /workspace via virtio-fs                            (unchanged — post-pivot)
7. Read injected env vars from /workspace/.pen-env           (unchanged)
8. First-boot hook:
   - Check if kernel cmdline has `pen.mode=build` → run build flow (see Image build pipeline)
   - Else check for /var/lib/pen/setup-done marker
     - Absent AND setup script present in env → run it; on success
       `mkdir -p /var/lib/pen && touch /var/lib/pen/setup-done`
       (the file lands on the overlay's upper layer via normal overlayfs copy-up)
     - Present → skip
9. exec /bin/sh -l                                           (unchanged)
```

### Where the setup script comes from

The host writes it into the env share alongside `.pen-env`:
- `.pen-env` → existing env var file
- `.pen-setup` → setup script body (new)

Guest init copies both to `/run/` (tmpfs) and deletes the originals, matching the existing env-injection pattern. This avoids needing a third share just for the setup script.

## Image build pipeline

Custom images are built by booting a **builder VM** that uses the base pen image and repacks its own rootfs.

### Builder VM flow

1. Host computes the profile's image hash; if a cached `initrd` matches, skip the build entirely.
2. Host creates a temporary directory with:
   - `control/packages` — newline-separated package list
   - `control/build.sh` — the profile's `build` script
   - `output/` — empty; will receive the new initrd
3. Host boots a VM with:
   - Base `vmlinuz` + `initrd`
   - Kernel cmdline: `console=hvc0 pen.mode=build`
   - Two virtio-fs shares: `control` (read-only) and `output` (read-write)
   - No workspace share, no overlay disk
4. Guest init detects `pen.mode=build`, executes the build sequence instead of launching a shell:
   ```sh
   apk update
   xargs apk add < /control/packages
   sh /control/build.sh
   rm -rf /var/cache/apk/*
   cd / && find . -xdev \
     \( -path ./control -o -path ./output -o -path ./proc -o -path ./sys \
        -o -path ./dev -o -path ./run -o -path ./tmp \) -prune -o -print \
     | cpio -o -H newc 2>/dev/null | gzip > /output/initrd
   poweroff -f
   ```
5. Host waits for VM to halt, verifies `/output/initrd` exists, moves it to `~/.config/pen/images/profiles/<name>/initrd`, writes `build.hash`.

### Key properties

- **Network is required during build** (apk repo access). The existing NAT setup handles this.
- **No host-side Alpine tools needed** — all apk/cpio/gzip work happens inside the guest.
- **Reuses existing virt plumbing** — just another VM config, plus multi-share support.
- **Build logs stream to the builder VM's console**, which `pen image build` attaches to so the user can see progress.

### Changes to `internal/virt`

- `VMConfig` currently has `ShareDir`/`ShareTag` (single share) and no block devices. Add:
  ```go
  type VMConfig struct {
      // ...existing fields...
      Shares []Share         // replaces ShareDir/ShareTag (with back-compat shim if needed)
      Disks  []Disk          // new: block devices
  }
  type Share struct { HostPath, Tag string; ReadOnly bool }
  type Disk  struct { ImagePath string; ReadOnly bool }
  ```
- `apple.go` wires `Shares` into `SetDirectorySharingDevicesVirtualMachineConfiguration` (slice already supports multiple entries) and `Disks` into `SetStorageDevicesVirtualMachineConfiguration` via `VirtioBlockDeviceConfiguration` + `DiskImageStorageDeviceAttachment`.

## Overlay disk management

- **Format:** raw ext4, sparse file.
- **Creation:** host creates an empty sparse file of the requested size via `os.Truncate` on first `pen shell` for a VM. No mkfs on the host — the guest formats on first boot, which avoids needing `mke2fs` on macOS.
- **Location:** `~/.config/pen/vms/<name>/overlay.img`.
- **Attachment:** wired into `VMConfig.Disks` as the sole (initially) block device, appears as `/dev/vda` in the guest.
- **Sizing:** default 10G sparse; actual host footprint grows only as the guest writes to it. `--disk-size` on first `pen shell` overrides; ignored thereafter.
- **Resize:** explicitly out of scope for v1. User can `pen delete` and recreate if they need more space.
- **Back-compat:** if a VM has no `overlay.img` (e.g., created before this feature, or the user passed `--no-disk`), init skips the overlay stage and runs in the existing stateless mode. This keeps existing workflows working.

## Implementation phases

Each phase ships a usable, testable slice. Land one at a time.

### Phase 1 — Overlay disk plumbing (no profiles yet)

Goal: `apk add` inside a `pen shell` session persists across reboots.

- **Breaking `VMConfig` shape change** (internal only — `virt` is not a public package):
  - Replace `ShareDir`/`ShareTag` with `Shares []Share` (each with `HostPath`, `Tag`, `ReadOnly`).
  - Add `Disks []Disk` (each with `ImagePath`, `ReadOnly`).
  - Wire `VirtioBlockDeviceConfiguration` + `DiskImageStorageDeviceAttachment` in `apple.go`.
  - Migrate the sole caller (`internal/commands/shell.go`) in the same PR — do not leave a compatibility shim.
- **Base image additions to `images/alpine/build.sh`:**
  - Add `e2fsprogs` to the apk package list so the guest has `mkfs.ext4` available on first boot. (Busybox has `mount`, `pivot_root`, `cpio`, `gzip` built in — no other additions needed.)
- Create `overlay.img` lazily under `~/.config/pen/vms/<name>/` in `pen shell` if absent (sparse file via `os.Truncate`).
- Update the guest init script to detect `/dev/vda`, mkfs if unformatted, set up overlayfs, pivot_root.
- Add `--disk-size` flag (first-boot only).
- Update `pen delete` to remove `overlay.img`.
- Test: `apk add vim`, exit, `pen shell`, verify `vim` still present.
- Test: back-compat — when no `/dev/vda` is present the init falls through to the existing stateless path cleanly.

### Phase 2 — Profiles and first-boot setup hook

Goal: a profile's `setup` script runs exactly once per fresh VM.

- Add `internal/profile` package: TOML parsing, `~/.config/pen/profiles/` loading.
- Add `--profile` flag to `pen shell`. Profile name persisted in `vm.json`.
- Host writes `.pen-setup` to the workspace share alongside `.pen-env` before boot.
- Guest init reads it post-pivot, checks `/var/lib/pen/setup-done`, runs if absent, then `mkdir -p /var/lib/pen && touch /var/lib/pen/setup-done` on success.
- Add `pen profile list` and `pen profile show`.
- Test: first `pen shell --profile foo bar` runs setup; second run doesn't; editing the profile doesn't re-trigger.

### Phase 3 — Custom image builds

Goal: profile `packages` + `build` produce a cached custom initrd reused across VMs.

- Add `internal/image/build.go`: hash computation, cache lookup, builder VM orchestration.
- Extend guest init with `pen.mode=build` branch: reads `/control/packages` + `/control/build.sh`, runs them, repacks rootfs to `/output/initrd`, halts.
- Add `pen image build <profile>` command (explicit) and automatic build-on-stale in `pen shell --profile`.
- Add `pen image list`.
- Wire `pen shell --profile` to use the profile's custom `initrd` when available, base `initrd` when not.
- Test: `pen image build claude` produces an initrd containing `claude`; `pen shell --profile claude foo` boots with `claude` already installed.
- Test: editing `packages` triggers a rebuild on next `pen shell --profile`; editing `setup` does not.

### Phase 4 — Polish

- `pen list` shows profile column.
- Status message when profile `setup` changed but existing VMs won't re-run it.
- `pen delete` warns about overlay data loss if the disk is non-trivially populated.
- Integration tests covering the three phases end-to-end.
- Update README / architecture docs to reflect the two-layer model.

## Testing strategy

- **Unit tests** for profile parsing, hash computation, disk file creation, config merging.
- **Integration tests** (macOS-only, same gate as existing `make test-integration`):
  - Overlay persistence: install a package, reboot VM, verify presence.
  - Profile setup idempotency: run setup, reboot, confirm marker prevents re-run.
  - Image build determinism: same profile → same hash → cache hit.
  - Builder VM smoke test: build a tiny profile with one package, verify the produced initrd is usable.
- **Back-compat test:** existing `pen shell foo --dir .` (no profile, no disk flag) still works; the new init's overlay stage is a no-op when `/dev/vda` is absent.

## Non-goals (for this plan)

- **Resizing overlay disks.** Delete + recreate if more space is needed.
- **Sharing overlay disks across VMs.** Each VM owns its own disk; no per-profile shared mutable layer. If image bloat becomes an issue, see the squashfs-sidecar future plan — not this one.
- **Re-running setup on profile changes.** Option 1 was chosen deliberately.
- **Nested profiles / inheritance.** A profile stands alone on top of the pen base image.
- **Snapshot/restore.** See the vm-snapshots future plan.
- **Windows/Linux hosts.** Still macOS + Apple Virtualization.framework only.

## Open questions to resolve during implementation

### 1. Multi-share support in `Code-Hex/vz/v3`

The builder VM in Phase 3 needs two simultaneous virtio-fs shares (`control` read-only, `output` read-write). The API takes a slice (`SetDirectorySharingDevicesVirtualMachineConfiguration([]DirectorySharingDeviceConfiguration)`) so multi-share is expected to work, but nothing in the current codebase exercises it.

**Action:** as the **first task of Phase 3**, write a throwaway smoke test that boots a minimal VM with two shares and verifies both are mounted inside the guest. Do this *before* building the rest of the builder pipeline — if it doesn't work, the entire image-build approach needs rethinking and it's better to discover that early.

### 2. Disk-not-clean recovery

If a VM crashes mid-write (e.g., host `kill -9` the pen process), ext4 journal replay on next boot should handle it. Validate in Phase 1 by forcibly killing pen during heavy writes and confirming the next boot is clean — if journal replay doesn't happen automatically under the busybox init, we may need an explicit `e2fsck -p /dev/vda` step before mounting.
