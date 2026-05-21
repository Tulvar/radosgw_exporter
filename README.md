# 📦 RADOSGW Exporter

Prometheus-экспортер для Ceph RADOSGW Admin API.
Разработан для Kubernetes, поддерживает сбор метрик по пользователям, бакетам, usage, квотам и S3-Select.
Имеет гибкое управление метриками, чтобы избежать проблем с высокой кардинальностью.

---

## 🚀 Возможности

* Сбор метрик из RGW Admin API
* Подача данных в Prometheus формате
* Поддержка TLS и k8s
* Управление нагрузкой через ENV флаги
* Поддержка квот пользователей и бакетов
* Детальная статистика usage (опционально)
* Метрики S3-Select (опционально)
* Минимальная зависимость от Ceph версии
---

## 📌 Требования

* Доступ к RADOSGW Admin API
* Действительные ACCESS_KEY и SECRET_KEY
* Prometheus или VictoriaMetrics
---

## ⚙️ Переменные окружения
### 🔐 Обязательные

| Переменная  | Описание  |
|---|---|
|  RADOSGW_ENDPOINT | URL до RADOSGW Admin API   |
|  ACCESS_KEY | Admin access key  |
|  SECRET_KEY | Admin secret key  |

```bash
RADOSGW_ENDPOINT=https://rgw.example.com/admin
ACCESS_KEY=xxxx
SECRET_KEY=yyyy
```
---

## 🏁 Управление группами метрик

Каждый флаг включает/выключает отдельную группу метрик.
Это критично важно на больших кластерах, где могут быть миллионы бакетов/пользователей.

✔️ ENABLE_USER_STATS (по умолчанию: true)

Включает метрики по пользователям:
```bash
radosgw_user_total_bytes
radosgw_user_total_objects
radosgw_user_quota_*
radosgw_user_bucket_quota_*
```
Отключить:

```bash
ENABLE_USER_STATS=false
```

✔️ ENABLE_BUCKET_STATS (по умолчанию: true)

Включает метрики по бакетам:

```bash
radosgw_bucket_usage_bytes
radosgw_bucket_usage_objects
radosgw_bucket_quota_*
```
Отключить:
```bash
ENABLE_BUCKET_STATS=false
```

✔️ ENABLE_USAGE_METRICS (по умолчанию: false)

```bash
radosgw_usage_ops
radosgw_usage_successful_ops
radosgw_usage_failed_ops
radosgw_usage_sent_bytes
radosgw_usage_received_bytes
radosgw_usage_epoch
```
Включать только если действительно нужны эти данные:
```bash
ENABLE_USAGE_METRICS=true
```

## 🔧 Дополнительные переменные
| Переменная  | Значение по умолчанию  | Описание  |  
|---|---|---|
| INSECURE_SKIP_VERIFY  |  false |  Отключает TLS проверку |   
|  SCRAPE_TIMEOUT |  15 |  Таймаут сбора метрик (сек) |  
| USAGE_CACHE_TTL | 0s | TTL кеша usage-метрик (`0s` = отключено, поддерживаются значения `60`, `30s`, `2m`) |
| USERS_BUCKETS_CACHE_TTL | 0s | TTL кеша users/buckets-метрик (`0s` = отключено, поддерживаются значения `60`, `30s`, `2m`) |
| MAX_USERS_PER_SCRAPE | 0 | Лимит пользователей за один scrape (0 = без лимита) |
| LOG_LEVEL | info | Уровень логов: debug/info/warn/error |
| LOG_FORMAT | json | Формат логов: json/text |
| METRICS_PORT  | 9242  | HTTP порт экспорта метрик  |  
---
## 📊 Метрики доступны по адресу

```bash
http://<ip>:9242/metrics
```
