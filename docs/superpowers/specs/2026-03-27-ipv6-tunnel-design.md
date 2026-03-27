# IPv6 6in4 Tunnel Manager for UniFi UCG-Fiber

## Overview

Решение для получения IPv6 на UCG-Fiber (UniFi OS 5.x) при отсутствии нативной поддержки IPv6 от провайдера. Используется протокол 6in4 (SIT tunnel) через любого tunnel broker'а (Hurricane Electric, ip4market, 6in4.ru и др.).

Состоит из shell-скрипта для управления туннелем и статического Go-бинарника (ARM64) с REST API и встроенным веб-интерфейсом.

## Constraints

- UCG-Fiber, UniFi OS 5.x, ядро 5.4.213-ui-ipq9574 (ARM64/aarch64)
- Модуль ядра `sit.ko` доступен (`CONFIG_IPV6_SIT=m`)
- Публичный статический WAN IPv4 (`78.36.x.x` на `ppp0`, PPPoE)
- WAN-интерфейс: `ppp0` (не ethX)
- Policy-based routing с VPN-таблицами — 6in4 должен идти напрямую через `ppp0`, мимо VPN
- ISP не блокирует IP protocol 41
- Persistent storage: `/data/` (переживает перезагрузки, возможно firmware updates)
- `/etc/rc.local` работает при перезагрузке
- Go-сервер слушает на `0.0.0.0:8686`, доступен по LAN без nginx

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                    UCG-Fiber                         │
│                                                     │
│  /data/ipv6-tunnel/                                 │
│  ├── config.json          ← конфиг туннеля          │
│  ├── tunnel.sh            ← up/down/status туннеля  │
│  ├── ipv6-tunnel-server   ← Go бинарник (ARM64)     │
│  └── web/                 ← статика SPA             │
│      └── index.html                                 │
│                                                     │
│  /etc/rc.local                                      │
│  └── /data/ipv6-tunnel/tunnel.sh boot               │
│                                                     │
│  Сеть:                                              │
│  WAN (ethX) ──── sit-6in4 (6in4) ──── broker        │
│                    │                                 │
│                 br0 (LAN)                            │
│                 ::1/64 + SLAAC                       │
└─────────────────────────────────────────────────────┘
```

### Components

1. **`tunnel.sh`** — shell-скрипт, единственная точка управления туннелем. Команды: `boot`, `up`, `down`, `restart`, `status`. Вызывает `ip tunnel`, `ip addr`, `ip route`, `sysctl`, `nftables`. Определяет WAN IPv4 автоматически.

2. **`ipv6-tunnel-server`** — статический Go-бинарник (~5-8 MB), кросс-компилированный `CGO_ENABLED=0 GOOS=linux GOARCH=arm64`. HTTP-сервер на `0.0.0.0:8686` (доступен по LAN: `http://<router-ip>:8686/`). Отдаёт SPA (через `embed`), предоставляет REST API, вызывает `tunnel.sh` через `exec.Command`.

3. **`config.json`** — единый конфиг в `/data/ipv6-tunnel/`.

4. **`/etc/rc.local`** — одна строка вызова `tunnel.sh boot`. Вся логика в `/data/`, в rc.local минимум. Фрагмент хранится в `/data/ipv6-tunnel/rc-local-fragment.sh` для восстановления после firmware update.

## tunnel.sh — Network Logic

### `tunnel.sh up`

