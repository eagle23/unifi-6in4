# Руководство по сборке, деплою и настройке HE

Этот документ соответствует текущему состоянию репозитория после миграции на модель `one daemon = one source of truth`.

Текущая runtime-модель:

- один долгоживущий daemon обслуживает HTTP UI и API;
- локальные управляющие команды идут через `/data/ipv6-tunnel/daemon.sock`;
- desired state хранится в `/data/ipv6-tunnel/config.json`;
- observed state хранится в `/data/ipv6-tunnel/state.json`;
- web UI встроен прямо в бинарь;
- `scripts/tunnel.sh` теперь только compatibility wrapper над `ipv6-tunnel-server ctl ...`.

## Пути и интерфейсы

Локальный артефакт сборки:

- `bin/ipv6-tunnel-server`

Пути на роутере:

- бинарь: `/data/ipv6-tunnel/ipv6-tunnel-server`
- конфиг: `/data/ipv6-tunnel/config.json`
- state: `/data/ipv6-tunnel/state.json`
- control socket: `/data/ipv6-tunnel/daemon.sock`
- shell wrapper: `/data/ipv6-tunnel/tunnel.sh`
- web UI: `http://<router-ip>:8686/`

## Что нужно заранее

На Mac:

- `go`
- `make`
- `ssh`
- `scp`

На роутере:

- SSH-доступ
- возможность писать в `/data/ipv6-tunnel`
- `root` или эквивалентные права

## На каком этапе нужны данные Hurricane Electric

Данные HE **не нужны** для:

- `make build`
- `make install`
- `make deploy`
- проверки, что daemon, UI и `ctl status` вообще работают

Данные HE нужны только перед первым реальным включением туннеля:

- кнопкой `Start` в UI
- командой `ipv6-tunnel-server ctl up`
- командой `tunnel.sh up`

Минимум, что тебе нужно из HE:

- `Server IPv4 Address` -> `tunnel.remote_endpoint`
- `Client IPv6 Address` -> `tunnel.local_ipv6`
- routed `/64` prefixes -> `lan.networks[].prefix`

Опционально, но лучше заполнить:

- `Server IPv6 Address` -> `tunnel.remote_ipv6`

Сейчас `remote_ipv6` не критичен для bring-up daemon'а, но он остаётся частью config shape, и лучше его заполнять для совместимости и будущей чистки.

## Сборка на macOS

Запускай из корня репозитория:

```bash
cd /Users/eagle23/Documents/vpn/unifi-tunnel-4to6
make clean
make test
make build
```

Что это делает:

- `make test` запускает `go test ./... -v`
- `make build` собирает Linux ARM64 бинарь в `bin/ipv6-tunnel-server`

Проверить, что бинарь появился:

```bash
ls -lh bin/ipv6-tunnel-server
file bin/ipv6-tunnel-server
```

Ручной эквивалент сборки:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/ipv6-tunnel-server ./cmd/server/
```

## Первый install на роутер

Используй `install`, если на роутере ещё нет нормальной раскладки `/data/ipv6-tunnel`:

```bash
make install ROUTER_HOST=192.168.1.1 ROUTER_USER=root
```

Что делает `make install`:

- собирает бинарь;
- загружает `install.sh`, `ipv6-tunnel-server`, `tunnel.sh` и `rc-local-fragment.sh` в `/tmp` на роутере;
- запускает `/tmp/install.sh`;
- создаёт `/data/ipv6-tunnel`;
- устанавливает бинарь и shell wrapper;
- создаёт дефолтный `config.json` с `tunnel.enabled=false`, если файла ещё нет;
- добавляет boot entry в `/etc/rc.local`, который вызывает `/data/ipv6-tunnel/tunnel.sh boot`;
- убивает старый daemon, если он был, и запускает новый.

Важно:

- для дефолтного install данные HE **не нужны**;
- дефолтный конфиг специально оставляет туннель выключенным;
- после install daemon и UI должны подниматься даже с пустым tunnel config.

## Обновление уже установленной системы

Используй `deploy`, если система уже установлена и нужно просто обновить бинарь, UI и wrapper:

```bash
make deploy ROUTER_HOST=192.168.1.1 ROUTER_USER=root
```

Что делает `make deploy`:

- собирает бинарь;
- загружает:
  - `bin/ipv6-tunnel-server`
  - `scripts/tunnel.sh`
- напрямую перезапускает daemon через `/data/ipv6-tunnel/ipv6-tunnel-server`

Важно:

- `make deploy` предполагает, что install layout уже существует;
- `make deploy` не пересоздаёт `/etc/rc.local`;
- `make deploy` не перезаписывает существующий `config.json`.

## Smoke check после install или deploy

Запусти:

```bash
ssh root@192.168.1.1 '
ls -l /data/ipv6-tunnel
ls -l /data/ipv6-tunnel/daemon.sock
cat /data/ipv6-tunnel/config.json
cat /data/ipv6-tunnel/state.json
/data/ipv6-tunnel/ipv6-tunnel-server ctl status
/data/ipv6-tunnel/ipv6-tunnel-server ctl health
'
```

Что считается нормальным результатом на свежем install:

- `daemon.sock` существует;
- `config.json` существует;
- `state.json` существует;
- `ctl status` возвращает JSON, а не падает;
- туннель остаётся выключенным, пока ты явно его не включишь;
- UI доступен по `http://<router-ip>:8686/`.

