# Протокол доставки: модель и граница журнала

Файлы этого каталога — **только тестовый fixture эксперимента №2**, а не API Artel,
продуктовый bridge или расширение Pi. `protocol.mjs` моделирует fake Artel, локальный
журнал и вызов передатчика. Отдельный `sdk-matrix.mjs` через публичный SDK pinned Pi
1.5.2 и inline extension подключает эту модель к числовому loopback-провайдеру. Рабочие
API/БД Artel, реальные провайдеры, инструменты, сессии и учётные данные не используются.
Все тесты используют вымышленные сообщения и приватные временные каталоги.

## Неизменяемая идентичность и состояния

Ключ сообщения — `message_id`. При первом допуске fake Artel фиксирует неизменяемые
`message_id`, `generation`, `session_id` и полный payload (`content` и `details`).
Повтор с другим телом, поколением или Pi-сессией под тем же ID — конфликт, а не новая
попытка. Внутренний статус сообщения:

```text
отсутствует --admission commit--> admitted --spend commit--> unknown
unknown --before_agent_start receipt commit--> observed
```

`admitted` означает лишь сохранённое поручение. Право на вызов появляется только в
**первом успешном ответе spend**. Spend атомарно привязывает `attempt_id` к неизменяемой
идентичности сообщения, активному поколению, тестовой Pi-сессии, PID текущего владельца
и `journal_id`. Повтор admission/spend/receipt с совместимым телом возвращает состояние;
повтор spend никогда не возвращает право. Другой владелец, поколение или журнал не
восстанавливают израсходованное право. Параллельные дубли сериализуются fake Artel и
дают не более одного права и одного вызова.

Локальный журнал имеет собственный постоянный `journal_id` и переходы:

```text
intent --> attempt --> receipt
```

Bridge сначала сохраняет допуск/intent, затем запрашивает spend. После успешного ответа
spend он обязан зафиксировать attempt **до** передачи. Ошибка этой записи означает ноль
вызовов; серверное право остаётся потраченным, результат `unknown`. Если ответ spend
потерян после commit, локального attempt может не быть, но повтор всё равно возвращает
только `unknown` без права. Наличие lock, читаемого файла или совпадающего имени не
доказывает обратное.

## Граница доказательства

Положительная квитанция — только наблюдение конкретного сообщения с
`type=before_agent_start`, `messageType=custom`, совпадающими `details` и ID тестовой
Pi-сессии. Оно сначала записывается в локальный журнал, затем идемпотентно публикуется
в fake Artel с исходной привязкой attempt. До commit квитанции fake Artel остаётся
`unknown`; сбой записи/ответа восстанавливается повторной публикацией той же записи,
не повторным вызовом Pi.

`before_agent_start` не означает provider ACK, запрос провайдеру, `message_end`,
завершение хода, принятие или выполнение задачи. События завершения, ответ провайдера и
несовпадающие details/session отклоняются как квитанции. Поздняя квитанция после отзыва —
историческое свидетельство об уже начатом custom-ходе; она не меняет активное поколение.
Никакой внешний oracle не используется этой моделью для разблокировки повтора.

## Отзыв и остаточное окно

| Момент отзыва | Обязательный результат модели |
|---|---|
| До admission | Ошибка, ноль вызовов |
| После admission, до spend | Ошибка spend, ноль вызовов |
| После spend, до финальной проверки | `unknown`, ноль вызовов, право потрачено |
| После передачи | Вызов не отменяется; поздняя квитанция только историческая |

Непосредственно перед вызовом передатчика bridge повторно проверяет attempt, активное
поколение, сессию, PID и `journal_id`. Между этой последней проверкой и фактическим
синхронным входом в `sendMessage` остаётся **неустранимое этим протоколом окно**: отзыв
после успешной проверки может не отменить уже выдаваемый вызов. Тест фиксирует это окно
явным барьером; обещания отмены после допуска или exactly-once нет.

Передатчик PTY дополнительно делает синхронный `ctx.isIdle()` непосредственно перед
`sendMessage`. Если к этому моменту начался ручной ход, он возвращает `false`: вызова Pi
нет, а уже потраченное право и локальный attempt остаются в `unknown`; повтор права не
восстанавливает. Это отдельная busy-проверка, а не ослабление границы отзыва поколения.

## Журнал, проверяемые операции и восстановление

