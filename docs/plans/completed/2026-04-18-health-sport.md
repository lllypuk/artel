# Эпик H: Здоровье и спорт

Логирование тренировок, прогресс, личные рекорды, связь с целями.

**Статус:** 🟢 Готов к разработке  
**Приоритет:** 3

---

## Задачи

- [x] **H-1 Инструмент `workout`**  
  Логирование тренировок: вид (бег, силовая, йога...), дата, длительность (мин), нагрузка (вес/подходы/повторы), заметки.  
  Файлы: `internal/tools/workout.go`, миграция в `internal/storage/sqlite.go`

- [x] **H-2 История тренировок**  
  Поиск по виду, дате, периоду. Статистика: частота за неделю/месяц, суммарный объём, средняя длительность.  
  Используется в еженедельном отчёте (P-4).

- [x] **H-3 Шаблоны тренировок**  
  Сохранить программу (Push Day, Pull Day, Legs, Кардио и т.д.) с набором упражнений.  
  "Залогируй по шаблону Push Day" → агент подставляет упражнения, спрашивает веса/повторы.

- [x] **H-4 Прогресс и личные рекорды**  
  Отслеживание PR по упражнениям (жим, присед, становая и т.д.).  
  "Мой рекорд в жиме" → показывает историю весов. Автоматическое обновление PR при новом максимуме.

- [x] **H-5 Связь с целями**  
  Тренировка может быть привязана к долгосрочной цели из D-7 ("пробежать 10 км к сентябрю").  
  Прогресс тренировок виден в дашборде OKR.

---

## Схема БД (примерная)

```sql
CREATE TABLE workouts (
  id INTEGER PRIMARY KEY,
  date TEXT NOT NULL,
  type TEXT NOT NULL,       -- run/strength/yoga/...
  duration_min INTEGER,
  note TEXT,
  goal_id INTEGER,          -- FK -> objectives(id), опционально
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE workout_sets (
  id INTEGER PRIMARY KEY,
  workout_id INTEGER REFERENCES workouts(id),
  exercise TEXT NOT NULL,
  sets INTEGER,
  reps INTEGER,
  weight_kg REAL,
  distance_km REAL,
  note TEXT
);

CREATE TABLE workout_templates (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  exercises TEXT NOT NULL   -- JSON array
);
```

---

## Зависимости

- Используется в: P-4 (еженедельный отчёт)
- Связан с: D-7 (долгосрочные цели)
