# AmneziaWG Prometheus Monitoring

## Структура

```
awg-exporter/           ← деплоится на VPN-сервер
  Dockerfile
  docker-compose.yml
  exporter/
    main.go
    go.mod

monitoring/             ← деплоится на мониторинг-VPS
  docker-compose.yml
  .env.example
  prometheus/
    prometheus.yml
  grafana/
    provisioning/...
    dashboards/awg.json
```

## Деплой: VPN-сервер

### 1. Firewall — открыть порт 9586 только для мониторинг-VPS

```bash
# ufw
ufw allow from <MONITORING_VPS_IP> to any port 9586

# или iptables
iptables -A INPUT -p tcp --dport 9586 -s <MONITORING_VPS_IP> -j ACCEPT
iptables -A INPUT -p tcp --dport 9586 -j DROP
```

### 2. Скопировать и запустить exporter

```bash
scp -r awg-exporter/ root@VPN_SERVER_IP:/opt/awg-exporter
ssh root@VPN_SERVER_IP
cd /opt/awg-exporter
docker compose up -d --build
```

### 3. Проверить метрики

```bash
curl http://localhost:9586/metrics | grep awg_
```

## Деплой: мониторинг-VPS

### 1. Подставить IP VPN-сервера в prometheus.yml

```bash
sed -i 's/VPN_SERVER_IP/<реальный_IP>/' prometheus/prometheus.yml
```

### 2. Создать .env

```bash
cp .env.example .env
# отредактировать пароль Grafana
nano .env
```

### 3. Запустить

```bash
cd /opt/monitoring
docker compose up -d
```

### 4. Открыть Grafana

```
http://<MONITORING_VPS_IP>:3000
login: admin
password: <из .env>
```

Дашборд "AmneziaWG Monitoring" будет в папке "AmneziaWG".

## Метрики

| Метрика | Тип | Описание |
|---------|-----|----------|
| `awg_peer_receive_bytes_total` | counter | Байт получено от пира |
| `awg_peer_transmit_bytes_total` | counter | Байт отправлено пиру |
| `awg_peer_last_handshake_seconds` | gauge | Unix timestamp последнего хендшейка |
| `awg_peer_connected` | gauge | 1 если хендшейк < 3 мин назад |
| `awg_peers_total` | gauge | Всего пиров на интерфейсе |
| `awg_peers_online` | gauge | Онлайн пиров на интерфейсе |
| `awg_scrape_errors_total` | counter | Ошибки сбора данных |

## Labels

- `interface` — имя WG интерфейса (`awg0`)
- `peer` — публичный ключ пира
- `allowed_ips` — IP пира (`10.8.1.X/32`)
- `endpoint` — реальный IP:порт пира (пустой если нет хендшейка)
