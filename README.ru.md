# IPv6 Tunnel Manager для UCG Fiber

[English version](README.md)

`unifi-tunnel-4to6` — это небольшой on-router daemon для поднятия IPv6 `6in4` туннеля на UniFi-железе вроде UCG Fiber.

Он рассчитан на кейс, когда:

- провайдер даёт только IPv4 на WAN;
- в ядре роутера есть `sit`;
- хочется получить настоящий routed IPv6 в LAN;
- не хочется вручную держать в голове `ip`, `ip6tables`, RA, health-check и boot-скрипты.

## Что делает проект

Daemon управляет всем жизненным циклом `6in4` туннеля:

- создаёт и удаляет `sit-6in4`;
- назначает IPv6-адреса и default route;
- добавляет host-route до IPv4 endpoint брокера через WAN;
- включает IPv6 forwarding;
- назначает gateway-адреса на LAN bridge-интерфейсы вроде `br0`;
- шлёт Router Advertisements для SLAAC;
- выполняет health-check по IPv6;
- поднимает web UI и HTTP API;
- хранит desired state в `config.json`, а observed state в `state.json`;
- переживает типовые boot-race, например поздний PPPoE/WAN.

Сейчас модель такая:

- один daemon = один source of truth;
- `config.json` = desired state;
- `state.json` = observed state;
- shell-скрипты — это compatibility wrapper, а не runtime brain.

## Как это работает

### Что лежит на роутере

После установки используется каталог `/data/ipv6-tunnel`:

- `/data/ipv6-tunnel/ipv6-tunnel-server` — бинарник daemon
- `/data/ipv6-tunnel/config.json` — желаемая конфигурация
- `/data/ipv6-tunnel/state.json` — наблюдаемое состояние
- `/data/ipv6-tunnel/daemon.sock` — локальный control socket
- `/data/ipv6-tunnel/tunnel.sh` — совместимый shell wrapper

### Control plane

Daemon стартует в режиме `serve` и делает три вещи:

1. загружает `config.json`;
2. запускает reconcile loop против реального состояния системы;
3. поднимает UI, API и локальный control socket.

Он ещё и сам ретраит reconcile, если boot-порядок оказался неидеальным. Например:

- `ppp0` ещё не поднялся;
- у `br0` после ребута link-local IPv6 остался в плохом DAD-состоянии.

### Data plane

Если конфиг валиден и туннель включён, daemon обеспечивает:

- существование `sit-6in4`;
- правильный MTU на туннеле;
- назначение client tunnel IPv6;
- default IPv6 route через `sit-6in4`;
- `prefix::1/64` на LAN bridge-интерфейсе;
- Router Advertisement для SLAAC;
- TCP MSS clamp для IPv6 TCP.

### Поведение MTU

У `tunnel.mtu` два режима:

- `0` = auto
- `> 0` = ручной override

В auto-режиме MTU считается от WAN-интерфейса:

- effective tunnel MTU = `WAN_MTU - 20`

Пример:

- PPPoE WAN MTU `1492` -> tunnel MTU `1472`

Фактический MTU публикуется в `/api/status` и показывается в UI.

## Требования

### Хост сборки

- macOS или Linux
- установленный Go
- `ssh` и `scp`
- доступ к роутеру по `root`

### Роутер

- UniFi-роутер с поддержкой `sit`
- writable `/data`
- наличие `ip`, `ip6tables`, `iptables`, `sysctl`, `ping`

## Сборка

```bash
make build
```

Результат:

```text
bin/ipv6-tunnel-server
```

Тесты:

```bash
make test
```

## Установка на роутер

Первичная установка:

```bash
make install ROUTER_HOST=192.168.1.1 ROUTER_USER=root
```

Что делает `install`:

- копирует daemon и скрипты в `/data/ipv6-tunnel`;
- создаёт дефолтный `config.json`, если его ещё нет;
- добавляет boot entry в `/etc/rc.local`;
- запускает daemon.

Дефолтный порт UI:

```text
9086
```

После установки открывай:

```text
http://<router-ip>:9086/
```

## Обновление уже установленной системы

Обычный upgrade:

```bash
make deploy ROUTER_HOST=192.168.1.1 ROUTER_USER=root
```

`deploy` не трогает существующий `config.json`.

Он:

- заливает новые файлы через `/tmp/*.new`;
- заменяет бинарь и wrapper;
- перезапускает daemon через `tunnel.sh boot`.

## Быстрый старт с Hurricane Electric

Для сборки и установки данные HE не нужны.

Они нужны только в тот момент, когда ты реально хочешь включить туннель.

### Как маппить поля из HE

На странице туннеля HE значения маппятся так:

- `Server IPv4 Address` -> `tunnel.remote_endpoint`
- `Server IPv6 Address` -> `tunnel.remote_ipv6`
- `Client IPv6 Address` -> `tunnel.local_ipv6`
- `Routed /64` -> `lan.networks[].prefix`

Твой публичный WAN IPv4 руками никуда вводить не надо. Daemon читает его сам с WAN-интерфейса.

### Минимальный пример конфига

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
    "dns": ["2606:4700:4700::1111", "2001:4860:4860::8888"],
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

