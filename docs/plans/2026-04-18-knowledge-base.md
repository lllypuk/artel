# Эпик K: База знаний / Личная вики

Единая база: личная вики с иерархией + сохранённые статьи + список для чтения.
Особый приоритет — личная вики как структурированное хранилище знаний.

**Статус:** 🟢 Готов к разработке  
**Приоритет:** 4 — самый объёмный эпик

---

## Задачи

- [ ] **K-1 Инструмент `wiki`**  
  CRUD для вики-страниц с иерархией путей (`/dev/golang`, `/health/бег`, `/books/clean-code`).  
  Файлы: `internal/tools/wiki.go`, миграция в `internal/storage/sqlite.go`

- [ ] **K-2 Структура и навигация**  
  Создать / читать / редактировать / удалить страницу. Листинг дочерних страниц.  
  "Покажи /dev" → список всех страниц в разделе dev. "Дерево знаний" → полная структура.

- [ ] **K-3 Ссылки между страницами**  
  Внутренние ссылки в тексте `[[путь/к/странице]]`. Обратные ссылки (backlinks): "что ссылается на эту страницу?".  
  Хранить граф ссылок в отдельной таблице `wiki_links`.

- [ ] **K-4 Полнотекстовый поиск**  
  SQLite FTS5 по вики-страницам, сохранённым статьям и заметкам одновременно.  
  "Найди всё про Golang" → результаты из вики + статьи + заметки с указанием источника.

- [ ] **K-5 Сохранение статей**  
  URL → web_fetch → авто-саммари (LLM) + полный текст, хранение в базе с датой, тегами, источником.  
  "Сохрани эту статью: [URL]" → сохранено, показывает краткое резюме.

- [ ] **K-6 Список для чтения**  
  Закладки со статусом (to-read / reading / done), теги, приоритет (high/medium/low).  
  "Добавь в список для чтения" / "Что у меня в очереди на чтение?"

- [ ] **K-7 Быстрый захват**  
  "Запомни: ..." или "сохрани ссылку [URL]" — минимум вопросов, максимум скорости.  
  Попадает в inbox, потом можно структурировать.

- [ ] **K-8 Дайджест непрочитанного**  
  По запросу или в утреннем брифинге (P-2): "у тебя 3 непрочитанных статьи по Go и 2 по финансам".

---

## Схема БД (примерная)

```sql
CREATE TABLE wiki_pages (
  id INTEGER PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,  -- /dev/golang
  title TEXT NOT NULL,
  content TEXT NOT NULL,
  tags TEXT,                  -- JSON array
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE VIRTUAL TABLE wiki_pages_fts USING fts5(
  path, title, content, tags,
  content='wiki_pages', content_rowid='id'
);

CREATE TABLE wiki_links (
  from_path TEXT NOT NULL,
  to_path TEXT NOT NULL,
  PRIMARY KEY (from_path, to_path)
);

CREATE TABLE reading_list (
  id INTEGER PRIMARY KEY,
  url TEXT,
  title TEXT NOT NULL,
  summary TEXT,
  content TEXT,
  status TEXT DEFAULT 'to-read',  -- to-read/reading/done
  priority TEXT DEFAULT 'medium',
  tags TEXT,                       -- JSON array
  saved_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  read_at DATETIME
);
```

---

## Зависимости

- Используется в: P-2 (утренний брифинг — K-8)
- Использует: существующий `web_fetch` для K-5
- Дополняет: существующий инструмент `notes` (заметки остаются, вики — отдельный слой)
