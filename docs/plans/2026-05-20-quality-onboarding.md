# Эпик Q: Качество и onboarding

Остановиться, проверить качество, подготовить агента для первых пользователей.

**Дата создания:** 2026-05-20  
**Статус:** 🟢 Готов к разработке  
**Приоритет:** текущий — перед распространением

---

## Контекст

Агент работает, функции реализованы. Перед тем как давать доступ друзьям/знакомым,
нужно убедиться в качестве и подготовить материалы для onboarding.

**Модель распространения:** ты поднимаешь Docker-контейнер для каждого пользователя,
они дают токен бота — ты настраиваешь. Пользователи не деплоят сами.

**Реальное покрытие тестами (по состоянию на 2026-05-20):**

| Пакет | Покрытие | Оценка |
|-------|----------|--------|
| `internal/config` | 91.5% | ✅ хорошо |
| `internal/news` | 81.4% | ✅ хорошо |
| `internal/tools` | 66.2% | 🟡 есть дыры |
| `internal/agent` | 60.0% | 🟡 есть дыры |
| `internal/llm` | 59.4% | 🟡 норм для LLM |
| `internal/storage` | 57.2% | 🔴 критично |
| `internal/telegram` | 29.7% | 🟡 в основном интеграция |
| `cmd/agent` | 18.2% | ✅ wiring-код, норм |

**Критические дыры в покрытии:**
- `internal/storage/reminders.go` — 0% (CreateReminder, ListReminders, GetDueReminders, MarkReminderDone, RescheduleReminder, CancelReminder)
- `internal/storage/workout.go` — 0% (все методы CRUD)
- `internal/storage/sqlite.go` — UpdateNote, DeleteNote, GetNote = 0%
- `internal/tools/discipline.go` — 0% (весь инструмент)

---

## Задачи

### Трек A: Тесты (критические дыры)

- [x] **Q-1 Тесты: `internal/storage/reminders.go`**  
  Написать тесты для всех 6 функций: CreateReminder, ListReminders, GetDueReminders,
  MarkReminderDone, RescheduleReminder, CancelReminder.  
  Критично: напоминания используются в real-time планировщике.  
  Файл: `internal/storage/reminders_test.go`

- [x] **Q-2 Тесты: `internal/storage/workout.go`**  
  Написать тесты для: CreateWorkout, AddWorkoutSet, GetWorkout, ListWorkouts, DeleteWorkout,
  GetWorkoutStats, GetPersonalRecords, GetExerciseHistory, GetNewPRsFromWorkout,
  CreateWorkoutTemplate, ListWorkoutTemplates, GetWorkoutTemplate, DeleteWorkoutTemplate,
  CountWorkoutsForGoal.  
  Файл: `internal/storage/workout_test.go`

- [x] **Q-3 Тесты: `internal/storage/sqlite.go` — Notes CRUD**  
  UpdateNote, DeleteNote, GetNote сейчас = 0%.  
  Добавить тест-кейсы в `internal/storage/sqlite_test.go`.

- [x] **Q-4 Тесты: `internal/tools/discipline.go`**  
  Весь инструмент без покрытия. Написать тесты для основных actions:
  day_save, day_get, day_list, day_stats, week_save, week_get, dashboard.  
  Файл: `internal/tools/discipline_test.go`

- [x] **Q-5 Тесты: `internal/agent` — HandleMessageWithAttachments**  
  Функция HandleMessageWithAttachments = 0%. Добавить базовый тест.  
  Файл: `internal/agent/agent_test.go`

### Трек B: Документация для оператора

- [ ] **Q-6 Создать `DEPLOY.md`**  
  Пошаговое руководство для тебя (оператора) по запуску нового пользователя:
  1. Пользователь создаёт бота через @BotFather → получает токен
  2. Пользователь даёт тебе: токен + свой `chat_id` + API-ключ LLM (или используешь общий)
  3. `docker run` или `docker-compose` с env vars: TELEGRAM_TOKEN, TELEGRAM_ALLOWED_USERS, LLM_API_KEY
  4. Проверка: `/start` в Telegram
  
  Также: как обновить образ, как посмотреть логи, как остановить контейнер.

- [ ] **Q-7 Упростить `config.example.yaml`**  
  Разбить на секции: "минимальная конфигурация" (3–5 строк, всё необходимое) и
  "расширенная конфигурация" (всё остальное с комментарием "можно не менять").  
  Цель: новый пользователь должен знать что заполнять не читая весь файл.

### Трек C: Документация для пользователей

- [ ] **Q-8 Создать `USER_GUIDE.md`** (русский язык)  
  Что умеет агент с примерами запросов:
  - Заметки: "Запомни идею: ...", "Найди заметки по тегу #работа"
  - Напоминания: "Напомни мне в пятницу про встречу", "Какие у меня напоминания?"
  - Погода: "Какая погода в Москве на выходные?"
  - Новости: "Что нового в tech сегодня?"
  - Акции: "Какой курс SBER? Покажи S&P 500"
  - Поиск: "Найди информацию про ..."
  - Веб: "Прочитай эту статью: [URL]"
  - Дисциплина/OKR: "Как прошёл мой день?", "Мои цели на неделю"
  - Тренировки: "Записать тренировку: ..."

- [ ] **Q-9 Улучшить `/help`**  
  Текущий `/help` скорее всего перечисляет команды. Добавить раздел "что я умею" —
  короткое описание возможностей + подсказка "спроси меня о чём угодно".

- [ ] **Q-10 Onboarding-сообщение при первом `/start`**  
  Если это первое сообщение пользователя (нет истории в БД) — показать краткое
  приветствие с 3–5 примерами что можно попросить агента сделать.

### Трек D: Инфраструктура

- [ ] **Q-11 Публикация образа на ghcr.io или Docker Hub**  
  GitHub Actions workflow: при пуше в `main` — собирать образ и пушить с тегом `latest` и `sha`.
  Это позволит делать `docker pull` вместо сборки на каждой машине.

- [ ] **Q-12 Скрипт `scripts/add-user.sh`**  
  Минимальный bash-скрипт: принимает имя пользователя, токен, chat_id, [api_key] →
  запускает `docker run` с правильными параметрами, именем контейнера `agent-<name>`,
  именованным volume `agent-<name>-data`.

---

## Порядок выполнения

```
Q-1, Q-2, Q-3, Q-4 (тесты, параллельно)
      ↓
Q-5 (agent тест)
      ↓
Q-6, Q-7, Q-8 (документация, параллельно)
      ↓
Q-9, Q-10 (улучшения бота)
      ↓
Q-11, Q-12 (инфраструктура)
```

---

## Roadmap после Q

После завершения этого эпика — вернуться к бэклогу:

| Фаза | Что | Когда |
|------|-----|-------|
| **Phase 1** | K: База знаний / Вики | следующий эпик |
| **Phase 1** | G: GitHub + Мониторинг | параллельно с K |
| **Phase 2** | Multi-tenant: один процесс, несколько ботов | при росте до 10+ пользователей |
| **Phase 3** | Cloud SaaS: веб-интерфейс, биллинг, Postgres | при коммерциализации |

### Путь к multi-tenant (Phase 2)

Текущая архитектура (`chat_id` изоляция + один контейнер на юзера) легко
трансформируется в multi-tenant без переписывания:

1. Добавить `user_id` / `tenant_id` к сущностям где сейчас `chat_id`
2. Один процесс запускает N Telegram-полеров (по одному на бота)
3. Shared SQLite или Postgres
4. Веб-интерфейс для регистрации (отдельный сервис)

Текущая изоляция через `chat_id` — правильный задел под это.
