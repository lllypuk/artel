# Переименование проекта agent-shatrov → artel (полный объём)

## Overview

Проект `agent-shatrov` (персональный AI-агент на Go с Telegram/Mattermost) переименовывается в **Artel**.
Текущее название — просто фамилия автора и не отражает суть. Новое название — русское слово *артель*
(самоорганизующийся кооператив мастеров без начальника), которое точно описывает архитектуру multi-agent
команды (analyst/developer/tester), координирующейся в Mattermost без оркестратора. Если проект взлетит,
слово «artel» может войти в западную агентскую лексику наряду с «orchestrator» / «swarm».

**Тэглайн:** *A cooperative of AI agents, each with its own craft.*

**Объём: полный** — переименование до конца: Go-модуль, бинарник, директория проекта, Docker-образы,
systemd-юнит, конфиги, документация, GitHub-репозиторий.

## Context (from discovery)

- **Go-модуль:** `github.com/lllypuk/agent-shatrov` (`go.mod`). Импорты в **93 файлах** ссылаются на этот path.
- **Бинарник:** `bin/agent` (Makefile `BINARY=bin/agent`), в Docker — `/usr/local/bin/agent`, в `pkill -x agent`.
- **Директория:** `/home/sasha/Project/agent-shatrov` → systemd `WorkingDirectory=%h/Project/agent-shatrov`.
- **Docker:** `docker-compose.bots.yml` — 3 сервиса (analyst/developer/tester) с `image: agent-shatrov:latest`.
  `docker-compose.yml` тоже упоминает `agent-shatrov`.
- **CI:** `.github/workflows/docker-publish.yml` — `ghcr.io/${{ github.repository_owner }}/agent-shatrov` + OCI label `agent-shatrov`.
- **Self-project detection:** `internal/tools/pi_coder.go` определяет self-project по совпадению пути с
  директорией агента (`os.Executable`/рабочая директория) — автоматически следует за переименованием директории.
- **Конфиги:** `config.example.yaml`, `configs/*-bot.yaml.example`, `docs/roles/*.yaml` содержат упоминания/пути.
- **Документация:** `README.md`, `CLAUDE.md`, `.claude/CLAUDE.md`, `DEPLOY.md`, `docs/*.md`, `tests/e2e/README.md`,
  `.github/copilot-instructions.md`.
- **Тесты:** ~30 `*_test.go` файлов с импортами `agent-shatrov` (чисто import-path, логики не касается).
- **Автозапуск:** systemd user-unit `scripts/agent-bots.service` поднимает Docker multi-bot; работает прямо сейчас.

## Development Approach

- **Подход:** Regular (механическое переименование, не feature-разработка; TDD неприменим).
- **Порядок:** сначала все кодовые/конфиг-переименования **внутри текущей директории** → `make lint test` →
  `make build` → smoke; затем переименование директории + systemd; затем Docker-образы; затем GitHub-repo.
- **Безопасность автозапуска:** переименование директории и systemd-юнит затрагивают рабочий автозапуск.
  Поэтому порядок «сначала код внутри старой директории, потом директория» гарантирует, что в любой момент
  есть рабочая сборка.
- **Тесты обязательны после каждого кодового шага:** `make lint test` + `make build`. E2E (минимум уровни
  1-2 по `docs/e2e-testing.md`) — после полного переименования.
- **Каждый чекбокс — сразу `[x]` после выполнения.** ➕ — новая задача, ⚠️ — блокер.

## Testing Strategy

- **Unit/lint:** `make lint test` после каждой задачи, затрагивающей `.go`/`go.mod`.
- **Build:** `make build` после задач, затрагивающих бинарник/Makefile/Dockerfile.
- **E2E (минимум уровни 1-2):**
  - Уровень 1 (smoke): `make build && go test ./...`; запуск `./bin/artel configs/analyst-bot.yaml` — бот
    стартует, WebSocket подключён, LLM отвечает (см. `docs/e2e-testing.md` «Быстрая проверка»).
  - Уровень 2 (pi_coder): self-project определяется под новым именем `artel`.
