# Legacy cleanup

Task 1 replaces the personal bot, without migrating its data. Git history is retained.

## Explicit tracked removal inventory

The following complete tracked subtrees belong exclusively to the old bot:

- `cmd/agent/` — bot entrypoint
- `internal/` — old agent, providers, storage, tools, transports and tests (replaced with v2 packages)
- `configs/` — bot example configurations and ignore rules
- `scripts/` — bot management/check scripts (replaced with v2 checks)
- `tests/e2e/` — bot-specific fixtures, reports and shell tests
- `docs/roles/` — old bot role definitions

Individual removed files:

- `config.example.yaml`
- `Dockerfile`, `docker-compose.yml`, `docker-compose.bots.yml`, `.dockerignore`
- `.github/workflows/docker-publish.yml`
- `DEPLOY.md`, `USER_GUIDE.md`
- `docs/backlog.md`, `docs/e2e-testing.md`, `docs/headless-setup.md`, `docs/multi-instance-guide.md`

README, Makefile, lint configuration and agent instructions are replaced, not retained as active bot instructions. Completed plans in `docs/plans/completed/` are historical bot records, not current instructions. All architecture, experiment and current plan files are preserved unchanged by cleanup.

## Local data inspection

Before removal, existence checks (including dangling symlinks) found none of:
`data/`, `config.yaml`, `.env`, `configs/analyst-bot.yaml`,
`configs/developer-bot.yaml`, `configs/tester-bot.yaml`.
Their deletion is a no-op. No credential contents were read and no symlinks followed.
No external installation, Docker volume, host service, shared Pi/SSH directory or other project was modified.

## Initial validation

Task 1 requires `make lint test build`. Qt smoke tests load production QML offscreen, without a daemon or bot/provider environment. Local authorization, storage and CRUD are intentionally not part of the skeleton.
