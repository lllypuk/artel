# Срез 4b — вопрос Pi и асинхронный ответ владельца

## Статус и граница подтверждения

База плана — `origin/main`
`98484e29e69ee5a0b89ac61e1c3a1c5ce5b9b319`. Реализация Tasks 1–4 зафиксирована
коммитами через `df9ecaf3f25da8ff4a291352e70fcca26de2809b`; документация
Task 5 — в `820349c`. Это локальный кодовый срез, не установка, обновление
работающих сессий или пилот.

Стабильный контракт описан в [development](../../development.md) и
[README расширения](../../../integrations/pi-observer/README.md). Маршруты,
поля, состояния и лимиты сверены с [OpenAPI](../../../api/openapi.yaml);
формулировки о реальном tool call, явной передаче task UUID, production QML,
повторном custom-ходе, busy/revoke, остановленном Pi и неоднозначном spend
сверены с PTY-оракулом `integrations/pi-observer/tests/questions_pty.py`.

## Что фиксируют Tasks 1–4

- Новый delivery-допуск включается только флагом
  `artelctl delivery enable --allow-owner-questions`; writer атомарно закрепляет
  `owner_questions_v1` при attach. Существующий допуск не изменяется на месте.
- `artel_ask_owner` получает явно переданный владельцем UUID открытой задачи,
  сохраняет вопрос через delivery UDS и завершает tool call без ожидания ответа.
- Owner API сохраняет не более одного ответа и его строку общей очереди в одной
  транзакции. Ответ возвращается в исходную Pi-сессию отдельным custom-ходом
  `artel-owner-answer`; readiness не нужен для сохранения ответа.
- Состояние вопроса `open`/`answered`/`closed` отделено от доставки ответа
  `stored`/`unknown`/`observed`. `observed` подтверждает только matching
  `before_agent_start`; неизвестный исход не повторяется автоматически.
- Close/archive/revoke и смена назначения закрывают unanswered questions и
  необратимо блокируют неотправленные ответы. История и уже записанный
  `observed` сохраняются; reopen/unarchive или новый допуск очередь не
  возобновляют. Новый runtime с тем же Pi-session UUID не наследует допуск.
- Вопрос и ответ ограничены 4 КиБ UTF-8 без NUL, POST — 32 КиБ, страница
  владельца — пятью строками и 320 КиБ. Текст не попадает в observer telemetry,
  domain events/SSE или логи. Artel не исполняет текст, не выдаёт разрешений и
  не меняет состояние задачи; обе строки становятся контекстом для provider,
  настроенного в Pi.
- Миграция v3→v4 атомарна и сохраняет поручения, попытки и квитанции 4a. Ошибка
  миграции откатывается к v3; после успешной миграции старый бинарник отказывается
  открыть v4, обратного in-place downgrade нет. False/absent opt-in сохраняет
  wire/receipt-совместимость 4a. Старый extension не получает 4b-вопросы/ответы;
  старый daemon отклоняет opted-in enable без grant и confirmed config.

План записывает зелёные адресные гейты Tasks 1–4. В частности, для Task 4 на
`df9ecaf` зафиксированы `./desktop/bin/test` и `make lint test build`. Это
результаты соответствующих задач, **не повторные прогоны Task 5**. Ссылки
в Task 5 были сверкой контракта и существующих оракулов, а не новыми
прогонами. Результаты на итоговом кандидате приведены отдельно ниже.

## Проверки Task 5

Все запуски использовали синтетические данные, временные каталоги/HOME,
эфемерный loopback-порт и приватный Unix socket. Рабочий daemon, реальные
credentials, provider и production-настройки не использовались.

