```markdown
# radosgw_exporter

[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

> **EN** — [English version below](#radosgw_exporter-1)  
> **RU** — экспортер метрик Ceph RADOS Gateway для Prometheus

---

## 📌 Описание (RU)

`radosgw_exporter` — это экспортер [Prometheus](https://prometheus.io/), который собирает метрики использования, квот и статистики бакетов из **Ceph RADOS Gateway (RGW)** через Admin API.

Он позволяет мониторить:
- Операции по бакетам и категориям (`ops`, `successful_ops`, `bytes_sent`, `bytes_received`)
- Использование бакетов (`bucket_usage_bytes`, `bucket_usage_objects`)
- Квоты пользователей и бакетов
- Общее потребление по пользователям

Экспортер написан на **Go**, не требует зависимостей от `librados`, работает в **Kubernetes** и поддерживает **graceful shutdown**, **логирование**, **TLS** и **безопасную передачу секретов**.

---

## 🚀 Быстрый старт (RU)

### 1. Создайте админ-пользователя в Ceph

```bash
radosgw-admin user create \
  --uid=radosgw-exporter \
  --display-name="RADOSGW Exporter" \
  --caps="buckets=read;users=read;usage=read;metadata=read"
```

Сохраните `access_key` и `secret_key`.

### 2. Запустите экспортер

```bash
export RADOSGW_ENDPOINT="https://ceph-gw.example.com"
export ACCESS_KEY="..."
export SECRET_KEY="..."
export STORE="prod-cluster"
export METRICS_PORT=9242
export INSECURE_SKIP_VERIFY=false  # true только для тестов!

go run .
```

### 3. Проверьте метрики

```bash
curl http://localhost:9242/metrics | grep radosgw
```

---

## 📦 Docker

```bash
docker build -t radosgw-exporter .
docker run -p 9242:9242 \
  -e RADOSGW_ENDPOINT="https://ceph:443" \
  -e ACCESS_KEY="..." \
  -e SECRET_KEY="..." \
  radosgw-exporter
```

---

## 🛡️ Безопасность

- Никогда не храните `ACCESS_KEY` и `SECRET_KEY` в коде или ConfigMap.
- Используйте `Secret` в Kubernetes:
  ```yaml
  envFrom:
    - secretRef:
        name: radosgw-exporter-secret
  ```

---

## ⚙️ Переменные окружения

| Переменная | По умолчанию | Описание |
|-----------|--------------|--------|
| `RADOSGW_ENDPOINT` | — | URL RADOSGW (без `/admin`) |
| `ACCESS_KEY` | — | **Обязательно** |
| `SECRET_KEY` | — | **Обязательно** |
| `STORE` | `us-east-1` | Лейбл `store` в метриках |
| `METRICS_PORT` | `9242` | Порт для `/metrics` |
| `INSECURE_SKIP_VERIFY` | `false` | Игнорировать ошибки TLS (только для dev) |

---

## 📈 Метрики

- `radosgw_usage_ops_total`
- `radosgw_usage_sent_bytes_total`
- `radosgw_usage_bucket_bytes`
- `radosgw_usage_user_quota_size_bytes`
- `radosgw_up` — `1` если экспортер работает, `0` — если ошибка
- и другие (см. исходный код)

---

## 📜 Лицензия

MIT

---

<br><br>

---

# radosgw_exporter

[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

> **RU** — [Русская версия выше](#radosgw_exporter)  
> **EN** — Prometheus exporter for Ceph RADOS Gateway metrics

---

## 📌 Description (EN)

`radosgw_exporter` is a [Prometheus](https://prometheus.io/) exporter that fetches usage, quota, and bucket statistics from **Ceph RADOS Gateway (RGW)** via Admin API.

It exposes metrics for:
- Bucket operations (`ops`, `successful_ops`, `bytes_sent`, `bytes_received`)
- Bucket usage (`bucket_usage_bytes`, `bucket_usage_objects`)
- User and bucket quotas
- Per-user total consumption

Written in **Go**, with **no CGO dependencies**, **Kubernetes-ready**, and supports **graceful shutdown**, **structured logging**, **TLS**, and **secure secret handling**.

---

## 🚀 Quick Start (EN)

### 1. Create an admin user in Ceph

```bash
radosgw-admin user create \
  --uid=radosgw-exporter \
  --display-name="RADOSGW Exporter" \
  --caps="buckets=read;users=read;usage=read;metadata=read"
```

Save the `access_key` and `secret_key`.

### 2. Run the exporter

```bash
export RADOSGW_ENDPOINT="https://ceph-gw.example.com"
export ACCESS_KEY="..."
export SECRET_KEY="..."
export STORE="prod-cluster"
export METRICS_PORT=9242
export INSECURE_SKIP_VERIFY=false  # true only for dev!

go run .
```

### 3. Check metrics

```bash
curl http://localhost:9242/metrics | grep radosgw
```

---

## 📦 Docker

```bash
docker build -t radosgw-exporter .
docker run -p 9242:9242 \
  -e RADOSGW_ENDPOINT="https://ceph:443" \
  -e ACCESS_KEY="..." \
  -e SECRET_KEY="..." \
  radosgw-exporter
```

---

## 🛡️ Security

- Never store `ACCESS_KEY` / `SECRET_KEY` in code or ConfigMaps.
- Use Kubernetes `Secret`:
  ```yaml
  envFrom:
    - secretRef:
        name: radosgw-exporter-secret
  ```

---

## ⚙️ Environment Variables

| Variable | Default | Description |
|--------|--------|-----------|
| `RADOSGW_ENDPOINT` | — | RGW endpoint URL (without `/admin`) |
| `ACCESS_KEY` | — | **Required** |
| `SECRET_KEY` | — | **Required** |
| `STORE` | `us-east-1` | `store` label value |
| `METRICS_PORT` | `9242` | Port for `/metrics` |
| `INSECURE_SKIP_VERIFY` | `false` | Skip TLS verification (dev only) |

---

## 📈 Metrics

- `radosgw_usage_ops_total`
- `radosgw_usage_sent_bytes_total`
- `radosgw_usage_bucket_bytes`
- `radosgw_usage_user_quota_size_bytes`
- `radosgw_up` — `1` if healthy, `0` on error
- and more (see source)

---

## 📜 License

MIT
```
