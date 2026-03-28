#!/bin/bash
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CONFIG_FILE="${SCRIPT_DIR}/config.json"
TUNNEL_IFACE="sit-6in4"
SERVER_BIN="${SCRIPT_DIR}/ipv6-tunnel-server"

log_message() {
    logger -t ipv6-tunnel "$*"
}

read_config() {
    if [ ! -f "$CONFIG_FILE" ]; then
        echo "Error: config file not found: $CONFIG_FILE" >&2
        exit 1
    fi
}

get_json() {
    python3 -c "import sys,json; d=json.load(open('$CONFIG_FILE')); print($1)" 2>/dev/null \
        || jq -r "$2" "$CONFIG_FILE" 2>/dev/null
}

get_bool() {
    local raw
    raw=$(get_json "$1" "$2" 2>/dev/null || echo "false")
    case "$raw" in
        true|True|1) echo "true" ;;
        *) echo "false" ;;
    esac
}

# Iterates over LAN networks, calling $1 iface prefix_addr prefix_len for each
foreach_lan_network() {
    local callback="$1"
    local lan_enabled
    lan_enabled=$(get_bool "d['lan']['enabled']" '.lan.enabled')
    [ "$lan_enabled" = "true" ] || return 0
    local count
    count=$(get_json "len(d['lan']['networks'])" '.lan.networks | length' 2>/dev/null || echo "0")
    [ "$count" -gt 0 ] || return 0
    for i in $(seq 0 $((count - 1))); do
        local iface prefix
        iface=$(get_json "d['lan']['networks'][$i]['interface']" ".lan.networks[$i].interface")
        prefix=$(get_json "d['lan']['networks'][$i]['prefix']" ".lan.networks[$i].prefix")
        "$callback" "$iface" "${prefix%/*}" "${prefix##*/}"
    done
}

do_up() {
    read_config
    local remote_endpoint local_ipv6 ttl mtu wan_iface wan_ip

    remote_endpoint=$(get_json "d['tunnel']['remote_endpoint']" '.tunnel.remote_endpoint')
    local_ipv6=$(get_json "d['tunnel']['local_ipv6']" '.tunnel.local_ipv6')
    ttl=$(get_json "d['tunnel']['ttl']" '.tunnel.ttl')
    mtu=$(get_json "d['tunnel']['mtu']" '.tunnel.mtu')
    wan_iface=$(get_json "d['server']['wan_interface']" '.server.wan_interface')
    wan_ip=$(ip -4 addr show "$wan_iface" | grep -oP 'inet \K[0-9.]+')

    log_message "bringing tunnel up: remote=$remote_endpoint local=$wan_ip"

    modprobe sit 2>/dev/null || true

    # Route to broker endpoint via WAN (bypass VPN PBR)
    ip route add "${remote_endpoint}/32" dev "$wan_iface" src "$wan_ip" 2>/dev/null || true

    ip tunnel add "$TUNNEL_IFACE" mode sit \
        remote "$remote_endpoint" \
        local "$wan_ip" \
        ttl "$ttl"

    ip link set "$TUNNEL_IFACE" mtu "$mtu"
    ip link set "$TUNNEL_IFACE" up
    ip -6 addr add "$local_ipv6" dev "$TUNNEL_IFACE"
    ip -6 route add ::/0 dev "$TUNNEL_IFACE"
    sysctl -w net.ipv6.conf.all.forwarding=1 > /dev/null

    _add_prefix() { ip -6 addr add "${2}1/${3}" dev "$1" 2>/dev/null || true; log_message "assigned ${2}1/${3} to $1"; }
    foreach_lan_network _add_prefix

    # Firewall rules (nftables)
    nft add table ip filter 2>/dev/null || true
    nft add chain ip filter ipv6tunnel-input '{ type filter hook input priority 0; }' 2>/dev/null || true
    nft add rule ip filter ipv6tunnel-input iifname "$wan_iface" ip protocol 41 accept 2>/dev/null || true

    nft add table ip6 filter 2>/dev/null || true
    nft add chain ip6 filter ipv6tunnel-forward '{ type filter hook forward priority 0; }' 2>/dev/null || true
    nft add rule ip6 filter ipv6tunnel-forward iifname "$TUNNEL_IFACE" ct state established,related accept 2>/dev/null || true
    nft add rule ip6 filter ipv6tunnel-forward oifname "$TUNNEL_IFACE" accept 2>/dev/null || true
    nft add rule ip6 filter ipv6tunnel-forward iifname "$TUNNEL_IFACE" ct state new drop 2>/dev/null || true

    log_message "tunnel up"
}

