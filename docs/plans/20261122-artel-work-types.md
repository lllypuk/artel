# Artel 6а — типы работ Epic / Task / Bug

## Overview

Добавить в существующий ManualWorkspace три типа работ с необязательной группировкой Task/Bug под Epic одного проекта. Владелец создаёт Epic, переводит его в работу, добавляет Task и Bug, проводит Bug через очередь, видит вычисляемую сводку и отдельно закрывает Epic. Startup остаётся Team; глобальная Board относится к 6б.

Согласованные правила:

- Тип `epic`, `task` или `bug` выбирается при создании и не меняется.
- Task/Bug используют существующие `backlog / queued / in_progress / done / cancelled` и проектную очередь. Epic имеет отдельные явные состояния `open / in_progress / closed`, не занимает очередь или проектный `in_progress`, не является поручением и не получает автоматический приоритет/закрытие по детям.
- Новый Epic начинается с `open`; допустимы ручные `open ↔ in_progress`, `in_progress → closed` и `closed → open`. Состояние не вычисляется из детей. Закрытие разрешено пустому Epic или при всех детях `done/cancelled`.
- Task/Bug может иметь одного необязательного родителя — Epic того же проекта в состоянии `open/in_progress`. Привязка, перенос и отвязка сохраняют тип, UUID и позицию в очереди и пишутся в историю. Завершённого ребёнка можно отвязать от закрытого Epic; затем его можно возобновить. Epic родителя не имеет, поэтому циклы невозможны.
- Закрытый Epic не имеет активных детей. Reopen ребёнка требует открытия Epic или отвязки. При конкуренции close Epic/reopen ребёнка проходит только операция, оставляющая допустимое состояние.
- У Bug необязательные `expected_behavior`, `actual_behavior`, `fix_evidence`. Пустое доказательство не блокирует ручное `done`; карточка показывает «Доказательство не указано». Заполненное поле — запись владельца, не независимая проверка и не новый Git/revmux-гейт.
- Сводка Epic вычисляется из дочерних работ по действующим статусам, включая отдельный `cancelled`; она не является сохранённым счётчиком и не меняет состояние Epic.

Критерии приёмки: additive миграция сохраняет все текущие записи и квитанции; старый API без типа создаёт Task без Epic; серверные инварианты нельзя обойти прямыми операциями; production-QML сценарий выше переживает перезапуск daemon и повторное открытие карточки.

## Context

- `internal/domain/domain.go`, `internal/app/app.go`: Task DTO, допустимые переходы, CAS, idempotency и owner events.
- `internal/store/schema.sql`, `internal/store/store.go`, `internal/store/migration_test.go`: JSON record плюс SQL identity/state, очередь, partial unique index одного `in_progress`, `user_version=6`.
- `internal/api/domain.go`, `api/openapi.yaml`: строгий command JSON; omitted поле отличается от явного значения.
- `internal/store/{delivery,questions,results}.go` и связанные API: комиссии и вопросы имеют существующие open/close gates, results — archive/CAS/replay gates без проверки done/cancelled. Все они получают только дополнительную проверку исполняемого типа, включая записывающие DeliveryReceipt и AttachReview.
- `desktop/src/{apiclient,panel}.{h,cpp}`, `desktop/src/ManualWorkspace.qml`: C++ validation/viewmodel и production QML редактора/карточки. Независимые черновики и Team/observer поведение сохраняются.
- Завершённые планы 4b/5а перенесены в `docs/plans/completed/`; roadmap синхронизирован с PR #17. Настоящее revmux-демо остаётся отдельным и не является зависимостью 6а.

## Development Approach

TDD: сначала миграционные и доменные отказы, затем API/integration, затем C++/production-QML. Выполнять задачи последовательно, не продвигаться с красными проверками. После каждой кодовой итерации `make lint test`; перед завершением каждой задачи её адресные проверки и `make lint test build`. Использовать Go 1.26.3, golangci-lint 2.10.1, Qt 6.8+, временные БД/HOME/порты и fictional данные.

План обновляется при изменении согласованного объёма. Board, автоматический Review, новые роли и обязательный Bug proof-gate не расширяют этот срез.

## Testing Strategy

