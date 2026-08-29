# rssam

Самостоятельный RSS-агрегатор на Go: веб-интерфейс, фоновый опрос лент, фильтры, вебхуки и мосты к источникам без обычного RSS.

Лицензия: [MIT](LICENSE).

## Возможности

- Категории, ленты, непрочитанное, поиск, избранное
- Роли: **admin** настраивает ленты, фильтры и вебхуки; обычный пользователь читает записи (в том числе по меткам фильтров)
- Фильтры (regex RE2) с действиями: метка, вебхук, удаление
- Вебхуки HTTP, Telegram и Max; после доставки — действие с записью (`on_success_entry`). Протокол для приёмника: [docs/WEBHOOK_RECEIVER_SPEC.md](docs/WEBHOOK_RECEIVER_SPEC.md)
- На ленте можно хранить только хеш до срабатывания фильтра (экономия места)
- Источники: обычный RSS/Atom, Telegram, Max, MaxStat, VK Search, Rutube, Dzen News, Smotrim
- Per-feed: интервал опроса, TLS без проверки сертификата (сайты с корпоративным/РФ CA), retention
- REST `/v1` (API-ключи, `X-Auth-Token`), спецификация: `GET /openapi.json` или файл [`internal/http/openapi.json`](internal/http/openapi.json)
- Метрики Prometheus: `GET /metrics` (токен `METRICS_TOKEN`)

## Требования

- Go 1.26+
- PostgreSQL 16+ (в `docker compose` — PostgreSQL 17)

## Запуск

```bash
cp .env.example .env
# задайте ADMIN_USERNAME и ADMIN_PASSWORD
docker compose up -d postgres
go build -trimpath -o bin/rssam ./cmd/rssam
./bin/rssam
```

Миграции выполняются при старте (`RUN_MIGRATIONS=true`). Проверка: `curl -sS http://127.0.0.1:8080/healthz` — ответ `ok`.

UI: `http://127.0.0.1:8080/ui/login` (нужно `UI_ENABLED=true`).

Всё в Docker: `docker compose up --build`. Если установлен `make`: `make test`, `make build`.

Не коммитьте `.env`. Не поднимайте второй экземпляр rssam на той же базе — два poller’а делят очередь `jobs`.

## Конфигурация

Все переменные и комментарии — в [`.env.example`](.env.example). Кратко:

| Область | Примеры |
|---------|---------|
| Сервер и БД | `DATABASE_URL`, `LISTEN_ADDR`, `LOG_LEVEL`, `UI_ENABLED` |
| Admin при первом старте | `ADMIN_USERNAME`, `ADMIN_PASSWORD` |
| Worker | `WORKER_POOL_SIZE`, `SCHEDULER_TICK`, интервалы polling, daily reset |
| Мосты | `MAX_API_BASE_URL`, `TELEGRAM_PROXY_SERVICE_URL`, `VK_ACCESS_TOKEN`, MaxStat, Dzen, Smotrim |
| SSRF | `FETCH_ALLOW_PRIVATE_NETWORK` (для LAN API мостов), `FETCH_ALLOWED_CIDRS` |
| Вебхуки и cleanup | `WEBHOOK_*`, `REMOVED_RETENTION_DAYS`, `CLEANUP_INTERVAL` |

Для Telegram-каналов нужен внешний SOCKS/proxy-service; для Max — HTTP API канала. Без них эти типы лент не опрашиваются.

## Деплой бинарником

Пример unit: [`deploy/rssam.service`](deploy/rssam.service) (`WorkingDirectory` и `EnvironmentFile` — `/opt/rssam`). Grafana/Prometheus: `deploy/grafana/`, `deploy/prometheus/`.

## Разработка

```bash
go test ./...
```

Интеграционные тесты storage: `DATABASE_URL=... go test ./internal/storage/... -tags=integration`.
