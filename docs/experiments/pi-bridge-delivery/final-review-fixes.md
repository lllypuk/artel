# Artel 5а — ревью исправлений после исходного 75fb7ef

Это отдельная запись для исправленной дельты. Исходный неуспешный результат сохранён в [final-review-75fb7ef.md](final-review-75fb7ef.md) и не перезаписывается.

## Граница ревью

- Ветка: `fix/artel-5a-final-review`.
- Base/HEAD на момент независимого ревью: `64306f0fe9ad92d3051bdaae23b1d2da2bbd7d0f`.
- Рецензент: отдельная видимая Pi-сессия `ArtelTest`, без Codex/mailbox.
- Финально перепроверенный рабочий diff: 17 modified-файлов, +674/−80, плюс один untracked evidence-файл; SHA-256 diff `b96425cac2dee695dddf1b07475e0654848c962fb0c33e29ab4427e1d65bc954`.
- Evidence-файл исходной проверки на момент ревью: SHA-256 `fd938b6ef4bcb44f6b499ed60e4af9e4415749bc5701e9f67bc38120fe576a20`.

## Исправления и перепроверка

Основная дельта закрывает исходные P2/P3:

- fresh attach сравнивает непустые scope_path и отклоняет пробельный finished_at;
- read-only `/reviews/receipt` восстанавливает legacy/current exact bytes по исходным method/path/key/hash без записи; общий recovery доступен только для ранее fresh-eligible атрибуции и не расширяет старую unattributed-ветку;
- desktop сохраняет task-scoped чтение только на полностью известном чужом SSE-интервале, учитывает все события и консервативно перечитывает при релевантном событии, overflow, reconnect, смене БД или snapshot ahead;
- production-QML проверяет held second page A при событиях B и новый result A в смешанном интервале с новым expected_result_id.

Первое ревью дельты дополнительно нашло два P3: пропущенный SSE ID мог считаться полным покрытием, а общий lookup был доступен старой ошибке атрибуции. Исправлены требованием строго `seq == cursor + 1` с reconnect и ограничением recovery на attribution-valid пары. Добавлены gap/reconnect и одинаковые-агрегаты/изменённая-атрибуция негативные тесты.

Контрольная Pi-перепроверка: **оба замечания закрыты, новых дефектов в проверенных ветках не найдено**. Остаточные риски: отдельные overflow/duplicate/старое событие и snapshot-started-before-gap не воспроизводились отдельными прогонами; эти ветки проверены по коду. Это не доказательство полных гейтов или живого revmux-цикла.

Локальные отчёты: `/tmp/artel-5a-75fb7ef/delta-review-result.md` и `delta-recheck-result.md`.

## Промежуточные проверки

На финально перепроверенной дельте:

- адресные Go tests importer/store/api/artelctl — PASS;
- Qt `taskScopedInvalidationCoverage`, включая gap/reconnect — PASS;
- production-QML `reviewedTaskResult`, включая B-only и mixed B+A controls — PASS;
- `make lint test build` с Go 1.26.3, golangci-lint 2.10.1 и `TMPDIR=/tmp` — **exit 0**; 17 Python tests, все Go tests, 118 ArtelTests и 42 ClientTests, 0 lint issues.

Полные `make check` и `make package-check` выполняются отдельно на зафиксированном коммите и здесь не отмечаются заранее.
