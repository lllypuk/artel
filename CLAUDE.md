# Artel v2

Local manual project/task panel: Go 1.26.3 API and CLI, SQLite, native C++17 / Qt Quick 6.8+ desktop. The bot product is retired; no Telegram, Mattermost, LLM, Docker or Pi runtime dependency.

## Quality gates

After every code change run `make lint test`. Before completing each plan task run its additional gates, initially `make lint test build`. Final acceptance uses `make check` and, on Arch, `make package-check`; see `docs/development.md` for isolated CLI examples and delivery boundaries. Never commit failing lint/tests or disable checks to pass. Use golangci-lint 2.10.1; `GOLANGCI_LINT` can point to a private installation. No automatic host package installation.

## Boundaries

- Follow `docs/plans/20261117-artel-v2-foundation-desktop.md`, one task per iteration. Architecture/experiment documents describe future scope, not permission to implement it.
- `arteld` owns SQLite; desktop/CLI use HTTP, never direct SQL. C++ owns logic and asynchronous networking; QML owns layout/actions. No shell from QML.
- Manual state changes do not run agents, verify MRs or deploy anything. Do not add slash commands or bot tools.
- Preserve Git history, architecture, experiments and historical completed plans. Cleanup inventory: `docs/legacy-cleanup.md`.
- Tests use temporary paths, private HOME and ephemeral ports; never production data/services or real credentials. Do not change desktop settings.
- No main push, merge, deployment, package installation or service activation as part of this plan.

## Layout

`cmd/arteld`, `cmd/artelctl`, `internal/api`, `internal/domain`, `internal/store`, `internal/app`, `desktop/src`, `desktop/tests`, `scripts`. The daemon owns `artel.sqlite` under its private locked data directory; domain mutations run in immediate transactions with events/receipts. HTTP exposes authenticated domain CRUD, snapshot/history and replayable SSE alongside public `/healthz`. Desktop uses ApiClient plus a registered C++ Panel viewmodel for manual screens; QML receives no credential. Appearance adapters only read/watch theme/language settings. UI tests load production QML with temporary HTTP/daemon fixtures; screenshot fixtures must contain only fictional data.