- SQLite: миграция существующей v6 БД с очередью, in_progress, результатом/review, delivery/question и receipts; rollback неуспешной DDL; старый бинарник не открывает v7; identity/version/history сохраняются.
- Domain/API: создание каждого типа, immutable type, omitted/explicit parent, все допустимые/запрещённые переходы, CAS, replay, чужой проект/родитель, parent неверного типа и попытка назначить родителя Epic.
- Concurrency: close Epic против reopen ребёнка в обоих порядках; отвязка завершённого ребёнка закрытого Epic; связь/очередь/результаты не меняются неявно.
- Integration: прямые queue/transition, commission, owner question, results submit/attach и delivery receipt для Epic отклоняются; прежние Task/Bug операции и replay не ломаются.
- Desktop: строгая проверка DTO, фильтры родителей, вычисляемая сводка, независимые черновики, empty/missing evidence, production-QML сценарий и restart.

## Solution Overview

Сохранить единую `tasks` модель и добавить additive тип/родителя, а не создавать отдельные таблицы Epic: это сохраняет UUID, историю, очередь и существующие результаты. SQL ограничивает допустимые kind/state и same-project parent; app дополнительно применяет все бизнес-правила в одной транзакции.

Альтернативы отклонены: отдельная модель/таблица Epic с миграцией связанных данных; автоматический статус Epic по детям; разрешение менять тип; Board вместо минимального ManualWorkspace; обязательное доказательство Bug на `done`.

## Technical Details

