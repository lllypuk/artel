# Headless Setup — автозапуск ботов через systemd + Docker

Инструкция для запуска agent-shatrov в headless-режиме (без монитора, автозапуск при загрузке).
3 бота (аналитик, разработчик, тестер) в Docker-контейнерах, общаются через Mattermost.

## Предварительные требования

1. **Docker** установлен и работает.
2. **docker compose** plugin доступен.
3. **Git-репозиторий** agent-shatrov склонирован в `~/Project/agent-shatrov`.
4. **Mattermost** сервер доступен по сети (например, на хосте, порт 8065).
5. **Ollama** (или LLM API) доступен по сети (например, на хосте, порт 11434).
6. **pi** — конфиг в `~/.pi/` для Ollama endpoint.

## Шаг 1. Enable linger для пользователя

Чтобы user-level systemd сервисы запускались до логина:

```bash
loginctl enable-linger $USER
```

Проверьте:
```bash
loginctl show-user $USER | grep Linger
# Linger=yes
```

## Шаг 2. Установить и включить Docker

```bash
# Убедитесь, что Docker установлен и демон запущен
sudo systemctl enable docker
sudo systemctl start docker

# Добавьте пользователя в группу docker (если ещё не в группе)
sudo usermod -aG docker $USER
# Перелогиньтесь, чтобы изменения группы вступили в силу
```

## Шаг 3. Установить systemd service

```bash
# Копируем service file в user systemd directory
mkdir -p ~/.config/systemd/user/
cp ~/Project/agent-shatrov/scripts/agent-bots.service ~/.config/systemd/user/

# Перезагрузить systemd, включить и запустить
systemctl --user daemon-reload
systemctl --user enable agent-bots.service
systemctl --user start agent-bots.service
```

Проверьте статус:
```bash
systemctl --user status agent-bots.service
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
- Убедитесь, что `server_url: "http://host-gateway:8065"` указывает на Mattermost.
- Убедитесь, что `base_url: "http://host-gateway:11434/v1"` указывает на Ollama.

> `host-gateway` — это специальный Docker hostname, указывающий на хост-машину.
> Он работает благодаря `extra_hosts: ["host-gateway:host-gateway"]` в `docker-compose.bots.yml`.

## Шаг 5. Проверитьpi конфиг

pi использует `~/.pi/` для своих настроек (Ollama endpoint и т.д.).
Эта директория монтируется в контейнер: `~/.pi:/home/agent/.pi:ro`.

Убедитесь, что в `~/.pi/` есть нужные конфиги, например:
```bash
cat ~/.pi/config.yaml
# Должен содержать Ollama endpoint, модели и т.д.
```

ОБратите внимание, что адрес Ollama в `~/.pi/config.yaml` (если он использует
`localhost` или `127.0.0.1`) **не будет работать** из контейнера.
Замените на `http://host-gateway:11434` или используйте IP хоста.

## Шаг 6. SSH ключи

Контейнеры монтируют `~/.ssh:/home/agent/.ssh:ro` — SSH ключи хоста доступны в Docker.
Убедитесь, что ключи имеют права `chmod 600` и `~/.ssh` — `chmod 700`.

## Шаг 7. Проверка запуска

```bash
# Ручной запуск для проверки
cd ~/Project/agent-shatrov
docker compose -f docker-compose.bots.yml up -d

# Логи
docker compose -f docker-compose.bots.yml logs -f analyst

# Остановить
docker compose -f docker-compose.bots.yml down
```

Если всё работает, systemd поднимет контейнеры при загрузке автоматически.

## Управление

```bash
# Статус
systemctl --user status agent-bots.service

# Перезапуск (после изменения конфигов)
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

- **Контейнеры не запускаются**: проверьте `journalctl --user -u agent-bots.service -f`.
- **Mattermost недоступен**: проверьте, что Mattermost слушает на `0.0.0.0` или на IP хоста, а не только на `localhost`.
- **Ollama недоступен**: та же проверка + в `~/.pi/config.yaml` замените `localhost` на `host-gateway` или IP хоста.
- **Git не работает**: проверьте SSH-ключи и права доступа. Попробуйте `docker compose -f docker-compose.bots.yml exec analyst git ls-remote git@github.com:...`.
- **Изменения в service file**: после правки выполните `systemctl --user daemon-reload`.