На Linux fixture принимает только принадлежащий текущему UID каталог `0700` и обычные
файлы `0600`; открытие использует `O_NOFOLLOW`, затем проверяет `fstat`. Повреждённый JSON,
нечитаемый файл, другой тип/`journal_id`, неподтверждённый владелец или права — отказ до
передачи. Каждая запись создаёт временный файл `0600` в том же каталоге, проверяет ошибки
`write`/`fsync`/`close`, делает `rename` и `fsync` каталога. Идентичность текущего файла
проверяется перед заменой. Это модель process crash при сохранённых файлах, **не гарантия
при power loss**, откате/потере серверного журнала или защита от другого кода того же UID.

`afterCommit` и `beforeResponse` fake Artel — раздельные явные барьеры фиксации и ответа;
`afterSpendResponse` и `afterFinalVerification` отделяют последующие границы bridge.
Тесты не синхронизируются сном. В сценарии восстановления родитель останавливает собственный
дочерний процесс после commit spend и до ответа, затем новый процесс заново открывает те же
`server.json` и `journal.json`. Старая память недоступна; повтор возвращает `unknown`, ноль
новых вызовов. Отдельный сценарий доказывает, что новый PID и новый журнал также не получают
право.

## Запуск модели

Из корня Artel, с Node 22.23.2:

```bash
node --test docs/experiments/pi-bridge-delivery/*.test.mjs
```

Проверяются успех, повторы, конфликт payload/identity/receipt, параллельные дубли, сбой
записи attempt/receipt, отличие custom observation от completion/provider ACK, повреждённый/
нечитаемый/чужой/несовпадающий журнал, отзыв на всех границах, потерянные ответы после commit
и fresh-process recovery. Эта команда не запускает SDK/PTY матрицы.

## Изолированная SDK crash-матрица

`sdk-matrix.mjs` — отдельный runner; `sdk-worker.mjs` создаёт только тестовые SDK-сессии
через `createAgentSession`, `DefaultResourceLoader`, `SessionManager`, `SettingsManager` и
`ModelRuntime`. Требуются абсолютный pinned Node 22.23.2 и именно установленный пакет
`/home/sasha/.local/share/pi-node/node-v22.23.2-linux-x64/lib/node_modules/@earendil-works/pi-coding-agent`
версии 1.5.2. Runner отклоняет другую версию, отсутствующие публичные API, неприватный/
рабочий `PROBE_ROOT`, наследуемые переменные вне белого списка и HOME/XDG/Pi dirs вне
`PROBE_ROOT`. Ничего не устанавливает и не меняет настройки хоста.

Точное воспроизведение из корня Artel (все значения вымышленные, ключ — только литерал
`fake-not-a-secret`):

```bash
NODE=/home/sasha/.local/share/pi-node/node-v22.23.2-linux-x64/bin/node
PACKAGE=/home/sasha/.local/share/pi-node/node-v22.23.2-linux-x64/lib/node_modules/@earendil-works/pi-coding-agent
umask 077
ROOT=$(mktemp -d /tmp/artel-pi-delivery-XXXXXXXX)
mkdir -p "$ROOT/home" "$ROOT/config" "$ROOT/cache" "$ROOT/state" "$ROOT/data" "$ROOT/agent"
env -i \
  HOME="$ROOT/home" XDG_CONFIG_HOME="$ROOT/config" XDG_CACHE_HOME="$ROOT/cache" \
  XDG_STATE_HOME="$ROOT/state" XDG_DATA_HOME="$ROOT/data" \
  PI_CODING_AGENT_DIR="$ROOT/agent" PI_OFFLINE=1 \
  PATH="$(dirname "$NODE"):/usr/bin:/bin" \
  PROBE_ROOT="$ROOT" PI_TEST_PACKAGE="$PACKAGE" \
  "$NODE" docs/experiments/pi-bridge-delivery/sdk-matrix.mjs
```

Полные event traces, конфигурации и фактические HTTP body/headers записываются только под
этим приватным `PROBE_ROOT` вне worktree (`*-trace.jsonl`, `provider-requests.jsonl` и логи
owned children). В Git сохраняются лишь runner и сокращённые счётчики `sdk-result.json`.
Watchdog ограничивает сценарий; SIGKILL направляется только объектам собственных дочерних
`ChildProcess`. Все остановки и потери синхронизируются IPC/filesystem barriers, не sleeps.

### Независимый oracle и позитивный контроль

На каждом `before_agent_start` inline extension создаёт новый UUID и сохраняет тип инициатора,
session ID, details/prompt и привязку attempt. `before_provider_headers` ставит этот токен
только для provider `artel-fixture` и model `fixture`. Родительский числовой HTTP fixture на
`127.0.0.1` отдельно сохраняет полное принятое body и считает запросы по токену. Runner сверяет
первый запрос с token event, identity/generation/session/attempt/journal и уникальным маркером.
Инструменты, retry и compaction отключены. Provider trace недоступен worker recovery и не
может превратить `unknown` в квитанцию. Отсутствие токена/хука или barrier timeout завершает
прогон ошибкой, а не PASS.

