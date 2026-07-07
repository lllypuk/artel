#!/bin/bash
# Проверка loop safeguard: чередует @-mention сообщения от двух ботов.
# Перед запуском задайте ANALYST_TOKEN, DEV_TOKEN и CHANNEL_ID.

set -e

ANALYST_TOKEN="<analyst-bot-token>"
DEV_TOKEN="<developer-bot-token>"
CHANNEL_ID="<channel-id>"

post_as() {
  local token="$1" msg="$2"
  curl -s -X POST http://localhost:8065/api/v4/posts \
    -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' \
    -d "{\"channel_id\":\"$CHANNEL_ID\",\"message\":\"$msg\"}" > /dev/null
}

count_bot_messages() {
  TOKEN=$(cat /tmp/mm_admin_token.txt 2>/dev/null || echo "")
  if [ -z "$TOKEN" ]; then
    echo "  (admin token not found in /tmp/mm_admin_token.txt, skip count)"
    return
  fi
  curl -s -H "Authorization: Bearer $TOKEN" \
    "http://localhost:8065/api/v4/channels/$CHANNEL_ID/posts?since=0" | python3 -c "
import sys, json
o = json.load(sys.stdin)
bots = set(['<analyst-bot-user-id>', '<developer-bot-user-id>'])
count = sum(1 for pid in o['order'][:30] if o['posts'][pid]['user_id'] in bots)
print(f'  bot messages in last 30: {count}')
"
}

for i in $(seq 1 12); do
  echo "iteration $i: analyst -> developer"
  post_as "$ANALYST_TOKEN" "@developer-bot тест $i"
  sleep 12
  echo "iteration $i: developer -> analyst"
  post_as "$DEV_TOKEN" "@analyst-bot ответ $i"
  sleep 12
  count_bot_messages
done
