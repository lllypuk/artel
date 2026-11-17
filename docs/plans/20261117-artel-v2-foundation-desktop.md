# Artel v2: чистый каркас и нативная панель проектов и задач

## Overview

Заменить прежнего Telegram/Mattermost-бота локальной панелью управления разработкой: **Go API + SQLite + самостоятельное C++/Qt Quick приложение**. Первый законченный срез — пользователь вручную создаёт проект, задачи, составляет очередь, меняет состояние работы и видит историю; данные сохраняются при закрытии UI и перезапуске сервера. Два клиента не перезаписывают изменения друг друга молча.

План составлен по поручению владельца 2026-11-17. **Запускает ralphex сам владелец. Создание этого файла не запускает реализацию.** План предназначен для автономного последовательного выполнения после запуска, без необходимости читать историю чата.

Входит: очистка legacy внутри репозитория, обновление инструкций и сборки, локальная авторизация, API-контракт, проекты/задачи/очередь/история, нативный UI, reconnect, тесты, systemd unit и Arch-упаковка как файлы поставки.

Не входит: Pi bridge и agterm-адаптер, запуск/наблюдение агентов, LLM, чат, вопросы и разрешения владельцу, проверки кода/MR/CI в продукте, автоматический scheduler, Git-операции над каталогами проектов, Android, macOS-порт, mini/Caddy, Docker, публикация релиза, установка пакета и включение сервиса на машине владельца.

Будущие Лид, Продакт, разработчик и технический ревьюер — разные Pi-роли. Прямое согласование разработчик ↔ Продакт сохранено в архитектуре, но **не реализуется в этом срезе**. Не добавлять фиктивных агентов, сообщений и индикаторов online.

## Context (from discovery)

Репозиторий: `github.com/lllypuk/artel`, исходный HEAD при планировании `f3ba07c`. Проверять актуальный diff перед началом, не сбрасывать чужие изменения.

Существующее состояние:

- `cmd/agent/`, всё существующее `internal/` — персональный бот и его инструменты; архитектуры новой панели там нет.
- `config.example.yaml`, `configs/`, Dockerfile/compose, `scripts/`, `tests/e2e/`, старые руководства и `.github/workflows/docker-publish.yml` обслуживают прежний продукт.
- `Makefile` запускает `go fix` внутри lint и имеет опасные для нового процесса `pkill -x artel`/nohup targets. Заменить, не переносить.
- `go.mod`: Go **1.26.3**; `.github/workflows/ci.yml`: golangci-lint **2.10.1**. С ними исходный `make lint test` прошёл. Host Go 1.27.3 и lint 2.15.0 не являются проверенной комбинацией проекта.
- Qt/qmake на машине: **6.12.0**; Qt tools лежат в `/usr/lib/qt6/bin`, не все доступны через PATH. План использует Qt **6.8+** и C++17; не обещает совместимость с более старыми Qt. g++, make, makepkg, desktop-file-validate и systemd-analyze доступны. **clang-format в PATH не найден**: до запуска владелец должен предоставить его (Arch package `clang`) либо разрешить отдельную подготовку среды; исполнитель не пропускает эту проверку и не устанавливает host packages сам.
- `ralphex` доступен. Соседней панели для консультации нет; split не создавался.
- В репозитории уже есть новые, пока untracked, `docs/architecture/` и `docs/experiments/`. Это материалы владельца/текущей работы, сохранить и включить в результат, не считать мусором.
- На момент проверки отсутствуют `data/`, `config.yaml`, `.env`, реальные `configs/{analyst,developer,tester}-bot.yaml`. Содержимое секретных конфигов не читалось.

Основные материалы (пути от корня репозитория, сохраняют смысл после перемещения этого плана в completed):

- `docs/architecture/20261117-artel-v2-direction.md` — решения владельца, границы и roadmap.
- `docs/architecture/20261117-artel-v2-user-flow.md` — информационная структура экранов; использовать только относящееся к ручному срезу.
- `docs/architecture/20261117-artel-v2-pi-bridge.md`, `20261117-artel-v2-agterm-adapter.md`, `20261117-artel-v2-pi-experiment-01.md` в той же папке — будущая интеграция, не расширение текущего scope.
- `docs/experiments/pi-bridge/` — сохранить воспроизводимый эксперимент; не включать Pi как runtime-зависимость нового приложения.
- Навык Omarchy: `/home/sasha/.pi/agent/skills/omarchy-app/SKILL.md` и `templates.md`. Прочитать перед Qt-реализацией; при копировании шаблонов сохранить MIT attribution, не заменять владельца существующего LICENSE.

## Development Approach

- Вести разработку в отдельной feature-ветке; не выполнять main push, merge, deploy или запуск сервисов владельца. Коммиты делает исполнитель в рамках ralphex; этот план не требует самостоятельной публикации MR.
- Исполнение и агентные ревью — через Pi, согласно решению владельца. Модель/провайдера и настройки ralphex выбирает владелец при запуске; не менять глобальные profiles/config и не запускать параллельного writer. Если выбранный runner противоречит этому условию — сообщить blocker, не подменять харнес скрыто.
- Задачи выполнять последовательно. **Каждая задача с изменениями кода включает новые/обновлённые тесты успешных и ошибочных путей.** Сначала тесты доменных инвариантов/конкурентности (TDD); Qt — тесты viewmodel и реального QML в той же задаче.
- Завершить задачу и её обязательные проверки до следующей. Красную проверку не помечать зелёной и не обходить отключением линтера/теста. Отсутствующую зависимость/нехватку прав показать как blocker.
- Не поддерживать совместимость с ботом и не переносить его историю. Сохранить Git-историю; новая чистая БД.
- Инструкции нового продукта, команды проверок и начальный компилируемый каркас вводятся **одной первой задачей**, чтобы удаление бота не оставило сломанный репозиторий.
- Не строить плагины, универсальный workflow engine, очереди доставки сообщений или таблицы будущих ролей «на вырост». Текущие данные + append-only events + receipts идемпотентных HTTP-мутаций достаточны. Outbox доставки появится вместе с bridge.
- Отметки `[x]` ставить после фактического выполнения. Изменение scope записывать в план; новый внешний контракт/операционные полномочия не придумывать.

