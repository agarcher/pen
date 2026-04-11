#!/usr/bin/env bash
#
# Build a minimal Alpine Linux VM image for pen.
# Produces: vmlinuz (kernel) and initrd (initramfs with rootfs baked in).
#
# Requirements: curl, tar, cpio, gzip
# Works on macOS (no root needed — everything runs in userspace).
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WORK_DIR="${SCRIPT_DIR}/.work"

# If RELEASE_DIR is set, produce arch-suffixed artifacts for GitHub Releases.
# Otherwise, install directly to the local cache.
if [ -n "${RELEASE_DIR:-}" ]; then
    OUT_DIR="${RELEASE_DIR}"
    RELEASE_MODE=1
else
    OUT_DIR="${HOME}/.config/pen/images"
    RELEASE_MODE=0
fi

# Alpine version and architecture
ALPINE_VERSION="3.21"
ALPINE_RELEASE="3.21.3"
MIRROR="https://dl-cdn.alpinelinux.org/alpine"

# Architecture: override with ARCH env var, or detect from host.
if [ -z "${ARCH:-}" ]; then
    case "$(uname -m)" in
        arm64|aarch64) ARCH="aarch64" ;;
        x86_64)        ARCH="x86_64" ;;
        *)             echo "Unsupported architecture: $(uname -m)"; exit 1 ;;
    esac
fi

MINIROOTFS_URL="${MIRROR}/v${ALPINE_VERSION}/releases/${ARCH}/alpine-minirootfs-${ALPINE_RELEASE}-${ARCH}.tar.gz"
APK_REPO_URL="${MIRROR}/v${ALPINE_VERSION}/main/${ARCH}"

echo "==> Preparing build directories"
rm -rf "${WORK_DIR}"
mkdir -p "${WORK_DIR}/rootfs" "${OUT_DIR}"

echo "==> Downloading Alpine minirootfs"
curl -fSL "${MINIROOTFS_URL}" -o "${WORK_DIR}/minirootfs.tar.gz"

# We need the kernel AND its matching modules. The Alpine netboot files
# (vmlinuz-virt, initramfs-virt) lag behind the main repo and their
# shipped initramfs-virt omits ext4, which we need for the overlay disk.
# Fetch the authoritative source — the linux-virt apk package — which
# ships both a kernel and the full module tree for the same version.
echo "==> Resolving linux-virt package version"
curl -fSL "${APK_REPO_URL}/APKINDEX.tar.gz" -o "${WORK_DIR}/APKINDEX.tar.gz"
mkdir -p "${WORK_DIR}/apkindex"
tar xzf "${WORK_DIR}/APKINDEX.tar.gz" -C "${WORK_DIR}/apkindex"
LINUX_VIRT_VERSION=$(awk -v RS='' '/(^|\n)P:linux-virt\n/ { for (i=1;i<=NF;i++) if ($i ~ /^V:/) { sub(/^V:/,"",$i); print $i; exit } }' "${WORK_DIR}/apkindex/APKINDEX")
if [ -z "${LINUX_VIRT_VERSION}" ]; then
    echo "error: could not resolve linux-virt version from APKINDEX" >&2
    exit 1
fi
echo "==> linux-virt version: ${LINUX_VIRT_VERSION}"

echo "==> Downloading linux-virt apk (kernel + modules)"
curl -fSL "${APK_REPO_URL}/linux-virt-${LINUX_VIRT_VERSION}.apk" -o "${WORK_DIR}/linux-virt.apk"
mkdir -p "${WORK_DIR}/linux-virt-extract"
# apk files are multi-stream gzip (signature || control || data); gunzip -c
# decodes all streams concatenated so a single tar pass extracts everything.
( cd "${WORK_DIR}/linux-virt-extract" && gunzip -c "${WORK_DIR}/linux-virt.apk" | tar xf - 2>/dev/null ) || true
if [ ! -f "${WORK_DIR}/linux-virt-extract/boot/vmlinuz-virt" ]; then
    echo "error: no boot/vmlinuz-virt extracted from linux-virt apk" >&2
    exit 1
fi
if [ ! -d "${WORK_DIR}/linux-virt-extract/lib/modules" ]; then
    echo "error: no lib/modules extracted from linux-virt apk" >&2
    exit 1
fi

echo "==> Installing kernel"
if [ "$RELEASE_MODE" = "1" ]; then
    cp "${WORK_DIR}/linux-virt-extract/boot/vmlinuz-virt" "${OUT_DIR}/vmlinuz-${ARCH}"