do_down() {
    log_message "bringing tunnel down"

    nft delete chain ip filter ipv6tunnel-input 2>/dev/null || true
    nft delete chain ip6 filter ipv6tunnel-forward 2>/dev/null || true

    if [ -f "$CONFIG_FILE" ]; then
        _del_prefix() { ip -6 addr del "${2}1/${3}" dev "$1" 2>/dev/null || true; }
        foreach_lan_network _del_prefix

        local remote_endpoint wan_iface
        remote_endpoint=$(get_json "d['tunnel']['remote_endpoint']" '.tunnel.remote_endpoint' 2>/dev/null || echo "")
        wan_iface=$(get_json "d['server']['wan_interface']" '.server.wan_interface' 2>/dev/null || echo "ppp0")
        if [ -n "$remote_endpoint" ]; then
            ip route del "${remote_endpoint}/32" dev "$wan_iface" 2>/dev/null || true
        fi
    fi

    ip -6 route del ::/0 dev "$TUNNEL_IFACE" 2>/dev/null || true
    ip tunnel del "$TUNNEL_IFACE" 2>/dev/null || true

    log_message "tunnel down"
}

do_restart() {
    do_down
    sleep 1
    do_up
}

do_status() {
    local tunnel_up="false"
    local wan_ip=""
    local local_ipv6=""
    local ping_ok="false"
    local ping_ms=0

    if ip link show "$TUNNEL_IFACE" &>/dev/null; then
        tunnel_up="true"
    fi

    if [ -f "$CONFIG_FILE" ]; then
        local wan_iface
        wan_iface=$(get_json "d['server']['wan_interface']" '.server.wan_interface' 2>/dev/null || echo "ppp0")
        wan_ip=$(ip -4 addr show "$wan_iface" 2>/dev/null | grep -oP 'inet \K[0-9.]+' || echo "")
        local_ipv6=$(get_json "d['tunnel']['local_ipv6']" '.tunnel.local_ipv6' 2>/dev/null || echo "")
    fi

    if [ "$tunnel_up" = "true" ] && [ -f "$CONFIG_FILE" ]; then
        local target
        target=$(get_json "d['health']['target']" '.health.target' 2>/dev/null || echo "2001:4860:4860::8888")
        local ping_out
        if ping_out=$(ping -6 -c 1 -W 3 "$target" 2>/dev/null); then
            ping_ok="true"
            ping_ms=$(echo "$ping_out" | grep -oP 'time=\K[0-9.]+' | head -1 | cut -d. -f1)
            [ -z "$ping_ms" ] && ping_ms=0
        fi
    fi

    local networks_json="[]"
    if [ -f "$CONFIG_FILE" ]; then
        local lan_enabled
        lan_enabled=$(get_bool "d['lan']['enabled']" '.lan.enabled')
        if [ "$lan_enabled" = "true" ]; then
            local count
            count=$(get_json "len(d['lan']['networks'])" '.lan.networks | length' 2>/dev/null || echo "0")
            if [ "$count" -gt 0 ]; then
            networks_json="["
            for i in $(seq 0 $((count - 1))); do
                local iface prefix
                iface=$(get_json "d['lan']['networks'][$i]['interface']" ".lan.networks[$i].interface")
                prefix=$(get_json "d['lan']['networks'][$i]['prefix']" ".lan.networks[$i].prefix")
                [ "$i" -gt 0 ] && networks_json="${networks_json},"
                networks_json="${networks_json}{\"interface\":\"${iface}\",\"prefix\":\"${prefix}\"}"
            done
            networks_json="${networks_json}]"
            fi
        fi
    fi

    cat <<STATUSEOF
{"tunnel_up":${tunnel_up},"interface":"${TUNNEL_IFACE}","local_ipv6":"${local_ipv6}","wan_ipv4":"${wan_ip}","networks":${networks_json},"ping_ok":${ping_ok},"ping_ms":${ping_ms}}
STATUSEOF
}

do_boot() {
    log_message "boot: starting tunnel and server"
    # Start server FIRST so UI is always reachable even if tunnel fails
    if [ -x "$SERVER_BIN" ]; then
        "$SERVER_BIN" &
        log_message "server started (pid $!)"
    else
        log_message "server binary not found: $SERVER_BIN"
    fi
    # Tunnel up is best-effort at boot — don't kill the script on failure
    do_up || log_message "tunnel up failed at boot (fix via UI at port 8686)"
}

case "${1:-}" in
    up)      do_up ;;
    down)    do_down ;;
    restart) do_restart ;;
    status)  do_status ;;
    boot)    do_boot ;;
    *)
        echo "Usage: $0 {up|down|restart|status|boot}" >&2
        exit 1
        ;;
esac
