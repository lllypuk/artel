# Artel desktop: обзор команды, canon × Zed

Статус: подтверждены первый экран (вся команда), направление `canon` и ориентир Zed Editor. Документ — brief будущей реализации, не отчёт о выполненном редизайне. Связанный исполняемый план находится в `docs/plans/` либо после выполнения в `docs/plans/completed/`; активные инструкции не должны ссылаться на soon-to-move файл.

## Задача и пользователь

Владелец открывает локальный Artel, чтобы сразу понять состояние всех **подключённых наблюдателей** по всем проектам. Mode: **Operate**, native Qt Quick desktop, Omarchy. Затем — перейти к наблюдателю, заявленной терминальной сессии или к ручным задачам проекта. Это не IDE, не чат и не новый orchestrator.

## Выбранное направление

- Пользователь выбрал вариант 4 `canon`: обычные desktop navigation, фильтры, читаемый состав команды и детали справа. Источник компоновки — `docs/design/reference/canon-team-overview.html` и `canon-team-overview.png`, fictional data. Это структурный ориентир, не pixel-exact HTML comp для копирования в Qt.
- Пользователь назвал Zed Editor эталоном качества. Первичные источники просмотрены 2026-11-18: [appearance](https://zed.dev/docs/appearance), [публичный screenshot agent sidebar](https://zed.dev/img/parallel-agents/sidebar.webp), [официальная страница](https://zed.dev/). В screenshot видны тонкие границы, негромкая иерархия поверхностей и явное выделение текущего элемента; документация разделяет UI-font и editor/terminal-font.
- Переносим плотность и ясность рабочей среды, не содержимое editor/agent panel, multibuffer, code font, Zed iconography/assets или исполнение агентов. Системный читаемый UI sans, не моноширинный интерфейс. Никаких маркетинговых hero, декоративных графиков, неонового свечения, glassmorphism или имитации терминала.
- Поверхности нейтральные тёмные; исходная canon-палитра — отправная точка, не обязательство копировать Zed-тему. Omarchy accent используется для selection/focus/primary action с проверенным контрастом; error/warning/freshness имеют свои семантические роли и текст.

## Информационная архитектура

1. Постоянная левая навигация: **Команда**, **Проекты**, затем существующий список проектов. Не добавлять отдельный глобальный журнал или inbox, которым нет соответствующего API. История остаётся внутри задачи.
2. **Команда — стартовый экран каждого запуска.** Вверху заголовок, компактная сводка подключённых наблюдений и фильтры. Основное поле — roster по observer identity, не по каждой runtime. Справа — выбранный наблюдатель; на узкой ширине — раскрытые inline details, не новое окно.
3. **Проекты** — обзор проектов с доступом к созданию/архиву. Выбор проекта открывает workspace: задачи и очередь в основной области, описание/редактирование/история выбранной задачи справа. На узком экране детали раскрываются внутри workspace. Не переносить все операции на главный экран.
4. Кандидаты/непривязанные регистрации, superseded и revoked — отдельная компактная область/фильтр управления подключениями. Имя наблюдателя — owner label, не автоматически назначенная роль. Технические UUID, versions, provider/model и управление привязкой доступны в деталях.

## Правдивость состава

- Текущий состав: одна запись на **неотозванный observer с текущей binding**, включая stale/stopped. Две runtime одного observer не считаются двумя участниками.
- Непривязанные/candidate runtimes не увеличивают численность работающей команды. Для observer с bound runtime его дополнительные candidates показываются только в подключениях, не как дубликаты состава.
- Idle = «Свободен»; waiting_user = «Ожидает ввода в Pi», не «ждёт согласования владельца в Artel». Stale/unknown скрывает утверждение о текущей активности: видна потеря наблюдения, а не ложное «работает» или «свободен». Stopped не равен completed.
- Никакого task-assignment, token spend, продуктивности, MR progress или fabricated timeline: этих фактов нет в observer API. Текущая ручная задача проекта не объявляется задачей выбранного агента.
- Сводка — counts по тем же данным/срезу, что список; фильтры не заставляют исчезнуть выбранную роль молча. При offline все сохранённые показатели явно cached/unknown, не live.

## Взаимодействия и состояния

- Поиск по label и имени проекта, фильтры проекта/активности/свежести не меняют `Panel.project`, task selection, manual draft или observer binding. Явный «Открыть проект» проходит прежние guards.
- Строка открывает детали; «Открыть сессию agterm» — прежний best-effort переход без запуска и без обещания точного pane focus. Rebind/unbind/revoke/prune — вторичный блок с прежними CAS и подтверждениями.
- Перейти из редактора в обзор команды для чтения можно, сохранив draft и base version. Видна ненавязчивая возможность вернуться к черновику; выбор другого проекта/сущности остаётся защищён. Ctrl+S действует только в видимом редакторе. Close/discard/unknown-outcome confirmations глобальны и не теряются при смене экрана.
- Empty: объяснить, что ещё никто не подключён; дать краткие шаги CLI provisioning и доступ к существующей документации. Не имитировать работающий onboard flow/agent launch. Нулевой результат фильтра отличается от пустого реестра.
- Loading, no project, archived/read-only, API offline, pending, conflict и unknown mutation outcome имеют собственные сообщения и действия восстановления. Не превращать отказ в временно пустой «здоровый» список.

## Визуальная система и диапазоны

- Нейтральные фон/панель/raised/selection, 1px разделители; corners связываются с существующим Appearance radius. Primary, secondary, quiet и destructive — разные роли, не все кнопки одинаковой тяжести.
- Стартовая шкала размеров в логических Qt единицах: body 14, secondary ≥12, panel title 18, screen title 24; исходить из системного UI font и проверять при увеличении масштаба. Сетка отступов 4/8/12/16/24/32; контролы ~32–36, строки ~48–56. Результат должен проходить реальные размеры и контраст, а не защищать цифры против рендера.
- Основные окна проверки: 1440×900, 1280×820, 1000×680; scaling 100/150/200%. Не уменьшать существующий minimum size без отдельной валидации всех потоков. Ни длинные русские слова, ни строки Unicode, ни длинные критерии не перекрывают действия. Большие списки — ListView/модель, не Repeater всей команды.
- 0/1/12/256 observers, до 8 runtime records на observer, long/duplicate labels, множество проектов, archived/missing project, empty/search-no-result. Stable selection по IDs, не по индексу.
- Color-independent statuses, visible focus, keyboard route/list/details/edit actions, standard shortcuts, accessible labels. Не делать все даты или все notices верхнеуровневыми заголовками. Relative last-seen в списке, полный timestamp — в details.
- Motion по умолчанию не нужна; допустима короткая ненавязчивая смена selection/details без пульсирующих статусов и постоянной анимации. Скорость, читаемость и фокус важнее эффектов.

## Границы и приёмка

Только desktop presentation/C++ viewmodels/fixtures/docs. API/store/agent protocol, scoped credentials, token handling, ручные states, idempotency и durability не менять. Рабочий сервер владельца не останавливать, не перезапускать, его data/config/token не читать для визуализации. Все live/native acceptance fixtures отдельные. Существующие isolated pinned-Pi SDK/PTY tests с loopback deterministic provider разрешены; пользовательские Pi-сессии/credentials и внешние providers не используются. Переход к observer из общего обзора валидируется по unfiltered sanitized records, независимо от project filter управления подключениями.

Приёмка — production QML, manual и observer процессы на тестовых данных, screenshot matrix и визуальная проверка, keyboard/contrast/scale/long-text, no-Pi baseline, `make check`/`make package-check`. HTML sketch или Chromium screenshot не заменяют native UI evidence. Установка/подмена работающего бинарника, запуск пользовательского pilot, merge/push main не включены.

PRODUCT.md, этот brief, будущий DESIGN.md и source fixture/screenshots нужно сохранять в Git. Временные `.impeccable/questions`, browser profiles, review screenshots и невыбранные макеты не публиковать автоматически. Закрытое окно выбора не открывать снова без запроса пользователя.
