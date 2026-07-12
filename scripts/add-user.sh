#!/usr/bin/env bash
# add-user.sh — запускает Docker-контейнер агента для нового пользователя.
# Использование: TELEGRAM_TOKEN=... LLM_API_KEY=... ./scripts/add-user.sh <username> <chat_id> <llm_base_url>
#
# Переменные окружения:
#   TELEGRAM_TOKEN   — токен бота от @BotFather (обязательный)
#   LLM_API_KEY      — API-ключ LLM (обязательный)
#   IMAGE            — образ Docker (по умолчанию: ghcr.io/lllypuk/artel:latest)

set -euo pipefail

IMAGE="${IMAGE:-ghcr.io/lllypuk/artel:latest}"

usage() {
    echo "Использование: TELEGRAM_TOKEN=... LLM_API_KEY=... $0 <username> <chat_id> <llm_base_url>"
    echo ""
    echo "  username        — имя пользователя (латиница, цифры, дефис)"
    echo "  chat_id         — chat_id пользователя"
    echo "  llm_base_url    — Base URL LLM API, например https://api.z.ai/api/coding/paas/v4"
    echo ""
    echo "Переменные окружения (секреты):"
    echo "  TELEGRAM_TOKEN  — токен бота от @BotFather (обязательный)"
    echo "  LLM_API_KEY     — API-ключ LLM (обязательный)"
    echo "  IMAGE           — Docker-образ (по умолчанию: ghcr.io/lllypuk/artel:latest)"
    exit 1
}

if [ $# -lt 3 ]; then
    echo "Ошибка: недостаточно аргументов." >&2
    echo "" >&2
    usage
fi

if [ -z "${TELEGRAM_TOKEN:-}" ]; then
    echo "Ошибка: переменная окружения TELEGRAM_TOKEN не задана." >&2
    echo "Пример: TELEGRAM_TOKEN=... LLM_API_KEY=... $0 ..." >&2
    exit 1
fi

if [ -z "${LLM_API_KEY:-}" ]; then
    echo "Ошибка: переменная окружения LLM_API_KEY не задана." >&2
    echo "Пример: TELEGRAM_TOKEN=... LLM_API_KEY=... $0 ..." >&2
    exit 1
fi

USERNAME="$1"
CHAT_ID="$2"
LLM_BASE_URL_VAL="$3"

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
    -e "LLM_API_KEY=${LLM_API_KEY}" \
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