- **Полный e2e (уровни 1-4, Docker):** `docker compose -f docker-compose.bots.yml build` + запуск 3 ботов —
  по возможности; минимум уровни 1-2 обязателен.

## Progress Tracking
- `[x]` — выполнено; ➕ — новая задача; ⚠️ — блокер. План синхронизировать с фактом.

## Solution Overview

**Решение:** последовательное переименование всех поверхностей от кода к инфраструктуре, с проверкой
сборки/тестов между этапами. Не используется `go mod edit` + `sed` вслепую по всему дереву — импорты
меняются через `go mod edit -module github.com/lllypuk/artel` + `find ... -exec sed` по `*.go` с
последующей верификацией `go build ./...`. Бинарник `agent` → `artel` меняется в Makefile/Dockerfile/compose/
systemd одномоментно (он не упоминается в Go-коде по имени, только через `os.Executable`).

**Ключевые решения:**
1. Сначала переименовать код внутри `/home/sasha/Project/agent-shatrov`, доказать работоспособность,
   **потом** двигать директорию — иначе посреди переименования нет рабочей точки отката.
2. Переименование директории делать через `git mv` не получится (директория выше git-root) — это `mv`
   файловой системы + обновление systemd `WorkingDirectory`. Git-история внутри репо сохраняется.
3. GitHub-repo rename — отдельный ручной шаг (через gh/web), после него обновить `git remote origin` URL.

## Technical Details

- **Go module path:** `github.com/lllypuk/agent-shatrov` → `github.com/lllypuk/artel`.
  Команда: `go mod edit -module github.com/lllypuk/artel`.
  Импорты: `sed -i 's|github.com/lllypuk/agent-shatrov|github.com/lllypuk/artel|g'` по всем `*.go`.
- **Бинарник:** `agent` → `artel`. Затронутые файлы: `Makefile` (`BINARY=bin/agent`, `pkill -x agent` ×2),
  `Dockerfile` (`/app/agent`, `/usr/local/bin/agent`, `ENTRYPOINT`), `scripts/agent-bots.service` (без
  прямого упоминания бинарника, но `WorkingDirectory`), `docker-compose.bots.yml` (если упоминает).
- **Директория:** `mv /home/sasha/Project/agent-shatrov /home/sasha/Project/artel`.
- **Docker-образы:** `image: agent-shatrov:latest` → `image: artel:latest` (×3 в `docker-compose.bots.yml`,
  ×N в `docker-compose.yml`); `docker build -t agent-shatrov` → `artel` (Dockerfile/Makefile если есть тег).
- **systemd:** `WorkingDirectory=%h/Project/agent-shatrov` → `%h/Project/artel`; `Description=Agent-Shatrov ...`
  → `Description=Artel ...`. После `mv` директории: `systemctl --user daemon-reload` + `restart`.
- **CI:** `docker-publish.yml` — `images: ghcr.io/.../agent-shatrov` → `.../artel`; OCI label
  `org.opencontainers.image.title=agent-shatrov` → `artel`.
- **Документация:** тэглайн + сноска про «artel — a Russian word for a self-organizing cooperative of
  craftsmen» в `README.md` (顶部).
- **Self-project:** проверка, что после `mv` директории pi_coder определяет self-project как `artel`.

## What Goes Where

- **Implementation Steps** (`[ ]`): всё, что делается в этом репо — код, конфиги, доки, Dockerfile, systemd,
  Makefile, CI, директория.
- **Post-Completion** (без чекбоксов): GitHub-repo rename (ручной), обновление `git remote`, пересборка
  Docker-образов на сервере, smoke live-проверка автозапуска после перезагрузки.

## Implementation Steps

### Task 1: Переименовать Go-модуль и импорты