## Technical Details

### Структура и владение состоянием

```text
cmd/arteld/                 Go сервер
cmd/artelctl/               минимальная CLI диагностики/чтения
internal/domain/            Project, Task, переходы и ошибки
internal/app/               транзакционные сценарии
internal/store/             SQLite, migrations, events, mutation receipts
internal/api/               HTTP handlers, middleware, SSE
internal/local/             пути, owner credential, single-instance lock
api/openapi.yaml            контракт v1
desktop/
  artel.pro                 qmake, TARGET=artel
  src/                      C++ модели/клиент, QML, theme/translator adapters
  tests/                    Qt Test / QML integration harness
  bin/build, bin/test        локальная сборка и проверки
packaging/arch/              PKGBUILD, artel.desktop, artel.svg
packaging/systemd/           arteld.service
scripts/                    check, desktop-lint, e2e, package-check
tests/e2e/                  новый native/API e2e, не старые bot scripts
```

`arteld` владеет SQLite. Desktop/CLI не открывают БД и не пишут туда напрямую. Закрытие `artel` не завершает сервер; UI не вызывает systemctl и не запускает сервер скрыто.

Linux defaults: data `${XDG_DATA_HOME:-$HOME/.local/share}/artel/v2`, credential `${XDG_CONFIG_HOME:-$HOME/.config}/artel/local-owner.token`. Независимые явные `--data-dir`, `--token-file`, `--listen`; тесты всегда передают временные пути. Не выводить data path из cwd. Платформенные пути/темы/сервис вынести из домена и QML; macOS сейчас не реализовывать.

SQLite: versioned migrations, foreign_keys, busy_timeout, WAL, synchronous=FULL, транзакции мутация+event+receipt; сервер один на data-dir (неблокирующий lock удерживается весь срок жизни). Ошибка migration/lock/IO завершает запуск без пересоздания БД. Не открывать legacy DB; несоответствующая схема — диагностическая ошибка. UTC RFC3339 timestamps, глобальный возрастающий event sequence, не порядок по часам клиента. Это не обещание абсолютной сохранности при отказе hardware.

### Локальная авторизация

- HTTP/JSON только на числовом IPv4 loopback: default `127.0.0.1:7331`, для тестов `127.0.0.1:0`. Нелокальный bind отклонять; не добавлять remote режим.
- `arteld` при первом запуске создаёт случайный owner token минимум 32 байта в private-файле, существующий не перезаписывает. Parent 0700, token 0600; создание atomic/exclusive, запрет symlink и небезопасных прав. Не читать содержимое существующих чужих credentials.
- Token читается desktop/CLI через локальный C++/Go adapter, не попадает в QML properties, URL, stdout, логи, screenshot или Git. Передаётся `Authorization: Bearer` также для SSE. Постоянного token в QSettings нет. Секретный файл — локальный bootstrap одного владельца, не будущая схема удалённых device credentials.
- Все `/v1/*` требуют token, включая snapshot/events; `/healthz` без auth возвращает только liveness, без путей/версий задач. Сравнение секрета constant-time. Никакого CORS `*`; браузерные Origin-запросы на API отклонять. Ограничить Host локальным адресом/фактическим портом, запретить redirect с credential на стороне клиента.
- Нет shell/произвольного чтения файлов/registration API. Один OS UID с полным доступом к файлам не является изолированным adversary — это не sandbox.
- Desktop default endpoint совпадает с daemon; `--endpoint` допускает только HTTP `127.0.0.1:<port>` в этом срезе. Ошибка credential/сервера показана явно, без fallback на demo.

### Домен и очередь

**Project:** UUID, name (1–120 символов), root_path (абсолютный путь рабочего каталога), description (до 16 KiB), version, queue_version, archived, created_at/updated_at. Один проект может включать несколько репозиториев; git-init/clone/scanning не делать. При создании/изменении пути проверить существование каталога и canonical path, запретить дубликат canonical root у неархивированных проектов. Исчезновение каталога позже не удаляет задачи: показывать предупреждение path unavailable.

**Task:** UUID, project_id (неизменяемый), title (1–200), description и acceptance_criteria (каждое до 64 KiB), state, version, created_at/updated_at. Только текстовые поля: не исполнять Markdown/HTML/ссылки как команды. Внешнего assignee/run сейчас нет.

Состояния:

| Переход | Правило |
|---|---|
| создание → backlog | задача ещё не стоит в очереди |
| backlog ↔ queued | через замену очереди проекта |
| queued → in_progress | только первый элемент; не более одной in_progress в проекте |
| in_progress → done или backlog | ручная отметка владельца, никакой агент не запускается/останавливается |
| backlog/queued/in_progress → cancelled | ручная отмена; из queued атомарно убрать из очереди |
| done/cancelled → backlog | явное переоткрытие |

`done` в UI называется **«Завершено вручную»**, не «MR готов», не «проверено» и не «агент завершил». Все изменения имеют источник `owner_manual`. Отмена/начало работы — только учёт, не операционный сигнал.

Очередь — полный упорядоченный список queued task IDs данного проекта. PUT очереди может включать backlog для добавления и исключать queued для возврата в backlog; нет чужих, дублирующихся или terminal/in_progress IDs. Неуказанные backlog не изменяются. Один атомарный update, новая queue_version; версии задач увеличиваются при изменении их state. Чистый reorder меняет queue_version и event, но не содержание задач. Переход из queued тоже меняет queue_version. No-op с актуальной версией не создаёт event/новую версию.

Ручное редактирование задачи не меняет её проект/state. Архив проекта разрешён только без queued/in_progress; архивные задачи доступны для чтения, все изменения их/очереди отклоняются. Unarchive повторно проверяет уникальность root. Hard delete проектов/задач не нужен.

### API и конфликтность

