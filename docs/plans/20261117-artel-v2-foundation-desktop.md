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

- [ ] Реализовать `internal/local/`: XDG defaults/explicit overrides, private credential initialization без перезаписи, symlink/permissions checks, data-dir lock, безопасное завершение по signal. Секреты никогда не печатать.
- [ ] Реализовать HTTP server bootstrap и owner-auth middleware в `cmd/arteld`/`internal/api`: только loopback, headers/Origin/Host/body limits, deadlines и sanitized errors. Диагностика фактического bound address для :0 не содержит token.
- [ ] Задать `api/openapi.yaml` с операциями/DTO/ошибками из Technical Details; реализацию domain handlers вводить следующими задачами. Минимальная CLI: `status --json`, `snapshot --json` (последняя заработает в Task 4), --endpoint/--token-file; никаких shell tools.
- [ ] Написать тесты первого/повторного init, небезопасных прав и symlink, конкурирующих запусков, bind refusal, missing/wrong token, Origin/Host, oversized body и secret redaction. Test processes используют только temp paths/ephemeral ports.
- [ ] Выполнить `make lint test build`; сверить auth схемы OpenAPI с middleware tests.

### Task 3: Implement transactional storage, manual state transitions and queue rules

- [ ] Создать versioned schema и store/app/domain для Project, Task, queue entries, events, mutation_receipts и database instance UUID. Event хранит action, object IDs, owner_manual, timestamp, до/после изменённых полей и версии; не читать для него содержимое проектных каталогов.
- [ ] Реализовать валидацию полей/путей, уникальность active roots, project archive/unarchive, все переходы и queue semantics из Technical Details. Каталог проекта только проверять, ничего в нём не создавать и не запускать.
- [ ] Реализовать атомарные CAS/idempotency/event updates; no-op semantics; изменение queue_version при переходах из queued. Источник истины — transaction, не предварительная проверка в handler.
- [ ] Написать unit tests таблицы переходов и ошибок, SQLite integration tests FK/unique/migrations/reopen/rollback, повторов ключа и другого payload, одновременных edit/reorder/start/archive. Проверить две записи in_progress отвергаются, инварианты не нарушаются после отказа транзакции.
- [ ] Выполнить `make lint test test-race build`; тесты подтверждают отсутствие partial mutation/event/receipt при ошибках.

### Task 4: Expose the API, snapshots, audit history and replayable SSE

- [ ] Реализовать все domain endpoints OpenAPI, expected versions, idempotency responses, structured errors, body/field limits. Текстовую валидацию и правила не дублировать независимо от app layer.
- [ ] Реализовать согласованный snapshot с instance UUID/event_seq, history pagination/filter cursors, SSE replay из SQLite с heartbeat/slow-client policy и reset_required. Закрывать streams при shutdown; не зависеть от сохранности in-memory notification.
- [ ] Завершить `artelctl status/snapshot --json`: работает с тем же auth и API, bounded timeout и корректные nonzero exit; plaintext credentials в argv не поддерживать.
- [ ] Добавить API contract tests всех операций/ответов/ошибок, включая malformed JSON, unknown fields, missing key/version, cross-project IDs, archive, no-op, большие snapshot. Проверять OpenAPI validity и соответствие тестируемых request/response схемам; validator dependency закрепить в репозитории, не использовать непинованный npx download в CI.
- [ ] Добавить SSE integration tests: event между snapshot и subscribe, reconnect, смена instance, future cursor, slow reader, burst, отказ клиента после commit и повтор мутации с прежним key без второго event.
- [ ] Выполнить `make lint test test-race build`; CLI test подтверждает доступ к temp daemon, не production instance.

### Task 5: Build the native API client, models and connection/recovery state

- [ ] В `desktop/src/` реализовать asynchronous ApiClient, private credential/path adapter, модели проектов/задач/очереди и viewmodel. QML не знает token, SQL и OS-команд. Ввести objectName и типизированные bindings для lint/tests.
- [ ] Реализовать snapshot→SSE, coalesced reload, request generations, stale/error/loading states, bounded response/parser limits, redirect rejection и shutdown cleanup. Не перезаписывать новое состояние старым HTTP-ответом; instance change сбрасывает cursor.
- [ ] Реализовать отдельный draft/pending mutation state, expected versions и reuse ключа при uncertain outcome. Не ставить offline edits в очередь; пользователь сохраняет возможность видеть/копировать несохранённый draft.
- [ ] Добавить Qt Test с управляемым HTTP fixture: авторизация, malformed/truncated SSE/JSON, delayed/out-of-order replies, unauthorized, redirect, timeout, reconnect, 409 и сохранение draft; secret не появляется в signals/UI diagnostics. Дополнить smoke связкой client + реальный temp arteld.
- [ ] Выполнить `make lint test build`, проверив отсутствие блокирующих сетевых вызовов в GUI thread.

### Task 6: Implement project, task, queue and history screens