Дальше можно:

- сохранить через UI и нажать `Start`;
- или включить из shell:

```bash
ssh root@192.168.1.1 '/data/ipv6-tunnel/ipv6-tunnel-server ctl up'
```

## CLI

Локальный CLI:

```bash
/data/ipv6-tunnel/ipv6-tunnel-server ctl status
/data/ipv6-tunnel/ipv6-tunnel-server ctl health
/data/ipv6-tunnel/ipv6-tunnel-server ctl up
/data/ipv6-tunnel/ipv6-tunnel-server ctl down
/data/ipv6-tunnel/ipv6-tunnel-server ctl restart
```

Совместимый shell wrapper:

```bash
/data/ipv6-tunnel/tunnel.sh status
/data/ipv6-tunnel/tunnel.sh up
/data/ipv6-tunnel/tunnel.sh down
/data/ipv6-tunnel/tunnel.sh restart
/data/ipv6-tunnel/tunnel.sh boot
```

## HTTP API

Публичные HTTP-маршруты:

- `GET /api/status`
- `GET /api/config`
- `PUT /api/config`
- `GET /api/health`
- `POST /api/tunnel/up`
- `POST /api/tunnel/down`
- `POST /api/tunnel/restart`

Если задан `server.auth_token`, используй:

```text
Authorization: Bearer <token>
```

## Важные особенности

### UI и dataplane разделены

UI должен быть доступен даже если:

- конфиг неполный;
- туннель не поднялся;
- boot-порядок был плохим.

Это специально. Сломанный туннель не должен убивать единственную recovery surface.

### Смена порта — startup-only

`server.port` читается при старте процесса.

Если меняешь его, перезапусти daemon.

### LAN prefixes

Если брокер дал только один routed `/64`, обычно его надо раздавать только в одном L2-сегменте.

Если нужен чистый routed IPv6 сразу в нескольких VLAN, понадобится:

- либо более крупный delegated prefix;
- либо несколько брокеров и policy-схема;
- либо другой подход вроде ULA + NPTv6/NAT66.

## Как проверить, что всё работает

### На роутере

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

Признаки, что всё ок:

- `reconcile_state` = `ready`
- `sit-6in4` существует и поднят
- на `br0` есть `prefix::1/64`
- default IPv6 route указывает на `sit-6in4`
- health probe успешен

### На клиенте macOS

```bash
route -n get -inet6 default
ifconfig en0 | grep inet6
ping6 -c 3 2001:4860:4860::8888
curl -6 https://ifconfig.me
```

Признаки, что всё ок:

- у клиента есть глобальный IPv6 из routed prefix
- default IPv6 route ведёт на link-local адрес роутера
- `ping6` работает
- `curl -6` работает

## Troubleshooting

### Посмотреть observed state

```bash
cat /data/ipv6-tunnel/state.json
```

### Посмотреть текущий конфиг

```bash
cat /data/ipv6-tunnel/config.json
```

### Посмотреть tunnel interface

```bash
ip link show dev sit-6in4
ip -6 addr show dev sit-6in4
```

### Посмотреть состояние LAN bridge

```bash
ip -6 addr show dev br0
```

### Смотреть Router Advertisements

```bash
tcpdump -ni br0 'icmp6 && ip6[40] == 134'
```

### Туннель поднят, но клиенты не получают IPv6

Проверь:

- `reconcile_state` в `/api/status`
- `last_error`
- `ip -6 addr show dev br0`

Daemon теперь сам умеет переживать два типовых reboot-failure:

- WAN ещё не готов во время первого reconcile;
- link-local IPv6 на bridge застрял в `dadfailed` или `tentative`.

### UI не открывается

Проверь текущий порт:

```bash
python3 -c 'import json; print(json.load(open("/data/ipv6-tunnel/config.json"))["server"]["port"])'
```

Проверь listener и процесс:

```bash
ss -ltnp | grep 9086 || true
pgrep -af ipv6-tunnel-server
```

## Структура репозитория

- `cmd/server` — entry point daemon
- `internal/control` — reconcile loop, state store, boot recovery logic
- `internal/api` — HTTP и local control API
- `internal/ra` — логика Router Advertisement
- `internal/config` — загрузка конфига, migration, defaults, validation
- `internal/tunnel` — модель статуса и локальный client
- `scripts/install.sh` — первичная установка
- `scripts/tunnel.sh` — compatibility wrapper
- `web` — embedded web UI
- `docs/superpowers` — design notes и планы реализации

## Короткие заметки

- UI встроен в бинарь.
- Дефолтный порт — `9086`.
- `tunnel.mtu = 0` означает auto.
- `server.wan_interface` по умолчанию `ppp0`.
- Health checks по умолчанию идут на `2001:4860:4860::8888`.

## Связанные документы

- [План перехода на one-daemon архитектуру](docs/superpowers/plans/2026-03-28-one-daemon-source-of-truth.md)
- [Инструкция по сборке, деплою и настройке HE](docs/superpowers/plans/2026-03-28-build-deploy-and-he-setup.md)
- [Изначальная design-spec](docs/superpowers/specs/2026-03-27-ipv6-tunnel-design.md)
