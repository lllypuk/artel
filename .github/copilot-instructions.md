# Artel v2

Follow root CLAUDE.md. Build a local manual project/task panel: Go API + CLI, SQLite and C++17/Qt Quick 6.8+ native desktop. Retired bot interfaces/tools must not return.

After any code change run `make lint test`; run the current task's additional validation before completion. Go 1.26.3 and golangci-lint 2.10.1 are pinned. C++ warnings, clang-format and QML checks are mandatory. Tests load production QML offscreen and use temporary data, never live services or credentials. Do not install packages, activate services, merge or deploy. Architecture future plans do not expand the active task.
