# Протокол доставки: модель и граница журнала

Файлы этого каталога — **только тестовый fixture эксперимента №2**, а не API Artel,
продуктовый bridge или расширение Pi. `protocol.mjs` моделирует fake Artel, локальный
журнал и вызов передатчика. Отдельный `sdk-matrix.mjs` через публичный SDK pinned Pi
1.5.2 и inline extension подключает эту модель к числовому loopback-провайдеру. Рабочие
API/БД Artel, реальные провайдеры, инструменты, сессии и учётные данные не используются.
Все тесты используют вымышленные сообщения и приватные временные каталоги.
[Отчёт Task 4](evidence.md) связывает наблюдения с допустимым восстановлением,
фиксирует чистый повтор и предлагает продуктовую границу квитанции.

## Неизменяемая идентичность и состояния

Ключ сообщения — `message_id`. При первом допуске fake Artel фиксирует неизменяемые
`message_id`, `generation`, `session_id` и полный payload (`content` и `details`).
`details.message_id` обязан совпадать с неизменяемым `message_id` при допуске,
при проверяемом переоткрытии сервера/журнала и при публикации квитанции.
Payload должен без потерь сохраняться как дерево JSON. Например, `-0`, `undefined`,
разреженные массивы и свойства с getter отклоняются до admission/intent/spend.
Повтор с другим телом, поколением или Pi-сессией под тем же ID — конфликт, а не новая
попытка. Внутренний статус сообщения:

```text
отсутствует --admission commit--> admitted --spend commit--> unknown
unknown --before_agent_start receipt commit--> observed
```

`admitted` означает лишь сохранённое поручение. Право на вызов появляется только в
**первом успешном ответе spend**. Spend атомарно привязывает `attempt_id` к неизменяемой
идентичности сообщения, активному поколению, тестовой Pi-сессии, PID и идентификатору
запуска текущего процесса-владельца, а также `journal_id`. Fake Artel сверяет PID с
текущим процессом при spend и PID/идентификатор запуска при verify. Повтор admission/spend/receipt с совместимым телом возвращает состояние;
повтор spend никогда не возвращает право. Другой владелец, поколение или журнал не
восстанавливают израсходованное право. Параллельные дубли сериализуются fake Artel и
дают не более одного права и одного вызова даже при двух живых отправителях
с разными локальными журналами.

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
в fake Artel с исходной привязкой к потраченному attempt. Fake Artel проверяет
тип, инициатор, details и session ID при каждой публикации, в том числе после
перезапуска writer. До commit квитанции fake Artel остаётся
`unknown`. Если квитанция уже сохранена локально, сбой публикации или потерю её
ответа можно восстановить повтором публикации той же записи. Если локальная запись
не сохранилась, повторить её можно только пока доступно исходное наблюдение; иначе
исход остаётся `unknown`. Ни один из этих путей не повторяет вызов Pi.

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
поколение и владельца по серверному файлу, а затем сохранённый attempt и `journal_id`
по локальному файлу. Между серверной проверкой и фактическим
синхронным входом в `sendMessage` остаётся **неустранимое этим протоколом окно**: отзыв
после успешной проверки может не отменить уже выдаваемый вызов. Тест фиксирует это окно
явным барьером; обещания отмены после допуска или exactly-once нет.

Передатчик PTY дополнительно делает синхронный `ctx.isIdle()` непосредственно перед
`sendMessage`. Если к этому моменту начался ручной ход, он возвращает `false`: вызова Pi
нет, а уже потраченное право и локальный attempt остаются в `unknown`; повтор права не
восстанавливает. Это отдельная busy-проверка, а не ослабление границы отзыва поколения.

## Журнал, проверяемые операции и восстановление

На Linux fixture принимает только принадлежащий текущему UID каталог `0700` и обычные
файлы `0600`; `O_NOFOLLOW | O_NONBLOCK` и `fstat` отклоняют подмену symlink/FIFO.
Протокольные операции Fake Artel перечитывают состояние, проверяют его и выполняют
commit под одним Linux abstract socket lock на путь документа. `snapshot()` и геттеры
состояния читают кэш текущего handle без lock и перечитывания. Ядро освобождает lock
при завершении процесса. Переходы локального журнала сериализуются; отдельный writer
со старым снимком не может затереть новые записи. Для восстановления обязателен исходный
`journal_id`, сохранённый вызывающей стороной при подготовке, а не прочитанный из
восстанавливаемого файла. Пропавший журнал не создаётся заново.

Запись использует временный файл `0600`, `fsync`, `close`, `rename` и `fsync` каталога.
При ошибке после `rename` текущий handle перестаёт обслуживать чтение/запись;
проверенное переоткрытие сверяет данные и синхронизирует каталог, не возвращая
потраченное право. Это модель process crash при сохранённых файлах, **не гарантия
при power loss**, откате/потере серверного журнала или защита от другого кода того же UID.

