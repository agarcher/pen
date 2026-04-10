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
KERNEL_URL="${MIRROR}/v${ALPINE_VERSION}/releases/${ARCH}/netboot/vmlinuz-virt"
INITRAMFS_URL="${MIRROR}/v${ALPINE_VERSION}/releases/${ARCH}/netboot/initramfs-virt"

echo "==> Preparing build directories"
rm -rf "${WORK_DIR}"
mkdir -p "${WORK_DIR}/rootfs" "${OUT_DIR}"

echo "==> Downloading Alpine minirootfs"
curl -fSL "${MINIROOTFS_URL}" -o "${WORK_DIR}/minirootfs.tar.gz"

echo "==> Downloading kernel"
if [ "$RELEASE_MODE" = "1" ]; then
    curl -fSL "${KERNEL_URL}" -o "${OUT_DIR}/vmlinuz-${ARCH}"
else
    curl -fSL "${KERNEL_URL}" -o "${OUT_DIR}/vmlinuz"
fi

echo "==> Extracting minirootfs"
cd "${WORK_DIR}/rootfs"
tar xzf "${WORK_DIR}/minirootfs.tar.gz"

echo "==> Configuring guest init"
# Create the init script that runs inside the initramfs
cat > "${WORK_DIR}/rootfs/init" << 'INITEOF'
#!/bin/sh
# pen guest init — runs as PID 1 inside the VM
#
# Two stages:
#   stage 1: bare initramfs. Mount essentials, bring up network, set up the
#            overlayfs over /dev/vda if present, then chroot into the merged
#            view and re-exec this script as stage 2.
#   stage 2: same script with PEN_INIT_STAGE2=1. Mount workspace, source env,
#            launch the interactive shell.
#
# When /dev/vda is absent (legacy/back-compat) stage 1 falls through to the
# stage 2 work in the original initramfs — ephemeral, like the pre-overlay
# behavior.

if [ -z "${PEN_INIT_STAGE2:-}" ]; then
    # ===== STAGE 1 =====
    mount -t proc proc /proc
    mount -t sysfs sysfs /sys
    mount -t devtmpfs devtmpfs /dev
    mount -t tmpfs tmpfs /tmp
    mount -t tmpfs tmpfs /run
    mkdir -p /dev/pts
    mount -t devpts devpts /dev/pts

    hostname pen
    ip link set lo up

    if ip link show eth0 >/dev/null 2>&1; then
        ip link set eth0 up
        udhcpc -b -i eth0 -q -s /etc/udhcpc/default.script 2>/dev/null &
    fi

    if [ -b /dev/vda ]; then
        # Format the overlay disk on first boot. e2fsprogs is not in the
        # base minirootfs, so install it lazily — only the very first boot
        # of a fresh VM pays this cost. Subsequent boots see an already-
        # formatted disk and skip both the install and the mkfs.
        if ! blkid /dev/vda >/dev/null 2>&1; then
            if ! command -v mkfs.ext4 >/dev/null 2>&1; then
                # Wait briefly for DHCP so apk can reach the repos.
                i=0
                while [ $i -lt 5 ]; do
                    ip route get 8.8.8.8 >/dev/null 2>&1 && break
                    sleep 1
                    i=$((i+1))
                done
                echo "pen: installing e2fsprogs (one-time, fresh disk)..."
                apk update >/dev/null 2>&1 || true
                if ! apk add e2fsprogs >/dev/null 2>&1; then
                    echo "pen: failed to install e2fsprogs; running ephemeral" >&2
                    PEN_INIT_STAGE2=1
                    export PEN_INIT_STAGE2
                    exec /init
                fi
            fi
            echo "pen: formatting overlay disk..."
            mkfs.ext4 -q /dev/vda
        fi

        mkdir -p /overlay
        mount /dev/vda /overlay
        mkdir -p /overlay/upper /overlay/work

        mkdir -p /newroot
        mount -t overlay overlay \
            -o lowerdir=/,upperdir=/overlay/upper,workdir=/overlay/work \
            /newroot

        # Move the essential virtual filesystems into the new root. Their
        # target dirs are visible via the lower layer.
        mount --move /proc /newroot/proc
        mount --move /sys  /newroot/sys
        mount --move /dev  /newroot/dev
        mount --move /run  /newroot/run
        mount --move /tmp  /newroot/tmp

        # Re-exec this same /init under the merged rootfs view as stage 2.
        # /overlay is outside the chroot and thus invisible from here on.
        PEN_INIT_STAGE2=1
        export PEN_INIT_STAGE2
        exec chroot /newroot /init
    fi
    # No overlay disk — fall through to stage 2 in the initramfs (ephemeral).
fi

# ===== STAGE 2 =====
mkdir -p /workspace
mount -t virtiofs workspace /workspace 2>/dev/null && echo "workspace mounted at /workspace" || true

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

# Launch interactive shell on the console
exec /bin/sh -l
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
