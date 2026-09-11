# Спецификация приёма webhook от rssam

Документ для разработчика **принимающей системы** (диспетчер Max/Telegram, middleware, serverless и т.д.).

Версия протокола: **event_version = 1**.

---

## 1. Когда приходит webhook

rssam ставит доставку в очередь в двух случаях:

| Триггер | `event_type` | Поле `filter` | `match_details` |
|---------|--------------|---------------|-----------------|
| **Фильтр** — запись совпала с фильтром, в actions выбран webhook | `entry_matched` | объект фильтра | детали совпадения (если есть) |
| **Лента** — на ленте задан webhook, пришла новая запись | `new_entry` | отсутствует | отсутствует |

Одна пара `(webhook_id, entry_id)` доставляется **не более одного раза** (идемпотентность на стороне rssam).

---

## 2. HTTP-запрос

### Метод и URL

- Настраивается в rssam при создании webhook (`POST` по умолчанию).
- URL задаёт администратор в UI `/ui/webhooks` или через API `POST /v1/webhooks`.

### Заголовки

| Заголовок | Обязательность | Описание |
|-----------|----------------|----------|
| `Content-Type` | да (если не переопределён) | `application/json` |
| `X-RSSAM-Signature` | если задан `secret` | `sha256=<hex>` — HMAC-SHA256 **сырого тела** запроса |
| Произвольные | опционально | JSON-объект в поле `headers` webhook в rssam, например `{"X-Custom":"value"}` |

### Тело

Два режима:

1. **Без `body_template`** — rssam шлёт стандартный JSON (см. §3).
2. **С `body_template`** — тело = результат Go `text/template` (см. §5). Подпись считается по **фактическому** телу после рендера.

### Ожидаемый ответ

- **2xx** — доставка успешна, повторов не будет. На стороне rssam лог получает статус `sent` (не `delivered`).
- **408, 429, 5xx, сеть/таймаут** — rssam повторяет с exponential backoff.
- **Остальные 4xx** (400, 401, 403, 404, …) — сразу `dead`, без повторов.
- Если webhook **выключен** после постановки в очередь — доставка **ставится на паузу** (`failed`, попытка не тратится) и возобновляется после включения.

Параметры повторов (env rssam, для справки):

| Переменная | По умолчанию |
|------------|--------------|
| `WEBHOOK_TIMEOUT` | `10s` |
| `WEBHOOK_MAX_ATTEMPTS` | `10` |
| `WEBHOOK_RETRY_BASE` | `5s` |
| `WEBHOOK_RETRY_MAX` | `1h` |

**Рекомендация приёмнику:** отвечать `200`/`204` как можно быстрее; тяжёлую работу (отправка в Max/TG) — в фоне.

---

## 3. Стандартный JSON payload (без body_template)

```json
{
  "event_version": 1,
  "event_type": "entry_matched",
  "entry": { ... },
  "feed": { "ID": 7, "Title": "Название ленты" },
  "filter": { ... },
  "match_details": { ... },
  "sent_at": "2026-08-15T20:00:00Z"
}
```

### Корневые поля

| Поле | Тип | Всегда | Описание |
|------|-----|--------|----------|
| `event_version` | int | да | Версия схемы, сейчас `1` |
| `event_type` | string | да | `entry_matched` или `new_entry` |
| `entry` | object | да | Запись RSS |
| `feed` | object | да | Лента: `ID`, `Title` (название из rssam) |
| `filter` | object | только `entry_matched` | Сработавший фильтр (без rules/actions в delivery) |
| `match_details` | object | опционально | JSON из `filter_matches.details` |
| `sent_at` | string (RFC3339 UTC) | да | Время формирования payload |

### Объект `entry`

> **Важно:** ключи в **PascalCase** (сериализация Go-структуры без `json`-тегов).

| Поле | Тип | Описание |
|------|-----|----------|
| `ID` | int64 | ID записи в rssam |
| `FeedID` | int64 | ID ленты |
| `Title` | string | Заголовок |
| `URL` | string | Ссылка на статью |
| `Content` | string | Текст/summary (может быть пустым на hash-only лентах до match) |
| `OriginalContent` | string | Исходный контент до rewrite |
| `ContentFetched` | bool | Был ли scrape полного текста |
| `Author` | string \| null | Автор |
| `PublishedAt` | string \| null | RFC3339 |
| `Hash` | string | Дедуп-хеш записи |
| `Status` | string | `unread`, `read`, `removed` |
| `Starred` | bool | В избранном |
| `CreatedAt` | string | RFC3339 |
| `UpdatedAt` | string | RFC3339 |