`afterCommit` и `beforeResponse` fake Artel — раздельные явные барьеры фиксации и ответа;
`afterSpendResponse` и `afterFinalVerification` отделяют последующие границы bridge.
Тесты не синхронизируются сном. В сценарии восстановления родитель останавливает собственный
дочерний процесс после commit spend и до ответа, затем новый процесс заново открывает те же
`server.json` и `journal.json`. Старая память недоступна; повтор возвращает `unknown`, ноль
новых вызовов. Отдельная проверка останавливается после admission до spend: читаемая
подмена журнала с другим ID отклоняется, оба файла остаются неизменными, вызовов нет.

## Запуск модели

Из корня Artel на Linux с Node 22.23.2 и `mkfifo`:

```bash
"$NODE" --test docs/experiments/pi-bridge-delivery/*.test.mjs
```

Проверяются успех, повторы, конфликт payload/identity/receipt, параллельные дубли, сбой
записи attempt/receipt, отличие custom observation от completion/provider ACK, повреждённый/
нечитаемый/чужой/несовпадающий журнал, отзыв на всех границах, потерянные ответы после commit
и fresh-process recovery. Эта команда не запускает SDK/PTY матрицы.

## Изолированная SDK crash-матрица

`sdk-matrix.mjs` — отдельный runner; `sdk-worker.mjs` создаёт только тестовые SDK-сессии
через `createAgentSession`, `DefaultResourceLoader`, `SessionManager`, `SettingsManager` и
`ModelRuntime`. Укажите абсолютные пути к уже установленным Node **22.23.2** и
пакету `@earendil-works/pi-coding-agent` **1.5.2**. Путь установки не зафиксирован.
Для обеих матриц нужны Linux и принадлежащий текущему UID каталог `PROBE_ROOT`
режима `0700` вне checkout с абсолютным путём `/tmp/artel-pi-delivery-*`; для PTY
также нужны Python 3.11+ и GNU `timeout`. Runner отклоняет другую версию, отсутствующие
публичные API, лишние переменные окружения и неприватные HOME/XDG/Pi dirs. Ничего
не устанавливает и не меняет настройки хоста.

Точное воспроизведение из корня Artel (все значения вымышленные, ключ — только литерал
`fake-not-a-secret`):

```bash
: "${NODE:?set the absolute path to Node 22.23.2}"
: "${PACKAGE:?set the absolute path to the installed Pi 1.5.2 package}"
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

Полные event traces, конфигурации и HTTP body/headers сохраняются в новом каталоге
`sdk-*` под приватным `PROBE_ROOT` вне worktree (`*-trace.jsonl`, `provider-requests.jsonl`
и логи owned children). В Git сохраняются лишь runner и сокращённые счётчики `sdk-result.json`.
Watchdog ограничивает сценарий; SIGKILL направляется только объектам собственных дочерних
`ChildProcess`. Истечение watchdog блокирует запуск recovery, а SIGTERM — запуск
любого worker, включая проверочные. Ошибка записи диагностического лога делает прогон
неуспешным, но не прерывает остановку и reap. Точки сбоя задаются IPC/filesystem barriers.

### Независимый oracle и позитивный контроль

На каждом `before_agent_start` inline extension создаёт новый UUID и сохраняет тип инициатора,
session ID, details/prompt и привязку attempt. `before_provider_headers` ставит этот токен
только для provider `artel-fixture` и model `fixture`. Родительский числовой HTTP fixture на
`127.0.0.1` отдельно сохраняет полное принятое body и считает запросы по токену. У каждого
сценария свой listener; он связывает запрос со сценарием до чтения тела. Worker использует
тестовый `fetch` с одним реальным loopback POST на отдельном сокете, без пула, редиректов
и транспортных повторов. Перед записью нужно получить IPC ACK регистрации уникального
transport ID (не токена хода). После остановки работников listener остаётся открыт, пока HTTP
fixture не прочитает полное тело для каждой регистрации; лимит drain — 3 с. Истечение срока,
незарегистрированный или повторный transport ID — ошибка измерения, а не PASS. Отсутствие
HTTP callback или выход процесса не подтверждают drain. Регрессия буферизует полный повтор
на idle keep-alive сокете до HTTP callback и проверяет, что повтор не теряется при shutdown.
Это граница управляемого SDK fixture, не проверка штатного пула `fetch` или произвольных клиентов.

Runner взаимно-однозначно сверяет token events и запросы, типы инициаторов, identity/generation/
session/attempt/journal/owner и точный текст последнего user-сообщения в разобранном `body.messages`.
Инструменты, retry и compaction отключены. Provider trace недоступен worker recovery и не
может превратить `unknown` в квитанцию. После выхода работников и drain runner сверяет
итоговое число Pi send, HTTP и start с ожиданием сценария, а не только друг с другом.
Отсутствие токена/хука или barrier timeout завершает прогон ошибкой, а не PASS.

Позитивный контроль выполняется первым: один custom message даёт один Pi send, один
коррелированный provider request и одну custom-квитанцию. Idempotent redelivery не добавляет
вызовов. Отдельный ручной ход со старым маркером получает новый токен, `messageType=user` и
второй provider request, но ноль новых bridge send и запусков receipt writer по полному
потоку IPC после reap. Это различает маркер в истории и новую доставку даже при
идемпотентной записи квитанции.

В сценарии Pi-only crash исходный coordinator остаётся жив. Runner подключает оба IPC
направления к новому Pi с тем же session ID, затем повторяет обычный `deliver`. После
shutdown и drain сверяются дельты IPC `pi-transmit`/Pi/HTTP и полное содержимое сохранённых
server/journal файлов до crash и после повтора; квитанция отсутствует. Совпадение ID не
означает восстановление истории Pi.

Измеренные сценарии и счётчики: [SDK-матрица в отчёте](evidence.md#sdk-точка-сбоя--наблюдения--допустимое-восстановление) и [санитизированный JSON](sdk-result.json).

## Настоящий Pi TUI в изолированном PTY

`pty_matrix.py` запускает публичный CLI Pi 1.5.2 в настоящем Linux PTY,
без окна, agterm и SDK-обхода. Два явных тестовых `-e` загружаются вместе с
`--no-extensions --no-skills --no-prompt-templates --no-context-files --no-themes --no-mcp
--no-builtin-tools`; `session_start` дополнительно требует `ctx.mode=tui` и пустой активный
набор инструментов. Preflight берёт публичный `pi` entry point из `bin` пакета и проверяет
`pi --version`; запуск проверяет реальный TUI, модель и инструменты без приватных API.
Приватные settings отключают retry/compaction; внешние конфиги, credentials и ресурсы не
наследуются и не читаются. Ничего не устанавливается и не патчится.

Точное воспроизведение из корня Artel:

```bash
: "${NODE:?set the absolute path to Node 22.23.2}"
: "${PACKAGE:?set the absolute path to the installed Pi 1.5.2 package}"
umask 077
ROOT=$(mktemp -d /tmp/artel-pi-delivery-XXXXXXXX)
mkdir -p "$ROOT/home" "$ROOT/config" "$ROOT/cache" "$ROOT/state" "$ROOT/data" "$ROOT/agent" "$ROOT/sessions"
timeout 240s env -i \
  HOME="$ROOT/home" XDG_CONFIG_HOME="$ROOT/config" XDG_CACHE_HOME="$ROOT/cache" \
  XDG_STATE_HOME="$ROOT/state" XDG_DATA_HOME="$ROOT/data" \
  PI_CODING_AGENT_DIR="$ROOT/agent" PI_OFFLINE=1 LANG=C.UTF-8 \
  PATH="$(dirname "$NODE"):/usr/bin:/bin" \
  PROBE_ROOT="$ROOT" PI_TEST_NODE="$NODE" PI_TEST_PACKAGE="$PACKAGE" \
  /usr/bin/python3 -B docs/experiments/pi-bridge-delivery/pty_matrix.py
