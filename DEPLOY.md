# Deployment Guide (Operator)

Руководство для оператора по запуску агента для новых пользователей.

## Требования

- Docker + Docker Compose
- Токен Telegram-бота (получить у @BotFather)
- API-ключ LLM (z.ai / OpenRouter / любой OpenAI-совместимый)
- `chat_id` пользователя (как узнать — см. ниже)

---

## Шаг 1. Создать бота в Telegram

1. Открыть [@BotFather](https://t.me/BotFather) в Telegram
2. Отправить `/newbot`
3. Придумать имя и username бота (username должен заканчиваться на `bot`)
4. Скопировать токен вида `1234567890:AAF...`

---

## Шаг 2. Узнать chat_id пользователя

Попросить пользователя отправить любое сообщение боту [@userinfobot](https://t.me/userinfobot) — он ответит их `chat_id`.

Или временно поставить `allowed_users: [0]`, запустить агента, попросить пользователя написать `/start` и посмотреть в логах: `chat_id=XXXXXX`.

---

## Шаг 3. Подготовить конфигурацию

Минимальный `config.yaml` (3 обязательных параметра):

```yaml
telegram:
  token: "1234567890:AAF..."   # токен от BotFather
  allowed_users:
    - 123456789               # chat_id пользователя

llm:
  base_url: "https://api.z.ai/api/coding/paas/v4"
  api_key: "..."              # API-ключ

database:
  path: "./data/agent.db"
```

> Остальные параметры (weather, stocks, web, news и т.д.) уже включены по умолчанию в `config.example.yaml`.
> Скопируй его и заполни только обязательные поля.

```bash
cp config.example.yaml config.yaml
# Отредактировать: token, allowed_users, api_key
```

---

## Шаг 4. Запустить контейнер

### Скрипт add-user.sh (рекомендуется)

Самый быстрый способ — использовать готовый скрипт. Секреты передаются через переменные окружения:

```bash
TELEGRAM_TOKEN=... LLM_API_KEY=... ./scripts/add-user.sh <username> <chat_id> <llm_base_url>
```

Пример:

```bash
export TELEGRAM_TOKEN="1234567890:AAF..."
export LLM_API_KEY="my-api-key"
./scripts/add-user.sh alice 123456789 "https://api.z.ai/api/coding/paas/v4"
```

> **Важно:** передавай секреты через переменные окружения, а не позиционными аргументами — так они не попадают в shell history и не видны в `ps`.

Скрипт автоматически:
- Создаёт контейнер с нужными env vars
- Проверяет, что контейнер продолжает работать через 2 секунды после старта
- Выводит логи и код ошибки, если контейнер сразу завершился

> **Важно:** `api_key` и `llm_base_url` обязательны — без них контейнер запустится, но сразу завершится с ошибкой валидации конфига.

### Вариант A: вручную через env vars

```bash
export TELEGRAM_TOKEN="1234567890:AAF..."
export LLM_API_KEY="..."

docker run -d \
  --name agent-username \
  --restart unless-stopped \
  -e TELEGRAM_TOKEN \
  -e TELEGRAM_ALLOWED_USERS="123456789" \
  -e LLM_API_KEY \
  -e LLM_BASE_URL="https://api.z.ai/api/coding/paas/v4" \
  -v agent-username-data:/app/data \
  ghcr.io/lllypuk/artel:latest
```

### Вариант B: через config.yaml

```bash
docker run -d \
  --name agent-username \
  --restart unless-stopped \
  -v ./config.yaml:/app/config.yaml:ro \
  -v agent-username-data:/app/data \
  ghcr.io/lllypuk/artel:latest
```

### Вариант C: docker-compose

Скопировать `docker-compose.yml` из репозитория и заполнить env vars:

```yaml
services:
  agent-username:
    image: ghcr.io/lllypuk/artel:latest
    container_name: agent-username
    restart: unless-stopped
    volumes:
      - agent-username-data:/app/data
    environment:
      - TELEGRAM_TOKEN=1234567890:AAF...
      - TELEGRAM_ALLOWED_USERS=123456789
      - LLM_API_KEY=...
      - LLM_BASE_URL=https://api.z.ai/api/coding/paas/v4
    # Если нужен доступ к прокси на хосте:
    extra_hosts:
      - "host-gateway:host-gateway"

volumes:
  agent-username-data:
```

```bash
docker compose up -d
docker compose logs -f agent-username
```

---

## Переменные окружения

Все переменные окружения переопределяют соответствующие поля `config.yaml`.

| Переменная              | Обязательная | Описание                                                    |
|-------------------------|:------------:|-------------------------------------------------------------|
| `TELEGRAM_TOKEN`        | ✅           | Токен бота от @BotFather                                    |
| `TELEGRAM_ALLOWED_USERS`| ✅           | `chat_id` пользователей через запятую: `123456789,987654321`|
| `LLM_API_KEY`           | ✅ *         | API-ключ LLM-провайдера (также принимает `GLM_API_KEY`); только для одиночного провайдера |
| `LLM_BASE_URL`          | ✅ *         | Base URL API, например `https://api.z.ai/api/coding/paas/v4`; только для одиночного провайдера |
| `WEB_API_KEY`           | —            | API-ключ для поиска (Brave / Serper)                        |
| `LOG_LEVEL`             | —            | Уровень логирования: `debug`, `info`, `warn`, `error`       |
| `HTTPS_PROXY`           | —            | HTTP/HTTPS прокси для всех исходящих запросов               |

> \* Обязательны при использовании одиночного провайдера (без `llm.providers` в config.yaml).

> **Конфиг-файл не обязателен.** Если `/app/config.yaml` отсутствует, агент запускается только на переменных окружения.
> Минимально необходимые переменные для запуска: `TELEGRAM_TOKEN`, `TELEGRAM_ALLOWED_USERS`, `LLM_API_KEY`, `LLM_BASE_URL`.

---

## Шаг 5. Проверка

```bash
# Логи в реальном времени
docker logs -f agent-username

# Проверить что контейнер запущен
docker ps | grep agent-username
```

Попросить пользователя отправить `/start` своему боту — должно прийти приветствие.

---

## Управление контейнером

```bash
# Остановить
docker stop agent-username

# Перезапустить
docker restart agent-username

# Обновить образ
docker pull ghcr.io/lllypuk/artel:latest
docker stop agent-username && docker rm agent-username
# Запустить заново командой из Шага 4

# Посмотреть логи (последние 100 строк)
docker logs --tail 100 agent-username

# Удалить контейнер (данные сохранятся в volume)
docker rm agent-username

# Удалить данные (необратимо!)
docker volume rm agent-username-data
```

---

## Список активных контейнеров

```bash
docker ps --filter "name=agent-" --format "table {{.Names}}\t{{.Status}}\t{{.CreatedAt}}"
```

---

## Если нет прямого доступа к интернету

Добавь прокси в env:

```bash
-e HTTPS_PROXY="http://127.0.0.1:2080"
```

Или в `config.yaml`:

```yaml
telegram:
  proxy: "http://127.0.0.1:2080"
```

---

## Часто используемые LLM-провайдеры

| Провайдер | BASE_URL | Где взять ключ |
|-----------|----------|----------------|
| z.ai (GLM) | `https://api.z.ai/api/coding/paas/v4` | [z.ai](https://z.ai) |
| OpenRouter | `https://openrouter.ai/api/v1` | [openrouter.ai/keys](https://openrouter.ai/keys) |
| OpenAI | `https://api.openai.com/v1` | [platform.openai.com](https://platform.openai.com) |

---

## Сборка из исходников (опционально)

```bash
git clone https://github.com/lllypuk/artel
cd artel
docker build -t artel:local .
# Затем использовать artel:local вместо ghcr.io/...
```
