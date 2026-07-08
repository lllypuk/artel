# Plan: Боты в Docker — автозапуск всех проектов

**Дата:** 2026-07-08
**Статус:** Ready for implementation

## Контекст

3 бота (аналитик, разработчик, тестер) в Docker-контейнерах с автозапуском.
Общение только через Mattermost. Доступ ко всем проектам в ~/Project/ через
автообнаружение. Headless — без монитора, в автозапуске через systemd.

Раздельные модели: агент qwen3.5:cloud, pi_coder kimi-k2.7-code:cloud (без конфликтов Ollama).

Дизайн: brainstorm session, validated sections 1–6.
Валидация: `make lint && make test && make build`.

## Tasks

### Task 1: Auto-discovery — config + Registry

**Файлы:** `internal/config/config.go`, `internal/config/config_test.go`, `internal/projects/projects.go`, `internal/projects/projects_test.go`

- [x] Добавить поля `ProjectsDir string` и `ExcludeProjects []string` в `PiCoderConfig`
- [x] В `NewRegistry`: если `ProjectsDir` задан — сканировать каталог на git-репозитории (os.ReadDir → .git)
- [x] Автообнаруженные проекты: name = basename(path), path = abs(path)
- [x] Exclude: пропускать проекты из `ExcludeProjects` по имени
- [x] Явные `Projects` из конфига переопределяют автообнаруженные (если имя совпадает — берётся явный)
- [x] Обновить `config.example.yaml`: секция pi_coder с `projects_dir` и `exclude_projects`
- [x] Тесты: автообнаружение 3 git-репозиториев, exclude работает, объединение с явными проектами

### Task 2: Dockerfile — multi-stage с pi

**Файлы:** `Dockerfile`, `.dockerignore`

- [ ] Stage 1: golang:1.26-alpine — сборка agent binary + копирование Go toolchain для runtime
- [ ] Stage 2: alpine:3.20 — nodejs, npm, git, openssh-client, gcc, musl-dev, ca-certificates
- [ ] Копировать Go toolchain в stage 2 (для go test/build внешних проектов)
- [ ] Установить pi: `npm install -g @earendil-works/pi-coding-agent`
- [ ] Копировать agent binary из stage 1
- [ ] WORKDIR /home/agent
- [ ] Проверить `docker build .` проходит

### Task 3: docker-compose.bots.yml

**Файлы:** `docker-compose.bots.yml`

- [ ] 3 сервиса: analyst, developer, tester
- [ ] Каждый: `build: .`, `restart: always`, `command: ["/usr/local/bin/agent", "/config.yaml"]`
- [ ] Общие volumes: `~/Project:/home/projects:rw`, `~/.ssh:/home/agent/.ssh:ro`, `~/.pi:/home/agent/.pi:ro`
- [ ] Per-service: свой config.yaml (:ro), свой named volume для data
- [ ] `extra_hosts: ["host-gateway:host-gateway"]`
- [ ] Environment: GIT_AUTHOR_NAME, GIT_AUTHOR_EMAIL, GIT_COMMITTER_*, LLM_BASE_URL, OLLAMA_BASE_URL, HOME=/home/agent
- [ ] 3 named volumes: analyst_data, developer_data, tester_data

### Task 4: Конфиги ботов

**Файлы:** `configs/analyst-bot.yaml.example`, `configs/developer-bot.yaml.example`, `configs/tester-bot.yaml.example`, `configs/.gitignore`

- [ ] analyst-bot.yaml.example: mattermost + role analyst + llm qwen3.5:cloud + pi_coder kimi-k2.7-code:cloud + projects_dir /home/projects
- [ ] developer-bot.yaml.example: role developer + те же настройки
- [ ] tester-bot.yaml.example: role tester + те же настройки
- [ ] Все с плейсхолдерами для токенов (<analyst-bot-token> и т.д.)
- [ ] server_url: "http://host-gateway:8065", base_url: "http://host-gateway:11434/v1"
- [ ] pi_coder.projects_dir: "/home/projects", exclude_projects: ["test-project","nanoGPT"]
- [ ] database.path: "/home/agent/data/agent.db", logging.file: "/home/agent/data/agent.log"
- [ ] configs/.gitignore: реальные конфиги без .example

### Task 5: systemd service + автозапуск

**Файлы:** `scripts/agent-bots.service`, `docs/headless-setup.md`

- [ ] agent-bots.service: systemd user unit, oneshot + RemainAfterExit, ExecStart=docker compose up -d
- [ ] WantedBy=default.target
- [ ] docs/headless-setup.md: инструкция по установке (enable-linger, enable docker, install service, configs, tokens)
- [ ] Обновить README: секция "Docker multi-bot" с ссылкой на headless-setup.md

### Task 6: Валидация — lint, test, build, docker

- [ ] `make lint` — без ошибок
- [ ] `make test` — все unit-тесты проходят
- [ ] `make build` — собирается
- [ ] `docker build -t agent-shatrov .` — образ собирается
- [ ] `docker compose -f docker-compose.bots.yml config` — конфиг валиден

## Зависимости

Task 1 (auto-discovery) независим — можно делать первым.
Tasks 2-3 (Dockerfile + compose) зависят от 1 (нужно projects_dir в config).
Task 4 (конфиги) зависит от 1.
Task 5 (systemd) независимо от кода.
Task 6 валидация — после всех.

## Риски

- Размер Docker образа (~500MB): Go + Node.js + pi + git. Приемлемо для локального использования.
- SSH agent forwarding в Docker: может потребовать ssh-agent на хосте + монтирование сокета.
- pi config в Docker: нужен ~/.pi с Ollama endpoint, можно смонтировать с хоста.
- Права доступа: volume ~/Project:rw — pi может модифицировать любой проект (изоляция позже через Docker user namespaces).