```

Runner принимает только абсолютный pinned Node 22.23.2/package и приватный `PROBE_ROOT`
`0700` вне worktree; окружение вне белого списка отклоняется. Все ожидания измерения
используют общий абсолютный deadline 30 с. Очистка имеет отдельный бюджет до 5 с;
истечение 30 с до публикации результата исключает PASS. Вся команда ограничена внешним `timeout`. Синхронизация идёт через Unix-socket ответы,
файловые события и provider barriers, не через sleeps. Для обычного сценария runner
сигналит только своей Pi group. После graceful exit/SIGKILL проверяются reap, пустая
owned process group, закрытие PTY/control FD и удаление socket. В проверках зачистки
супервизор создаёт группу до запуска probe, Pi её наследует; PGID не передаётся из probe.
Проверяются штатная зачистка при SIGTERM, принудительная остановка при игнорировании
SIGTERM и истечение срока startup после запуска Pi, но до ready. Супервизор сам собирает
осиротевшую Pi как Linux subreaper и проверяет, что группа пуста.

Полные ANSI PTY traces, control/events и фактические provider headers/body остаются только
в новом каталоге `pty-*` под приватным `PROBE_ROOT` (`pty-*.raw`, `control.jsonl`, `*-events.jsonl`,
`provider-requests.jsonl`). В Git сохраняются лишь сокращённые счётчики `pty-result.json`.
Provider oracle на каждом `before_agent_start` создаёт свежий UUID, а
`before_provider_headers` ставит его только для `artel-fixture/fixture`. Runner сопоставляет
точное последнее user-сообщение в разобранном `body.messages` с типом инициатора,
session ID, details/prompt, generation, attempt, journal и owner. Итоговая сверка идёт
после остановки Pi и завершения принятых HTTP-запросов: сверяются ожидаемые числа
Pi send, HTTP и start, а в manual-first серверная запись должна оставаться отсутствующей
и локальная — содержать только intent. В custom-first после выхода Pi проверяется,
что ручные ходы не вызвали дополнительных запусков receipt writer. Повтор запроса для одного
токена, отсутствующий токен/хук или barrier timeout — FAIL, не PASS. Provider trace не доступен логике recovery bridge.

Измеренные сценарии и счётчики: [PTY-матрица в отчёте](evidence.md#настоящий-pi-tui-в-pty-отдельная-матрица) и [санитизированный JSON](pty-result.json).
