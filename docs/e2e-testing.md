# E2E тестирование ботов

Инструкция для end-to-end проверки multi-bot setup (analyst, developer, tester)
при дальнейшей разработке Artel. Проверяет полную цепочку:
Mattermost → WebSocket → AuthGate → LLM → pi_coder → ответ в Mattermost.

## Предусловия

1. **Mattermost** запущен (Docker, порт 8065).
2. **Ollama** запущен на `0.0.0.0:11434` (не `127.0.0.1` — нужен для Docker).
   Проверка: `ss -tlnp | grep 11434` → `*:11434`.
3. **Боты** созданы в Mattermost: analyst-bot, developer-bot, tester-bot.
4. **Токены** ботов созданы через Mattermost API.
5. **Канал** для тестирования (по умолчанию `Agent E2E`, ID `uy1woyi9qpfbzjjre18ro76ogh`).
6. **Конфиги** `configs/*.yaml` заполнены реальными токенами (не `.example`).

## Уровни тестирования

### Уровень 1: Smoke-тест одного бота

Проверяет базовую связку: WebSocket → AuthGate → LLM → ответ.

```bash
# 1. Сборка
make build

# 2. Запуск одного бота локально (без Docker)
./bin/artel configs/analyst-bot.yaml &

# 3. Проверка логов — WebSocket подключён
sleep 3
tail -5 data/analyst-bot.log

# Ожидаемые строки:
#   "mattermost transport authorized" username=analyst-bot
#   "mattermost: websocket connected"
#   "PiCoder tool enabled" projects=[...]

# 4. Отправка DM через Mattermost API
SASHA_TOKEN="<owner-token>"
ANALYST_BOT_ID="<analyst-bot-user-id>"

# Создать DM-канал
curl -s -X POST -H "Authorization: Bearer $SASHA_TOKEN" \
  -H "Content-Type: application/json" \
  -d "[\"<sasha-user-id>\",\"$ANALYST_BOT_ID\"]" \
  http://localhost:8065/api/v4/channels/direct

# Отправить сообщение
DM_CHANNEL="<dm-channel-id>"
curl -s -X POST -H "Authorization: Bearer $SASHA_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"channel_id\": \"$DM_CHANNEL\", \"message\": \"Привет! Какой проект ты можешь доработать?\"}" \
  http://localhost:8065/api/v4/posts

# 5. Проверка ответа (через ~10-20 сек)
sleep 15
curl -s -H "Authorization: Bearer $SASHA_TOKEN" \
  "http://localhost:8065/api/v4/channels/$DM_CHANNEL/posts?per_page=2" | python3 -m json.tool

# Ожидание: бот отвечает списком доступных проектов

# 6. Остановка
pkill -f "bin/artel configs/analyst-bot.yaml"
```

**Критерий успеха:** бот отвечает в DM, в логе нет ошибок, auth gate
отклоняет собственное эхо (`reason=self`).

### Уровень 2: Тест pi_coder

Проверяет запуск pi внутри проекта через tool call.

```bash
# 1. Запустить бота (если не запущен из уровня 1)
./bin/artel configs/analyst-bot.yaml &

# 2. Отправить задачу на анализ проекта
SASHA_TOKEN="<owner-token>"
DM_CHANNEL="<dm-channel-id>"

curl -s -X POST -H "Authorization: Bearer $SASHA_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"channel_id\": \"$DM_CHANNEL\", \"message\": \"Проанализируй структуру проекта shatrov.tech через pi_coder. Кратко.\"}" \
  http://localhost:8065/api/v4/posts

# 3. Дождаться ответа (pi может работать 30-90 сек)
sleep 60

# 4. Проверить ответ — должен содержать реальную структуру проекта
curl -s -H "Authorization: Bearer $SASHA_TOKEN" \
  "http://localhost:8065/api/v4/channels/$DM_CHANNEL/posts?per_page=2" | python3 -m json.tool
```

**Критерий успеха:** ответ содержит реальные файлы/директории проекта,
а не сообщение об ошибке.

### Уровень 3: Межбот-взаимодействие

Проверяет @mention routing, auth gate для каналов, работу 3 ботов одновременно.

