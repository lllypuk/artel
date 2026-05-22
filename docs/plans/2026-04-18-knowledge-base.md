# Эпик K: База знаний / Личная вики

Единая база: личная вики с иерархией + сохранённые статьи + список для чтения.
Особый приоритет — личная вики как структурированное хранилище знаний.

**Статус:** 🟢 Готов к разработке  
**Приоритет:** 4 — самый объёмный эпик

---

## Overview

Добавить в агента инструменты для ведения личной вики и списка для чтения:
- **wiki** — CRUD страниц с иерархией путей, граф ссылок, FTS5-поиск
- **reading_list** — сохранение статей (URL → fetch → summary) + управление очередью чтения
- **unified search** — поиск одновременно по вики и статьям

## Context

### Архитектурные паттерны проекта

- Инструменты реализуют интерфейс `Tool` (`Name()`, `Description()`, `Parameters()`, `Execute()`)
- Регистрация в `cmd/agent/tools.go` (функция `registerOptionalTools`)
- DDL-миграции только в `internal/storage/sqlite.go` (в функции `migrate`)
- CRUD каждого домена — в отдельном файле `internal/storage/<domain>.go`
- Все таблицы скоупятся по `chat_id` (обязательно)
- `chat_id` берётся из контекста: `ctx.Value(tools.ChatIDKey).(int64)`
- FTS5 требует триггеров INSERT/UPDATE/DELETE для синхронизации (см. `notes_fts` в sqlite.go)
- Флаги `hasXxxFTS bool` в struct `DB`, методы `HasXxxFTS() bool`
- Для снижения цикломатической сложности (лимит 20) выносить логику в free-функции
- UTC для всех дат: `time.Now().UTC()`, сравнения через `.UTC().Format("2006-01-02 15:04:05")`

### Существующие файлы для справки

- `internal/tools/notes.go` — образец инструмента
- `internal/storage/sqlite.go` — место для DDL (в функцию `migrate`)
- `cmd/agent/tools.go` — место для регистрации инструментов
- `internal/config/config.go` — место для секций конфига

### Lint и тесты

После каждой задачи: `make lint test`

---

## Схема БД

```sql
-- wiki_pages
CREATE TABLE wiki_pages (
  id         INTEGER PRIMARY KEY,
  chat_id    INTEGER NOT NULL,
  path       TEXT NOT NULL,
  title      TEXT NOT NULL,
  content    TEXT NOT NULL,
  tags       TEXT,                  -- JSON array
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(chat_id, path)
);
CREATE INDEX idx_wiki_pages_chat ON wiki_pages(chat_id);

-- FTS5 + триггеры
CREATE VIRTUAL TABLE wiki_pages_fts USING fts5(
  path, title, content, tags,
  content='wiki_pages', content_rowid='id'
);
CREATE TRIGGER wiki_pages_fts_insert AFTER INSERT ON wiki_pages BEGIN
  INSERT INTO wiki_pages_fts(rowid, path, title, content, tags)
  VALUES (new.id, new.path, new.title, new.content, new.tags);
END;
CREATE TRIGGER wiki_pages_fts_delete AFTER DELETE ON wiki_pages BEGIN
  DELETE FROM wiki_pages_fts WHERE rowid = old.id;
END;
CREATE TRIGGER wiki_pages_fts_update AFTER UPDATE ON wiki_pages BEGIN
  DELETE FROM wiki_pages_fts WHERE rowid = old.id;
  INSERT INTO wiki_pages_fts(rowid, path, title, content, tags)
  VALUES (new.id, new.path, new.title, new.content, new.tags);
END;

-- wiki_links (граф ссылок [[path]])
CREATE TABLE wiki_links (
  chat_id   INTEGER NOT NULL,
  from_path TEXT NOT NULL,
  to_path   TEXT NOT NULL,
  PRIMARY KEY (chat_id, from_path, to_path)
);

-- reading_list
CREATE TABLE reading_list (
  id        INTEGER PRIMARY KEY,
  chat_id   INTEGER NOT NULL,
  url       TEXT,
  title     TEXT NOT NULL,
  summary   TEXT,
  content   TEXT,
  status    TEXT DEFAULT 'to-read',   -- to-read / reading / done
  priority  TEXT DEFAULT 'medium',    -- high / medium / low
  tags      TEXT,                     -- JSON array
  saved_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
  read_at   DATETIME
);
CREATE INDEX idx_reading_list_chat ON reading_list(chat_id);
CREATE INDEX idx_reading_list_status ON reading_list(chat_id, status);
```

