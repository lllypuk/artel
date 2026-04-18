# Эпик G: GitHub + Мониторинг

Универсальная интеграция с GitHub и мониторинг сайтов.
Полностью конфигурируемый через `config.yaml` — подходит для любого деплоя.

**Статус:** 🟢 Готов к разработке  
**Приоритет:** 5

---

## Задачи

- [ ] **G-1 Инструмент `github`**  
  Интеграция с GitHub REST API (api.github.com). Работает с любыми репозиториями из конфига.  
  Файлы: `internal/tools/github.go`

- [ ] **G-2 Конфиг репозиториев**  
  В `config.yaml`: список репо с owner/name/label. GitHub token для приватных репо и повышенных лимитов.

- [ ] **G-3 Активность по репо**  
  "Что нового в моих проектах?" → последние коммиты, открытые PR, открытые issues по всем репо из конфига.  
  Фильтр: за последние N дней.

- [ ] **G-4 GitHub Actions статус**  
  Статус последних CI/CD workflow runs по каждому репо.  
  Проактивный алерт (через P-1) если pipeline упал.

- [ ] **G-5 Инструмент `monitor`**  
  HTTP ping для списка URL из конфига: проверка доступности, время ответа, статус-код.  
  Файлы: `internal/tools/monitor.go`, `cmd/agent/monitor.go` (цикл проверок)

- [ ] **G-6 Конфиг мониторинга и алерты**  
  В `config.yaml`: список URL, интервал проверки, таймаут. Проактивный алерт если сайт упал или вернулся.

---

## Конфигурация (пример)

```yaml
github:
  enabled: true
  token: "ghp_..."           # опционально, для приватных репо
  repos:
    - owner: "lllypuk"
      name: "agent-shatrov"
      label: "Агент"
    - owner: "lllypuk"
      name: "Family-Finances-Service"
      label: "Финансы"
    - owner: "lllypuk"
      name: "shatrov.tech"
      label: "Сайт"

monitor:
  enabled: true
  interval_minutes: 5
  timeout_seconds: 10
  sites:
    - url: "https://shatrov.tech"
      name: "Личный сайт"
    - url: "https://budget.shatrov.tech"
      name: "Финансы"
```

---

## Зависимости

- Используется в: P-1 (алерты через проактивный планировщик)
- Не зависит от других эпиков — можно реализовывать независимо