Для диспетчера в Max/TG обычно достаточно: `feed.Title`, `Title`, `URL`, при необходимости `Content`, `FeedID`.

### Объект `feed`

> Ключи в **PascalCase**, как у `entry`.

| Поле | Тип | Описание |
|------|-----|----------|
| `ID` | int64 | ID ленты (совпадает с `entry.FeedID`) |
| `Title` | string | Название ленты в rssam |

Полная конфигурация ленты (интервал, мост, иконка и т.д.) **не** включается.

### Объект `filter` (только `entry_matched`)

| Поле | Тип | Описание |
|------|-----|----------|
| `ID` | int64 | ID фильтра |
| `UserID` | int64 | Владелец |
| `Name` | string | Имя фильтра (удобно как «метка» в тексте сообщения) |
| `Enabled` | bool | Включён |
| `CreatedAt` | string | RFC3339 |
| `UpdatedAt` | string | RFC3339 |

Поля `Rules`, `Actions`, `ScopeItems` в webhook **не заполняются** (пустые массивы).

### Пример: feed-level (`new_entry`)

```json
{
  "event_version": 1,
  "event_type": "new_entry",
  "entry": {
    "ID": 12345,
    "FeedID": 7,
    "Title": "Заголовок новости",
    "URL": "https://example.com/news/1",
    "Content": "",
    "OriginalContent": "",
    "ContentFetched": false,
    "Author": null,
    "PublishedAt": "2026-08-15T18:30:00Z",
    "Hash": "abc123...",
    "Status": "unread",
    "Starred": false,
    "CreatedAt": "2026-08-15T18:31:00Z",
    "UpdatedAt": "2026-08-15T18:31:00Z"
  },
  "feed": {
    "ID": 7,
    "Title": "Название ленты"
  },
  "sent_at": "2026-08-15T18:31:05Z"
}
```

### Пример: filter-level (`entry_matched`)

```json
{
  "event_version": 1,
  "event_type": "entry_matched",
  "entry": {
    "ID": 12345,
    "FeedID": 7,
    "Title": "Заголовок",
    "URL": "https://example.com/news/1",
    "Content": "Краткое описание...",
    "Hash": "abc123...",
    "Status": "unread",
    "Starred": false,
    "CreatedAt": "2026-08-15T18:31:00Z",
    "UpdatedAt": "2026-08-15T18:31:00Z"
  },
  "feed": {
    "ID": 7,
    "Title": "Название ленты"
  },
  "filter": {
    "ID": 3,
    "UserID": 1,
    "Name": "Важное",
    "Enabled": true,
    "CreatedAt": "2026-01-01T00:00:00Z",
    "UpdatedAt": "2026-01-01T00:00:00Z"
  },
  "match_details": {
    "matched_rules": [1, 2]
  },
  "sent_at": "2026-08-15T18:31:05Z"
}
```

---

## 4. Проверка подписи (`secret`)

Если в webhook задан `secret`, rssam добавляет:

```
X-RSSAM-Signature: sha256=<hex>
```

где `<hex>` = `HMAC-SHA256(secret, raw_request_body)` в lowercase hex.

### Псевдокод

```
expected = "sha256=" + hex(hmac_sha256(secret, request_body_bytes))
if not constant_time_equals(request.header["X-RSSAM-Signature"], expected):
    return 401
```

### Go

```go
mac := hmac.New(sha256.New, []byte(secret))
mac.Write(body)
sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
```

### Python

```python
import hmac, hashlib

expected = "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
if not hmac.compare_digest(request.headers.get("X-RSSAM-Signature", ""), expected):
    raise HTTPException(401)
```

Подпись считается по **байтам тела как пришли** (после `body_template`, если он задан).

---

## 5. Кастомное тело (`body_template`)

Опциональное поле в настройках webhook. Шаблон — Go `text/template` с переменными:

