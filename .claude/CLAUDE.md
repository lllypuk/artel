# Artel v2 contributor instructions

Read and follow the root `CLAUDE.md` and current implementation plan. This is a native Qt Quick desktop plus local Go API, not a bot.

Mandatory after code changes: `make lint test`; task completion also requires the plan's additional gates, initially `make lint test build`. No skipping failing checks, host package installs, real credentials or production service mutations. Keep tests isolated and preserve architecture/experiment documents.