else
    cp "${WORK_DIR}/linux-virt-extract/boot/vmlinuz-virt" "${OUT_DIR}/vmlinuz"
fi

echo "==> Extracting minirootfs"
cd "${WORK_DIR}/rootfs"
tar xzf "${WORK_DIR}/minirootfs.tar.gz"

# The alpine minirootfs is pure userspace — it ships zero kernel modules.
# The virt kernel builds virtio_pci in, but every paravirt driver (blk,
# net, fs, overlay) plus ext4 itself is a loadable module. Without
# /lib/modules/<kver>/ nothing can see /dev/vda, eth0, the workspace
# share, mount an overlayfs, or mount an ext4 filesystem. Graft the full
# module tree from the linux-virt apk we just extracted.
echo "==> Grafting kernel modules from linux-virt"
mkdir -p "${WORK_DIR}/rootfs/lib"
cp -R "${WORK_DIR}/linux-virt-extract/lib/modules" "${WORK_DIR}/rootfs/lib/"

echo "==> Configuring guest init"
# Create the init script that runs inside the initramfs
cat > "${WORK_DIR}/rootfs/init" << 'INITEOF'
#!/bin/sh
# pen guest init — runs as PID 1 inside the VM.
#
# Two stages:
#   stage 1: bare initramfs. Mount essentials, bring up network, format
#            /dev/vda on first boot, compose overlayfs over the rootfs,
#            chroot in and re-exec as stage 2.
#   stage 2: same /init with PEN_INIT_STAGE2=1. Mount workspace, source
#            env, launch the interactive shell, poweroff cleanly when it
#            exits.
#
# /dev/vda is always expected — pen shell creates overlay.img before boot.
# Any failure to set up the overlay is a hard error; we print a message
# and poweroff so the user gets a clean exit rather than a half-broken VM.

set -u

pen_trace() { echo "pen-init: $*"; }

pen_die() {
    echo "pen-init: FATAL: $*" >&2
    echo "pen-init: powering off in 3s" >&2
    sleep 3
    sync
    poweroff -f
    # poweroff should not return; if it somehow does, block so PID 1
    # doesn't exit and trigger a kernel panic.
    while :; do sleep 3600; done
}

if [ -z "${PEN_INIT_STAGE2:-}" ]; then
    # ===== STAGE 1 =====
    mount -t proc proc /proc
    mount -t sysfs sysfs /sys
    mount -t devtmpfs devtmpfs /dev
    mount -t tmpfs tmpfs /tmp
    mount -t tmpfs tmpfs /run
    mkdir -p /dev/pts
    mount -t devpts devpts /dev/pts

    # Load the kernel modules we need. The Alpine virt kernel builds
    # virtio_pci in, but every paravirt device driver (blk, net, fs,
    # overlay) plus ext4 itself is a loadable module shipped in
    # /lib/modules/<uname -r>/, grafted into the rootfs at image build
    # time from the linux-virt apk.
    for mod in virtio_blk virtio_net virtiofs overlay ext4; do
        modprobe "$mod" || pen_die "modprobe $mod failed"
    done

    hostname pen
    ip link set lo up

    # The PCI probe that creates eth0 is async with respect to modprobe.
    # Retry briefly so DHCP can start before we move on.
    i=0
    while [ $i -lt 10 ]; do
        ip link show eth0 >/dev/null 2>&1 && break
        sleep 1
        i=$((i+1))
    done
    ip link show eth0 >/dev/null 2>&1 || pen_die "eth0 never appeared"
    ip link set eth0 up
    # Alpine's udhcpc handler lives at /usr/share/udhcpc/, not /etc/udhcpc/.
    # Without the -s script udhcpc gets a lease but never applies the
    # returned IP/route/DNS to the interface.
    udhcpc -i eth0 -q -n -t 10 -s /usr/share/udhcpc/default.script \
        >/dev/null 2>&1 || pen_die "udhcpc failed — no IPv4 lease"
    pen_trace "network up"

    [ -b /dev/vda ] || pen_die "/dev/vda missing — is the overlay disk attached?"

    # Format the overlay disk on first boot. e2fsprogs isn't in the base
    # minirootfs, so apk-install it lazily — only the first boot of a
    # fresh VM pays this cost. Busybox blkid exits 0 with empty output
    # on an unformatted device, so we test for empty output rather than
    # exit status.
    if [ -z "$(blkid /dev/vda 2>/dev/null)" ]; then
        pen_trace "overlay disk is unformatted"
        if ! command -v mkfs.ext4 >/dev/null 2>&1; then
            pen_trace "installing e2fsprogs (one-time, fresh disk)..."
            apk update 2>&1 | sed 's/^/apk: /'
            apk add e2fsprogs 2>&1 | sed 's/^/apk: /' || \
                pen_die "apk add e2fsprogs failed"
            command -v mkfs.ext4 >/dev/null 2>&1 || \
                pen_die "mkfs.ext4 still missing after apk add"
        fi
        pen_trace "formatting overlay disk..."
        mkfs.ext4 -F /dev/vda 2>&1 | sed 's/^/mkfs: /' || \
            pen_die "mkfs.ext4 failed"
    fi

    # Specify -t ext4 explicitly: busybox `mount` autodetection can return
    # EINVAL on an unpartitioned whole-disk ext4 filesystem.
    mkdir -p /overlay
    mount -t ext4 /dev/vda /overlay || pen_die "mount /dev/vda on /overlay failed"
    mkdir -p /overlay/upper /overlay/work

    mkdir -p /newroot
    mount -t overlay overlay \
        -o lowerdir=/,upperdir=/overlay/upper,workdir=/overlay/work \
        /newroot || pen_die "mount overlayfs failed"

    # Move the essential virtual filesystems into the new root. Their
    # target dirs are visible via the lower layer.
    mount --move /proc /newroot/proc
    mount --move /sys  /newroot/sys
    mount --move /dev  /newroot/dev
    mount --move /run  /newroot/run
    mount --move /tmp  /newroot/tmp

    # Re-exec this same /init under the merged rootfs view as stage 2.
    # /overlay stays outside the chroot and is invisible from here on.
    PEN_INIT_STAGE2=1
    export PEN_INIT_STAGE2
    exec chroot /newroot /init