- [ ] Реализовать обзор, создание/редактирование/архивацию проекта, backlog/queue/current/terminal sections и карточку задачи с критериями и history pagination. Использовать реальные API данные, без встроенной demo-загрузки в обычном запуске.
- [ ] Реализовать создание/редактирование задачи, queue add/remove/up/down, ручное начало/завершение/отмену/reopen с ограничениями и явными подписями. Архивные объекты read-only. Не показывать будущие agent/chat/MR controls как работающие.
- [ ] Реализовать conflict/unknown/offline/error screens, retry/reload и предупреждение о dirty draft при закрытии. Доступные действия выводить из состояния, но серверные проверки сохраняются обязательными.
- [ ] Добавить Omarchy Theme/Translator adapters с fallback, ru/en, безопасными keyboard shortcuts и accessibility labels. Не менять реальные desktop настройки; platform paths изолированы. Сохранить attribution использованных шаблонов.
- [ ] Написать interaction tests реального QML: ввод кириллицы, tab/keyboard/mouse actions, queue порядок, reload сохранённых данных, конфликт и draft, offline disable, подтверждение выхода, длинные строки/ошибки, смена языка/темы на temp fixtures. Снимки offscreen использовать для визуальной проверки, не вместо assertions.
- [ ] Выполнить `make lint test build`; сохранить пример screenshot с фиктивными данными для README, без путей/имени владельца/секретов.

### Task 7: Add full-stack restart/concurrency acceptance tests

- [ ] Создать `tests/e2e/` native harness, который запускает свой arteld на :0 и загружает production QML/client с temp HOME/data/token; всегда завершает только собственные процессы, включая failure cleanup. Никаких agterm/Pi/Docker/real project mutations.
- [ ] Через UI создать проект (temp каталог), минимум три задачи, задать порядок, отредактировать критерии, отметить первую in_progress/done. Проверить порядок/поля/историю через второй API client; исход ручной, не агентный.
- [ ] Закрыть и открыть UI при живом сервере, затем остановить/перезапустить сервер с той же БД и восстановить UI: данные, instance UUID, версии и очередь сохраняются; закрытие окна не останавливает API.
- [ ] Проверить два клиента с одинаковой старой версией, одновременный reorder/start, потерю ответа после commit и повтор с тем же key; один итоговый effect/event, видимый конфликт, несохранённый текст не теряется. Проверить SSE/reconnect не пропускает изменения.
- [ ] Реализовать `make test-e2e`/`make check`; провести обязательный полный `make check` с bounded timeouts и корректными ненулевыми кодами при отрицательных assertions. Не объявлять UI проверенным только по запуску headless API tests.

### Task 8: Package the local desktop/service and wire mandatory CI

- [ ] Добавить `packaging/systemd/arteld.service`: отдельный user service с foreground ExecStart=/usr/bin/arteld, Restart=on-failure и корректным graceful shutdown; без Docker/network-online зависимостей, pkill, автозапуска агентов. Не устанавливать/enable/start unit на машине владельца.
- [ ] Добавить Arch PKGBUILD, desktop entry с app id `artel`, SVG icon, attribution/license. Пакет содержит artel/arteld/artelctl, unit, icon/desktop entry и нужные Qt/QML runtime deps; не содержит БД/token или абсолютных путей рабочей копии. Сборка из текущего source snapshot, не stale git HEAD.
- [ ] Создать `make package-check`: makepkg без -i/-s/privileged действий, inspect package files и проверить desktop/unit syntax. Отсутствующие build dependencies — blocker с точным списком, не скрытый sudo/pacman на хосте.
- [ ] Обновить `.github/workflows/ci.yml`: Go job на Go из go.mod + lint 2.10.1 + tests/race; native/full-stack job в Arch build environment с Qt 6.8+, toolchain, clang-format и обязательными build/lint/Qt/e2e/package checks. Пакеты допустимо устанавливать только в ephemeral CI container; выводить точные версии инструментов. Не вводить publish/deploy workflow.
- [ ] Добавить проверки скриптов exit codes/preflight, packaging assertions и невозможности непреднамеренной установки/автозапуска. Прогнать `make check` и `make package-check` локально в разрешённом окружении; после любых исправлений повторить затронутые проверки.

### Task 9: Verify acceptance, finish documentation and hand off

- [ ] Сверить результат с Overview и всеми ручными/domain/API инвариантами; убрать устаревшие активные bot инструкции и ссылки, не удаляя историю завершённых планов/новые архитектурные материалы.
- [ ] Завершить README (скриншот, dependencies, build/check, auth bootstrap, отдельный запуск daemon/UI, shortcuts, offline/conflict semantics), `docs/development.md`, `docs/legacy-cleanup.md` и актуальные инструкции агентов. Описать limitations: manual-only, нет Pi/MR/Продакта/remote, prototype не является orchestrator.
- [ ] Добавить/обновить doc/CLI example smoke tests и проверить примеры на temp instance. Финально выполнить `make check`, `make package-check`, `git diff --check`, secret/privacy scan изменённых файлов и содержательное review diff; исправить findings и повторить проверки. Не публиковать реальные credentials/paths/screenshots.
- [ ] Зафиксировать в плане/отчёте точные команды, версии, результаты и ограничения. Все Task checkboxes отметить только при фактическом выполнении; при blocker оставить незавершёнными. Не выдавать наличие CI YAML за выполненный remote CI.

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
