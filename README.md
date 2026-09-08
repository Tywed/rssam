# rssam

Самостоятельный RSS-агрегатор на Go: веб-интерфейс, фоновый опрос лент, фильтры, вебхуки и мосты к источникам без обычного RSS.

Лицензия: [MIT](LICENSE). Репозиторий: [github.com/Tywed/rssam](https://github.com/Tywed/rssam).

## Возможности

- Категории, ленты, непрочитанное, поиск, избранное
- Роли: **admin** настраивает ленты, фильтры и вебхуки; обычный пользователь читает записи (в том числе по меткам фильтров)
- Фильтры (regex RE2) с действиями: метка, вебхук, удаление
- Вебхуки HTTP, Telegram и Max; после доставки — `on_success_entry`. Протокол: [docs/WEBHOOK_RECEIVER_SPEC.md](docs/WEBHOOK_RECEIVER_SPEC.md)
- На ленте можно хранить только хеш до срабатывания фильтра
- Источники: RSS/Atom, Telegram, Max, MaxStat, VK Search, Rutube, Dzen News, Smotrim
- Импорт/экспорт OPML; в экспорт добавляются атрибуты `rssam:*` (интервал, тип источника, TLS, hash-only, срок хранения, правила, вебхук по имени) — при импорте в другой инстанс настройки лент восстанавливаются, чужие читалки их игнорируют
- REST `/v1` (API-ключи), схема `GET /openapi.json` (без встроенного Swagger UI)
- Метрики Prometheus: `GET /metrics` (`METRICS_TOKEN`)
- Админка: состояние лент с журналом последних опросов (ошибки, длительность, новые записи), очередь, автостоп по ошибкам; сроки хранения журналов и автоочистка настраиваются из UI

## Требования

- Linux (systemd), PostgreSQL 16+ (нативно 17)
- root для установки; процесс работает от пользователя `rssam`

## Установка одной командой

Нужен опубликованный [GitHub Release](https://github.com/Tywed/rssam/releases) (`rssam-linux-amd64.tar.gz` + sha256).

```bash
curl -fsSL https://raw.githubusercontent.com/Tywed/rssam/main/install.sh | sudo sh
```

Затем откройте `http://<host>:8080/ui/login`. Логин/пароль admin — из `.env` (`ADMIN_USERNAME` / `ADMIN_PASSWORD`).

| | |
| --- | --- |
| Бинарь | `/opt/rssam/bin/rssam` |
| Конфиг | `/opt/rssam/.env` |
| Логи update | `/opt/rssam/log/update.log` |
| Health | `GET /healthz` → `ok` (не `/health`) |

Не запускайте второй экземпляр rssam на той же `DATABASE_URL`: два poller’а делят очередь `jobs`.

Self-update и restart из веб-UI в **Docker не работают** — обновляйте образ.

### Опции installer

```bash
./install.sh                         # install latest release
./install.sh v0.1.0                  # конкретная версия
./install.sh --quiet
./install.sh --with-postgres         # создать роль/БД rssam, если нет
./install.sh update                  # или: install.sh --update
./install.sh remove                  # БД и .env по умолчанию сохраняются
```

Обновление:

```bash
curl -fsSL https://raw.githubusercontent.com/Tywed/rssam/main/install.sh | sudo sh -s -- --update
```

Из UI (admin → Система): перезапуск и обновление, если установлены sudoers из installer.

## Сборка из исходников

```bash
cd /home/rssam/rssam
VERSION=$(git describe --tags --always)
COMMIT=$(git rev-parse --short HEAD)
DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
go build -trimpath \
  -ldflags="-s -w -X rssam/internal/version.Version=${VERSION} -X rssam/internal/version.Commit=${COMMIT} -X rssam/internal/version.Date=${DATE}" \
  -o bin/rssam ./cmd/rssam
go test ./...
```

Деплой на этот хост после сборки:

```bash
sudo systemctl stop rssam
sudo cp bin/rssam /opt/rssam/bin/rssam
sudo systemctl start rssam
sleep 1
curl -sS --fail --retry 5 --retry-delay 1 --retry-connrefused http://127.0.0.1:8080/healthz
```

## Docker (разработка)

```bash
cp .env.example .env
docker compose up --build
```

Не поднимайте compose-app на prod `DATABASE_URL` параллельно с systemd.

## Публикация в интернете

rssam слушает plain HTTP (`LISTEN_ADDR`, по умолчанию `:8080`) и сам TLS не терминирует. Минимум для внешнего доступа:

1. Реверс-прокси с TLS (nginx/Caddy) перед rssam; rssam слушает только `127.0.0.1:8080`. В прокси: `proxy_set_header Host $host; X-Forwarded-For $remote_addr; X-Forwarded-Proto $scheme;` и включённый WebSocket-upgrade для `/ws/v1`.
2. `HSTS=true` — HSTS-заголовок и cookie `Secure`.
3. `TRUSTED_PROXIES` — адрес прокси, если он не на этом же хосте (по умолчанию доверяется только loopback; иначе rate-limit считает клиентом сам прокси).
4. Сменить `ADMIN_PASSWORD`, убрать `AUTH_TOKEN` из `.env` (это dev-режим: один общий токен вместо API-ключей). `METRICS_TOKEN` нужен только если `/metrics` кто-то читает.
5. `FETCH_ALLOW_PRIVATE_NETWORK=false` (по умолчанию): rssam ходит по URL, которые вводят пользователи; SSRF-guard не пускает его в приватные сети.
6. Не публиковать порт PostgreSQL. `docker-compose.yml` в репозитории — dev-конфигурация (пароль `rssam`, порт 5432 наружу).

Ограничения по умолчанию: логин — 10 попыток, затем одна в 5 с с одного IP (`LOGIN_RATE_LIMIT_*`); тело запроса 1 MiB (`MAX_REQUEST_BODY_BYTES`); лента до 16 MiB; заголовки 1 MiB. Обновление из UI запускает `rssam-update` через sudo — доступно только admin и принимает только semver-тег.

## Конфигурация

Переменные — в [`.env.example`](.env.example). Кратко: `DATABASE_URL`, `LISTEN_ADDR`, `WORKER_POOL_SIZE`, мосты, SSRF `FETCH_ALLOW_PRIVATE_NETWORK`.

`GITHUB_REPO=Tywed/rssam` — сравнение версии в UI с latest release.
