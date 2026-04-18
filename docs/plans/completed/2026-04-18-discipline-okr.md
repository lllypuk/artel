# Эпик D: Самодисциплина + OKR

Система самодисциплины на базе двух подходов:
- **B**: вечерний чек-ин + еженедельный ревью (рефлексия)
- **C**: OKR + декомпозиция на задачи (система целей)

**Статус:** 🟢 Готов к разработке  
**Приоритет:** 2 — ядро личного помощника

---

## Задачи

- [x] **D-1 CRUD ревью дней и недель**  
  CRUD для ревью дней и недель в SQLite. Схема: `day_reviews`, `week_reviews`.  
  Файлы: `internal/tools/discipline.go`, миграция в `internal/storage/sqlite.go`

- [x] **D-2 Ревью дня**  
  Структурированный разбор: что планировал / что сделал / почему не сделал / оценка 1-10 / настроение / заметка.  
  Хранится в БД, доступен для анализа. Инициируется вечерним чек-ином (P-3).

- [x] **D-3 Еженедельный ревью**  
  Шаблон по воскресеньям: wins, fails, главный паттерн недели, план на следующую неделю.  
  Инициируется еженедельным отчётом (P-4).

- [x] **D-4 История и паттерны**  
  Команды агенту: "покажи ревью за месяц", "в какие дни чаще срываюсь", "средняя оценка недели".  
  Анализ на основе истории day_reviews.

- [x] **D-5 OKR инструмент**  
  Квартальные Objectives + Key Results: создать, обновить прогресс (0-100%), завершить, архивировать.  
  Схема: `objectives`, `key_results`. Файл: `internal/tools/goals.go`

- [x] **D-6 Декомпозиция целей**  
  OKR → недельные задачи → ежедневные действия. Связь с напоминаниями (existing reminders tool).  
  "Разбей эту цель на задачи на эту неделю" → агент использует `goals` + `reminders` совместно.

- [x] **D-7 Долгосрочные / среднесрочные цели**  
  Цели с горизонтом: 1 год, квартал, месяц. Статус (active/paused/done), прогресс в %, заметки.  
  Часть инструмента `goals` (параметр `horizon`).

- [x] **D-8 Дашборд дисциплины**  
  По запросу: сводка — стрик ревью дней (сколько дней подряд делал ревью), % выполнения OKR по текущему кварталу, топ-3 активные цели.

---

## Схема БД (примерная)

```sql
CREATE TABLE day_reviews (
  id INTEGER PRIMARY KEY,
  date TEXT NOT NULL,
  planned TEXT,
  done TEXT,
  not_done TEXT,
  reason TEXT,
  score INTEGER,  -- 1-10
  mood TEXT,
  note TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE objectives (
  id INTEGER PRIMARY KEY,
  title TEXT NOT NULL,
  horizon TEXT,  -- year/quarter/month
  quarter TEXT,  -- 2026-Q2
  status TEXT DEFAULT 'active',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE key_results (
  id INTEGER PRIMARY KEY,
  objective_id INTEGER REFERENCES objectives(id),
  title TEXT NOT NULL,
  progress INTEGER DEFAULT 0,  -- 0-100
  deadline TEXT,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

---

## Зависимости

- Используется в: P-3 (вечерний чек-ин), P-4 (еженедельный отчёт), P-5 (напоминания о целях)
