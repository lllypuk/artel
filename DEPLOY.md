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

### Вариант A: через env vars (рекомендуется для нескольких пользователей)

```bash
docker run -d \
  --name agent-username \
  --restart unless-stopped \
  -e TELEGRAM_TOKEN="1234567890:AAF..." \
  -e TELEGRAM_ALLOWED_USERS="123456789" \
  -e LLM_API_KEY="..." \
  -e LLM_BASE_URL="https://api.z.ai/api/coding/paas/v4" \
  -v agent-username-data:/app/data \
  ghcr.io/lllypuk/agent-shatrov:latest
```

### Вариант B: через config.yaml

```bash
docker run -d \
  --name agent-username \
  --restart unless-stopped \
  -v ./config.yaml:/app/config.yaml:ro \
  -v agent-username-data:/app/data \
  ghcr.io/lllypuk/agent-shatrov:latest
```

### Вариант C: docker-compose (если нужен прокси или host network)

```bash
# Скопировать docker-compose.yml, отредактировать переменные
docker-compose up -d
```

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
docker pull ghcr.io/lllypuk/agent-shatrov:latest
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
git clone https://github.com/lllypuk/agent-shatrov
cd agent-shatrov
docker build -t agent-shatrov:local .
# Затем использовать agent-shatrov:local вместо ghcr.io/...
```
