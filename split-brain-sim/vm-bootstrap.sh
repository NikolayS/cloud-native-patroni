#!/usr/bin/env bash
#
# Bootstrap script for the CNPG partial-connectivity reproduction.
#
# Run on the Hetzner VM (Ubuntu 24.04):
#   curl -fsSL https://raw.githubusercontent.com/NikolayS/cloudnative-pg/claude/sweet-ride-Oyvby/split-brain-sim/vm-bootstrap.sh | bash
#
# Or after SSH'ing in:
#   git clone -b claude/sweet-ride-Oyvby https://github.com/NikolayS/cloudnative-pg /root/cnpg
#   cd /root/cnpg/split-brain-sim
#   ./vm-bootstrap.sh
#
# Installs: docker, kind, kubectl, helm. Then runs the reproduction.
# Idempotent: safe to re-run.
#
set -euo pipefail

log()  { echo -e "\n\033[1;36m[bootstrap]\033[0m $*"; }
info() { echo -e "  \033[0;32m✓\033[0m $*"; }
warn() { echo -e "  \033[1;33m⚠\033[0m $*"; }

# -------------------------------------------------------------------
log "Installing system packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq \
    ca-certificates curl gnupg lsb-release \
    git iptables jq postgresql-client uuid-runtime \
    >/dev/null
info "base packages installed"

# -------------------------------------------------------------------
log "Installing Docker"
if ! command -v docker >/dev/null 2>&1; then
    install -m 0755 -d /etc/apt/keyrings
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
    chmod a+r /etc/apt/keyrings/docker.gpg
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
      https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" \
      > /etc/apt/sources.list.d/docker.list
    apt-get update -qq
    apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-compose-plugin >/dev/null
    systemctl enable --now docker
fi
info "docker: $(docker --version)"

# -------------------------------------------------------------------
log "Installing kind"
if ! command -v kind >/dev/null 2>&1; then
    curl -fsSL -o /usr/local/bin/kind \
        https://kind.sigs.k8s.io/dl/v0.24.0/kind-linux-amd64
    chmod +x /usr/local/bin/kind
fi
info "kind: $(kind --version)"

# -------------------------------------------------------------------
log "Installing kubectl"
if ! command -v kubectl >/dev/null 2>&1; then
    K_VER=$(curl -fsSL https://dl.k8s.io/release/stable.txt)
    curl -fsSL -o /usr/local/bin/kubectl \
        "https://dl.k8s.io/release/${K_VER}/bin/linux/amd64/kubectl"
    chmod +x /usr/local/bin/kubectl
fi
info "kubectl: $(kubectl version --client --output=yaml 2>/dev/null | grep gitVersion | head -1)"

# -------------------------------------------------------------------
log "Tuning kernel for kind"
sysctl -w fs.inotify.max_user_watches=524288 >/dev/null
sysctl -w fs.inotify.max_user_instances=512 >/dev/null
info "inotify limits raised"

# -------------------------------------------------------------------
log "Reproduction scripts available in this repo:"
echo "  - cnpg-kind-partial-connectivity-repro.sh  (THE partial-connectivity race)"
echo "  - cnpg-kind-7407-repro.sh                  (basic CNPG kind partition)"
echo "  - cnpg-kind-pgque-repro.sh                 (pg_cron/PgQue variant)"
echo "  - cnpg-kind-sync5-kubelet-dead-repro.sh    (kubelet-dead adversarial)"
echo "  - cnpg-kind-sync6-logged-fq-skew-repro.sh  (FailoverQuorum skew)"
echo ""
echo "  Run the partial-connectivity reproduction:"
echo "    cd $(pwd)"
echo "    ./cnpg-kind-partial-connectivity-repro.sh 2>&1 | tee /root/repro.log"
echo ""
info "bootstrap complete"