```bash
# 1. Создать конфиги для всех 3 ботов (см. configs/*.yaml.example)
# 2. Добавить всех 3 ботов в канал Agent E2E через Mattermost API

# 3. Запустить все 3 бота локально
nohup bash -c './bin/artel configs/analyst-bot.yaml > data/analyst-stdout.log 2>&1' &>/dev/null &
nohup bash -c './bin/artel configs/developer-bot.yaml > data/developer-stdout.log 2>&1' &>/dev/null &
nohup bash -c './bin/artel configs/tester-bot.yaml > data/tester-stdout.log 2>&1' &>/dev/null &

sleep 5
ps aux | grep "[b]in/agent"

# 4. Тест: упомянуть одного бота — только он должен ответить
SASHA_TOKEN="<owner-token>"
CHANNEL_ID="uy1woyi9qpfbzjjre18ro76ogh"

curl -s -X POST -H "Authorization: Bearer $SASHA_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"channel_id\": \"$CHANNEL_ID\", \"message\": \"@analyst-bot перечисли доступные проекты\"}" \
  http://localhost:8065/api/v4/posts

sleep 15

# 5. Проверить логи:
#    analyst-bot: нет rejection для сообщения Sasha → обработано
#    developer-bot: reason=owner_not_addressed → корректно отклонено
#    tester-bot: reason=owner_not_addressed → корректно отклонено
tail -3 data/analyst-bot.log data/developer-bot.log data/tester-bot.log

# 6. Тест: упомянуть двух ботов — оба должны ответить
curl -s -X POST -H "Authorization: Bearer $SASHA_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"channel_id\": \"$CHANNEL_ID\", \"message\": \"@analyst-bot @developer-bot скажите каждый по одному слову\"}" \
  http://localhost:8065/api/v4/posts

sleep 20
# Ожидание: analyst и developer отвечают, tester молчит
tail -5 data/analyst-bot.log data/developer-bot.log data/tester-bot.log

# 7. Остановка
pkill -f "bin/artel configs"
```

**Критерии успеха:**
- @mention одного бота → отвечает только он
- @mention двух ботов → отвечают оба
- Неупомянутые боты: `owner_not_addressed` в логе
- Bot echo своего ответа: `reason=self` в логе

### Уровень 4: Docker

Проверяет полную работу в Docker-контейнерах.

```bash
# 1. Остановить локальных ботов
pkill -f "bin/artel configs"

# 2. Собрать образ
docker build -t artel:latest .

# 3. Обновить models.json для Docker (один раз после сборки)
#    При первом запуске контейнеров нужно заменить 127.0.0.1 на host-gateway
#    в pi models.json. См. раздел "Известные проблемы" ниже.

# 4. Запустить через compose
docker compose -f docker-compose.bots.yml up -d

# 5. Проверить что все 3 контейнера запущены и WebSocket подключён
docker ps --filter "name=artel"
for c in analyst developer tester; do
    echo "=== $c ==="
    docker exec artel-$c-1 cat /home/agent/data/agent.log | tail -3
done

# Ожидаемые строки:
#   "mattermost: websocket connected" url=ws://host-gateway:8065/...
#   "PiCoder tool enabled" projects=[...]

# 6. Тест WebSocket → LLM
curl -s -X POST -H "Authorization: Bearer $SASHA_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"channel_id\": \"$CHANNEL_ID\", \"message\": \"@analyst-bot сколько проектов доступно? просто число\"}" \
  http://localhost:8065/api/v4/posts

sleep 15
# Ожидание: бот отвечает "11"

# 7. Тест pi_coder из Docker
curl -s -X POST -H "Authorization: Bearer $SASHA_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"channel_id\": \"$CHANNEL_ID\", \"message\": \"@analyst-bot через pi_coder проанализируй проект flowra\"}" \
  http://localhost:8065/api/v4/posts

sleep 60
# Ожидание: бот отвечает с реальной структурой проекта

# 8. Остановка
docker compose -f docker-compose.bots.yml down
```

**Критерии успеха:**
- 3 контейнера запущены, WebSocket подключён к `host-gateway:8065`
- LLM (qwen3.5:cloud) отвечает через `host-gateway:11434/v1`
- pi_coder запускается в контейнере, pi видит модели Ollama
- Auto-discovery находит 11 проектов из `/home/projects`
- Volume mounts работают: projects (rw), .ssh (ro), .pi (rw), data (named volume)

## Известные проблемы и решения

### Ollama слушает на 127.0.0.1

Docker-контейнеры не могут достучаться до `127.0.0.1` хоста.
Решение: `OLLAMA_HOST=0.0.0.0:11434` через systemd override.