| Переменная | Тип | Описание |
|------------|-----|----------|
| `{{.entry}}` | struct | Поля записи (`.entry.Title`, `.entry.URL`, …) |
| `{{.feed}}` | struct | Лента (`.feed.ID`, `.feed.Title`) |
| `{{.filter}}` | struct \| nil | Фильтр; при `new_entry` — `nil`, обращение к `.filter.Name` **упадёт** |
| `{{.payload}}` | string | Стандартный JSON из §3 как строка |

Пример для Max API (feed-level, без фильтра):

```json
{"text":"{{.feed.Title}}\n\n{{.entry.Title}}\n\n{{.entry.URL}}"}
```

Пример с меткой фильтра (только `entry_matched`):

```json
{"text":"Метка: #{{.filter.Name}}\n\n{{.entry.Title}}\n\n{{.entry.URL}}"}
```

Для Telegram (локальный Bot API) шаблон может быть сложнее — HTML-экранирование и `link_preview_options` нужно собирать в шаблоне или на стороне приёмника из сырого `payload`.

---

## 6. Рекомендуемая архитектура приёмника

```
rssam webhook POST
    → verify X-RSSAM-Signature (если secret)
    → parse JSON (или свой body_template)
    → по event_type / filter.Name маршрутизация
    → Max API / Telegram Bot API
    → HTTP 200
```

### Маппинг на бывший `tt-rss-dispatch.php`

| Было (PHP) | Стало (rssam → приёмник) |
|------------|--------------------------|
| Метка TT-RSS | `filter.Name` или отдельный webhook на ленту |
| `since_id` + cron | не нужен — rssam сам опрашивает ленты |
| Отдельный state Max/TG | два webhook в rssam или маршрутизация в приёмнике |
| `build_max_text()` | `feed.Title` + `entry.Title` + `entry.URL` (+ `filter.Name`) |
| UTM-очистка ссылок | rssam убирает `utm_*`, `yclid`, `fbclid` и подобные параметры сам (0.1.11); остальное — `rewrite_rules` на ленте или в приёмнике |

### Идемпотентность на стороне приёмника

rssam не шлёт дубликат `(webhook_id, entry_id)`, но при **ручном retry** из UI (`POST /v1/webhook-logs/{id}/retry`) тот же payload может прийти снова. Приёмнику желательно дедуплицировать по `entry.ID` (или `entry.Hash`).

---

## 7. Тестирование

1. Создать webhook в rssam с URL приёмника.
2. Кнопка «Тест» в UI или `POST /v1/webhooks/{id}/test` — реальная отправка фиктивной записи (`test feed` / `test entry`), **без очереди и без `webhook_logs`**. Для Telegram/Max проверяется `ok`/`success` в ответе API.
3. Привязать webhook к ленте или фильтру → дождаться новой записи → проверить журнал `/ui/webhooks/{id}/logs`.

---

## 8. Ограничения и заметки

- Исходящие запросы проходят SSRF-guard; private IP разрешены только если у rssam `FETCH_ALLOW_PRIVATE_NETWORK=true`.
- На ленте с `store_hash_only` поле `Content` может быть пустым до срабатывания фильтра.
- При `DEDUP_ONLY_STORAGE=true` после успешной доставки rssam может очистить `Content` записи в БД (на payload это не влияет — он уже отправлен).
- Ключи JSON в `entry`/`filter` — **PascalCase**; при парсинге в JS/Python учитывайте регистр или используйте `body_template` с нужным форматом.

---

## 9. Встроенные цели Telegram и Max

В UI `/ui/webhooks` тип цели может быть **HTTP**, **Telegram** или **Max**. rssam сам вызывает API, отдельный приёмник не нужен.

| Тип | Куда | Успех |
|-----|------|--------|
| Telegram | `POST {api_base}/bot{token}/sendMessage` | HTTP 2xx **и** JSON `ok: true` |
| Max | `POST {api_base}/channel/id/{chat_id}/messages` (или `/channel/{name}/messages`, `/channel/user/{user_id}/messages`) | HTTP 2xx **и** JSON `success: true` |
| HTTP | как в §2 | любой 2xx |

Пустой Bot API base → `https://api.telegram.org`. LAN-адреса мостов чтения лент **не** подставляются. Для Max поле API обязательно; сервис PyMax должен быть уже авторизован.
