#!/bin/bash
set -eu

INSTALL_DIR="${IPV6_TUNNEL_INSTALL_DIR:-/data/ipv6-tunnel}"
RC_LOCAL="${IPV6_TUNNEL_RC_LOCAL:-/etc/rc.local}"
TEMP_DIR="${IPV6_TUNNEL_TEMP_DIR:-/tmp}"
MARKER_START="# --- ipv6-tunnel ---"
MARKER_END="# --- /ipv6-tunnel ---"
BOOT_COMMAND="${INSTALL_DIR}/tunnel.sh boot"

log_message() {
    echo "[install] $*"
}

render_rc_local() {
    local had_shebang="0"
    if [ -f "${RC_LOCAL}" ] && head -n 1 "${RC_LOCAL}" | grep -q '^#!'; then
        had_shebang="1"
    fi
    if [ "${had_shebang}" = "1" ]; then
        head -n 1 "${RC_LOCAL}"
    else
        echo "#!/bin/sh"
    fi
    echo
    if [ -f "${RC_LOCAL}" ]; then
        if [ "${had_shebang}" = "1" ]; then
            tail -n +2 "${RC_LOCAL}" | awk '$0 != "exit 0" { print }'
        else
            awk '$0 != "exit 0" { print }' "${RC_LOCAL}"
        fi
    fi
    if ! grep -q "${MARKER_START}" "${RC_LOCAL}" 2>/dev/null; then
        echo "${MARKER_START}"
        echo "${BOOT_COMMAND}"
        echo "${MARKER_END}"
        echo
    fi
    echo "exit 0"
}

ensure_rc_local() {
    local tmp_file
    mkdir -p "$(dirname "${RC_LOCAL}")"
    tmp_file="$(mktemp "${RC_LOCAL}.XXXXXX")"
    render_rc_local > "${tmp_file}"
    mv "${tmp_file}" "${RC_LOCAL}"
    chmod +x "${RC_LOCAL}"
}

log_message "installing ipv6-tunnel to ${INSTALL_DIR}"

mkdir -p "${INSTALL_DIR}"

pkill -f "^${INSTALL_DIR}/ipv6-tunnel-server($| )" 2>/dev/null || true
sleep 1

mv "${TEMP_DIR}/ipv6-tunnel-server" "${INSTALL_DIR}/ipv6-tunnel-server"
mv "${TEMP_DIR}/tunnel.sh" "${INSTALL_DIR}/tunnel.sh"
mv "${TEMP_DIR}/rc-local-fragment.sh" "${INSTALL_DIR}/rc-local-fragment.sh"

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

if grep -q "${MARKER_START}" "${RC_LOCAL}" 2>/dev/null; then
    log_message "normalizing ${RC_LOCAL}"
else
    log_message "adding boot entry to ${RC_LOCAL}"
fi
ensure_rc_local

log_message "starting server"
nohup "${INSTALL_DIR}/ipv6-tunnel-server" >/dev/null 2>&1 </dev/null &

log_message "installation complete"
UI_PORT=$(python3 -c "import json; print(json.load(open('${INSTALL_DIR}/config.json')).get('server',{}).get('port',9086))" 2>/dev/null || echo 9086)
log_message "access UI at http://$(hostname -I | awk '{print $1}'):${UI_PORT}/"

rm -f "${TEMP_DIR}/ipv6-tunnel-server" "${TEMP_DIR}/tunnel.sh" "${TEMP_DIR}/rc-local-fragment.sh"