Позитивный контроль выполняется первым: один custom message даёт один Pi send, один
коррелированный provider request и одну custom-квитанцию. Idempotent redelivery не добавляет
вызовов. Отдельный ручной ход со старым маркером получает новый токен, `messageType=user` и
второй provider request, но ноль новых bridge send/receipt. Это различает маркер в истории и
новую доставку.

### Фактическая матрица

Ниже `Pi` — команды `sendMessage` от bridge, `HTTP` — принятые fixture полные запросы,
`starts` — независимые `before_agent_start`; финальный статус относится к fake Artel после
обычного redelivery/recovery. Все 24 сценария прошли на Pi 1.5.2/Node 22.23.2.

| Сценарий | Pi | HTTP | starts | Финал |
|---|---:|---:|---:|---|
| positive-control (включая manual old marker) | 1 | 2 | 2 | observed |
| admission response lost before commit | 1 | 1 | 1 | observed |
| admission response lost after commit | 1 | 1 | 1 | observed |
| crash before spend; доказанно нерасходованное продолжение | 1 | 1 | 1 | observed |
| crash after spend commit, before response | 0 | 0 | 0 | unknown |
| crash after spend response, before local attempt | 0 | 0 | 0 | unknown |
| crash after local attempt, before Pi | 0 | 0 | 0 | unknown |
| whole-SDK crash after transmission, held before receipt | 1 | 1 | 1 | unknown |
| HTTP disconnect after full body, before response headers | 1 | 1 | 1 | observed |
| crash after local receipt, before POST | 1 | 1 | 1 | observed |
| receipt POST response lost before commit | 1 | 1 | 1 | observed |
| receipt POST response lost after commit | 1 | 1 | 1 | observed |
| local attempt write failure | 0 | 0 | 0 | unknown |
| local receipt write failure | 1 | 1 | 1 | unknown |
| Pi crash, coordinator alive, held before receipt | 1 | 1 | 1 | unknown |
| coordinator crash, Pi alive: late completion и Pi restart | 1 | 1 | 1 | unknown |
| redelivery with missing local journal | 1 | 1 | 1 | observed; reopen rejected |
| redelivery with restored readable pre-attempt copy, same journal ID | 0 | 0 | 0 | unknown |
| competing sender/new owner/new journal | 1 | 1 | 1 | unknown |
| revocation before admission | 0 | 0 | 0 | absent |
| revocation after admission, before spend | 0 | 0 | 0 | admitted |
| revocation after spend, before final verification | 0 | 0 | 0 | unknown |
| revocation after transmission; историческая квитанция | 1 | 1 | 1 | observed; generation 8 |
| measured residual final-check-to-send race | 1 | 1 | 1 | observed; generation 8 |

Crash recovery выполняется новым процессом поверх тех же server/journal файлов. В ветке
coordinator-dead/Pi-alive старый ход поздно завершается после смерти writer; после перезапуска
старой Pi-сессии новый coordinator получает `unknown`, сохраняет прежний attempt ID и делает
ноль новых Pi/HTTP вызовов. Отсутствующий журнал отклоняется до redelivery; readable копия
с совпадающим ID не доказывает нерасходованность. Persisted receipt публикуется повторно с
тем же attempt/key без Pi. Ни один сценарий не реконструирует квитанцию по provider oracle.

Отзыв после финальной проверки, но до синхронного входа в `sendMessage`, измерен отдельно:
вызов всё же происходит. Это остаточное окно протокола, а не обещание отмены или exactly-once.
SDK/json и process crash не доказывают PTY/TUI, внешних провайдеров, power loss, восстановление
agterm/zmx или надёжность реальной сессии. Ниже PTY проверяется отдельно.

## Настоящий Pi TUI в изолированном PTY

`pty_matrix.py` запускает установленный `dist/cli.js` Pi 1.5.2 в настоящем Linux PTY,
без окна, agterm и SDK-обхода. Два явных тестовых `-e` загружаются вместе с
`--no-extensions --no-skills --no-prompt-templates --no-context-files --no-themes --no-mcp
--no-builtin-tools`; `session_start` дополнительно требует `ctx.mode=tui` и пустой активный
набор инструментов. `pty-preflight.mjs` заранее передаёт точные аргументы парсеру pinned Pi.
Приватные settings отключают retry/compaction; внешние конфиги, credentials и ресурсы не
наследуются и не читаются. Ничего не устанавливается и не патчится.

Точное воспроизведение из корня Artel:

```bash
NODE=/home/sasha/.local/share/pi-node/node-v22.23.2-linux-x64/bin/node
PACKAGE=/home/sasha/.local/share/pi-node/node-v22.23.2-linux-x64/lib/node_modules/@earendil-works/pi-coding-agent
umask 077
ROOT=$(mktemp -d /tmp/artel-pi-delivery-XXXXXXXX)
mkdir -p "$ROOT/home" "$ROOT/config" "$ROOT/cache" "$ROOT/state" "$ROOT/data" "$ROOT/agent" "$ROOT/sessions"
timeout 240s env -i \
  HOME="$ROOT/home" XDG_CONFIG_HOME="$ROOT/config" XDG_CACHE_HOME="$ROOT/cache" \
  XDG_STATE_HOME="$ROOT/state" XDG_DATA_HOME="$ROOT/data" \
  PI_CODING_AGENT_DIR="$ROOT/agent" PI_OFFLINE=1 LANG=C.UTF-8 \
  PATH="$(dirname "$NODE"):/usr/bin:/bin" \
  PROBE_ROOT="$ROOT" PI_TEST_PACKAGE="$PACKAGE" \
  /usr/bin/python3 -B docs/experiments/pi-bridge-delivery/pty_matrix.py
```

Runner принимает только абсолютный pinned Node 22.23.2/package и приватный `PROBE_ROOT`
`0700` вне worktree; окружение вне белого списка отклоняется. Каждый сценарий ограничен
watchdog, а команда — внешним `timeout`. Синхронизация идёт через Unix-socket ответы,
файловые события и provider barriers, не через sleeps. SIGKILL направляется только группе
созданного runner-ом Pi-процесса. После graceful exit/SIGKILL проверяются reap, пустая
owned process group, закрытие PTY/control FD и удаление socket. Все 12 процессов финального
прогона очищены, orphan count равен нулю.

Полные ANSI PTY traces, control/events и фактические provider headers/body остаются только
в приватном `PROBE_ROOT` (`pty-*.raw`, `control.jsonl`, `*-events.jsonl`,
`provider-requests.jsonl`). В Git сохраняются лишь сокращённые счётчики `pty-result.json`.
Provider oracle на каждом `before_agent_start` создаёт свежий UUID, а
`before_provider_headers` ставит его только для `artel-fixture/fixture`. Runner сопоставляет
полное фактическое body с типом инициатора, session ID, details/prompt, generation, attempt,
journal и owner. Повтор запроса для одного токена, отсутствующий токен/хук или barrier timeout
— FAIL, не PASS. Provider trace не доступен логике recovery bridge.

### Подтверждённая PTY-матрица

| Сценарий | Pi sends | HTTP | starts custom/user | Итог |
|---|---:|---:|---:|---|
| positive custom + idempotent redelivery | 1 | 1 | 1/0 | `observed`, второй вызов отсутствует |
| manual first, provider удержан | 0 | 1 | 0/1 | ноль admission/spend, intent только local pending, Pi queue пуст |
| busy появилась после spend/attempt | 0 | 1 | 0/1 | финальный `ctx.isIdle()` отклонил send; право потрачено, `unknown` |
| custom first, draft + отдельный old-marker user turn | 1 | 3 | 1/2 | draft сохранён; три свежих токена; старый маркер не создал receipt |
| crash после attempt, до Pi; restart | 0 | 0 | 0/0 | `unknown`, ноль новых вызовов |
| crash после передачи, до receipt; restart | 1 | 1 | 1/0 | `unknown`, ноль новых вызовов |
| crash после local receipt, до POST; restart | 1 | 1 | 1/0 | тот же receipt/attempt опубликован, `observed`, ноль новых вызовов |
| owned PTY cleanup control | 0 | 0 | 0/0 | SIGKILL и graceful exit очищены, orphans=0 |

В manual-first ветке внешняя команда доставки при busy пишет только local intent; у bridge
нет `agent_settled` follow-up. В custom-first ветке уникальный ручной текст вводится в настоящий
TUI editor **без Enter** во время held streaming. Публичный `ctx.ui.getEditorText()` подтверждает
сохранность до и после custom `agent_settled`; Enter отправляется только затем. Provider body
и `before_agent_start` подтверждают отсутствие потери, подмены и дубля. Отдельный ручной ход со
старым маркером получает новый токен и `messageType=user`, но не новый bridge send/receipt.

Steer/follow-up очередь в этой матрице не проверяется; от неё здесь не требуется новый
`before_agent_start`. Результаты ограничены process crash, числовым loopback fixture и данной
версией Pi: это не provider ACK, completion, exactly-once, power-loss или внешний provider claim.
Итоговое продуктовое решение и Task 4 этим стендом не выполняются.
