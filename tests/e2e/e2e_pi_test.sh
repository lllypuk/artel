#!/bin/bash
# E2E тест pi_coder с внешними проектами через Mattermost.
# Перед запуском задайте TOKEN, CHANNEL_ID и пути.
#
# Требования:
#   - Запущенный Mattermost (http://localhost:8065)
#   - Три инстанса artel с config-*-e2e-pi.yaml
#   - Установленный pi (https://github.com/mariozechner/pi)
#   - Запущенный Ollama с qwen3.5:cloud (http://localhost:11434)
#   - Тестовый проект в /home/sasha/Project/test-project

set -e

TOKEN="<admin-personal-token>"
CHANNEL_ID="<channel-id>"

post() {
  local msg="$1"
  echo ">>> POST: $msg"
  curl -s -X POST "http://localhost:8065/api/v4/posts" \
    -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' \
    -d "{\"channel_id\":\"$CHANNEL_ID\",\"message\":\"$msg\"}" > /dev/null
}

read_last() {
  local limit="${1:-10}"
  echo "--- Последние $limit сообщений ---"
  curl -s -H "Authorization: Bearer $TOKEN" \
    "http://localhost:8065/api/v4/channels/$CHANNEL_ID/posts?since=0" | python3 -c "
import sys, json, time
o = json.load(sys.stdin)
for pid in o['order'][:$limit]:
    p = o['posts'][pid]
    ts = time.strftime('%H:%M:%S', time.localtime(p['create_at']/1000))
    print(f'{ts} {p[\"user_id\"][:8]}: {p[\"message\"][:200]}')
"
}

waitread() {
  local sleep_sec="${1:-30}"
  local limit="${2:-15}"
  echo ">>> Ожидание ${sleep_sec}с..."
  sleep "$sleep_sec"
  read_last "$limit"
}

echo "=========================================="
echo "E2E: pi_coder + внешние проекты"
echo "=========================================="
echo ""

#--- Сценарий 1: Анализ внешнего проекта ---
echo "[1] Анализ test-project через developer-bot"
post "@developer-bot проанализируй проект test-project через pi_coder"
waitread 60 20

#--- Сценарий 2: Доступные проекты ---
echo ""
echo "[2] Доступные проекты"
post "@analyst-bot какие проекты доступны для работы через pi_coder?"
waitread 30 10

#--- Сценарий 3: Self-project через pi_coder ---
echo ""
echo "[3] Self-project (artel)"
post "@developer-bot открой проект artel, добавь комментарий в README"
waitread 60 20

#--- Сценарий 4: Non-self restart игнорируется ---
echo ""
echo "[4] Non-self + restart (должно игнорироваться)"
post "@tester-bot запусти тесты test-project с перезапуском"
waitread 60 20

#--- Сценарий 5: Тестирование проекта ---
echo ""
echo "[5] Тесты test-project через tester-bot"
post "@tester-bot запусти тесты проекта test-project через pi_coder"
waitread 60 20

#--- Сценарий 6: Координация аналитик → разработчик ---
echo ""
echo "[6] Координация: аналитик → разработчик"
post "@analyst-bot попроси разработчика проанализировать test-project"
waitread 90 30

#--- Сценарий 7: Координация разработчик → тестировщик ---
echo ""
echo "[7] Координация: разработчик → тестировщик"
post "@developer-bot закончи задачу и попроси тестировщика проверить test-project"
waitread 90 30

#--- Сценарий 8: Non-self rebuild игнорируется ---
echo ""
echo "[8] Non-self + rebuild (должно игнорироваться)"
post "@developer-bot пересобери test-project через pi_coder"
waitread 30 10

#--- Сценарий 9: Несуществующий проект ---
echo ""
echo "[9] Несуществующий проект"
post "@developer-bot открой проект nonexistent через pi_coder"
waitread 30 10

echo ""
echo "=========================================="
echo "E2E завершён. Проверьте вывод выше."
echo "=========================================="