fi

# ===== STAGE 2 =====
mkdir -p /workspace
mount -t virtiofs workspace /workspace || pen_die "mount /workspace failed"

# Read injected env vars from the shared directory.
# The host writes .pen-env before boot; we copy it to tmpfs and delete the original.
if [ -f /workspace/.pen-env ]; then
    cp /workspace/.pen-env /run/pen-env
    rm -f /workspace/.pen-env
fi

# Set up environment
export HOME=/root
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export TERM=xterm-256color
[ -f /run/pen-env ] && . /run/pen-env
cd /workspace 2>/dev/null || cd /root

echo ""
echo "  pen vm ready"
echo ""

# Launch interactive shell on the console. We deliberately do NOT exec it —
# when the user's shell exits, we need to stay in PID 1 to run a clean
# poweroff. Letting PID 1 exit would cause a kernel panic, which Apple's
# Virtualization.framework does not report as a state transition, leaving
# pen blocked reading from a dead console pipe.
/bin/sh -l

# Best-effort graceful shutdown. poweroff -f issues RB_POWER_OFF which VZ
# sees as a clean state change; the host pen process then exits normally.
sync
poweroff -f
INITEOF
chmod +x "${WORK_DIR}/rootfs/init"

# Configure shell profile — sources injected env vars
cat > "${WORK_DIR}/rootfs/etc/profile" << 'PROFILEEOF'
export HOME=/root
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export TERM=xterm-256color
export PS1='pen:\w\$ '
[ -f /run/pen-env ] && . /run/pen-env
cd /workspace 2>/dev/null || true
PROFILEEOF

# DNS resolution
mkdir -p "${WORK_DIR}/rootfs/etc"
echo "nameserver 8.8.8.8" > "${WORK_DIR}/rootfs/etc/resolv.conf"

echo "==> Building initrd (cpio archive)"
cd "${WORK_DIR}/rootfs"
if [ "$RELEASE_MODE" = "1" ]; then
    find . | cpio -o -H newc 2>/dev/null | gzip > "${OUT_DIR}/initrd-${ARCH}"
else
    find . | cpio -o -H newc 2>/dev/null | gzip > "${OUT_DIR}/initrd"
fi

echo "==> Cleaning up"
rm -rf "${WORK_DIR}"

if [ "$RELEASE_MODE" = "1" ]; then
    echo "==> Done! Release artifacts:"
    echo "    ${OUT_DIR}/vmlinuz-${ARCH}"
    echo "    ${OUT_DIR}/initrd-${ARCH}"
else
    echo "==> Done!"
    echo "    Kernel: ${OUT_DIR}/vmlinuz"
    echo "    Initrd: ${OUT_DIR}/initrd"
    echo ""
    echo "    Test with: pen shell test --dir ."
fi