## Когда вносить данные Hurricane Electric

Только после того, как daemon уже работает и smoke test прошёл.

Нормальных путей два.

### Вариант 1: через UI

Открываешь:

```text
http://<router-ip>:8686/
```

Заполняешь поля так:

- `Remote Endpoint (IPv4)` -> HE `Server IPv4 Address`
- `Local IPv6` -> HE `Client IPv6 Address`
- `Remote IPv6` -> HE `Server IPv6 Address`
- `TTL` -> оставляешь `255`, если нет причины менять
- `MTU` -> оставляешь `1480`, если точно не знаешь, что нужен другой
- `WAN Interface` -> обычно `ppp0` на UniFi
- `Networks` -> твои routed LAN `/64`
- `DNS` -> нужные тебе IPv6 DNS

Сначала сохраняешь конфиг.

Потом включаешь туннель через `Start`.

### Вариант 2: правка `config.json` по SSH

Пример:

```bash
ssh root@192.168.1.1 'cat > /data/ipv6-tunnel/config.json' <<'EOF'
{
  "tunnel": {
    "enabled": false,
    "broker": "he",
    "remote_endpoint": "216.66.80.30",
    "local_ipv6": "2001:470:abcd:100::2/64",
    "remote_ipv6": "2001:470:abcd:100::1/64",
    "ttl": 255,
    "mtu": 1480
  },
  "lan": {
    "enabled": true,
    "dns": ["2606:4700:4700::1111", "2001:4860:4860::8888"],
    "mode": "slaac",
    "networks": [
      {
        "interface": "br0",
        "prefix": "2001:470:beef:1::/64",
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
    "port": 8686,
    "wan_interface": "ppp0",
    "auth_token": ""
  }
}
EOF
```

Это запишет конфиг, но туннель всё ещё останется выключенным.

Так у тебя будет ещё одна контрольная точка перед реальным применением dataplane.

## Как включить туннель

После того как данные HE уже внесены, включаешь туннель:

```bash
ssh root@192.168.1.1 '/data/ipv6-tunnel/ipv6-tunnel-server ctl up'
```

Compatibility wrapper:

```bash
ssh root@192.168.1.1 '/data/ipv6-tunnel/tunnel.sh up'
```

Через UI:

- открываешь UI и нажимаешь `Start`

Все три пути попадают в один и тот же daemon brain.

## Как проверить, что туннель реально поднялся

Запусти:

```bash
ssh root@192.168.1.1 '
/data/ipv6-tunnel/ipv6-tunnel-server ctl status
cat /data/ipv6-tunnel/state.json
ip tunnel show
ip -6 addr show dev sit-6in4
ip -6 route show
'
```

Что ты хочешь увидеть:

- `desired_enabled: true`
- `config_valid: true`
- `tunnel_up: true`
- `reconcile_state` больше не висит в ошибке
- существует `sit-6in4`
- на `sit-6in4` висит HE client IPv6
- есть дефолтный IPv6 route через `sit-6in4`

Если ты настроил LAN prefixes, отдельно проверь bridge:

```bash
ssh root@192.168.1.1 '
ip -6 addr show dev br0
'
```

Там должен появиться router-side IPv6, производный от твоего `/64`.

## Fresh health probe

Запусти:

```bash
ssh root@192.168.1.1 '
/data/ipv6-tunnel/ipv6-tunnel-server ctl health
'
```

Это просит daemon сделать fresh health probe и обновляет `state.json`.

## Как выключить или откатить dataplane

Если нужно остановить dataplane, но не сносить install:

```bash
ssh root@192.168.1.1 '/data/ipv6-tunnel/ipv6-tunnel-server ctl down'
```

Compatibility wrapper:

```bash
ssh root@192.168.1.1 '/data/ipv6-tunnel/tunnel.sh down'
```

UI и daemon при этом должны остаться живыми.

## Operational notes

- Пустой или неполный конфиг больше не должен мешать запуску daemon и UI.
- `server.port` в новой модели startup-only.
- Нормальный путь: оставить `server.port=8686`.
- Если ты руками меняешь `server.port` в `config.json`, для применения нужен restart daemon'а.
- `ctl` теперь зависит от `/data/ipv6-tunnel/daemon.sock`, значит daemon уже должен быть запущен.
- `make install` нужен для первой инициализации, `make deploy` для обычных обновлений.

## Рекомендуемая последовательность целиком

Используй такой порядок, если тебе не нужен какой-то специальный сценарий:

1. `make clean`
2. `make test`
3. `make build`
4. `make install ...` для первой установки или `make deploy ...` для обновлений
5. проверить `ctl status`, `ctl health` и `state.json`
6. проверить UI на `:8686`
7. внести данные HE
8. выполнить `ctl up`
9. проверить `state.json`, `ip tunnel show`, `ip -6 addr` и `ip -6 route`
10. протестировать клиентов в LAN