Записать эти операции в `api/openapi.yaml`; JSON errors `{code,message,current_version?}` без внутренних SQL/path/token. HTTP 400 — плохой запрос, 401 — credential, 404 — объект, 409 — версия/инвариант, 413 — лимит, 500 — безопасная ошибка. Reject unknown JSON fields, trailing JSON и oversized body (256 KiB); строковые лимиты проверять до записи.

| Method/path | Назначение |
|---|---|
| GET `/healthz` | liveness |
| GET `/v1/snapshot` | полный согласованный snapshot проектов/задач/очередей + event_seq в одной read transaction |
| POST `/v1/projects` | создать проект |
| PATCH `/v1/projects/{id}` | name/root/description/archived + expected_version |
| POST `/v1/projects/{id}/tasks` | создать backlog task |
| PATCH `/v1/tasks/{id}` | текстовые поля + expected_version |
| POST `/v1/tasks/{id}/transition` | целевое state + expected_version; при выходе из queued также expected_queue_version |
| PUT `/v1/projects/{id}/queue` | ordered task_ids + expected_queue_version |
| GET `/v1/events?instance_id=UUID&after=N&limit=100` | история; optional project_id/task_id filters, limit 1–500, next cursor учитывает просмотренный диапазон |
| GET `/v1/stream?instance_id=UUID&after=N` | SSE invalidation по сохранённому event log |

Мутации требуют UUID `Idempotency-Key`. Receipt хранит key, method/path, canonical validated payload hash, HTTP status/body. Повтор одинакового успешного запроса возвращает прежний результат **до** повторной проверки expected_version; иной payload с тем же key — 409. Receipt, domain mutation и event коммитятся вместе. Неуспешный запрос не оставляет частично изменённое состояние. В этом срезе receipts/events не очищаются автоматически. Повтор после потерянного ответа использует тот же key; пользовательское редактирование создаёт новый key.

Для optimistic concurrency нельзя делать read-then-write вне транзакции. Два редактора одного version: один успех, второй 409; чужой текст не перезаписывается. Queue CAS и проверка состояний проходят в одной транзакции с изменением; проверять также гонки archive/transition/reorder. Параллельные unrelated изменения сохраняются независимо.

Snapshot MVP целиком, без скрытой пагинации/обрезки; desktop ограничивает входной JSON 32 MiB и при превышении показывает явную ошибку. Сервер возвращает 413 вместо частичного snapshot при превышении того же лимита. История пагинируется отдельно; payload SSE не копирует полные описания.

SSE передаёт `id=event_seq`, kind/object IDs; heartbeat, replay после курсора, ограниченный буфер медленного клиента и отключение с последующим replay. Append event и его публикация не имеют окна потери: stream читает SQLite log, in-memory notification только будит. Не держать write transaction во время network write. Реализовать streaming timeout отдельно от обычного HTTP WriteTimeout. Будущий/недопустимый cursor → явный reset_required, затем новый snapshot. Cursor в SSE/query относится к фактически обслуживаемой БД; добавить database instance UUID в snapshot/stream; events/stream принимают ожидаемый instance_id, несовпадение → 409 reset_required до replay. Сбрасывать курсор при смене instance, не сравнивать разные БД по одному seq.

### Desktop-сценарий

Три экрана/области без пустых будущих вкладок:

1. **Обзор:** список проектов, числа backlog/queued/in_progress/done, последнее изменение, кнопка создания проекта, строка API connection/stale. Фильтр archived.
2. **Проект:** имя и root, backlog, упорядоченная очередь, ручная текущая задача, завершённые/отменённые; создать задачу, добавить/убрать из очереди, переместить вверх/вниз. Drag-and-drop не обязателен.
3. **Карточка задачи:** текст, критерии, ручное state, допустимые действия и pageable история. Редактор хранит draft отдельно от серверной модели.

Кнопки disabled при pending/offline, но draft сохраняется в памяти окна. На 409 показать конфликт и предложить перечитать актуальный объект, не затирая draft автоматически. На timeout мутации показывать «исход сохранения неизвестен» и повторить только с прежним key; не обещать откат. После ответа обновить snapshot. Новые network ответы со старым request generation/event_seq не заменяют более новое состояние, особенно после reconnect/смены выбранного проекта.

Начальная загрузка: snapshot с seq → stream после seq. Event инвалидирует view, reload согласованного snapshot с coalescing; время последнего успешного обновления видно. При reconnect перечитать snapshot, при смене instance сбросить старый cursor. Offline — cached-in-memory read-only, без скрытой очереди изменений. Не считать пустой ответ/невалидный JSON пустым портфелем. Закрытие с dirty draft требует подтверждения.

Qt: `QGuiApplication`, `QNetworkAccessManager`, C++ QObject/viewmodels и QAbstractListModel, QML только layout/actions. Никакого WebView и shell из QML. Идентификаторы `objectName` для UI tests. QML load/network/protocol errors видны пользователю и тестам. Async network, cancellation, deadlines; не блокировать UI thread.

Omarchy: Material dark, live accent/rounding и отдельный Theme adapter с fallback без Omarchy; Translator adapter читает язык, все строки qsTr/tr, каталоги **ru/en**, fallback English для остальных. Тесты используют свои theme/language files, не переключают системную тему/язык. Стандартные Qt shortcuts для сохранения/отмены/выхода; односимвольные shortcuts не перехватывают ввод в TextField/TextArea. README перечисляет реальные shortcuts. Базовая переносимость через QStandardPaths/adapters, без обещания macOS support.

## Testing Strategy

Команды нового Makefile:

- `make build`: `arteld`, `artelctl` → `build/bin/`; Qt `artel` → `desktop/build/`.
- `make lint`: read-only Go lint (2.10.1), Go format-check; C++ compiler warnings as errors, `clang-format --dry-run --Werror`, QML qmllint/qmlformat check. Локализованные обоснованные Qt-generated исключения допустимы; нельзя глобально игнорировать QML warnings или собственный C++ код.
- `make test`: Go unit/integration, Qt unit/model/QML interaction tests offscreen; никакой зависимости от запущенного production API.
- `make test-race`: Go `-race` тесты storage/API/concurrency.
- `make test-e2e`: отдельный temp HOME/data/token, настоящий `arteld` и Qt harness с production QML/C++ клиентом; bounded timeouts, cleanup только собственных PID/каталогов.
- `make package-check`: собрать Arch package без установки/скачивания host dependencies и проверить содержимое, desktop entry, service unit.
- `make check`: lint + test + test-race + build + test-e2e. Package-check отдельно обязателен на Arch перед итоговой приёмкой.