```bash
# 1. Загрузить модуль ядра
modprobe sit

# 2. Определить WAN IPv4 с ppp0
WAN_IP=$(ip -4 addr show ppp0 | grep -oP 'inet \K[0-9.]+')

# 3. Явный маршрут до broker endpoint через ppp0 (мимо VPN PBR)
ip route add <remote_endpoint>/32 dev ppp0 src $WAN_IP

# 4. Создать 6in4 туннель
ip tunnel add sit-6in4 mode sit \
    remote <remote_endpoint> \
    local $WAN_IP \
    ttl <ttl>

# 5. Установить MTU
ip link set sit-6in4 mtu <mtu>

# 6. Поднять интерфейс
ip link set sit-6in4 up

# 7. Назначить IPv6 на туннельный интерфейс
ip -6 addr add <local_ipv6> dev sit-6in4

# 8. Дефолтный IPv6 маршрут через туннель
ip -6 route add ::/0 dev sit-6in4

# 9. Включить IPv6 forwarding
sysctl -w net.ipv6.conf.all.forwarding=1

# 10. Для каждой LAN-сети из config.networks[]:
#     ip -6 addr add <prefix>::1/64 dev <interface>
#     Пример:
#     ip -6 addr add 2001:470:xxxx:1::1/64 dev br0
#     ip -6 addr add 2001:470:xxxx:8::1/64 dev br8

# 11. Запустить RA (Router Advertisement) для каждого LAN-интерфейса
#     Go-сервер рассылает RA через mdlayher/ndp

# 12. Firewall (nftables):
#     - Разрешить protocol 41 (SIT) inbound на ppp0
#     - Разрешить IPv6 forwarding sit-6in4 <-> br0,br8,...
#     - Блокировать входящие IPv6 из интернета на LAN (stateful)
```

### `tunnel.sh down`

Зеркально: удалить firewall-правила, адреса, маршруты, туннель.

### `tunnel.sh status`

Вывод JSON:
```json
{
  "tunnel_up": true,
  "interface": "sit-6in4",
  "local_ipv6": "2001:470:...",
  "wan_ipv4": "85.x.x.x",
  "lan_enabled": true,
  "lan_prefix": "2001:470:.../64",
  "ping_ok": true,
  "ping_ms": 42
}
```

### `tunnel.sh boot`

```bash
tunnel.sh up
/data/ipv6-tunnel/ipv6-tunnel-server &
```

### SLAAC

При `forwarding=1` ядро Linux само по себе **не рассылает** Router Advertisements. Для раздачи IPv6 на LAN через SLAAC потребуется `radvd` или аналог. `tunnel.sh up` должен запускать `radvd` с минимальным конфигом (интерфейс br0, prefix, DNS). Если `radvd` нет в системе — проверить наличие на роутере, при необходимости использовать статический бинарник в `/data/ipv6-tunnel/`. Альтернатива — Go-сервер может сам рассылать RA через raw socket (библиотека `ndp` для Go).

## REST API

