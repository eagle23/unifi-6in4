# Настройка 6in4 на VPS

Эта инструкция обезличена. Подставь свои значения вместо плейсхолдеров:

- `<VPS_IPV4>`: IPv4 VPS
- `<HOME_PUBLIC_IPV4>`: домашний белый IPv4
- `<ROUTED_IPV6_PREFIX>`: выданный серверу routed IPv6 prefix, например `/48`
- `<VPS_IPV6_GATEWAY>`: IPv6 gateway VPS
- `<VPS_WAN_INTERFACE>`: внешний интерфейс VPS, например `eth0` или `ens3`
- `<TUNNEL_VPS_IPV6>`: IPv6 адрес VPS внутри 6in4 tunnel-link
- `<TUNNEL_HOME_IPV6>`: IPv6 адрес домашнего роутера внутри 6in4 tunnel-link
- `<HOME_ROUTED_PREFIX>`: routed prefix, который уйдёт через туннель домой, например `/56`
- `<HOME_LAN_PREFIX>`: LAN prefix дома, обычно `/64`
- `<HOME_LAN_GATEWAY>`: адрес роутера в домашнем LAN, обычно первый адрес из `<HOME_LAN_PREFIX>`

Нормальная схема такая:

- tunnel-link держать на ULA или отдельном техническом IPv6;
- глобальный routed prefix прокидывать домой через маршрут на tunnel interface;
- на VPS включить `proxy_ndp` и `ndppd`, иначе аплинк не сможет вернуть трафик на адреса из домашнего prefix.

## Итоговая схема

### Tunnel link

- VPS tunnel IPv6: `<TUNNEL_VPS_IPV6>/64`
- Home tunnel IPv6: `<TUNNEL_HOME_IPV6>/64`

### Prefix за домашним роутером

- routed block: `<HOME_ROUTED_PREFIX>`
- первый LAN можно отдать как `<HOME_LAN_PREFIX>`

Если дома только один сегмент, обычно достаточно одного `/64`, но на VPS удобно держать маршрут на родительский prefix, например `/56`.

## 1. Проверить вводные на VPS

Убедись, что:

- у VPS реально есть `<VPS_IPV4>`;
- у VPS есть рабочий IPv6 наружу;
- провайдер не режет `IP protocol 41`;
- `<ROUTED_IPV6_PREFIX>` реально routed на этот сервер;
- внешний интерфейс VPS это `<VPS_WAN_INTERFACE>`.

Проверка:

```bash
ip -4 addr show dev <VPS_WAN_INTERFACE>
ip -6 route show
ping -6 -n -c 3 2001:4860:4860::8888
```

## 2. Включить IPv6 forwarding и NDP proxy

Создай файл:

```bash
cat >/etc/sysctl.d/60-6in4.conf <<'EOF'
net.ipv6.conf.all.forwarding = 1
net.ipv6.conf.default.forwarding = 1
net.ipv6.conf.<VPS_WAN_INTERFACE>.proxy_ndp = 1
EOF
```

Применить:

```bash
sysctl --system
```

Подставь свой uplink-интерфейс вместо `<VPS_WAN_INTERFACE>`.

## 3. Поднять 6in4 через systemd

Создай unit:

```bash
cat >/etc/systemd/system/sit-home.service <<'EOF'
[Unit]
Description=6in4 tunnel to home
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStartPre=-/sbin/ip tunnel del sit-home
ExecStart=/sbin/ip tunnel add sit-home mode sit local <VPS_IPV4> remote <HOME_PUBLIC_IPV4> ttl 255
ExecStart=/sbin/ip link set dev sit-home mtu 1480 up
ExecStart=/sbin/ip -6 addr add <TUNNEL_VPS_IPV6>/64 dev sit-home
ExecStart=/sbin/ip -6 route replace <HOME_ROUTED_PREFIX> dev sit-home
ExecStop=-/sbin/ip tunnel del sit-home

[Install]
WantedBy=multi-user.target
EOF
```

Запуск:

```bash
systemctl daemon-reload
systemctl enable --now sit-home.service
systemctl status sit-home.service --no-pager -l
```

Проверка:

```bash
ip tunnel show
ip -6 addr show dev sit-home
ip -6 route show
```

Ожидаемо:

- интерфейс `sit-home` в `UP`;
- адрес `<TUNNEL_VPS_IPV6>/64` на `sit-home`;
- маршрут `<HOME_ROUTED_PREFIX> dev sit-home`.

## 4. Разрешить protocol 41 и IPv6 forward

Если firewall не пустой, добавь минимум это.

### Вариант с iptables/ip6tables

```bash
iptables -I INPUT -i <VPS_WAN_INTERFACE> -p 41 -s <HOME_PUBLIC_IPV4> -d <VPS_IPV4> -j ACCEPT

ip6tables -I FORWARD -o sit-home -j ACCEPT
ip6tables -I FORWARD -i sit-home -m state --state ESTABLISHED,RELATED -j ACCEPT
```

Если у тебя nftables, смысл тот же:

- разрешить `protocol 41` на вход с `<HOME_PUBLIC_IPV4>`;
- разрешить IPv6 forwarding через `sit-home`.

