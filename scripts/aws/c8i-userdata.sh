#!/usr/bin/env bash
#
# c8i-userdata.sh — EC2 user-data bootstrap for the MIC paper's AMD64
# benchmark reference platform.
#
# Target instance:   c8i.4xlarge  (16 vCPU, Intel Xeon 6 / Granite Rapids)
# Target AMI:        Canonical Ubuntu 24.04 LTS, AMD64, HVM, EBS-backed.
#                    SSM parameter:
#                      /aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id
#
# How to use:
#   1. Launch a c8i.4xlarge from the AWS console with the AMI above.
#   2. Paste this entire file into "User data" (Advanced details).
#   3. Attach an IAM role with SSM Session Manager access if you want to
#      connect without opening SSH, or attach an SSH key pair the usual way.
#   4. Wait ~10–15 minutes for first-boot bootstrap to finish. The marker
#      file /home/ubuntu/BOOTSTRAP_DONE appears when the script completes.
#      Bootstrap log: /var/log/mic-bootstrap.log
#   5. Once logged in as 'ubuntu', run:
#          cd ~/medical-image-codec
#          ./run-paper-benchmarks.sh
#      Results land in results/<timestamp>/.
#
# What this script installs:
#   - Build tools (build-essential, cmake, ninja, pkg-config, git)
#   - libzstd-dev from apt (for the cgo_zstd path)
#   - Go 1.25.0 from the official tarball (go.mod requires >= 1.25)
#   - OpenJPH v0.15.0 from source (matches the version referenced in the paper)
#   - CharLS 2.4.2 from source (matches docs/jpegls-comparison.md)
#   - Clones the medical-image-codec repo to /home/ubuntu/medical-image-codec
#
# What this script does NOT do (deliberate — bench-time decisions):
#   - Pin CPU governor, disable turbo, disable SMT. The methodology used for
#     the prior AMD64 reference left these at vendor defaults; do the same
#     here unless you intentionally want a different policy.
#   - Run benchmarks. The user opens a session and runs them by hand so
#     output and variance can be inspected interactively.

set -euo pipefail
exec > >(tee -a /var/log/mic-bootstrap.log) 2>&1

echo "=== MIC c8i bootstrap starting at $(date -u +%Y-%m-%dT%H:%M:%SZ) ==="
echo "    instance: $(curl -s --max-time 2 http://169.254.169.254/latest/meta-data/instance-type || echo 'unknown')"
echo "    arch:     $(uname -m)"
echo "    kernel:   $(uname -sr)"

# ---------------------------------------------------------------------------
# 1. System packages
# ---------------------------------------------------------------------------
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get upgrade -y
apt-get install -y \
  build-essential \
  ca-certificates \
  cmake \
  curl \
  git \
  libzstd-dev \
  ninja-build \
  pkg-config \
  python3 \
  unzip

# ---------------------------------------------------------------------------
# 2. Go toolchain (matches go.mod: go 1.25.0)
# ---------------------------------------------------------------------------
GO_VERSION="1.25.0"
GO_TARBALL="go${GO_VERSION}.linux-amd64.tar.gz"
GO_URL="https://go.dev/dl/${GO_TARBALL}"

echo "=== Installing Go ${GO_VERSION} ==="
cd /tmp
curl -fsSLO "${GO_URL}"
rm -rf /usr/local/go
tar -C /usr/local -xzf "${GO_TARBALL}"
rm -f "${GO_TARBALL}"

# Expose go on PATH for all login shells (including 'ubuntu').
cat >/etc/profile.d/mic-go.sh <<'EOF'
export PATH=/usr/local/go/bin:$PATH
export GOPATH=$HOME/go
export PATH=$GOPATH/bin:$PATH
EOF
chmod 0644 /etc/profile.d/mic-go.sh

# Make go available to this script too.
export PATH=/usr/local/go/bin:$PATH
go version

# ---------------------------------------------------------------------------
# 3. OpenJPH (HTJ2K reference — v0.15.0 to match the paper)
# ---------------------------------------------------------------------------
echo "=== Building OpenJPH v0.15.0 ==="
cd /tmp
rm -rf OpenJPH
git clone --depth 1 --branch 0.15.0 https://github.com/aous72/OpenJPH.git
cd OpenJPH
mkdir -p build && cd build
cmake -G Ninja \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_C_FLAGS="-O3 -march=native" \
  -DCMAKE_CXX_FLAGS="-O3 -march=native" \
  -DCMAKE_INSTALL_PREFIX=/usr/local \
  ..
ninja
ninja install
ldconfig
cd /tmp && rm -rf OpenJPH

# ---------------------------------------------------------------------------
# 4. CharLS (JPEG-LS reference — v2.4.2 to match docs/jpegls-comparison.md)
# ---------------------------------------------------------------------------
echo "=== Building CharLS 2.4.2 ==="
cd /tmp
rm -rf charls
git clone --depth 1 --branch 2.4.2 https://github.com/team-charls/charls.git
cd charls
mkdir -p build && cd build
cmake -G Ninja \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_CXX_FLAGS="-O3 -march=native" \
  -DCMAKE_INSTALL_PREFIX=/usr/local \
  -DBUILD_SHARED_LIBS=ON \
  -DCHARLS_BUILD_TESTS=OFF \
  -DCHARLS_BUILD_SAMPLES=OFF \
  ..
ninja
ninja install
ldconfig
cd /tmp && rm -rf charls

# ---------------------------------------------------------------------------
# 5. Clone the medical-image-codec repo
# ---------------------------------------------------------------------------
echo "=== Cloning medical-image-codec ==="
REPO_URL="${MIC_REPO_URL:-https://github.com/pappuks/medical-image-codec.git}"
REPO_DIR="/home/ubuntu/medical-image-codec"

if [[ ! -d "${REPO_DIR}/.git" ]]; then
  sudo -u ubuntu git clone "${REPO_URL}" "${REPO_DIR}"
fi
chown -R ubuntu:ubuntu "${REPO_DIR}"

# ---------------------------------------------------------------------------
# 6. Preflight: confirm the cgo_ojph + cgo_zstd builds link cleanly
# ---------------------------------------------------------------------------
echo "=== Preflight: building cgo_ojph + cgo_zstd targets ==="
sudo -u ubuntu -H bash -lc "
  cd ${REPO_DIR}
  export PATH=/usr/local/go/bin:\$PATH
  go build -tags cgo_ojph ./ojph/
  go build -tags 'cgo_ojph cgo_zstd' ./ojph/
"

# ---------------------------------------------------------------------------
# 7. Done — drop the marker for the user.
# ---------------------------------------------------------------------------
sudo -u ubuntu touch /home/ubuntu/BOOTSTRAP_DONE
echo "=== MIC c8i bootstrap finished at $(date -u +%Y-%m-%dT%H:%M:%SZ) ==="
echo "    next: ssh in as 'ubuntu', cd medical-image-codec, ./run-paper-benchmarks.sh"
