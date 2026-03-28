#!/bin/bash
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SERVER_BIN="${SCRIPT_DIR}/ipv6-tunnel-server"

log_message() {
    logger -t ipv6-tunnel "$*"
}

ensure_server_binary() {
    if [ ! -x "$SERVER_BIN" ]; then
        echo "Error: server binary not found or not executable: $SERVER_BIN" >&2
        exit 1
    fi
}

run_ctl() {
    ensure_server_binary
    exec "$SERVER_BIN" ctl "$@"
}

start_server() {
    ensure_server_binary
    if pgrep -f "$SERVER_BIN" >/dev/null 2>&1; then
        log_message "server already running"
        return 0
    fi
    "$SERVER_BIN" serve >/dev/null 2>&1 &
    log_message "server started (pid $!)"
}

do_boot() {
    log_message "boot: starting daemon only"
    start_server
}

case "${1:-}" in
    up)      shift; run_ctl up "$@" ;;
    down)    shift; run_ctl down "$@" ;;
    restart) shift; run_ctl restart "$@" ;;
    status)  shift; run_ctl status "$@" ;;
    health)  shift; run_ctl health "$@" ;;
    boot)    do_boot ;;
    *)
        echo "Usage: $0 {up|down|restart|status|health|boot}" >&2
        exit 1
        ;;
esac
