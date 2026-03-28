#!/bin/bash
set -eu

INSTALL_DIR="/data/ipv6-tunnel"
RC_LOCAL="/etc/rc.local"
MARKER_START="# --- ipv6-tunnel ---"
MARKER_END="# --- /ipv6-tunnel ---"

log_message() {
    echo "[install] $*"
}

log_message "installing ipv6-tunnel to ${INSTALL_DIR}"

mkdir -p "${INSTALL_DIR}/web"

cp /tmp/ipv6-tunnel-server "${INSTALL_DIR}/"
cp /tmp/tunnel.sh "${INSTALL_DIR}/"
cp /tmp/rc-local-fragment.sh "${INSTALL_DIR}/"
cp /tmp/index.html "${INSTALL_DIR}/web/"

chmod +x "${INSTALL_DIR}/ipv6-tunnel-server"
chmod +x "${INSTALL_DIR}/tunnel.sh"

if [ ! -f "${INSTALL_DIR}/config.json" ]; then
    log_message "generating default config.json"
    cat > "${INSTALL_DIR}/config.json" << 'CFGEOF'
{
  "tunnel": {
    "broker": "he",
    "remote_endpoint": "",
    "local_ipv6": "",
    "remote_ipv6": "",
    "ttl": 255,
    "mtu": 1480
  },
  "lan": {
    "enabled": false,
    "dns": ["2606:4700:4700::1111", "2001:4860:4860::8888"],
    "mode": "slaac",
    "networks": []
  },
  "health": {
    "enabled": true,
    "interval_sec": 30,
    "target": "2001:4860:4860::8888",
    "auto_restart": true
  },
  "server": {
    "port": 8686,
    "wan_interface": "ppp0",
    "auth_token": ""
  }
}
CFGEOF
fi

if ! grep -q "$MARKER_START" "$RC_LOCAL" 2>/dev/null; then
    log_message "adding boot entry to ${RC_LOCAL}"
    if grep -q "^exit 0" "$RC_LOCAL"; then
        sed -i "/^exit 0/i\\
${MARKER_START}\\
${INSTALL_DIR}/tunnel.sh boot\\
${MARKER_END}" "$RC_LOCAL"
    else
        cat >> "$RC_LOCAL" << RCEOF

${MARKER_START}
${INSTALL_DIR}/tunnel.sh boot
${MARKER_END}
RCEOF
    fi
else
    log_message "boot entry already present in ${RC_LOCAL}"
fi

pkill -f ipv6-tunnel-server 2>/dev/null || true
sleep 1

log_message "starting server"
"${INSTALL_DIR}/ipv6-tunnel-server" &

log_message "installation complete"
log_message "access UI at http://$(hostname -I | awk '{print $1}'):8686/"

rm -f /tmp/ipv6-tunnel-server /tmp/tunnel.sh /tmp/rc-local-fragment.sh /tmp/index.html
