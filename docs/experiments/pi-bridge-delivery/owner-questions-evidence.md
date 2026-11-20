# Срез 4b — вопрос Pi и асинхронный ответ владельца

## Статус и граница подтверждения

База плана — `origin/main`
`98484e29e69ee5a0b89ac61e1c3a1c5ce5b9b319`. Реализация Tasks 1–4 зафиксирована
коммитами через `df9ecaf3f25da8ff4a291352e70fcca26de2809b`; Task 5 меняет
только документацию в рабочем дереве. Это локальный кодовый срез, не установка,
обновление работающих сессий или пилот.

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
результаты соответствующих задач, **не повторные прогоны Task 5**. Тесты 4a,
unit/store/API/observer tests, PTY и production-QML сценарии здесь не
перезапускались; ссылки на них являются сверкой контракта и существующих
оракулов, а не новым доказательством их прохождения на текущем дереве.

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

## Оставшаяся приёмка

Пункты выше подтверждают только документацию и изолированный CLI-сценарий
Task 5. Они не заменяют Task 6: на итоговом SHA ещё нужны явно зафиксированный
4b-результат `make test-e2e` (внутри `make check`), полный `make check` и на Arch
`make package-check`, затем содержательное ревью итогового diff. До их фактического
выполнения итоговый кандидат не считается принятым. Публикация, merge, ручной
Wayland QA, активация режима, обновление сессий и пилот остаются за пределами
этого плана.