## 5. Настроить NDP proxy через ndppd

Это критично.

Без этого трафик на адреса из `<HOME_ROUTED_PREFIX>` не будет нормально возвращаться, потому что аплинк VPS будет считать этот prefix on-link и пытаться искать соседа через NDP на `<VPS_WAN_INTERFACE>`.

Установка:

```bash
apt update
apt install -y ndppd
```

Конфиг:

```bash
cat >/etc/ndppd.conf <<'EOF'
route-ttl 30000

proxy <VPS_WAN_INTERFACE> {
  router yes
  timeout 500
  ttl 30000

  rule <HOME_ROUTED_PREFIX> {
    static
  }
}
EOF
```

Запуск:

```bash
systemctl enable --now ndppd
systemctl restart ndppd
systemctl status ndppd --no-pager -l
```

Почему `static`, а не `auto`:

- за VPS у тебя не L2-сегмент с хостами, а роутер за 6in4;
- `auto` здесь хуже, потому что daemon начнёт пытаться искать соседей там, где у тебя обычная routed-схема;
- для routed prefix через туннель здесь нужен именно `static`.

## 6. Проверка с VPS

### Проверка нативного IPv6 VPS

```bash
ip -6 route get 2001:4860:4860::8888
ping -6 -n -c 4 2001:4860:4860::8888
```

### Проверка самого 6in4 tunnel-link

Это заработает, когда поднимешь домашнюю сторону:

```bash
ping -6 -n -c 4 <TUNNEL_HOME_IPV6>
```

### Проверка routed prefix через VPS

Временно назначь тестовый адрес из домашнего блока на loopback:

```bash
ip -6 addr add <HOME_LAN_GATEWAY>/128 dev lo
```

Проверка:

```bash
ping -6 -n -c 4 -I <HOME_LAN_GATEWAY> 2001:4860:4860::8888
```

Потом убрать:

```bash
ip -6 addr del <HOME_LAN_GATEWAY>/128 dev lo
```

Если этот пинг не идёт, почти всегда проблема в `ndppd` или `proxy_ndp` на `<VPS_WAN_INTERFACE>`.

## 7. Полезная диагностика на VPS

### Посмотреть приходит ли proto 41

```bash
tcpdump -ni <VPS_WAN_INTERFACE> 'ip proto 41 and host <HOME_PUBLIC_IPV4>'
```

### Посмотреть NDP/ICMPv6

```bash
tcpdump -ni <VPS_WAN_INTERFACE> icmp6
```

### Посмотреть статус туннеля

```bash
ip tunnel show
ip -6 addr show dev sit-home
ip -6 route show
journalctl -u sit-home.service -b --no-pager -n 50
journalctl -u ndppd -b --no-pager -n 50
```

## 8. Частые проблемы

### `add tunnel "sit0" failed: No buffer space available`

Обычно это значит, что до этого уже был создан tunnel, или в unit неаккуратно повторно вызывается `ip tunnel add`.

Лечится так:

```bash
ip tunnel del sit-home 2>/dev/null || true
systemctl restart sit-home.service
```

### Пинг с адреса из routed prefix не идёт наружу

Типичный симптом:

```bash
ping -6 -I <HOME_LAN_GATEWAY> 2001:4860:4860::8888
```

не отвечает.

Причина обычно одна из этих:

- не запущен `ndppd`;
- не включён `net.ipv6.conf.<VPS_WAN_INTERFACE>.proxy_ndp=1`;
- rule в `ndppd.conf` не совпадает с реальным prefix;
- провайдер VPS режет ICMPv6 или странно маршрутизирует prefix.

### Сам VPS пингует IPv6, а домашний prefix нет

Это обычно указывает именно на проблему возврата трафика к routed prefix, а не на проблему нативного IPv6 VPS.

### PPPoE дома и странные зависания сайтов

Если дома WAN на PPPoE, MTU на туннеле часто лучше опустить до `1472`.

На VPS при необходимости:

```bash
ip link set dev sit-home mtu 1472
```

И потом так же пропиши это в unit.

## 9. Что должно быть готово на сервере после настройки

Если серверная часть настроена правильно, то на VPS:

- работает обычный `ping -6` наружу;
- поднят `sit-home`;
- есть `<TUNNEL_VPS_IPV6>/64` на `sit-home`;
- есть маршрут `<HOME_ROUTED_PREFIX> dev sit-home`;
- включён `forwarding`;
- включён `proxy_ndp` на `<VPS_WAN_INTERFACE>`;
- запущен `ndppd`.

Это означает, что VPS уже готов принимать вторую сторону туннеля с домашнего адреса `<HOME_PUBLIC_IPV4>`.

## 10. Короткий чек-лист

```bash
sysctl net.ipv6.conf.all.forwarding
sysctl net.ipv6.conf.<VPS_WAN_INTERFACE>.proxy_ndp
systemctl status sit-home.service --no-pager -l
systemctl status ndppd --no-pager -l
ip tunnel show
ip -6 addr show dev sit-home
ip -6 route show
ping -6 -n -c 3 2001:4860:4860::8888
```

Если всё это ок, серверная часть сделана правильно.
