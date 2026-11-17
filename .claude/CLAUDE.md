# Artel v2 contributor instructions

Read and follow the root `CLAUDE.md`, stable `docs/development.md` contract and current approved task. This is a native Qt Quick desktop plus local Go API, not a bot. Opt-in Pi observation is telemetry only, never delivery, writer assignment, agent control or task completion. Keep observer auth/store/polling/mutations separate from manual drafts; never collect dialogue/tool payloads or alter global Pi/agterm/Omarchy settings. Pi/Node are optional observer runtimes only; the installed artifact is inert until explicitly loaded.

Mandatory after code changes: `make lint test`; task completion also requires the plan's additional gates, initially `make lint test build`. No skipping failing checks, host package installs, real credentials or production service mutations. Keep tests isolated and preserve architecture/experiment documents.
