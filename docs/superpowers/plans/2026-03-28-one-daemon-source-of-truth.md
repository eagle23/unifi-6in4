# One Daemon = One Source of Truth

## Summary

Собрать систему вокруг одного долгоживущего daemon-процесса, где:

- `config.json` хранит только desired state.
- daemon сам вычисляет observed state, применяет diff и публикует `state.json` и `/api/status`.
- HTTP API, web UI, health, RA и сетевое применение живут в одном процессе.
- shell остаётся только как thin compatibility/rescue слой на время миграции.

Принятые решения:

- миграция в `2-phase`;
- backend `mixed`: Go управляет orchestration/RA/status, а сетевые изменения применяются через `ip`, `nft`, `modprobe`, `ping` из daemon;
- UI и API остаются в том же daemon;
- допускается redesign модели, но в phase 1 сохраняем текущие REST entrypoints там, где это не ломает идею source of truth.

## Key Changes

### 1. New runtime model

Ввести новый orchestrator package, который владеет:

- config store;
- reconcile loop;
- RA manager;
- health worker;
- HTTP server;
- local control socket.

Разделить данные на:

- `config.json`: desired state, редактируется пользователем/API;
- `state.json`: observed state, `last_error`, `last_reconcile_at`, `health`, `wan_ipv4`, `tunnel_up`, `degraded_reasons`; пишется только daemon.

Добавить в конфиг `tunnel.enabled`.

Для старых конфигов без этого поля:

- если `remote_endpoint` и `local_ipv6` заполнены, мигрировать как `enabled=true`;
- иначе `enabled=false`.

Валидация конфига должна быть строгой до сохранения:

- если `tunnel.enabled=false`, разрешать неполный tunnel config;
- если `tunnel.enabled=true`, требовать валидные tunnel/LAN/server поля;
- невалидный `PUT /api/config` возвращает `400` и не меняет last-good desired state.

Конфиг сохранять атомарно: temp file + rename.

### 2. Reconcile instead of `up/down` shell logic

Убрать бизнес-логику из `scripts/tunnel.sh`; shell больше не вызывает `ip`/`nft` напрямую.

Daemon должен иметь явный reconcile pipeline:

- прочитать current WAN IPv4;
- если `enabled=false` или конфиг невалиден: гарантированно остановить RA, удалить LAN IPv6 prefixes, tunnel, default route и managed firewall rules, но HTTP/UI оставить живыми;
- если `enabled=true`: обеспечить `sit` module, broker route bypass, tunnel interface, MTU, IPv6 addr, default route, forwarding, LAN prefixes, firewall rules и RA;
- health failure не делает restart process, а запускает `force reconcile` dataplane-состояния.

Применение должно быть идемпотентным: повторный reconcile на уже правильной системе не должен ломать состояние.

RA manager должен жить под управлением orchestrator:

- `start/update/stop` по diff;
- не оставлять старые goroutine/advertiser при config change или tunnel disable.

### 3. New control-plane interface

Оставить текущие HTTP routes как public compatibility API:

- `GET /api/status`
- `GET /api/config`
- `PUT /api/config`
- `POST /api/tunnel/up`
- `POST /api/tunnel/down`
- `POST /api/tunnel/restart`
- `GET /api/health`

Переопределить семантику:

- `up` = поставить `tunnel.enabled=true`, сохранить config, запустить reconcile;
- `down` = поставить `tunnel.enabled=false`, сохранить config, запустить reconcile;
- `restart` = не менять desired state, а сделать `force reconcile` с пересозданием tunnel dataplane;
- `health` = выполнить fresh probe и обновить cached health в `state.json`.

`GET /api/status` в phase 1 должен сохранить текущие ключи:

- `tunnel_up`
- `wan_ipv4`
- `local_ipv6`
- `networks`
- `ping_ok`
- `ping_ms`

И добавить новые:

- `config_valid`
- `desired_enabled`
- `reconcile_state`
- `last_error`
- `degraded_reasons`

`server.auth_token` должен hot-reload'иться из config store.

`server.port` сделать startup-only в новой модели: оставить в bootstrap config/CLI, убрать из web UI редактирование, чтобы не городить self-reexec/listener handoff в phase 1.

### 4. CLI and shell compatibility

Бинарник должен поддерживать два режима:

- `serve` — основной daemon;
- `ctl up|down|restart|status|health` — локальный control client.

`ctl` должен говорить с работающим daemon через Unix socket в `/data/ipv6-tunnel/daemon.sock`.

`scripts/tunnel.sh` в phase 1 превратить в thin wrapper над `ipv6-tunnel-server ctl ...`.

`boot` в `rc.local` больше не поднимает tunnel сам; он только гарантирует запуск daemon.

`cmd/server/main.go` должен стартовать daemon/UI всегда, даже если config невалиден или tunnel disabled.

## Implementation Phases

### Phase 1

- Ввести orchestrator, state store и local control socket.
- Перенести lifecycle RA/health/tunnel/firewall под reconcile loop.
- Оставить текущий HTTP/API/UI shape, но сменить внутреннюю семантику на desired state.
- Переделать shell в wrapper.
- Починить embed/static serving без generated `cmd/server/web`.

### Phase 2

- Почистить legacy code paths `internal/tunnel.Manager` и прямые shell-dependent lifecycle места.
- При желании ввести `config_version: 2` и упростить config layout вокруг `desired_state`, но только после того, как phase 1 стабильно работает и migration покрыта тестами.
- После стабилизации можно удалить compatibility-ветки и оставить shell только как rescue/install glue.

## Test Plan

### Unit

- migration old config -> new config with derived `tunnel.enabled`;
- validation rules for enabled/disabled tunnel;
- reconcile planner for `enable`, `disable`, `force reconcile`, invalid config, WAN IP change;
- RA manager start/update/stop diff behavior;
- auth token hot reload from store.

### API

- `PUT /api/config` rejects invalid enabled config;
- `POST /api/tunnel/up|down` mutates desired state and returns updated status;
- `GET /api/health` exists and returns fresh probe result;
- `GET /api/status` keeps old keys and includes new degraded metadata.

### Integration with fake backend

- boot with blank config starts HTTP and keeps dataplane down;
- valid config + enabled brings tunnel to ready state;
- disable removes RA/routes/firewall/tunnel but daemon stays healthy;
- health failure triggers force reconcile, not process restart.

### Manual acceptance on UCG

- после reboot UI доступен даже с пустым config;
- включение через UI поднимает tunnel;
- выключение через UI реально гасит RA и dataplane;
- смена auth token работает без restart;
- `scripts/tunnel.sh status` продолжает работать как compatibility wrapper.

## Assumptions

- Этот репо остаётся on-router решением; внешний controller не вводим.
- Для phase 1 сетевое применение делаем через subprocess CLI, а не pure netlink rewrite.
- Порт API/UI фиксируем как bootstrap concern; редактирование порта из web UI исключаем из первой версии.
- Desired state хранится только в config; любые observed/runtime ошибки и health не пишутся обратно в config.
