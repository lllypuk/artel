# Plan: Мультипроектная разработка через pi_coder

**Дата:** 2026-07-08
**Статус:** Ready for implementation

## Контекст

agent-shatrov уже имеет multi-agent режим (Mattermost) и инструмент `pi_coder` для
модификации собственного кода. Сейчас `pi_coder` жёстко завязан на один `project_dir`.

Цель: боты могут анализировать, разрабатывать и тестировать **внешние проекты** через pi.
Список проектов задаётся в конфиге. Future: Docker-изоляция ботов.

Дизайн: [brainstorm session, validated sections 1–5]

Валидация: `make lint && make test && make build`. Тестирование через Ollama cloud models (qwen3.5:cloud).

## Tasks

### Task 1: Config — убрать `project_dir`, добавить `projects`

**Файлы:** `internal/config/config.go`, `internal/config/config_test.go`, `config.example.yaml`

- [x] Убрать поле `ProjectDir` из `PiCoderConfig`
- [x] Добавить структуру `ProjectConfig{Name, Path, Model, TimeoutSec}`
- [x] Добавить поле `Projects []ProjectConfig` в `PiCoderConfig`
- [x] Валидация: `enabled: true` без `projects` → ошибка
- [x] Валидация: каждый проект требует `name` и `path`
- [x] Валидация: имена проектов уникальны
- [x] Обновить `config.example.yaml`: секция `pi_coder` с `projects`
- [x] Тесты: парсинг `projects`, ошибки валидации

### Task 2: Package `internal/projects` — реестр

**Файлы:** `internal/projects/projects.go`, `internal/projects/projects_test.go` (новые)

- [x] Структура `Project{Name, Path, Model, TimeoutSec, SelfProject}`
- [x] Структура `Registry{projects map, order []string}`
- [x] `NewRegistry(configProjects, agentDir)` — резолв путей, автоопределение self, проверка существования директорий
- [x] `Get(name) (*Project, error)` — error со списком доступных имён при miss
- [x] `Names() []string` — стабильный порядок из конфига
- [x] `List() []*Project`
- [x] Тесты: валидные проекты, несуществующая директория (исключается), `"."` → SelfProject, дубликат имён, Get miss

### Task 3: PiCoderTool — зависимость от Registry

**Файлы:** `internal/tools/pi_coder.go`, `internal/tools/pi_coder_test.go`

- [x] `PiCoderTool` хранит `*projects.Registry`, `defaultModel`, `defaultTimeout`
- [x] `PiCoderConfig` (tool config) меняется: `Registry`, `DefaultModel`, `DefaultTimeout`, `RestartFunc`
- [x] `Parameters()`: `project` — обязательный, `enum` из `registry.Names()`
- [x] `Description()`: включает список имён проектов, указание какой self
- [x] `Execute()`: найти проект по имени → определить model/timeout (per-project override → default) → запустить pi в `project.Path`
- [x] `Execute()`: non-self + `restart=true` → игнорировать, предупреждение в выводе
- [x] `Execute()`: non-self + `rebuild=true` → игнорировать, предупреждение в выводе
- [x] `Execute()`: self + `rebuild`/`restart` → как сейчас (make build / syscall.Exec)
- [x] Убрать старые поля `projectDir`, `model`, `timeout` из структуры
- [x] Тесты: valid project → pi в правильной директории; unknown project → error; non-self + restart → игнор; per-project model override; project обязателен

### Task 4: Wiring — tools.go + main.go

**Файлы:** `cmd/agent/tools.go`, `cmd/agent/main.go`

- [x] `registerPiCoderTool` создаёт `projects.Registry`, передаёт в `PiCoderTool`
- [x] Вычислить `agentDir = filepath.Dir(configPath)` где configPath — первый аргумент или `"config.yaml"`
- [x] Передать `agentDir` в `registerOptionalTools` → `registerPiCoderTool` → `NewRegistry`
- [x] Убрать старый `projectDir` fallback

### Task 5: Документация — README + CLAUDE.md + config.example.yaml

**Файлы:** `README.md`, `CLAUDE.md`, `config.example.yaml`

- [x] Обновить секцию про `pi_coder` в README (новый формат конфига)
- [x] Обновить таблицу инструмента pi_coder
- [x] Обновить config.example.yaml комментарии
- [x] Обновить CLAUDE.md если есть упоминания pi_coder config

### Task 6: Валидация — lint, test, build

- [ ] `make lint` — без ошибок
- [ ] `make test` — все unit-тесты проходят
- [ ] `make build` — собирается

### Task 7: E2E артефакты — конфиги, скрипты, сценарии

**Файлы:** `tests/e2e/config-*-e2e-pi.yaml.example`, `tests/e2e/e2e_pi_test.sh` (новые)

- [ ] `config-analyst-e2e-pi.yaml.example` — аналитик, pi_coder enabled, projects: agent-shatrov + test-проект
- [ ] `config-developer-e2e-pi.yaml.example` — разработчик, pi_coder enabled, projects: agent-shatrov + test-проект
- [ ] `config-tester-e2e-pi.yaml.example` — тестировщик, pi_coder enabled, projects: agent-shatrov + test-проект

Каждый конфиг:
```yaml
pi_coder:
  enabled: true
  model: "qwen3.5:cloud"          # Ollama cloud model по умолчанию
  timeout: 300
  projects:
    - name: "agent-shatrov"
      path: "."
    - name: "test-project"
      path: "/home/sasha/Project/test-project"
      model: "qwen3.5:cloud"     # можно переопределить per-project
      timeout: 600
```

- [ ] Скрипт `e2e_pi_test.sh`: post `@developer-bot анализируй проект test-project через pi_coder` → waitread 60 20
- [ ] Скрипт: post `@analyst-bot какие проекты доступны` → проверка enum/Description
- [ ] Скрипт: post `@developer-bot открой agent-shatrov, добавь комментарий` → self-project
- [ ] Скрипт: post `@tester-bot запусти тесты test-project` → pi_coder test-project
- [ ] Скрипт: проверка игнора rebuild/restart для non-self проектов
- [ ] Сценарии координации: аналитик → разработчик → тестировщик → цикл
- [ ] Проверка safeguards (loop detection) при долгом bot-to-bot диалоге
- [ ] Запуск E2E тестов вручную (manual test — требует запущенный Mattermost + Ollama + pi)
- [ ] Отчёт `REPORT-pi-YYYY-MM-DD.md` по образцу `REPORT-2026-07-07.md`

## Зависимости

Tasks 1-2 не зависят друг от друга. Task 3 зависит от 1 и 2.
Task 4 зависит от 3. Task 5 после 1-4. Task 6 после всех. Task 7 после 6.

## Риски

- Pi может долго работать (timeout) — per-project timeout решает
- Пути в конфиге могут быть относительными — резолв через agentDir
- Несуществующие директории — Registry исключает, не блокирует старт
- Bot-to-bot координация требует правильных system_prompt для ролей
- E2E требует запущенного Mattermost + Ollama (cloud models, напр. qwen3.5:cloud) через OpenAI-compatible endpoint `http://localhost:11434/v1` + установленного pi