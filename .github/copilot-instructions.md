# Copilot Instructions for `agent-shatrov`

## Build, test, and lint commands

- Build the binary: `make build`
- Lint the repository: `make lint`  
  This runs `go fix ./...`, `go fmt ./...`, then `golangci-lint run ./...`.
- Run the full test suite: `make test`
- Run tests for one package: `go test ./internal/agent`
- Run a single test: `go test ./internal/agent -run TestHandleMessageSimpleResponse`
- Another targeted example: `go test ./cmd/agent -run TestMaybeFireEvent_Fires`

## High-level architecture

- `cmd/agent/main.go` is the composition root. It loads config, sets up logging, opens SQLite, builds the LLM provider, registers tools, creates the Telegram bot, and starts background schedulers.
- `internal/telegram` is only the transport and control layer: long polling, `allowed_users` auth, file/photo intake, and a small fixed set of slash commands for system operations.
- `internal/agent` is the core ReAct loop. It loads conversation history from SQLite, injects the system prompt and tool definitions, executes tool calls through the registry, stores tool/assistant messages, and records token usage.
- `internal/agent/context.go` implements the sliding context window. Older conversation history is summarized and the rolling summary is persisted back into SQLite so later turns can reuse it.
- `internal/tools` is the main feature surface exposed to the LLM. Each tool implements `Name`, `Description`, `Parameters`, and `Execute`, and the agent works through the registry rather than calling feature code directly.
- `internal/llm` abstracts the model provider. `client.go` handles a single OpenAI-compatible endpoint; `multi.go` adds provider priority, retry/backoff, health tracking, forced-provider selection, and model alias remapping across providers.
- `internal/storage` owns persistence. `internal/storage/sqlite.go` contains schema and additive migrations; feature-specific CRUD and query logic live in separate domain files such as `discipline.go`, `news.go`, `stats.go`, `stocks.go`, and `workout.go`.
- News is spread across multiple layers: `cmd/agent/news.go` wires it up, `internal/news` fetches/parses/manages feeds, `internal/storage/news.go` persists feeds/items/subscriptions/read state, and `internal/tools/news.go` exposes it to the agent.
- Background automation is scheduler-driven. Reminders poll due rows from SQLite, and the proactive scheduler sends synthetic `[АВТО: ...]` prompts into the agent for morning/evening/weekly digests and goal reminders.

## Key conventions

- Add new user-facing capabilities as agent tools in `internal/tools/`; do **not** add new Telegram slash commands in `internal/telegram/bot.go`. Slash commands are reserved for system controls like `/new`, `/model`, `/provider`, `/context`, `/status`, and `/help`.
- Tool registration has two tiers:
  - always-on tools are registered in `cmd/agent/main.go`
  - config-gated tools are registered in `cmd/agent/tools.go`
- When adding a new optional tool, also update the config surface: `internal/config/config.go`, `internal/config/config_test.go`, and `config.example.yaml`.
- Keep storage layering strict: schema/migrations in `internal/storage/sqlite.go`, domain models and CRUD in `internal/storage/<feature>.go`.
- Preserve `chat_id` isolation for all user data. For child entities without a direct `chat_id`, validate ownership through the parent entity before insert/update/delete.
- For partial tool updates, optional numeric JSON fields use pointer types like `*int` so omitted values stay distinguishable from explicit zero.
- Prefer UPSERT-style writes (`ON CONFLICT ... DO UPDATE`) for save/update flows where the codebase already follows that pattern.
- If Go code compares against SQLite `CURRENT_TIMESTAMP`, use UTC-formatted times; SQLite stores those timestamps in UTC.
- Some config fields intentionally treat `0` as "disabled" rather than "use default" (for example proactive goal reminders). Do not overwrite zero in constructors unless the existing code already treats that field as a positive-only default.
- User-facing bot text and system prompts are Russian; keep new prompts, notifications, and Telegram responses consistent with that.