**Files:**
- Modify: `go.mod`
- Modify: все `*.go` (≈93 файла с импортами `github.com/lllypuk/agent-shatrov/...`)

- [ ] `go mod edit -module github.com/lllypuk/artel`
- [ ] `grep -rl "github.com/lllypuk/agent-shatrov" --include="*.go" | xargs sed -i 's|github.com/lllypuk/agent-shatrov|github.com/lllypuk/artel|g'`
- [ ] `go build ./...` — убедиться, что все импорты резолвятся
- [ ] `make lint test` — должно быть чисто
- [ ] зафиксировать: `git commit -m "refactor: rename go module agent-shatrov → artel"`

### Task 2: Переименовать бинарник agent → artel (Makefile, Dockerfile)

**Files:**
- Modify: `Makefile`
- Modify: `Dockerfile`

- [ ] `Makefile`: `BINARY=bin/agent` → `bin/artel`; `pkill -x agent` → `pkill -x artel` (×2)
- [ ] `Dockerfile`: `/app/agent` → `/app/artel`; `COPY --from=builder /app/agent` → `/app/artel`;
      `/usr/local/bin/agent` → `/usr/local/bin/artel` (×2: COPY + ENTRYPOINT); `WORKDIR /home/agent` → `/home/artel`
- [ ] `make build` — `bin/artel` создаётся
- [ ] `make lint test` — чисто
- [ ] smoke: `./bin/artel --help` (или запуск с конфигом) стартует
- [ ] `git commit -m "refactor: rename binary agent → artel"`

### Task 3: Переименовать Docker-образы и compose-сервисы

**Files:**
- Modify: `docker-compose.bots.yml`
- Modify: `docker-compose.yml`
- Modify: `.github/workflows/docker-publish.yml`

- [ ] `docker-compose.bots.yml`: `image: agent-shatrov:latest` → `image: artel:latest` (×3)
- [ ] `docker-compose.yml`: все упоминания `agent-shatrov` → `artel`
- [ ] `docker-publish.yml`: `images: ghcr.io/.../agent-shatrov` → `.../artel`; OCI label `agent-shatrov` → `artel`
- [ ] `docker compose -f docker-compose.bots.yml config` — валидируется без ошибок
- [ ] `docker compose -f docker-compose.bots.yml build` — образ `artel:latest` собирается (уровень 4 e2e, по возможности)
- [ ] `git commit -m "refactor: rename docker image agent-shatrov → artel"`

### Task 4: Обновить конфиги и роли

**Files:**
- Modify: `config.example.yaml`
- Modify: `configs/*-bot.yaml.example`
- Modify: `docs/roles/*.yaml`

- [ ] `grep -rn "agent-shatrov" config.example.yaml configs/ docs/roles/` → заменить на `artel` (пути/упоминания)
- [ ] убедиться, что `agent-shatrov` как self-project-имя в примерах конфигов заменено на `artel`
- [ ] `make lint test` — чисто (конфиги не влияют на сборку, но проверка целостности)
- [ ] `git commit -m "refactor: rename config/role references agent-shatrov → artel"`

### Task 5: Обновить документацию (README, CLAUDE.md, docs, тэглайн, сноска)

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `.claude/CLAUDE.md`
- Modify: `DEPLOY.md`
- Modify: `docs/*.md` (упоминания)
- Modify: `tests/e2e/README.md`, `.github/copilot-instructions.md`

- [ ] `README.md`: заголовок → `# Artel`; под заголовком тэглайн *«A cooperative of AI agents, each with its own craft.»*;
      сноска/абзац: *«Artel — a Russian word for a self-organizing cooperative of craftsmen»*; все `agent-shatrov` → `artel`
- [ ] `CLAUDE.md` + `.claude/CLAUDE.md`: заголовок `# agent-shatrov` → `# Artel`; упоминания → `artel`
- [ ] `DEPLOY.md`, `docs/*.md`, `tests/e2e/README.md`, `.github/copilot-instructions.md`: `agent-shatrov` → `artel`
- [ ] `grep -rn "agent-shatrov" --include="*.md" .` (вне `docs/plans/`) — пусто
- [ ] `git commit -m "docs: rebrand to Artel + tagline + cultural note"`