- В `domain.Task` добавить `kind`, необязательный `parent_id` и Bug-поля. v7 требует **транзакционного перестроения `tasks`**, поскольку действующий state CHECK не знает `open/closed`: создать таблицу с новыми ограничениями, скопировать строки с прежними UUID/state/version, восстановить indexes и выполнить `PRAGMA foreign_key_check`. `kind='epic'` допускает только `open/in_progress/closed` и NULL parent; Task/Bug — прежние статусы; parent ссылается на работу того же проекта и не на саму запись. Partial unique index одной исполняемой работы действует только для Task/Bug.
- SQL колонки kind/parent/state являются единственным структурным авторитетом для ссылок и индексов; load синхронизирует их с JSON record, persist не пересериализует их разными значениями. Текстовые Bug-поля остаются в record. **Epic summary существует только в read-projection snapshot** и не попадает в record/историю.
- Существующие записи мигрируют как `kind=task`, `parent_id=NULL`. Перестроение покрывается чистой БД и заполненной v6 с queue_entries/events/results/questions/delivery/receipts; rollback возвращает исходные данные и `user_version=6`, успешная миграция устанавливает 7.
- `persist` освобождает unique index только для `kind IN ('task','bug')`, не переписывая Epic `in_progress` в backlog. App start/current/archive проверяет только исполняемые типы; Epic transition не читает и не меняет `queue_version`. Архивация проекта с единственным in_progress Epic разрешена, если нет queued/in_progress Task/Bug; archived projection read-only.
- Create без `kind` означает Task; явный Epic начинает `open`, Task/Bug — `backlog`. Bug-поля допустимы только у Bug и проходят существующие UTF-8/byte limits. Edit не принимает `kind` или JSON null parent. Omitted `parent_id` сохраняет связь даже с закрытым Epic, пустая строка снимает её. Проверка open/in_progress выполняется только при установке/переносе на другого родителя; явный прежний UUID — допустимый no-op. UI показывает текущего closed parent отдельно от кандидатов на замену.
- Transition выбирает правила по фактическому типу из store. Close Epic проверяет детей в той же immediate transaction и ожидаемую версию. Канонический `Command` хешируется до подстановки default kind/parent, поэтому v6 create/edit replay по старому ключу возвращает исходные status/body без события; старое receipt body может не содержать новых полей, новый snapshot их содержит.
- Queue/transition проверяют исполняемый тип. Delivery append/next/spend/verify/**receipt**, owner questions и results **submit/attach** используют server-side kind из tasks record. Это отдельный type-only предикат: существующие open/archive/CAS/revoke правила каждой операции не расширяются; SubmitResult/AttachReview не получают новую проверку done/cancelled. Fresh DeliveryReceipt и AttachReview отклоняются после exact receipt replay, но до новых записей/business replay; read-only `/legacy-receipt` и `/receipt` recovery остаются доступными.
- Snapshot/OpenAPI возвращают тип, родителя, Bug-поля и Epic summary через отдельную wire-проекцию в `internal/api/dto.go`; summary не добавляется в persisted domain.Task. **v7 дополнительно объявляет `work_types_version: 1` в самом snapshot**, независимо от наличия строк tasks и без отдельного HTTP-запроса. Отсутствие поля — legacy v6: новый Qt разрешает только обычное создание Task. Наличие поля включает Epic и требует валидный kind в каждой v7-строке; отсутствующий/некорректный kind — ошибка данных. Старый HTTP client может создать/изменить Task без новых полей. Новый Qt со старым daemon показывает прежние Task, но отключает новые действия с явным признаком необходимого upgrade. **Старый Qt не поддерживает v7/Epic вообще**: `open/closed` отклоняются текущим validator, а `in_progress` Epic может быть ошибочно принят за Task старым бинарником; это документируемое, неисправимое задним числом исключение, а не допустимая классификация.
- QML только отображает тип, родителя, сводку и допустимые действия. C++ выбирает родителей, строит payload и блокирует несовместимые действия; скрытые кнопки не заменяют server checks. Bug `done` с пустым evidence явно показывает «Доказательство не указано».

## Implementation Steps

### Task 1: Добавить типы и additive SQLite-миграцию

**Files:**
- Modify: `internal/domain/domain.go`, `internal/store/schema.sql`, `internal/store/store.go`, `internal/store/migration_test.go`, `internal/store/store_test.go`, `internal/store/questions_test.go`, `internal/store/observer_migration_test.go`, `internal/store/delivery_test.go`, `tests/staged_acceptance.py`.
- Create: `internal/store/work_types_schema.sql`, `internal/store/work_types_test.go`.

- [x] Добавить kind/parent/Bug DTO и v7: транзакционно перестроить tasks с новым CHECK, скопировать прежние строки, восстановить FK/indexes и синхронизировать SQL/JSON. Epic summary формировать только в read-projection; существующие записи мигрируют как Task без родителя.
- [x] Ограничить освобождение partial unique index при persist исполняемыми типами, чтобы посторонняя мутация сохраняла одновременные Epic/Bug `in_progress`; обновить **все** оракулы текущей `user_version` на 7 в store/staged tests, не меняя исторический `PRAGMA user_version=6` в `current_reviews_schema.sql`.
- [x] Проверить чистую и заполненную v6, сохранение UUID/state/version/queue/results/reviews/delivery/questions/events/receipts, `PRAGMA foreign_key_check` и отсутствие событий миграции.
- [x] Проверить SQL kind/state/parent ограничения, rollback с исходными данными/`user_version=6` и отказ старого бинарника на v7.
- [x] `go test ./internal/domain ./internal/store -count=1` и `make lint test build`.

### Task 2: Реализовать owner операции и доменные инварианты

**Files:**
- Modify: `internal/app/app.go`, `internal/app/app_test.go`, `internal/api/domain.go`, `internal/api/domain_test.go`, `internal/api/dto.go`, `internal/api/dto_test.go`, `internal/api/contract_test.go`, `internal/store/delivery.go`, `internal/store/questions.go`, `internal/store/results.go`, `api/openapi.yaml`.

- [x] Реализовать создание с обратной совместимостью, immutable type, редактирование полей/parent, переходы Epic и Task/Bug, snapshot/history, строгий OpenAPI и **в том же изменении** authoritative type-only gate для новых commission/question/results submit/attach и DeliveryReceipt.
- [x] В `internal/api/dto.go` добавить отдельную wire-проекцию Task/Epic summary и `work_types_version=1`; проверить пустой v7 snapshot, отдельный cancelled и отсутствие summary в persisted record/истории.
- [x] Развести Epic transition и executable start: Epic и Bug могут одновременно быть `in_progress`; второй исполняемый start отклоняется, Epic не меняет queue_version, archive с одним in_progress Epic допускается.
- [x] Проверить omitted kind/parent, явную отвязку/no-op прежнего parent, open/in_progress/closed, close пустого/terminal-only Epic, Bug fields и projection-only сводку.
- [x] Проверить реальную v6 create/edit квитанцию после миграции: тот же HTTP request/key возвращает исходные status/body без события, а новый request без kind создаёт Task.
- [x] Проверить отказы: смена типа, родитель не Epic/чужого проекта/closed при новой связи, parent у Epic, queue/start/commission/question/results submit/attach/DeliveryReceipt для Epic, reopen ребёнка closed Epic и неверные Bug-поля — без новых записей/events/receipts.
- [x] Проверить close/reopen гонку в обоих порядках, CAS/idempotency replay, отвязку завершённого ребёнка closed Epic с сохранением очереди и последующий reopen.
- [x] `go test ./internal/app ./internal/api -count=1` и `make lint test build`.

### Task 3: Доказать интеграционные ограничения и recovery

**Files:**
- Modify: `internal/store/delivery_test.go`, `internal/store/questions_test.go`, `internal/store/results_test.go`, `internal/api/results_test.go`, `internal/observerapi/delivery.go`, `internal/observerapi/delivery_test.go`, `internal/observerapi/questions.go`, `internal/observerapi/questions_test.go`.

- [x] Проверить единый server-side executable-type gate при commission, delivery next/spend/verify/**receipt**, owner question/answer и results **submit/attach**; read-only история и `/legacy-receipt`/`/receipt` recovery сохраняются. Изменять production код здесь только при обнаруженном пробеле Task 2.
- [x] Проверить отказ прямых API-операций для Epic с намеренно подготовленными fixture rows: messages/attempts/reviews/events/receipts неизменны, включая обход через stale UI или прямой owner/delivery HTTP.
- [x] Проверить неизменность прежнего Task/Bug поведения, recovery после unknown/revoke/close и миграционных квитанций.
- [x] `go test ./internal/store ./internal/api ./internal/observerapi -count=1` и `make lint test build`.

### Task 4: Показать типы и Epic workflow в ManualWorkspace

**Files:**
- Modify: `desktop/src/apiclient.h`, `desktop/src/apiclient.cpp`, `desktop/src/panel.h`, `desktop/src/panel.cpp`, `desktop/src/ManualWorkspace.qml`, `desktop/src/i18n/ru.json`, `desktop/tests/apiclient_tests.cpp`, `desktop/tests/artel_tests.cpp`.

- [x] Добавить DTO/viewmodel типа, parent и Bug-полей; выбор типа при создании, необязательный parent только подходящих Epic, Epic summary и допустимые действия.
- [x] Показать детей Epic, тип/родителя работы и Bug evidence; при done с пустым evidence выводить «Доказательство не указано». Сохранить независимые черновики и startup Team.
- [x] Проверить строгие DTO, текущего closed parent вне списка новых кандидатов, смену сводки после transition, запрет несовместимых кнопок/драфтов, keyboard/offline/conflict поведение и отсутствие credentials в QML.
- [x] Проверить новый Qt со старым daemon: прежние Task доступны, новые действия отключены с явным upgrade-признаком. Две пустые фикстуры доказывают, что `work_types_version` позволяет первой записью стать Epic на v7 и обычной Task на v6. При наличии признака v7 отсутствующий/некорректный kind отклоняется как ошибка данных. Зафиксировать legacy-validator fixtures для `open/closed` и `in_progress` Epic: старый Qt не поддерживает v7/Epic вообще, хотя последний случай может быть принят старым бинарником за Task и поэтому является документированным исключением, а не допустимой классификацией.
- [x] `./desktop/bin/test` и `make lint test build`.

### Task 5: Доказать сквозной сценарий и зафиксировать документацию

**Files:**
- Modify: `tests/e2e/acceptance.cpp`, `scripts/e2e`, `docs/development.md`, `docs/roadmap.md`, `PRODUCT.md`, `DESIGN.md`, `docs/plans/20261122-artel-work-types.md`.

- [ ] Добавить production-QML acceptance: создать Epic open→in_progress, добавить Task/Bug, отменить Task, провести Bug через очередь в done с пустым evidence **при остающемся in_progress Epic**, увидеть сводку, закрыть Epic, перезапустить daemon и проверить сохранение.
- [ ] Проверить положительное отображение owner-written fix_evidence, запрет reopen ребёнка под closed Epic, отвязку завершённого ребёнка и последующий reopen без изменения очереди; добавить negative controls прямых операций Epic.
- [ ] Обновить стабильный контракт/примеры и roadmap по фактической реализации, не объявляя Board или обязательный Bug proof-gate. Проверить ссылки, команды и `git diff --check`.
- [ ] Провести содержательное ревью итоговой дельты и исправить findings с повторной проверкой; затем `make check` и на Arch `make package-check` на точном итоговом SHA, зафиксировав результаты.

## Post-Completion

После успешного выполнения плана и приёмки владельцем перенести его в `docs/plans/completed/` и обновить все ссылки. Планирование 6б Board выполняется отдельно. Настоящее revmux-демо 5а, пилот, push/merge, установка пакета и активация сервисов требуют самостоятельных решений владельца и не входят в чеклист.
