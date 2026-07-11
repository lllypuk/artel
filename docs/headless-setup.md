# Headless Setup — автозапуск ботов через systemd + Docker

Инструкция для запуска agent-shatrov в headless-режиме (без монитора, автозапуск при загрузке).
3 бота (аналитик, разработчик, тестер) в Docker-контейнерах, общаются через Mattermost.

## Предварительные требования

1. **Docker** установлен и работает.
2. **docker compose** plugin доступен.
3. **Git-репозиторий** agent-shatrov склонирован в `~/Project/agent-shatrov`.
4. **Mattermost** сервер доступен по сети (например, на хосте, порт 8065).
5. **Ollama** слушает на `0.0.0.0:11434` (не `127.0.0.1` — нужен для Docker).
6. **pi** — конфиг в `~/.pi/` для Ollama endpoint; версия pi на хосте должна совпадать с версией в Dockerfile.

## Шаг 1. Ollama на 0.0.0.0

Docker-контейнеры не могут достучаться до `127.0.0.1` хоста. Ollama должна слушать на всех интерфейсах:

```bash
sudo mkdir -p /etc/systemd/system/ollama.service.d
echo '[Service]
Environment="OLLAMA_HOST=0.0.0.0:11434"' | sudo tee /etc/systemd/system/ollama.service.d/override.conf
sudo systemctl daemon-reload
sudo systemctl restart ollama
```

Проверка:
```bash
ss -tlnp | grep 11434
# Должно быть *:11434, а не 127.0.0.1:11434
```

## Шаг 2. Enable linger для пользователя

Чтобы user-level systemd сервисы запускались до логина:

```bash
loginctl enable-linger $USER
```

Проверка:
```bash
loginctl show-user $USER | grep Linger
# Linger=yes
```

## Шаг 3. Установить и включить Docker

```bash
sudo systemctl enable docker
sudo systemctl start docker

# Добавьте пользователя в группу docker (если ещё не в группе)
sudo usermod -aG docker $USER
# Перелогиньтесь, чтобы изменения группы вступили в силу
```

## Шаг 4. Подготовить конфиги ботов

```bash
cd ~/Project/agent-shatrov

# Скопировать примеры конфигов и заполнить токены
cp configs/analyst-bot.yaml.example configs/analyst-bot.yaml
cp configs/developer-bot.yaml.example configs/developer-bot.yaml
cp configs/tester-bot.yaml.example configs/tester-bot.yaml
```

Отредактируйте каждый конфиг:
- Замените `<analyst-bot-token>`, `<developer-bot-token>`, `<tester-bot-token>` на реальные Mattermost токены ботов.
- Проверьте `llm.api_key` — ключ для LLM API.
- `server_url: "http://host-gateway:8065"` — Mattermost.
- `base_url: "http://host-gateway:11434/v1"` — Ollama.
- `owner_user_id` — ваш user ID в Mattermost (узнать через `curl -H "Authorization: Bearer <token>" http://localhost:8065/api/v4/users/me`).

> `host-gateway` — специальный Docker hostname, указывающий на хост-машину.
> Работает благодаря `extra_hosts: ["host-gateway:host-gateway"]` в `docker-compose.bots.yml`.

## Шаг 5. Проверить pi конфиг

Pi использует `~/.pi/` для своих настроек. Эта директория монтируется в контейнер: `~/.pi:/home/agent/.pi:rw`
(должна быть writable — pi создаёт session-файлы и lock-файлы).

**Важно:** `~/.pi/agent/models.json` хранит Ollama endpoint как `http://127.0.0.1:11434/v1`.
В Docker `127.0.0.1` — это сам контейнер, а не хост. Замените один раз:

```bash
sed -i 's|http://127.0.0.1:11434|http://host-gateway:11434|g' ~/.pi/agent/models.json
```

Локальный pi продолжит работать (host-gateway резолвится и на хосте),
а Docker-контейнеры получат правильный endpoint.

**Версия pi:** проверьте, что версия pi в Dockerfile совпадает с версией pi на хосте:

```bash
pi --version  # на хосте
grep "pi-coding-agent@" Dockerfile  # в Dockerfile
# Должны совпадать!
```

Несовпадение версий → ошибки загрузки расширений из `~/.pi/agent/`.

## Шаг 6. SSH ключи

Контейнеры монтируют `~/.ssh:/home/agent/.ssh:ro` — SSH ключи хоста доступны в Docker.
Убедитесь, что ключи имеют права `chmod 600` и `~/.ssh` — `chmod 700`.

## Шаг 7. Собрать Docker образ

```bash
cd ~/Project/agent-shatrov
docker build -t agent-shatrov:latest .
```

## Шаг 8. Ручная проверка

```bash
# Запуск
docker compose -f docker-compose.bots.yml up -d

# Проверка что все 3 контейнера запущены
docker ps --filter "name=agent-shatrov"

# Логи (WebSocket подключён?)
for c in analyst developer tester; do
    echo "=== $c ==="
    docker exec agent-shatrov-$c-1 cat /home/agent/data/agent.log | tail -3
done

# Ожидаемые строки:
#   "mattermost: websocket connected" url=ws://host-gateway:8065/...
#   "PiCoder tool enabled" projects=[...]

# Остановить
docker compose -f docker-compose.bots.yml down
```

## Шаг 9. Установить systemd service

```bash
mkdir -p ~/.config/systemd/user/
cp ~/Project/agent-shatrov/scripts/agent-bots.service ~/.config/systemd/user/

systemctl --user daemon-reload
systemctl --user enable agent-bots.service
systemctl --user start agent-bots.service
```

Проверка:
```bash
systemctl --user status agent-bots.service
```

## Управление

```bash
# Статус
systemctl --user status agent-bots.service

# Перезапуск (после изменения конфигов или обновления образа)
docker compose -f docker-compose.bots.yml down
docker build -t agent-shatrov:latest .
systemctl --user restart agent-bots.service

# Логи Docker контейнеров
docker compose -f docker-compose.bots.yml logs -f --tail=100

# Остановить автозапуск
systemctl --user disable agent-bots.service

# Полностью убрать
systemctl --user stop agent-bots.service
rm ~/.config/systemd/user/agent-bots.service
systemctl --user daemon-reload
```

## Решение проблем

- **Контейнеры не запускаются**: `journalctl --user -u agent-bots.service -f`.
- **Mattermost недоступен**: проверьте, что Mattermost слушает на `0.0.0.0` или на IP хоста.
- **Ollama недоступен**: проверьте `ss -tlnp | grep 11434` → должно быть `*:11434`.
- **pi "Connection error"**: проверьте `~/.pi/agent/models.json` → `baseUrl` должен быть `http://host-gateway:11434/v1`.
- **pi "Failed to load extension"**: версия pi в Dockerfile не совпадает с хостом. Обновите Dockerfile.
- **pi read-only file system**: `~/.pi` монтирована как `:ro`. Исправьте на `:rw` в `docker-compose.bots.yml`.
- **Git не работает**: проверьте SSH-ключи и права. `docker exec agent-shatrov-analyst-1 git ls-remote git@github.com:...`.
- **Изменения в service file**: после правки `systemctl --user daemon-reload`.

Полное руководство по e2e тестированию: [`docs/e2e-testing.md`](e2e-testing.md).