Go-сервер слушает `0.0.0.0:8686`. Доступ: `http://<router-ip>:8686/`. Аутентификация — bearer token из `config.json`.

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/status` | Статус туннеля (up/down, uptime, адреса, ping) |
| `GET` | `/api/config` | Текущий конфиг |
| `PUT` | `/api/config` | Обновить конфиг + restart туннеля |
| `POST` | `/api/tunnel/up` | Поднять туннель |
| `POST` | `/api/tunnel/down` | Опустить туннель |
| `POST` | `/api/tunnel/restart` | Перезапустить туннель |
| `GET` | `/api/health` | Health check (ping IPv6 target) |

## Configuration

```json
{
  "tunnel": {
    "broker": "he",
    "remote_endpoint": "216.66.88.98",
    "local_ipv6": "2001:470:..::2/64",
    "remote_ipv6": "2001:470:..::1/64",
    "ttl": 255,
    "mtu": 1480
  },
  "lan": {
    "enabled": true,
    "dns": ["2606:4700:4700::1111", "2001:4860:4860::8888"],
    "mode": "slaac",
    "networks": [
      { "interface": "br0",  "prefix": "2001:470:xxxx:1::/64",  "comment": "Default" },
      { "interface": "br8",  "prefix": "2001:470:xxxx:8::/64",  "comment": "IOT" },
      { "interface": "br9",  "prefix": "2001:470:xxxx:9::/64",  "comment": "Proxmox main" },
      { "interface": "br10", "prefix": "2001:470:xxxx:10::/64", "comment": "Proxmox second" },
      { "interface": "br90", "prefix": "2001:470:xxxx:90::/64", "comment": "Cameras" },
      { "interface": "br100","prefix": "2001:470:xxxx:100::/64","comment": "Test network" }
    ]
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
    "auth_token": "your-secret-token"
  }
}
```

### Config fields

**tunnel:**
- `broker` — провайдер туннеля (he / ip4market / 6in4ru / custom). Информационное поле.
- `remote_endpoint` — IPv4 адрес сервера broker'а
- `local_ipv6` — IPv6 адрес локального конца туннеля (выдаёт broker)
- `remote_ipv6` — IPv6 адрес удалённого конца (выдаёт broker)
- `ttl` — TTL для 6in4 пакетов (default: 255)
- `mtu` — MTU туннельного интерфейса (default: 1480)

**lan:**
- `enabled` — раздавать ли IPv6 на LAN
- `dns` — IPv6 DNS серверы для клиентов
- `mode` — slaac (default) или dhcpv6
- `networks[]` — массив сетей, каждая:
  - `interface` — bridge-интерфейс (br0, br8, br9, br10, br90, br100)
  - `prefix` — /64 подсеть из роутируемого /48 блока broker'а
  - `comment` — название сети (для UI)

**health:**
- `enabled` — включить мониторинг
- `interval_sec` — интервал проверки
- `target` — IPv6 адрес для ping
- `auto_restart` — перезапускать туннель при падении

**server:**
- `port` — порт HTTP-сервера
- `wan_interface` — WAN-интерфейс для определения IPv4 ("auto" или конкретное имя)
- `auth_token` — токен для аутентификации API запросов

## Web UI

Single-page application, встроенный в Go-бинарник через `go:embed`. Vanilla HTML + CSS + JS, без фреймворков.

**Возможности:**
- Статус туннеля в реальном времени (обновление каждые 5 сек)
- Кнопки Start / Stop / Restart
- Форма настроек (все поля config.json)
- Тёмная тема, стилистически приближенная к UniFi UI

**Доступ:** `http://<router-ip>:8686/` напрямую (без nginx proxy).

## Firewall Rules (nftables)

При `tunnel.sh up`:
- Разрешить inbound protocol 41 (SIT/IPv6-in-IPv4) на WAN-интерфейсе
- Разрешить IPv6 forwarding между `sit-6in4` и `br0`
- Блокировать входящие IPv6-подключения из интернета к устройствам LAN (stateful: только established/related пропускать)

При `tunnel.sh down`:
- Удалить все добавленные правила

## Project Structure

```
unifi-tunnel-4to6/
├── cmd/
│   └── server/
│       └── main.go           ← entry point
├── internal/
│   ├── api/
│   │   ├── handler.go        ← HTTP handlers
│   │   └── server.go         ← HTTP server, middleware, auth
│   ├── config/
│   │   └── config.go         ← read/write config.json
│   └── tunnel/
│       └── manager.go        ← exec tunnel.sh, parse status
├── web/
│   └── index.html            ← SPA (embedded)
├── scripts/
│   ├── tunnel.sh             ← tunnel management
│   ├── install.sh            ← installer for router
│   └── rc-local-fragment.sh  ← fragment for /etc/rc.local
├── Makefile                  ← build + deploy
├── go.mod
└── go.sum
```

## Build & Deploy

**Build (on Mac):**
```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
    -ldflags="-s -w" \
    -o bin/ipv6-tunnel-server ./cmd/server/
```

**Install (on router):**
`install.sh` выполняется на роутере через SSH:
1. Создаёт `/data/ipv6-tunnel/`, копирует файлы
2. Генерирует дефолтный `config.json` (если нет)
3. Дописывает блок в `/etc/rc.local` (с проверкой дубликатов)
4. Запускает Go-сервер

**Update:**
`make deploy` — пересобрать, скопировать бинарник, перезапустить. Конфиг не трогает.

## Persistence Strategy

- Вся логика и данные в `/data/ipv6-tunnel/` — переживает перезагрузки
- В `/etc/rc.local` — только одна строка вызова
- Если `/etc/rc.local` слетит после firmware update — восстановление: `cat /data/ipv6-tunnel/rc-local-fragment.sh >> /etc/rc.local`
- `rc-local-fragment.sh` хранится в persistent partition для быстрого восстановления
