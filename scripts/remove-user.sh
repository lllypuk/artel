#!/usr/bin/env bash
# remove-user.sh — останавливает и удаляет контейнер агента пользователя.
# Использование: ./scripts/remove-user.sh <username> [--delete-data]
#
#   --delete-data  — также удаляет Docker volume с данными пользователя (необратимо!)

set -euo pipefail

usage() {
    echo "Использование: $0 <username> [--delete-data]"
    echo ""
    echo "  username       — имя пользователя"
    echo "  --delete-data  — удалить volume с данными (необратимо!)"
    exit 1
}

if [ $# -lt 1 ]; then
    echo "Ошибка: не указано имя пользователя." >&2
    echo "" >&2
    usage
fi

USERNAME="$1"
DELETE_DATA=0

if [ "${2:-}" = "--delete-data" ]; then
    DELETE_DATA=1
fi

CONTAINER_NAME="agent-${USERNAME}"
VOLUME_NAME="agent-${USERNAME}-data"

# Остановить и удалить контейнер
if docker ps -a --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"; then
    echo "Останавливаю контейнер '$CONTAINER_NAME'..."
    docker stop "$CONTAINER_NAME" > /dev/null
    echo "Удаляю контейнер '$CONTAINER_NAME'..."
    docker rm "$CONTAINER_NAME" > /dev/null
    echo "✅ Контейнер '$CONTAINER_NAME' удалён."
else
    echo "Контейнер '$CONTAINER_NAME' не найден, пропускаю."
fi

# Удалить volume если запрошено
if [ "$DELETE_DATA" -eq 1 ]; then
    if docker volume ls --format '{{.Name}}' | grep -qx "$VOLUME_NAME"; then
        echo "Удаляю volume '$VOLUME_NAME'..."
        docker volume rm "$VOLUME_NAME" > /dev/null
        echo "✅ Volume '$VOLUME_NAME' удалён."
    else
        echo "Volume '$VOLUME_NAME' не найден, пропускаю."
    fi
else
    echo ""
    echo "Данные сохранены в volume '$VOLUME_NAME'."
    echo "Чтобы удалить данные: ./scripts/remove-user.sh $USERNAME --delete-data"
fi