1. Изолированный CLI-сценарий через одноразовый Python here-doc собрал свежие
   `arteld`/`artelctl` во временном каталоге, поднял приватный daemon, выполнил
   реальные `observer provision`, синтетическую регистрацию runtime по UDS,
   `observer list`, `observer bind`, затем документированный
   `delivery enable --allow-owner-questions`, `delivery enable --resume` и
   `delivery revoke`. Проверены `allow_owner_questions: true` в confirmed config
   и server grant, исходный pending grant без writer, confirmed resume и revoke
   с удалением writer config. Результат:

   ```json
   {"bind": "bound", "enable_opt_in": "pending/confirmed-config", "provision": "confirmed", "registration": "synthetic", "resume": "confirmed", "revoke": "revoked", "secrets_printed": false}
   ```

   Никакой несуществующий `--help`-сценарий не использовался.
2. `python3 -B -m unittest tests.test_documentation.DocumentationTests.test_documented_cli_examples_on_temporary_instance`
   — документированные `status`/`snapshot` и observer
   `provision`/`provision --resume`/`list` выполнены против отдельного
   временного daemon; также проверены безопасные ошибки credentials/arguments.
3. Статическая doc-проверка через Python here-doc — локальные Markdown-ссылки в
   изменённых документах разрешаются; заявленные маршруты, tool/custom types,
   состояния, лимиты и schema v4 присутствуют в OpenAPI/реализации/PTY-оракуле;
   roadmap обозначает 4b кодовым срезом, а не пилотом или установкой.
4. `git diff --check` — ошибок нет.

## Task 6: проверки кандидата

Прогоны выполнены из `/home/sasha/projects/lllypuk/artel` на чистом
`820349cd75bd2db46631dcd8bc8b0910fbdd57ee` (`feature/artel-pi-delivery-02`),
до правок этой записи и чекбокса. База ревью — `origin/main` `98484e29…`;
merge-base `ced9d999ec0e87585da2c1660bcb638a80ece5fc` имеет то же дерево
`eafec1108e6b776dc9a402ac1569939a0db0c395`. Среда: Omarchy 4.5.0 (Arch),
x86_64, Go 1.26.3, golangci-lint 2.10.1, Qt/QtTest 6.12.0 (offscreen,
software backend), clang-format 23.1.3, Pi 1.5.2, Node 22.23.2, npm 12.0.2.

```sh
cd /home/sasha/projects/lllypuk/artel
export GOTOOLCHAIN=go1.26.3
export GOLANGCI_LINT=/home/sasha/.local/opt/golangci-lint-2.10.1/golangci-lint
make check > /tmp/artel-4b-exec.CStnZI/task6-check.log 2>&1
make package-check > /tmp/artel-4b-exec.CStnZI/task6-package.log 2>&1
make test-e2e > /tmp/artel-4b-exec.CStnZI/task6-e2e.log 2>&1
go test ./internal/store ./internal/api ./internal/observerapi -run 'Test(OwnerQuestion|DeliveryQuestion)' -count=1 > /tmp/artel-4b-exec.CStnZI/task6-questions-go.log 2>&1
```

- **`make check`: exit 2.** `make lint` прошёл (0 Go lint issues), 17 Python
  тестов прошли, `go test ./...` вернул зелёные результаты из кэша. На этапе
  `make test` процесс offscreen Qt `artel_tests` завершился с `SIGSEGV`,
  `SEGV_MAPERR`, адрес доступа `0x00000000000000ce`, в
  `ArtelTests::ownerQuestionPaginationAnswerAndStatuses()`; верхний кадр
  возникновения сбоя — `QQuickItem::isEnabled() const`. `./desktop/bin/test:21`,
  код 139; `Makefile:24: test` — 139, `Makefile:49: check` — 2. Причина
  сбоя не установлена. `test-race`, полный `build`, `observer-check` и
  `test-e2e` внутри `check` не запускались.