---

## Файловая структура

```
internal/
  tools/
    wiki.go          — инструмент wiki (K-1, K-2)
    reading_list.go  — инструмент reading_list (K-4)
  storage/
    wiki.go          — CRUD для wiki_pages, wiki_links, reading_list
    sqlite.go        — DDL-миграции (только CREATE TABLE / INDEX / TRIGGER)
cmd/agent/
  tools.go           — регистрация инструментов
```

---

## Задачи

### Task 1: DDL-миграции и storage/wiki.go

- [x] Добавить DDL в `internal/storage/sqlite.go`: таблицы `wiki_pages`, `wiki_pages_fts` (с триггерами), `wiki_links`, `reading_list` и индексы к ним (в функцию `migrate`, в секцию optional migrations по образцу других таблиц)
- [x] Добавить флаги `hasWikiFTS bool` в struct `DB` и метод `HasWikiFTS() bool`
- [x] Создать `internal/storage/wiki.go` с типами и CRUD:
  - Типы: `WikiPage`, `WikiLink`, `ReadingItem`
  - Функции wiki: `CreateWikiPage`, `GetWikiPage`, `UpdateWikiPage`, `DeleteWikiPage`, `ListWikiPages` (дочерние по prefix), `AllWikiPages` (все страницы чата), `RenameWikiPage` (транзакция: UPDATE wiki_pages + UPDATE wiki_links)
  - Функции wiki_links: `UpsertWikiLinks` (replace all links for a page), `GetBacklinks`
  - Функции reading_list: `CreateReadingItem`, `GetReadingItem`, `UpdateReadingItem`, `DeleteReadingItem`, `ListReadingItems` (с фильтрами status/tags)
- [x] Написать тесты в `internal/storage/wiki_test.go` (основные операции + rename cascade)
- [x] `make lint test` — все проверки зелёные

### Task 2: Инструмент `wiki`

- [x] Создать `internal/tools/wiki.go` реализующий интерфейс `Tool`
- [x] Операции: `create`, `get`, `edit`, `delete`, `list` (дочерние страницы), `tree` (полная структура чата), `rename`, `backlinks`
- [x] При `create`/`edit` — парсить `[[path]]` из content и вызывать `UpsertWikiLinks`
- [x] При `rename` — вызывать `RenameWikiPage` (каскадное обновление в транзакции)
- [x] Зарегистрировать в `cmd/agent/main.go`
- [x] `make lint test` — все проверки зелёные

### Task 3: Инструмент `reading_list`

- [ ] Создать `internal/tools/reading_list.go` реализующий интерфейс `Tool`
- [ ] Операции:
  - `save` — принять URL, вызвать `web_fetch` (через `WebFetchTool.Execute`), сохранить контент; LLM-саммари не делать (агент сам резюмирует при ответе пользователю)
  - `add` — добавить закладку без fetching (URL + title)
  - `list` — список с опциональными фильтрами `status`, `tags`, `priority`
  - `get` — полный контент записи по id
  - `update` — изменить status / tags / priority / title
  - `delete` — удалить запись
- [ ] Зарегистрировать в `cmd/agent/tools.go`
- [ ] `make lint test` — все проверки зелёные

### Task 4: Unified search

- [ ] Создать `internal/tools/knowledge_search.go` реализующий интерфейс `Tool`
- [ ] Поиск по вики и reading_list одновременно (UNION из двух FTS-запросов)
- [ ] Если `hasWikiFTS` недоступен — fallback на LIKE-поиск
- [ ] Результат: список совпадений с указанием источника (`wiki` / `article`) и сниппетом текста
- [ ] Зарегистрировать в `cmd/agent/tools.go`
- [ ] `make lint test` — все проверки зелёные

### Task 5: Дайджест непрочитанного

- [ ] В инструменте `reading_list` добавить операцию `digest` — сводка непрочитанного, сгруппированная по тегам: количество записей по каждому тегу со статусом `to-read`
- [ ] Формат вывода: компактный текст, пригодный для включения в утренний брифинг
- [ ] `make lint test` — все проверки зелёные

---

## Зависимости

- Используется в: P-2 (утренний брифинг — Task 5)
- Использует: существующий `web_fetch` для Task 3 (`save`)
- Дополняет: существующий инструмент `notes` (заметки остаются, вики — отдельный слой)
- Quick capture («запомни...») — покрывается существующим инструментом `notes`
