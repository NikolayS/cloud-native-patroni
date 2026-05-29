#!/usr/bin/env bash
#
# Run this on the Hetzner VM after first SSH to harden it.
# Idempotent. Run before anything else.
#
set -euo pipefail

log()  { echo -e "\n\033[1;36m[harden]\033[0m $*"; }
info() { echo -e "  \033[0;32m✓\033[0m $*"; }

# -------------------------------------------------------------------
log "Disabling SSH password authentication (keys only)"
sed -i \
    -e 's/^#*PasswordAuthentication.*/PasswordAuthentication no/' \
    -e 's/^#*PermitEmptyPasswords.*/PermitEmptyPasswords no/' \
    -e 's/^#*ChallengeResponseAuthentication.*/ChallengeResponseAuthentication no/' \
    -e 's/^#*KbdInteractiveAuthentication.*/KbdInteractiveAuthentication no/' \
    -e 's/^#*UsePAM.*/UsePAM no/' \
    /etc/ssh/sshd_config
# Override anything in sshd_config.d (Ubuntu 24.04 has 50-cloud-init.conf)
cat > /etc/ssh/sshd_config.d/00-harden.conf <<'EOF'
PasswordAuthentication no
PermitEmptyPasswords no
ChallengeResponseAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin prohibit-password
MaxAuthTries 3
LoginGraceTime 30
EOF
systemctl restart ssh
info "SSH: key-only authentication enforced"

# -------------------------------------------------------------------
log "Installing fail2ban (IP banning for failed SSH attempts)"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq fail2ban >/dev/null
cat > /etc/fail2ban/jail.local <<'EOF'
[sshd]
enabled = true
port = ssh
maxretry = 3
findtime = 600
bantime = 3600
EOF
systemctl enable --now fail2ban
systemctl restart fail2ban
info "fail2ban: 3 failures → 1 hour ban"

# -------------------------------------------------------------------
log "Enabling unattended security updates"
apt-get install -y -qq unattended-upgrades >/dev/null
cat > /etc/apt/apt.conf.d/20auto-upgrades <<'EOF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
EOF
info "unattended-upgrades: daily security patches"

# -------------------------------------------------------------------
log "Setting up host-level firewall (defense in depth)"
# Hetzner Cloud Firewall already blocks everything except SSH/ICMP at the
# network edge. This is a backup using ufw.
apt-get install -y -qq ufw >/dev/null
ufw --force reset >/dev/null
ufw default deny incoming
ufw default allow outgoing
ufw allow ssh
ufw --force enable
info "ufw: default-deny inbound (except SSH)"

# -------------------------------------------------------------------
log "Disabling unused services"
systemctl disable --now snapd.seeded.service snapd.service snapd.socket 2>/dev/null || true
info "snapd: disabled"

# -------------------------------------------------------------------
log "Verification"
echo ""
echo "Active SSH config:"
sshd -T 2>/dev/null | grep -iE "passwordauth|permitroot|maxauthtries|usepam" | sed 's/^/  /'
echo ""
echo "fail2ban status:"
fail2ban-client status sshd 2>/dev/null | sed 's/^/  /' || true
echo ""
echo "UFW status:"
ufw status verbose | sed 's/^/  /'
echo ""
info "Hardening complete."
echo ""
echo "Hetzner Cloud Firewall (network-level) is already blocking everything"
echo "except SSH (22) and ICMP. Even if you accidentally expose a service"
echo "on the VM (kind, k8s API, etc.), it won't be reachable from outside."