- **`make package-check`: exit 2.** Временный пакет `artel 2.0.0-1` собран;
  проверка манифеста, файлов и unit прошла до staged-тестов, отрицательный
  cleanup probe дал `PASS`. Позитивный staged-прогон остановился на
  `tests/staged_acceptance.py:108` с `AssertionError` на
  `assert db.execute('PRAGMA user_version').fetchone()[0] == 3`. Кандидат
  использует схему v4; oracle всё ещё требует v3. Следующие проверки сохранения
  identity и загрузки упакованного Pi-артефакта в этом прогоне не достигнуты.
  `package-check` сообщил exit 1 дочернего процесса (`Makefile:44`), `make`
  вернул 2. Пакет не устанавливался; проверка была в одноразовом каталоге.
- **Отдельный `make test-e2e`: exit 0.** Базовый Qt E2E — 3 passed, 0 failed;
  observer recovery и оба штатных отрицательных cleanup probe — `PASS`.
  Именно 4b: `scripts/e2e` вызвал `questions_pty.py`, который запускает
  `acceptance ownerQuestionRoundTrip -maxwarnings 0` с приватной fixture и
  обязательно проверяет нулевой код Qt-процесса (его вывод захватывается).
  Итог supervisor: `Ran 5 tests in 84.950s`, `OK`.
  - Реальный Pi tool call по явно переданному UUID появляется в уже выбранной
    пустой задаче; tool call и исходный ход завершаются до ввода ответа в
    production QML. Oracle видит ровно одну пару, один свежий custom-ход
    исходной сессии и matching `observed` с опубликованной квитанцией.
  - Без opt-in вопрос не сохраняется. Busy → сохранение ответа → revoke →
    освобождение Pi не порождает новый ход с ответом, история сохраняется.
    Ручной ход предшествует ответу; неоднозначный spend даёт `unknown` без
    автоповтора по независимому счётчику provider.
  - После остановки исходного Pi ответ можно сохранить без readiness;
    новый runtime с тем же Pi-session ID не получает текст и не тратит право.
    Extension из базы доставляет обычное 4a и не получает 4b-ответ; новый CLI
    против старого daemon не оставляет opted-in grant/confirmed config.
- **Адресный Go-прогон без кэша: exit 0** во всех трёх пакетах, включая
  `TestOwnerQuestionOpenAPIContract`. `api/openapi.yaml` отделяет
  delivery-UDS POST от owner list/answer и задаёт их авторизацию и лимиты.

**Приватность и граница операций.** `TestDeliveryQuestionChannelAuthenticationReplayAndStrictJSON`
проверяет отказ observer/owner credential на delivery POST, раздельный монтаж
маршрутов и отсутствие текста вопроса в observer registry;
`TestOwnerQuestionCreateReplayAndScope` проверяет отсутствие текста обеих
сторон в доменных events (источник SSE). По коду `src/index.ts` и
`internal/observerapi/http.go` observer отправляет только разрешённые метаданные
и счётчики, не читает диалог и не передаёт payload вопроса/ответа.
Текст доступен через owner API и отдельно авторизованный delivery writer,
не через observer. Ошибки маршрутов не возвращают тела или токены;
PTY-fixture отдельно проверяет отсутствие тестовых credentials в выводе
и запросах к loopback provider. Это не проверка логов внешних provider.
Ответ пишет только запись вопроса, строку delivery и квитанцию; сравнение
snapshot до/после в `ownerQuestionRoundTrip` подтверждает неизменность
состояния задачи. Artel не интерпретирует текст как команду, не запускает его
и не выдаёт разрешений на операции: extension передаёт `artel-owner-answer`
как контекст через `pi.sendMessage`. Одноразовый допуск конвейера защищает
именно доставку, а не разрешает Pi выполнить действие.

### Task 6: повтор после двух исправлений в тестах

Проверен тот же HEAD `820349cd75bd2db46631dcd8bc8b0910fbdd57ee` с
незакоммиченной дельтой в `desktop/tests/artel_tests.cpp` и
`tests/staged_acceptance.py` и с предыдущими правками этой записи и плана.
До внесения итогов повтора полный проверенный diff от HEAD сохранён в
`/tmp/artel-4b-exec.CStnZI/task6-retry-tested.diff` (SHA-256
`9c7524644eb97b47185ec944a3d52357ad6ad750e74edd2ca4e1861bd1d26cc6`);
дельта только двух тестовых файлов — `task6-retry-tests.diff` (SHA-256
`b56ed0cd8cd51b3a178d4c3cdf5e86454c89ad599d67194138d673af2b4d3630`).
Полный diff совпал до и после гейтов. Production-код не изменён.

