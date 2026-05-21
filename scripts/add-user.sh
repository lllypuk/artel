#!/usr/bin/env bash
# add-user.sh — запускает Docker-контейнер агента для нового пользователя.
# Использование: ./scripts/add-user.sh <username> <telegram_token> <chat_id> <api_key> <llm_base_url>
#
# Переменные окружения:
#   IMAGE        — образ Docker (по умолчанию: ghcr.io/lllypuk/agent-shatrov:latest)

set -euo pipefail

IMAGE="${IMAGE:-ghcr.io/lllypuk/agent-shatrov:latest}"

usage() {
    echo "Использование: $0 <username> <telegram_token> <chat_id> <api_key> <llm_base_url>"
    echo ""
    echo "  username        — имя пользователя (латиница, цифры, дефис)"
    echo "  telegram_token  — токен бота от @BotFather"
    echo "  chat_id         — chat_id пользователя"
    echo "  api_key         — API-ключ LLM (обязательный)"
    echo "  llm_base_url    — Base URL LLM API, например https://api.z.ai/api/coding/paas/v4 (обязательный)"
    echo ""
    echo "Переменные окружения:"
    echo "  IMAGE           — Docker-образ (по умолчанию: ghcr.io/lllypuk/agent-shatrov:latest)"
    exit 1
}

if [ $# -lt 5 ]; then
    echo "Ошибка: недостаточно аргументов." >&2
    echo "" >&2
    usage
fi

USERNAME="$1"
TELEGRAM_TOKEN="$2"
CHAT_ID="$3"
API_KEY="$4"
LLM_BASE_URL_VAL="$5"

CONTAINER_NAME="agent-${USERNAME}"
VOLUME_NAME="agent-${USERNAME}-data"

# Проверить, не существует ли уже контейнер с таким именем
if docker ps -a --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"; then
    echo "Ошибка: контейнер '$CONTAINER_NAME' уже существует." >&2
    echo "" >&2
    echo "Управление существующим контейнером:" >&2
    echo "  Логи:      docker logs -f $CONTAINER_NAME" >&2
    echo "  Статус:    docker ps | grep $CONTAINER_NAME" >&2
    echo "  Остановить: docker stop $CONTAINER_NAME" >&2
    echo "  Удалить:   ./scripts/remove-user.sh $USERNAME" >&2
    exit 1
fi

echo "Запуск контейнера для пользователя '$USERNAME'..."

docker run -d \
    --name "$CONTAINER_NAME" \
    --restart unless-stopped \
    -e "TELEGRAM_TOKEN=${TELEGRAM_TOKEN}" \
    -e "TELEGRAM_ALLOWED_USERS=${CHAT_ID}" \
    -e "LLM_API_KEY=${API_KEY}" \
    -e "LLM_BASE_URL=${LLM_BASE_URL_VAL}" \
    -v "${VOLUME_NAME}:/app/data" \
    "$IMAGE"

for i in $(seq 1 10); do
    sleep 1
    if [ "$(docker inspect -f '{{.State.Running}}' "${CONTAINER_NAME}" 2>/dev/null)" = "true" ]; then
        break
    fi
    if [ "$i" -eq 10 ]; then
        echo "❌ Контейнер не запустился в течение 10 секунд. Логи:" >&2
        docker logs "${CONTAINER_NAME}" 2>&1 | tail -20 >&2
        exit 1
    fi
done

echo ""
echo "✅ Контейнер '$CONTAINER_NAME' запущен и работает."
echo ""
echo "  Логи:        docker logs -f $CONTAINER_NAME"
echo "  Остановить:  docker stop $CONTAINER_NAME"
echo "  Удалить:     ./scripts/remove-user.sh $USERNAME"