### Task 6: Переименовать директорию проекта + обновить systemd-юнит

⚠️ Затрагивает рабочий автозапуск. Выполнять после Task 1-5 (есть рабочая сборка в старой директории).

**Files:**
- Modify: `scripts/agent-bots.service`
- Filesystem: `/home/sasha/Project/agent-shatrov` → `/home/sasha/Project/artel`

- [ ] `scripts/agent-bots.service`: `WorkingDirectory=%h/Project/agent-shatrov` → `%h/Project/artel`;
      `Description=Agent-Shatrov ...` → `Description=Artel ...`
- [ ] Остановить рабочий автозапуск: `systemctl --user stop agent-bots.service`
- [ ] `cd /home/sasha/Project && git -C agent-shatrov status --porcelain` — рабочее дерево чистое (или закоммичено)
- [ ] `mv /home/sasha/Project/agent-shatrov /home/sasha/Project/artel`
- [ ] `cd /home/sasha/Project/artel && make build` — собирается из нового пути
- [ ] `make lint test` — чисто из нового пути
- [ ] `systemctl --user daemon-reload && systemctl --user start agent-bots.service`
- [ ] проверить: `systemctl --user status agent-bots.service` — контейнеры поднялись
- [ ] `git commit` (внутри нового пути) `scripts/agent-bots.service`: `refactor: systemd unit path → artel`

### Task 7: Верификация — e2e минимум уровни 1-2

- [ ] Уровень 1 (smoke): `make build && go test ./...` — все тесты зелёные
- [ ] Уровень 1 (smoke): `./bin/artel configs/analyst-bot.yaml &; sleep 3; tail -3 data/analyst-bot.log; pkill -f "bin/artel"` — бот стартует, лог чистый
- [ ] Уровень 2 (pi_coder): убедиться, что self-project определяется как `artel` (а не `agent-shatrov`)
- [ ] Уровень 4 (Docker, по возможности): `docker compose -f docker-compose.bots.yml up -d` — 3 бота стартуют
- [ ] `grep -rn "agent-shatrov" . --exclude-dir=docs/plans` — пусто (кроме исторических plans/completed)

### Task 8: Финальная документация и закрытие

- [ ] обновить `docs/backlog.md`: отметить переименование (не эпик, заметка в шапке или логе)
- [ ] проверить `make lint test` финально
- [ ] переместить этот план в `docs/plans/completed/` (внутри нового пути `artel`)
- [ ] `git commit -m "docs: finalize rename to artel"`

## Post-Completion

**Ручные шаги (внешние, без чекбоксов):**

**GitHub:**
- Переименовать репозиторий `agent-shatrov` → `artel` через `gh repo rename artel` (или Settings на web).
- Обновить remote: `git remote set-url origin git@github.com:lllypuk/artel.git`.
- После rename CI `docker-publish.yml` будет пушить образ в `ghcr.io/lllypuk/artel` (путь обновлён в Task 3).

**Сервер / автозапуск:**
- На headless-сервере (если не эта машина): повторить `mv` директории + `systemctl --user daemon-reload` + restart.
- Пересобрать Docker-образы: `docker compose -f docker-compose.bots.yml build` (старый образ `agent-shatrov:latest` можно удалить: `docker rmi agent-shatrov:latest`).
- Smoke live-проверка после перезагрузки: `systemctl --user status agent-bots.service` + логи 3 ботов.

**Прочее:**
- Внешние ссылки на старый репо (закладки, `pi_coder.projects` путями на других машинах, `devbots`-донор)
  обновить вручную.
- Старый GitHub-repo после rename автоматически редиректит URL/clone — но лучше обновить ссылки.