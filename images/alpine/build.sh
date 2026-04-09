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
OUT_DIR="${HOME}/.config/pen/images"

# Alpine version and architecture
ALPINE_VERSION="3.21"
ALPINE_RELEASE="3.21.3"
MIRROR="https://dl-cdn.alpinelinux.org/alpine"

# Detect host architecture
case "$(uname -m)" in
    arm64|aarch64) ARCH="aarch64" ;;
    x86_64)        ARCH="x86_64" ;;
    *)             echo "Unsupported architecture: $(uname -m)"; exit 1 ;;
esac

MINIROOTFS_URL="${MIRROR}/v${ALPINE_VERSION}/releases/${ARCH}/alpine-minirootfs-${ALPINE_RELEASE}-${ARCH}.tar.gz"
KERNEL_URL="${MIRROR}/v${ALPINE_VERSION}/releases/${ARCH}/netboot/vmlinuz-virt"
INITRAMFS_URL="${MIRROR}/v${ALPINE_VERSION}/releases/${ARCH}/netboot/initramfs-virt"

echo "==> Preparing build directories"
rm -rf "${WORK_DIR}"
mkdir -p "${WORK_DIR}/rootfs" "${OUT_DIR}"

echo "==> Downloading Alpine minirootfs"
curl -fSL "${MINIROOTFS_URL}" -o "${WORK_DIR}/minirootfs.tar.gz"

echo "==> Downloading kernel"
curl -fSL "${KERNEL_URL}" -o "${OUT_DIR}/vmlinuz"

echo "==> Extracting minirootfs"
cd "${WORK_DIR}/rootfs"
tar xzf "${WORK_DIR}/minirootfs.tar.gz"

echo "==> Configuring guest init"
# Create the init script that runs inside the initramfs
cat > "${WORK_DIR}/rootfs/init" << 'INITEOF'
#!/bin/sh
# pen guest init — runs as PID 1 inside the VM

# Mount essential filesystems
mount -t proc proc /proc
mount -t sysfs sysfs /sys
mount -t devtmpfs devtmpfs /dev
mount -t tmpfs tmpfs /tmp
mount -t tmpfs tmpfs /run
mkdir -p /dev/pts
mount -t devpts devpts /dev/pts

# Set hostname
hostname pen

# Bring up loopback
ip link set lo up

# Try to bring up eth0 with DHCP (background, non-blocking)
if ip link show eth0 >/dev/null 2>&1; then
    ip link set eth0 up
    udhcpc -b -i eth0 -q -s /etc/udhcpc/default.script 2>/dev/null &
fi

# Mount workspace via virtio-fs if available
mkdir -p /workspace
mount -t virtiofs workspace /workspace 2>/dev/null && echo "workspace mounted at /workspace" || true

# Set up environment
export HOME=/root
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export TERM=xterm-256color
cd /workspace 2>/dev/null || cd /root

echo ""
echo "  pen vm ready"
echo ""

# Launch interactive shell on the console
exec /bin/sh -l
INITEOF
chmod +x "${WORK_DIR}/rootfs/init"

# Configure shell profile
cat > "${WORK_DIR}/rootfs/etc/profile" << 'PROFILEEOF'
export HOME=/root
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export TERM=xterm-256color
export PS1='pen:\w\$ '
cd /workspace 2>/dev/null || true
PROFILEEOF

# DNS resolution
mkdir -p "${WORK_DIR}/rootfs/etc"
echo "nameserver 8.8.8.8" > "${WORK_DIR}/rootfs/etc/resolv.conf"

echo "==> Building initrd (cpio archive)"
cd "${WORK_DIR}/rootfs"
find . | cpio -o -H newc 2>/dev/null | gzip > "${OUT_DIR}/initrd"

echo "==> Cleaning up"
rm -rf "${WORK_DIR}"

echo "==> Done!"
echo "    Kernel: ${OUT_DIR}/vmlinuz"
echo "    Initrd: ${OUT_DIR}/initrd"
echo ""
echo "    Test with: pen shell test --dir ."