Qt tools определять через `qmake6 -query QT_INSTALL_BINS` с явным fallback override, не полагаться на PATH. Для QML тестов `QT_QPA_PLATFORM=offscreen`, `QT_QUICK_BACKEND=software`, `QT_FORCE_STDERR_LOGGING=1`; ошибки загрузки QML фатальны. Использовать явную регистрацию C++ типов/корректные qmltypes, чтобы lint понимал properties, а не отключать unqualified/property warnings целиком.

Не устанавливать системные пакеты с sudo автоматически. Скрипт preflight печатает недостающие версии/компоненты и завершается ошибкой. Запуск agent-browser не требуется: UI нативный Qt, веб-фронтенда нет.

## Implementation Steps

### Task 1: Replace the bot with a buildable Go/Qt skeleton and updated instructions

- [x] Прочитать материалы Context, проверить git status; сохранить текущие архитектуру/эксперименты/план. Инвентаризировать legacy-файлы без чтения секретов и зафиксировать список удаления в `docs/legacy-cleanup.md`.
- [x] Удалить tracked `cmd/agent/`, старое `internal/`, bot configs/examples, Dockerfile/compose/`.dockerignore`, старые bot scripts и `tests/e2e/`, `.github/workflows/docker-publish.yml`, `docs/roles/`. Старые DEPLOY/USER_GUIDE/backlog/e2e/headless/multi-instance docs убрать из активной документации; `docs/plans/completed/` сохранить как явно исторические.
- [x] Проверить существование локальных legacy `data/`, `config.yaml`, `.env`, трёх реальных configs/*-bot.yaml: если отсутствуют — записать no-op. Если появились — включить в явный список только доказанно bot-owned пути; не следовать symlink. Не удалять глобальные `~/.pi`, `~/.ssh`, другие проекты, Docker volumes или host services. Очистка внешней установки — вне автоматических задач плана.
- [x] Создать компилируемые `cmd/arteld`, `cmd/artelctl`, `desktop/artel.pro`, минимальный native Main.qml и build/test scripts. Сервер пока предоставляет только healthz, UI честно показывает «каркас», CLI --help/--version; не изображать готовые CRUD.
- [x] Переписать `CLAUDE.md`, `.claude/CLAUDE.md` и `.github/copilot-instructions.md` под новый продукт, сохранив обязательные проверки. README — актуальная цель и команды. Заменить Makefile: без go fix внутри lint, без pkill/nohup/автозапуска сервиса. Обновить .gitignore под Go/Qt build и private artifacts; не игнорировать versioned desktop/bin scripts.
- [x] Очистить Go dependencies через tidy под Go 1.26.3; при добавлении storage в Task 3 использовать SQLite driver `modernc.org/sqlite` проверенной исходной версии 1.50.1. До его фактического импорта не удерживать unused dependency вопреки tidy; не менять toolchain без необходимости. Пересмотреть legacy-specific lint exclusions, не переносить blanket security exemptions без причины.
- [x] Добавить и выполнить smoke/unit tests: healthz, CLI error/exit codes, загрузка настоящего QML, отсутствие обязательных bot/provider env. Добавить preflight и начальные реальные build/lint/test targets; `make lint test build` должны пройти прежде следующей задачи.

Task 1 validation (2026-11-17): `GOLANGCI_LINT=/tmp/artel-pi-bridge-Fd40ok1E/tools/golangci-lint make lint test build` — PASS. Go 1.26.3, golangci-lint 2.10.1, Qt 6.12.0, clang-format 23.1.3, GCC 16.2.1. Go unit/process tests and production-QML offscreen tests passed (4 Qt results, no warnings/skips). `go mod tidy` removed all unused bot dependencies; SQLite 1.50.1 returns only with its actual import in Task 3. Existing Go-only CI now uses direct Go commands; mandatory native/full-stack CI remains Task 8, remote CI not claimed. Auth/storage/CRUD remain intentionally unavailable. Local legacy paths were absent (no-op); see `docs/legacy-cleanup.md`. Template attribution is retained in `desktop/NOTICE` without changing LICENSE.

### Task 2: Implement local daemon lifecycle, paths and owner authentication

- [x] Реализовать `internal/local/`: XDG defaults/explicit overrides, private credential initialization без перезаписи, symlink/permissions checks, data-dir lock, безопасное завершение по signal. Секреты никогда не печатать.
- [x] Реализовать HTTP server bootstrap и owner-auth middleware в `cmd/arteld`/`internal/api`: только loopback, headers/Origin/Host/body limits, deadlines и sanitized errors. Диагностика фактического bound address для :0 не содержит token.
- [x] Задать `api/openapi.yaml` с операциями/DTO/ошибками из Technical Details; реализацию domain handlers вводить следующими задачами. Минимальная CLI: `status --json`, `snapshot --json` (последняя заработает в Task 4), --endpoint/--token-file; никаких shell tools.
- [x] Написать тесты первого/повторного init, небезопасных прав и symlink, конкурирующих запусков, bind refusal, missing/wrong token, Origin/Host, oversized body и secret redaction. Test processes используют только temp paths/ephemeral ports.
- [x] Выполнить `make lint test build`; сверить auth схемы OpenAPI с middleware tests.

Task 2 validation (2026-11-17): `GOLANGCI_LINT=/tmp/artel-pi-bridge-Fd40ok1E/tools/golangci-lint make lint test build test-race` — PASS (0 lint issues, all Go tests/race tests and 4 offscreen Qt results passed, no skips). Versions unchanged from Task 1: Go 1.26.3, lint 2.10.1, Qt 6.12.0, clang-format 23.1.3, GCC 16.2.1. Process tests build their own daemon, bind :0, verify cross-process lock refusal, SIGTERM/restart, unchanged credential and redacted diagnostics. Negative tests cover unsafe files/parents, symlinks, FIFO credentials, wrong/missing/duplicate auth, Origin/Host, oversized bodies, occupied/nonlocal binds, redirect rejection and invalid/oversized CLI responses. YAML parse/reference/auth-policy check passed for all 10 operations; middleware tests exercise all nine protected operations. Pinned full OpenAPI schema/response validation remains Task 4, not claimed here. Public status uses healthz; authenticated snapshot explicitly returns 501 until domain handlers arrive. No owner service/package/settings changes or remote CI performed. `git diff --check` and local content review passed.

### Task 3: Implement transactional storage, manual state transitions and queue rules

- [x] Создать versioned schema и store/app/domain для Project, Task, queue entries, events, mutation_receipts и database instance UUID. Event хранит action, object IDs, owner_manual, timestamp, до/после изменённых полей и версии; не читать для него содержимое проектных каталогов.
- [x] Реализовать валидацию полей/путей, уникальность active roots, project archive/unarchive, все переходы и queue semantics из Technical Details. Каталог проекта только проверять, ничего в нём не создавать и не запускать.
- [x] Реализовать атомарные CAS/idempotency/event updates; no-op semantics; изменение queue_version при переходах из queued. Источник истины — transaction, не предварительная проверка в handler.
- [x] Написать unit tests таблицы переходов и ошибок, SQLite integration tests FK/unique/migrations/reopen/rollback, повторов ключа и другого payload, одновременных edit/reorder/start/archive. Проверить две записи in_progress отвергаются, инварианты не нарушаются после отказа транзакции.
- [x] Выполнить `make lint test test-race build`; тесты подтверждают отсутствие partial mutation/event/receipt при ошибках.

Task 3 validation (2026-11-17): `GOLANGCI_LINT=/tmp/artel-pi-bridge-Fd40ok1E/tools/golangci-lint make lint test test-race build` — PASS (0 lint issues, all Go/race tests, 4 offscreen Qt results, no skips). Go 1.26.3, golangci-lint 2.10.1, modernc.org/sqlite 1.50.1, Qt 6.12.0, clang-format 23.1.3, GCC 16.2.1. `go test -count=20 ./internal/app ./internal/store` — PASS; coverage: app 93.8%, store 70.9%, domain 66.7%. Independent connections exercise edit/edit, unrelated edits, reorder/start, start/start, archive/queue, archive/start, reorder/reorder and same-key races. Fault injection at callback, persistence, event, receipt and migration DDL verifies rollback; foreign databases are rejected without byte changes, DB/sidecar symlinks refused. Daemon startup now opens the database under its lifetime data-dir lock, preserves instance UUID across SIGTERM/restart and fails with redacted diagnostics on invalid storage. MVP transactions serialize mutations and load/persist a complete snapshot; no network I/O occurs inside them. HTTP CRUD/history/SSE remain Task 4; desktop functionality remains unavailable. `git diff --check`, local content review and secret/privacy scan passed. No production data, service activation, settings changes or remote CI.

### Task 4: Expose the API, snapshots, audit history and replayable SSE

- [x] Реализовать все domain endpoints OpenAPI, expected versions, idempotency responses, structured errors, body/field limits. Текстовую валидацию и правила не дублировать независимо от app layer.
- [x] Реализовать согласованный snapshot с instance UUID/event_seq, history pagination/filter cursors, SSE replay из SQLite с heartbeat/slow-client policy и reset_required. Закрывать streams при shutdown; не зависеть от сохранности in-memory notification.
- [x] Завершить `artelctl status/snapshot --json`: работает с тем же auth и API, bounded timeout и корректные nonzero exit; plaintext credentials в argv не поддерживать.
- [x] Добавить API contract tests всех операций/ответов/ошибок, включая malformed JSON, unknown fields, missing key/version, cross-project IDs, archive, no-op, большие snapshot. Проверять OpenAPI validity и соответствие тестируемых request/response схемам; validator dependency закрепить в репозитории, не использовать непинованный npx download в CI.
- [x] Добавить SSE integration tests: event между snapshot и subscribe, reconnect, смена instance, future cursor, slow reader, burst, отказ клиента после commit и повтор мутации с прежним key без второго event.
- [x] Выполнить `make lint test test-race build`; CLI test подтверждает доступ к temp daemon, не production instance.

Task 4 validation (2026-11-17): `GOLANGCI_LINT=/tmp/artel-pi-bridge-Fd40ok1E/tools/golangci-lint make lint test test-race build` — PASS (0 lint issues, all Go/race tests and 4 offscreen Qt results passed, no skips). `go test -count=5 ./internal/api ./cmd/artelctl` — PASS. Go 1.26.3, golangci-lint 2.10.1, modernc.org/sqlite 1.50.1, Qt 6.12.0, clang-format 23.1.3, GCC 16.2.1. All ten operations are covered by the pinned kin-openapi 0.133.0 validator; successful JSON requests and success/error responses are checked against their operation schemas, with SSE behavior tested over HTTP. OpenAPI uses validator-compatible 3.0.3 with equivalent singleton enums instead of 3.1 const, fixes a malformed YAML flow description, and documents audit field keys; the originally specified queue-array and before/after wire DTOs are preserved via adapters over storage maps/changes. API tests cover CAS winners/conflicts, no-op receipts/events, archive/cross-project refusal, duplicate/null/unknown/trailing JSON, limits, filtered cursor progress, a real >32 MiB stored snapshot returning 413, sanitized storage failures and all protected routes. SSE reads committed log pages (100 events), polls every 100 ms, heartbeats every 15 s and disconnects writes blocked for 2 s; tests cover snapshot/subscribe gap, 110-event burst, reconnect, wrong instance/future cursor, shutdown and deterministic slow-reader timeout without holding a transaction. A real HTTP connection is dropped after commit; same-key retry yields one project/event. The stream fixture's timeout was raised from 5 to 60 s after race instrumentation made burst setup exceed 5 s; production deadlines were not relaxed. CLI process tests build/start only a temporary daemon with private HOME/data/token and :0. `git diff --check`, local content review and secret/privacy scan passed. Desktop client/screens, package/service activation and remote CI remain outside this task; no production data or settings touched.

### Task 5: Build the native API client, models and connection/recovery state

- [x] В `desktop/src/` реализовать asynchronous ApiClient, private credential/path adapter, модели проектов/задач/очереди и viewmodel. QML не знает token, SQL и OS-команд. Ввести objectName и типизированные bindings для lint/tests.
- [x] Реализовать snapshot→SSE, coalesced reload, request generations, stale/error/loading states, bounded response/parser limits, redirect rejection и shutdown cleanup. Не перезаписывать новое состояние старым HTTP-ответом; instance change сбрасывает cursor.
- [x] Реализовать отдельный draft/pending mutation state, expected versions и reuse ключа при uncertain outcome. Не ставить offline edits в очередь; пользователь сохраняет возможность видеть/копировать несохранённый draft.
- [x] Добавить Qt Test с управляемым HTTP fixture: авторизация, malformed/truncated SSE/JSON, delayed/out-of-order replies, unauthorized, redirect, timeout, reconnect, 409 и сохранение draft; secret не появляется в signals/UI diagnostics. Дополнить smoke связкой client + реальный temp arteld.
- [x] Выполнить `make lint test build`, проверив отсутствие блокирующих сетевых вызовов в GUI thread.

Task 5 validation (2026-11-17): `GOLANGCI_LINT=/tmp/artel-pi-bridge-Fd40ok1E/tools/golangci-lint make lint test build` — PASS (0 lint issues, all Go tests, 5 production-QML results and 32 client Qt results passed, no warnings/skips). `./desktop/bin/test` repeated — PASS. Go 1.26.3, golangci-lint 2.10.1, Qt 6.12.0, clang-format 23.1.3, GCC 16.2.1. ApiClient is an asynchronous QObject viewmodel with read-only project/task/queue list models and independent editor/request copies; snapshot responses are capped at 32 MiB, SSE frames at 64 KiB and mutation requests at 256 KiB. Loopback-only requests bypass proxies and never follow redirects; credential lookup opens inode-checked private files/directories without following symlinks. Snapshot reloads are serialized/coalesced, generation-guarded and sequence-monotonic; SSE disconnect/reset requires a new snapshot, instance replacement resets the cursor. HTTP deadlines and SSE idle deadlines are separate. Tests cover malformed/truncated/oversized responses, fragmented CRLF SSE, reset_required, reconnect, delayed old-generation responses, older sequences, concurrent reload requests, auth/redirect/timeout errors, draft-preserving 409, byte-identical same-key unknown-outcome retry and edits made during retry. Unresolved mutations cannot replay into a different database instance. Event-loop timer assertions verify network waits do not block. A separately built real temporary arteld verifies authenticated snapshot/SSE bootstrap plus project/task creation, queue mutation and all three models; only its own process/temp paths are used. Production QML shows sanitized connection diagnostics through typed string properties; full CRUD screens/editor controls remain Task 6, not claimed here. `git diff --check`, local content review and secret/privacy scan passed. No production services/data/settings, package installation, remote CI or publication touched.

### Task 6: Implement project, task, queue and history screens

- [x] Реализовать обзор, создание/редактирование/архивацию проекта, backlog/queue/current/terminal sections и карточку задачи с критериями и history pagination. Использовать реальные API данные, без встроенной demo-загрузки в обычном запуске.
- [x] Реализовать создание/редактирование задачи, queue add/remove/up/down, ручное начало/завершение/отмену/reopen с ограничениями и явными подписями. Архивные объекты read-only. Не показывать будущие agent/chat/MR controls как работающие.
- [x] Реализовать conflict/unknown/offline/error screens, retry/reload и предупреждение о dirty draft при закрытии. Доступные действия выводить из состояния, но серверные проверки сохраняются обязательными.
- [x] Добавить Omarchy Theme/Translator adapters с fallback, ru/en, безопасными keyboard shortcuts и accessibility labels. Не менять реальные desktop настройки; platform paths изолированы. Сохранить attribution использованных шаблонов.
- [x] Написать interaction tests реального QML: ввод кириллицы, tab/keyboard/mouse actions, queue порядок, reload сохранённых данных, конфликт и draft, offline disable, подтверждение выхода, длинные строки/ошибки, смена языка/темы на temp fixtures. Снимки offscreen использовать для визуальной проверки, не вместо assertions.
- [x] Выполнить `make lint test build`; сохранить пример screenshot с фиктивными данными для README, без путей/имени владельца/секретов.

Task 6 validation (2026-11-17): `GOLANGCI_LINT=/tmp/artel-pi-bridge-Fd40ok1E/tools/golangci-lint make lint test build` — PASS (0 Go lint issues, all Go tests, 10 production-QML/UI results and 32 client Qt results, no warnings/skips). Go 1.26.3, golangci-lint 2.10.1, Qt 6.12.0, clang-format 23.1.3, GCC 16.2.1. Registered C++ Panel/Appearance types generate their own qmltypes; qmllint now enforces zero warnings without blanket exclusions. QML contains layout/actions only; editors capture base versions and database identity, preserve drafts on conflict/offline/unknown outcome, and require explicit adoption of a refreshed version before resaving a conflict. A replaced database cannot receive an old draft silently. History has bounded authenticated pagination, cancellable selection-scoped requests, sanitized failures and expandable before/after fields. Production-QML mouse/keyboard tests against a real temporary arteld create/edit projects and three tasks, edit Cyrillic criteria, reorder/remove queues, complete/cancel/reopen manually, archive/unarchive read-only objects, detect unavailable paths, reload saved values, exercise two-editor conflicts, preserve long drafts and confirm close/discard. An ephemeral HTTP fixture verifies 20+5 history pages, invalid-history recovery, byte-identical unknown-outcome retry and database replacement. Theme/language tests use only private fixtures, atomic replacement, English fallback and a fake rounding command; real settings are never written. `ARTEL_SCREENSHOT="$PWD/docs/screenshots/manual-panel.png" ./desktop/bin/test` generated English/Russian fictional examples, both visually inspected and linked from README. Standard Save/Cancel/Quit shortcuts, no single-letter interception; MIT attribution retained in desktop/NOTICE. `git diff --check`, local content review and secret/privacy scan passed. Full restart/concurrency acceptance remains Task 7; package/service/CI work remains Task 8. No host package installation, production services/data/settings, remote CI or publication touched.

### Task 7: Add full-stack restart/concurrency acceptance tests

- [x] Создать `tests/e2e/` native harness, который запускает свой arteld на :0 и загружает production QML/client с temp HOME/data/token; всегда завершает только собственные процессы, включая failure cleanup. Никаких agterm/Pi/Docker/real project mutations.
- [x] Через UI создать проект (temp каталог), минимум три задачи, задать порядок, отредактировать критерии, отметить первую in_progress/done. Проверить порядок/поля/историю через второй API client; исход ручной, не агентный.
- [x] Закрыть и открыть UI при живом сервере, затем остановить/перезапустить сервер с той же БД и восстановить UI: данные, instance UUID, версии и очередь сохраняются; закрытие окна не останавливает API.
- [x] Проверить два клиента с одинаковой старой версией, одновременный reorder/start, потерю ответа после commit и повтор с тем же key; один итоговый effect/event, видимый конфликт, несохранённый текст не теряется. Проверить SSE/reconnect не пропускает изменения.
- [x] Реализовать `make test-e2e`/`make check`; провести обязательный полный `make check` с bounded timeouts и корректными ненулевыми кодами при отрицательных assertions. Не объявлять UI проверенным только по запуску headless API tests.

Task 7 validation (2026-11-17): `GOLANGCI_LINT=/tmp/artel-pi-bridge-Fd40ok1E/tools/golangci-lint make lint test` and `GOLANGCI_LINT=/tmp/artel-pi-bridge-Fd40ok1E/tools/golangci-lint make check` — PASS (0 lint issues, all Go/race tests, 10 production-QML/UI results, 32 client results and 3 native acceptance results, no warnings/skips). `./scripts/e2e` repeated five times — PASS, including the intentional-failure/cleanup probe each time. Versions unchanged: Go 1.26.3, golangci-lint 2.10.1, modernc.org/sqlite 1.50.1, Qt 6.12.0, clang-format 23.1.3, GCC 16.2.1. The dedicated harness compiles production QML/Panel/ApiClient/Appearance, drives mouse/keyboard project/task/criteria/queue/manual-state actions and uses an independent HTTP oracle to verify ordered queues, fields and owner_manual transition history. Window close destroys the QML engine/client without stopping arteld; window recreation and daemon SIGTERM/restart preserve the exact snapshot, instance UUID, versions and queue. Because :0 may allocate a new port, restart explicitly reconnects to the newly advertised endpoint. Two editors demonstrate a visible 409 with retained draft after SSE refresh; simultaneous reorder/start yields one 200, one 409 and one event. A real forwarding proxy drops the first committed mutation response and verifies byte-identical same-key retry, two successful upstream responses and only one event. Request bytes containing Authorization are never printed by assertions. SSE and disconnected-client recovery converge to the independent snapshot. Each daemon uses a disposable HOME subdirectory, owned QProcess cleanup and Linux parent-death signaling; watchdogs bound test runs. An intentional failing assertion must return exactly 1 and leave no recorded process/data directory; the runner fails otherwise. `make check` serializes all mandatory local gates even under parallel make. `git diff --check`, local content review and secret/privacy scan passed. No production data/settings/services, package installation or remote CI; packaging/CI remain Task 8.

### Task 8: Package the local desktop/service and wire mandatory CI

- [x] Добавить `packaging/systemd/arteld.service`: отдельный user service с foreground ExecStart=/usr/bin/arteld, Restart=on-failure и корректным graceful shutdown; без Docker/network-online зависимостей, pkill, автозапуска агентов. Не устанавливать/enable/start unit на машине владельца.
- [x] Добавить Arch PKGBUILD, desktop entry с app id `artel`, SVG icon, attribution/license. Пакет содержит artel/arteld/artelctl, unit, icon/desktop entry и нужные Qt/QML runtime deps; не содержит БД/token или абсолютных путей рабочей копии. Сборка из текущего source snapshot, не stale git HEAD.
- [x] Создать `make package-check`: makepkg без -i/-s/privileged действий, inspect package files и проверить desktop/unit syntax. Отсутствующие build dependencies — blocker с точным списком, не скрытый sudo/pacman на хосте.
- [x] Обновить `.github/workflows/ci.yml`: Go job на Go из go.mod + lint 2.10.1 + tests/race; native/full-stack job в Arch build environment с Qt 6.8+, toolchain, clang-format и обязательными build/lint/Qt/e2e/package checks. Пакеты допустимо устанавливать только в ephemeral CI container; выводить точные версии инструментов. Не вводить publish/deploy workflow.
- [x] Добавить проверки скриптов exit codes/preflight, packaging assertions и невозможности непреднамеренной установки/автозапуска. Прогнать `make check` и `make package-check` локально в разрешённом окружении; после любых исправлений повторить затронутые проверки.

Task 8 validation (2026-11-17): `GOLANGCI_LINT=/tmp/artel-pi-bridge-Fd40ok1E/tools/golangci-lint make lint test`, then final `GOLANGCI_LINT=/tmp/artel-pi-bridge-Fd40ok1E/tools/golangci-lint make check` and `make package-check` — PASS. Go 1.26.3, golangci-lint 2.10.1, Qt 6.12.0, clang-format 23.1.3, GCC 16.2.1, Python 3.14.8, makepkg/pacman 7.1.0, systemd 262.1. Zero lint issues; all Go/race tests, 9 delivery safety tests, 10 production-QML results, 32 client results and 3 native acceptance results passed without test skips. The disposable Arch package builds all three binaries from current working-tree source (not HEAD), validates the archive SHA256, exact payload, root ownership/safe modes, ELF executables, desktop entry and user-unit syntax, and rejects checkout-path leakage. The first package attempt exposed an overbroad build-artifact filter excluding desktop/bin/build; fixed with a regression test and rerun. Final makepkg returned success but emitted `libfakeroot internal error: payload not recognized!` during cleanup; independent archive inspection confirmed root:root ownership, safe modes and all expected files. This nonfatal host-tool diagnostic is recorded, not hidden. Package-check removes its temporary package and never invokes install/dependency-install/service operations; missing tools/dependencies and root execution fail explicitly. Safety tests cover current uncommitted source, private/build exclusion, symlink refusal, manifest/ownership/mode rejection, missing dependencies, exact safe makepkg arguments and preflight nonzero exits. CI YAML parsed and job gate assertions passed; Go and unprivileged Arch full-stack/package jobs are wired, but no remote CI execution is claimed. `git diff --check`, local content review and secret/privacy scan passed. No host packages installed, services activated, production data/settings touched, push or deployment performed. Task 9 documentation/final acceptance remains separate.

### Task 9: Verify acceptance, finish documentation and hand off

- [x] Сверить результат с Overview и всеми ручными/domain/API инвариантами; убрать устаревшие активные bot инструкции и ссылки, не удаляя историю завершённых планов/новые архитектурные материалы.
- [x] Завершить README (скриншот, dependencies, build/check, auth bootstrap, отдельный запуск daemon/UI, shortcuts, offline/conflict semantics), `docs/development.md`, `docs/legacy-cleanup.md` и актуальные инструкции агентов. Описать limitations: manual-only, нет Pi/MR/Продакта/remote, prototype не является orchestrator.
- [x] Добавить/обновить doc/CLI example smoke tests и проверить примеры на temp instance. Финально выполнить `make check`, `make package-check`, `git diff --check`, secret/privacy scan изменённых файлов и содержательное review diff; исправить findings и повторить проверки. Не публиковать реальные credentials/paths/screenshots.
- [x] Зафиксировать в плане/отчёте точные команды, версии, результаты и ограничения. Все Task checkboxes отметить только при фактическом выполнении; при blocker оставить незавершёнными. Не выдавать наличие CI YAML за выполненный remote CI.

Task 9 validation (2026-11-17): `GOLANGCI_LINT=/tmp/artel-pi-bridge-Fd40ok1E/tools/golangci-lint make lint test`, `GOLANGCI_LINT=/tmp/artel-pi-bridge-Fd40ok1E/tools/golangci-lint make check`, `make package-check` and `git diff --check` — PASS. Go 1.26.3, golangci-lint 2.10.1, modernc.org/sqlite 1.50.1, Qt 6.12.0, clang-format 23.1.3, GCC 16.2.1, Python 3.14.8, makepkg/pacman 7.1.0, systemd 262.1. Zero lint issues; all Go/race tests, 11 Python delivery/documentation tests, 10 production-QML results, 32 client results and 3 native acceptance results passed with no skips. Package rebuilt from current source and independently inspected successfully; the Task 8 fakeroot cleanup diagnostic did not recur in this run. Nothing installed or activated.

Final content review compared the Overview and domain/API/desktop requirements with transaction/CAS/receipt code, API contract/security/recovery tests, client/editor guards and the production-QML restart/concurrency harness. Evidence covers manual CRUD/criteria/history, queue/transition/archive invariants, restart identity/version persistence, conflicts, one-effect retry, authenticated bootstrap, honest offline/unknown/manual UI and appearance fallback. README no longer describes completed acceptance/packaging as future work. `docs/development.md` records toolchain, isolation, architecture, limitations and runnable CLI examples; new tests execute those exact example commands with separately built binaries, private HOME/data/token and :0, verify JSON, missing-credential refusal/public status and invalid-argument exit codes, and check active documentation links. Cleanup inventory and root agent guidance are current; delegated instruction files still point to the root guidance. Historical completed plans, architecture, experiments and fictional screenshots are retained. Changed-file secret/privacy review found no credentials, personal data or new owner paths; only documented placeholders and the already-recorded private linter path above. No real credential files were inspected.

Limitations/handoff: manual-only prototype, not an orchestrator; no Pi/Product/MR checks, remote mode, macOS support, automatic pruning or large-scale storage guarantee. No publication, remote CI, normal-session Omarchy desktop smoke, package installation, user-service enablement, deployment or external legacy cleanup was performed or claimed. Those owner decisions remain Post-Completion, not unimplemented automatic plan tasks.

## Success Criteria

- В нормальном запуске новый desktop работает без Telegram/Mattermost/LLM/Pi/Docker и без Omarchy-файлов (с fallback-темой).
- Через Qt UI, не прямой SQL, можно создать проект/задачи, изменить описание/критерии, составить очередь, пройти ручные состояния и прочитать историю.
- Перезапуск сервера/клиента не теряет подтверждённые данные; UI закрывается независимо от сервера.
- Concurrent update приводит к явному 409/conflict, не silent last-write-wins. Повтор успешно закоммиченной мутации с тем же key не создаёт второй эффект.
- Unauthorized/небезопасный local bootstrap отвергаются; token отсутствует в обычных выводах и UI.
- Offline/unknown/ручные отметки видны честно; продукт не заявляет агентное выполнение или проверенный MR.
- Полные Go/Qt/API/e2e проверки зелёные, package проверен без установки, приложение имеет native desktop entry и отдельный service unit.

## Post-Completion

Владелец отдельно решает установку собранного Arch package и включение user service, выполняет desktop smoke на обычном Omarchy-сеансе и выбирает следующий план. Удаление внешних legacy Docker volumes/установок не требовалось для этого среза и автоматически не выполняется; при необходимости подготовить отдельный точный список объектов для очистки.

Будущий этап: проверка recovery/TUI Pi bridge и подключение участников. Полноценные продуктовые согласования Продакта, вопросы владельцу и workflow до MR остаются отдельными планами. Remote CI, если будет публикация рабочей ветки/MR, должен пройти на exact HEAD; merge/main push/deploy этим планом не разрешены.