```bash
sudo mkdir -p /etc/systemd/system/ollama.service.d
echo '[Service]
Environment="OLLAMA_HOST=0.0.0.0:11434"' | sudo tee /etc/systemd/system/ollama.service.d/override.conf
sudo systemctl daemon-reload
sudo systemctl restart ollama
```

### pi models.json с hardcoded 127.0.0.1

`~/.pi/agent/models.json` хранит `baseUrl: "http://127.0.0.1:11434/v1"`
для ollama-провайдера. В Docker `127.0.0.1` — это сам контейнер.
Env-переменные `OLLAMA_HOST` / `OLLAMA_BASE_URL` не переопределяют
этот файл.

Решение: после первого запуска контейнеров заменить в models.json:

```bash
for c in artel-analyst-1 artel-developer-1 artel-tester-1; do
    docker exec $c sed -i 's|http://127.0.0.1:11434|http://host-gateway:11434|g' \
        /home/agent/.pi/agent/models.json
done
```

Затем перезапустить контейнеры. Так как `~/.pi` монтируется rw,
изменение сохраняется в хостовом `~/.pi/agent/models.json` и
применяется при следующем запуске автоматически.

> **Альтернатива:** заменить `127.0.0.1` → `host-gateway` в
> `~/.pi/agent/models.json` на хосте один раз. Локальный pi продолжит
> работать (host-gateway резолвится и на хосте), а Docker-контейнеры
> получат правильный endpoint.

### pi версия в Docker ≠ pi на хосте

`~/.pi` монтируется в контейнер. Расширения из `~/.pi/agent/` совместимы
только с той же версией pi. Несовпадение версий → ошибки загрузки расширений.

Решение: фиксировать версию pi в Dockerfile, совпадающую с хостом:

```dockerfile
RUN npm install -g @earendil-works/pi-coding-agent@0.80.6
```

Проверить: `docker exec artel-analyst-1 pi --version`
должен совпадать с `pi --version` на хосте.

### ~/.pi read-only

`docker-compose.bots.yml` изначально монтирует `~/.pi:/home/agent/.pi:ro`.
Pi не может создавать session-файлы и lock-файлы.

Решение: монтировать как `:rw` (актуально в `docker-compose.bots.yml`).

### Node.js версия в Docker

Pi 0.80.6 требует Node ≥22.9.0. `alpine:3.20` поставляет Node 20.15.1.

Решение: использовать `node:22-alpine` как runtime-stage в Dockerfile
вместо `alpine:3.20` + `apk add nodejs`.

### Боты не инициируют межбот-общение

Боты отвечают "не могу взаимодействовать с другими ботами", потому что
system prompt не объясняет, что @mention коллеги-бота в канальном сообщении
вызовет его ответ (auth gate поддерживает `team_mention`).

Это не баг — инфраструктура работает (если бот упомянет @developer-bot в
ответе, developer-bot получит и обработает сообщение). Решается
корректировкой system_prompt для роли, если требуется активная
координация.

## Быстрая проверка (cheat sheet)

Минимальный набор команд для регрессионной проверки:

```bash
# Build + tests
make build && go test ./...

# Локальный smoke-тест
./bin/artel configs/analyst-bot.yaml &
sleep 3
tail -3 data/analyst-bot.log  # WebSocket connected?
pkill -f "bin/artel configs"

# Docker
docker compose -f docker-compose.bots.yml up -d
sleep 5
for c in analyst developer tester; do
    docker exec artel-$c-1 cat /home/agent/data/agent.log | tail -2
done
docker compose -f docker-compose.bots.yml logs --tail 5
docker compose -f docker-compose.bots.yml down
```

## Аварийные команды

```bash
# Просмотр логов контейнера
docker exec artel-analyst-1 cat /home/agent/data/agent.log | tail -20

# Проверка доступности Ollama из контейнера
docker exec artel-analyst-1 wget -qO- http://host-gateway:11434/api/tags | head -20

# Проверка доступности Mattermost из контейнера
docker exec artel-analyst-1 wget -qO- http://host-gateway:8065/api/v4/system/ping

# Проверка что pi видит модели
docker exec artel-analyst-1 pi --list-models 2>&1 | grep cloud

# Проверка volume mounts
docker exec artel-analyst-1 ls /home/projects/
docker exec artel-analyst-1 ls /home/agent/.ssh/
docker exec artel-analyst-1 ls /home/agent/data/

# Полный рестарт
docker compose -f docker-compose.bots.yml down
docker compose -f docker-compose.bots.yml up -d
```