- Стек core PID `3316082` и дизассемблирование исходного бинарника сопоставили
  `artel_tests+0xb7e9f` с возвратом из первого `isEnabled()` в проверке после
  клавиатурной отправки и ожидания `uncertain`. Кнопка была сохранена до
  обработки событий; `Panel::changed` обновляет модель `Repeater` и может
  заменить делегат. Теперь каждая попытка проверки ищет текущую кнопку по ID
  вопроса и требует, чтобы она существовала и была выключена. Соседние
  проверки защищены от отсутствующих элементов; helper ввода заново ищет
  поле после обработки клавиш и ввода. Клавиатурная отправка, отказ повторной
  отправки, replay, черновики, пагинация и статусы по-прежнему проверяются.
- Staged oracle требует ровно `PRAGMA user_version == 4` после запуска
  упакованного daemon на неизменной исторической v1 fixture. Сверка identity
  и остальные staged-проверки не изменены и дошли до успешного завершения.

Логи в `/tmp/artel-4b-exec.CStnZI`. С теми же версиями Go и golangci-lint,
с приватным HOME и синтетическими fixtures:

- `artel_tests -maxwarnings 0 ownerQuestionPaginationAnswerAndStatuses` —
  30/30 отдельных offscreen запусков с новым HOME, без сбоя;
  `task6-retry-focused.log`.
- `make check` — exit 0, `task6-retry-check.log`. Включённые `make lint` и
  `make test`: 0 Go lint issues, 17 Python-тестов, `go test ./...`, Qt
  `ArtelTests` 113/0 (включая проблемный selector) и `ApiClientTests` 36/0.
  `test-race`, `build` и `observer-check` прошли (38 unit и 4 probe).
  В `test-e2e` базовый Qt 3/0, observer recovery и оба cleanup probe — PASS;
  именно 4b (`questions_pty.py` → `ownerQuestionRoundTrip`) — 5/0,
  `Ran 5 tests in 89.574s`, `OK`.
- Сразу после `check`: Arch `make package-check` — exit 0,
  `task6-retry-package.log`. Staged cleanup probe, no-Pi запуск, миграция v1→v4
  с сохранением identity, загрузка standalone public-Pi артефакта, проверки
  манифеста, unit и приватности — PASS. Пакет удалён, не установлен и не
  активирован.

Первый wrapper повтора задал слишком длинный `TMPDIR`: `make check` завершился
с exit 2 на Go-тестах (путь AF_UNIX превышал лимит), до запуска Qt; следом
`make package-check` завершился с exit 0. Оба лога сохранены как
`task6-retry-check-long-tmpdir.log` и `task6-retry-package-long-tmpdir.log`.
Приведённые выше итоговые гейты выполнены последовательно на неизменном diff
с коротким `TMPDIR=/tmp`; тесты сами создавали приватные каталоги.

## Оставшаяся приёмка

Первоначальные красные гейты сохранены выше; их не заменяет отдельный E2E.
На явно указанной дельте оба обязательных локальных гейта прошли.
Содержательное ревью — отдельная фаза, здесь не выполнялось.
Прогоны были на синтетических данных с временными HOME, loopback и приватным
UDS; `observed` означает лишь matching `before_agent_start`, не подтверждение
обработки внешним provider или выполнения задачи. Текст вопроса/ответа
становится контекстом Pi, чьи обычные возможности не меняются. Полная
незакрытая PTY-матрица 4a, реальный provider, CI, публикация, merge, ручной
Wayland QA, активация режима, обновление сессий и пилот не подтверждены.
