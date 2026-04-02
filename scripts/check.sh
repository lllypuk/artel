#!/usr/bin/env bash
# check.sh — запускает make lint и make test, возвращает exit 1 при ошибках

set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$PROJECT_DIR"

FAILED=0

echo "=== make lint ==="
if ! make lint 2>&1; then
    echo "[FAIL] lint"
    FAILED=1
else
    echo "[OK] lint"
fi

echo ""
echo "=== make test ==="
if ! make test 2>&1; then
    echo "[FAIL] tests"
    FAILED=1
else
    echo "[OK] tests"
fi

echo ""
if [ "$FAILED" -eq 0 ]; then
    echo "✅ All checks passed"
    exit 0
else
    echo "❌ Some checks failed"
    exit 1
fi
