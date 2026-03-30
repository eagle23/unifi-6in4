# IPv6 Tunnel Manager for UCG Fiber

[Русская версия](README.ru.md)

`unifi-tunnel-4to6` is a small on-router daemon for running an IPv6 `6in4` tunnel on UniFi hardware such as UCG Fiber.

It is built for the case where:

- your ISP gives you only IPv4 on WAN;
- the router kernel supports `sit`;
- you want a real routed IPv6 prefix in LAN;
- you want a simple web UI and a local CLI instead of manually juggling `ip`, `ip6tables`, RA, and boot scripts.

## What It Does

The daemon manages the whole lifecycle of a `6in4` tunnel:

- creates and destroys `sit-6in4`;
- configures IPv6 addresses and default route;
- adds a host route to the tunnel broker endpoint over WAN;
- enables IPv6 forwarding;
- assigns routed IPv6 gateway addresses on LAN bridges such as `br0`;
- sends Router Advertisements for SLAAC;
- runs IPv6 health checks;
- exposes a web UI and HTTP API;
- keeps desired state in `config.json` and observed state in `state.json`;
- survives common boot races such as late PPPoE/WAN readiness.

This project now follows the model:

- one daemon = one source of truth;
- `config.json` = desired state;
- `state.json` = observed state;
- shell scripts are compatibility wrappers, not the runtime brain.

## How It Works

### Runtime layout on the router

Installation uses `/data/ipv6-tunnel`:

- `/data/ipv6-tunnel/ipv6-tunnel-server` - daemon binary
- `/data/ipv6-tunnel/config.json` - desired config
- `/data/ipv6-tunnel/state.json` - observed state
- `/data/ipv6-tunnel/daemon.sock` - local control socket
- `/data/ipv6-tunnel/tunnel.sh` - compatibility wrapper

### Control plane

The daemon starts in `serve` mode and does three things:

1. loads `config.json`;
2. runs a reconcile loop against the real system state;
3. serves UI, API, and local control socket.

It also keeps retrying reconcile when boot ordering is imperfect, for example:

- PPPoE is not ready yet;
- the LAN bridge link-local IPv6 address is still in a bad DAD state after reboot.

### Data plane

On a healthy config the daemon ensures:

- `sit-6in4` exists;
- tunnel MTU is correct;
- the client tunnel IPv6 is assigned;
- default IPv6 route points to `sit-6in4`;
- LAN bridge interfaces get `prefix::1/64`;
- RA is sent for SLAAC;
- if `lan.dns` is empty, RA advertises the router LAN IPv6 address such as `prefix::1` as RDNSS;
- TCP MSS is clamped on forwarded IPv6 TCP traffic.

### MTU behavior

`tunnel.mtu` supports two modes:

- `0` = auto
- `> 0` = manual override

Auto mode resolves MTU from the WAN interface:

- effective tunnel MTU = `WAN_MTU - 20`

Example:

- PPPoE WAN MTU `1492` -> tunnel MTU `1472`

The effective MTU is exposed in `/api/status` and in the UI.

## Requirements

### Build host

- macOS or Linux
- Go installed
- `ssh` and `scp`
- access to the router as `root`

### Router

- UniFi router with `sit` support
- writable `/data`
- `ip`, `ip6tables`, `iptables`, `sysctl`, `ping`

## Build

```bash
make build
```

This creates:

```text
bin/ipv6-tunnel-server
```

Run tests:

```bash
make test
```

## Install on a Router

First install:

```bash
make install ROUTER_HOST=192.168.1.1 ROUTER_USER=root
```

What it does:

- copies the daemon and scripts to `/data/ipv6-tunnel`;
- creates a default `config.json` if it does not exist;
- adds a boot entry to `/etc/rc.local`;
- starts the daemon.

Default UI port is:

```text
9086
```

After install, open:

```text
http://<router-ip>:9086/
```

## Update an Existing Install

For normal upgrades:

```bash
make deploy ROUTER_HOST=192.168.1.1 ROUTER_USER=root
```

`deploy` does not replace your existing `config.json`.

It uploads a new binary through `/tmp/*.new`, replaces the live files, and restarts the daemon through `tunnel.sh boot`.

## Quick Start with Hurricane Electric

You do not need Hurricane Electric data to build or install the daemon.

You only need HE data when you are ready to actually enable the tunnel.

### Values from HE

Map the HE tunnel page like this:

- `Server IPv4 Address` -> `tunnel.remote_endpoint`
- `Server IPv6 Address` -> `tunnel.remote_ipv6`
- `Client IPv6 Address` -> `tunnel.local_ipv6`
- `Routed /64` -> `lan.networks[].prefix`

Your public WAN IPv4 is not entered manually. The daemon reads it from the configured WAN interface.

### Minimal example

```json
{
  "tunnel": {
    "enabled": false,
    "broker": "he",
    "remote_endpoint": "216.66.80.90",
    "local_ipv6": "2001:470:27:103d::2/64",
    "remote_ipv6": "2001:470:27:103d::1/64",
    "ttl": 255,
    "mtu": 0
  },
  "lan": {
    "enabled": true,
    "dns": [],
    "mode": "slaac",
    "networks": [
      {
        "interface": "br0",
        "prefix": "2001:470:28:1038::/64",
        "comment": "Default LAN"
      }
    ]
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
```

