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

mkdir -p "${INSTALL_DIR}"

pkill -f "^${INSTALL_DIR}/ipv6-tunnel-server($| )" 2>/dev/null || true
sleep 1

mv /tmp/ipv6-tunnel-server "${INSTALL_DIR}/ipv6-tunnel-server"
mv /tmp/tunnel.sh "${INSTALL_DIR}/tunnel.sh"
mv /tmp/rc-local-fragment.sh "${INSTALL_DIR}/rc-local-fragment.sh"

chmod +x "${INSTALL_DIR}/ipv6-tunnel-server"
chmod +x "${INSTALL_DIR}/tunnel.sh"

if [ ! -f "${INSTALL_DIR}/config.json" ]; then
    log_message "generating default config.json"
    cat > "${INSTALL_DIR}/config.json" << 'CFGEOF'
{
  "tunnel": {
    "enabled": false,
    "broker": "he",
    "remote_endpoint": "",
    "local_ipv6": "",
    "remote_ipv6": "",
    "ttl": 255,
    "mtu": 0
  },
  "lan": {
    "enabled": false,
    "dns": [],
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
    "port": 9086,
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

log_message "starting server"
nohup "${INSTALL_DIR}/ipv6-tunnel-server" >/dev/null 2>&1 </dev/null &

log_message "installation complete"
UI_PORT=$(python3 -c "import json; print(json.load(open('${INSTALL_DIR}/config.json')).get('server',{}).get('port',9086))" 2>/dev/null || echo 9086)
log_message "access UI at http://$(hostname -I | awk '{print $1}'):${UI_PORT}/"

rm -f /tmp/ipv6-tunnel-server /tmp/tunnel.sh /tmp/rc-local-fragment.sh
