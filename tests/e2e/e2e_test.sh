#!/bin/bash
# Helper для E2E Mattermost: постит сообщения и читает последние посты канала.
# Перед запуском задайте TOKEN и CHANNEL_ID.

set -e

TOKEN="<admin-personal-token>"
CHANNEL_ID="<channel-id>"

post() {
  local msg="$1"
  curl -s -X POST "http://localhost:8065/api/v4/posts" \
    -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' \
    -d "{\"channel_id\":\"$CHANNEL_ID\",\"message\":\"$msg\"}"
}

read_last() {
  local limit="${1:-10}"
  curl -s -H "Authorization: Bearer $TOKEN" \
    "http://localhost:8065/api/v4/channels/$CHANNEL_ID/posts?since=0" | python3 -c "
import sys, json, time
o = json.load(sys.stdin)
for pid in o['order'][:$limit]:
    p = o['posts'][pid]
    ts = time.strftime('%H:%M:%S', time.localtime(p['create_at']/1000))
    print(f\"{ts} {p['user_id'][:8]}: {p['message'][:140]}\")
"
}

case "$1" in
  post)
    post "$2"
    ;;
  read)
    read_last "${2:-10}"
    ;;
  waitread)
    sleep "${2:-5}"
    read_last "${3:-10}"
    ;;
  *)
    echo "usage: $0 post <message> | read [limit] | waitread <sleep> [limit]"
    ;;
esac