An empty `lan.dns` means:

- auto mode;
- advertise the router LAN IPv6 address derived from each configured prefix;
- for `2001:470:28:1038::/64`, clients receive `2001:470:28:1038::1` as DNS.

Then either:

- save this through the UI and click `Start`;
- or run:

```bash
ssh root@192.168.1.1 '/data/ipv6-tunnel/ipv6-tunnel-server ctl up'
```

## CLI

The local compatibility CLI is:

```bash
/data/ipv6-tunnel/ipv6-tunnel-server ctl status
/data/ipv6-tunnel/ipv6-tunnel-server ctl health
/data/ipv6-tunnel/ipv6-tunnel-server ctl up
/data/ipv6-tunnel/ipv6-tunnel-server ctl down
/data/ipv6-tunnel/ipv6-tunnel-server ctl restart
```

The shell wrapper provides the same interface:

```bash
/data/ipv6-tunnel/tunnel.sh status
/data/ipv6-tunnel/tunnel.sh up
/data/ipv6-tunnel/tunnel.sh down
/data/ipv6-tunnel/tunnel.sh restart
/data/ipv6-tunnel/tunnel.sh boot
```

## HTTP API

Public HTTP routes:

- `GET /api/status`
- `GET /api/config`
- `PUT /api/config`
- `GET /api/health`
- `POST /api/tunnel/up`
- `POST /api/tunnel/down`
- `POST /api/tunnel/restart`

If `server.auth_token` is set, use:

```text
Authorization: Bearer <token>
```

## Important Behavior

### UI and tunnel are decoupled

The UI should still be available even when:

- the tunnel config is incomplete;
- the tunnel cannot be brought up;
- boot ordering is imperfect.

That is intentional. A broken tunnel should not remove the only recovery surface.

### Port changes are startup-only

`server.port` is read on process start.

If you change it, restart the daemon.

### LAN prefixes

If your broker only gives you one routed `/64`, you should normally advertise it in one LAN segment only.

If you want multiple VLANs with clean routed IPv6, you need either:

- a larger delegated prefix;
- multiple brokers and policy design;
- or a different model such as ULA + NPTv6/NAT66.

## How to Verify It Works

### On the router

```bash
ssh root@192.168.1.1 '
/data/ipv6-tunnel/ipv6-tunnel-server ctl status
ip tunnel show
ip link show dev sit-6in4
ip -6 addr show dev sit-6in4
ip -6 addr show dev br0
ip -6 route show
ping -6 -c 3 2001:4860:4860::8888
'
```

Good signs:

- `reconcile_state` is `ready`
- `sit-6in4` exists and is `UP`
- `br0` has `prefix::1/64`
- default IPv6 route points to `sit-6in4`
- health probe succeeds

### On a macOS client

```bash
route -n get -inet6 default
ifconfig en0 | grep inet6
ping6 -c 3 2001:4860:4860::8888
curl -6 https://ifconfig.me
```

Good signs:

- client has a global IPv6 from your routed prefix
- default IPv6 route points to the router link-local address
- `ping6` works
- `curl -6` works

## Troubleshooting

### Show observed state

```bash
cat /data/ipv6-tunnel/state.json
```

### Show current config

```bash
cat /data/ipv6-tunnel/config.json
```

### Show tunnel interface

```bash
ip link show dev sit-6in4
ip -6 addr show dev sit-6in4
```

### Show LAN bridge state

```bash
ip -6 addr show dev br0
```

### Watch Router Advertisements

```bash
tcpdump -ni br0 'icmp6 && ip6[40] == 134'
```

### Tunnel is up but clients get no IPv6

Check:

- `reconcile_state` in `/api/status`
- `last_error`
- `ip -6 addr show dev br0`

The daemon now handles two common reboot failures automatically:

- WAN not ready yet during the first reconcile;
- bridge link-local IPv6 stuck in `dadfailed` or `tentative`.

### UI does not open

Check the configured port:

```bash
python3 -c 'import json; print(json.load(open("/data/ipv6-tunnel/config.json"))["server"]["port"])'
```

Check listeners:

```bash
ss -ltnp | grep 9086 || true
pgrep -af ipv6-tunnel-server
```

## Repository Layout

- `cmd/server` - daemon entry point
- `internal/control` - reconcile loop, state store, boot recovery logic
- `internal/api` - HTTP and local control API
- `internal/ra` - Router Advertisement logic
- `internal/config` - config loading, migration, defaults, validation
- `internal/tunnel` - status model and local client
- `scripts/install.sh` - first install
- `scripts/tunnel.sh` - compatibility wrapper
- `web` - embedded web UI
- `docs/superpowers` - design notes and implementation plans

## Notes

- The UI is embedded in the binary.
- Default port is `9086`.
- `tunnel.mtu = 0` means auto.
- `server.wan_interface` defaults to `ppp0`.
- Health checks default to `2001:4860:4860::8888`.

## Related Docs

- [One daemon architecture plan](docs/superpowers/plans/2026-03-28-one-daemon-source-of-truth.md)
- [Build, deploy, and HE setup guide](docs/superpowers/plans/2026-03-28-build-deploy-and-he-setup.md)
- [Original design spec](docs/superpowers/specs/2026-03-27-ipv6-tunnel-design.md)
