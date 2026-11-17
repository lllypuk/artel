# Observer HTTP and owner CLI evidence (Task 3)

## Implemented boundary

- `arteld --observer-socket ABSOLUTE_PATH` opts into a second HTTP server on a private Unix socket. Without the flag only the existing numeric-loopback owner listener is started. No socket path is accepted from HTTP metadata.
- The socket directory is 0700, socket 0600, with a separate no-follow lifetime lock. Symlinks, wrong types/modes/ownership, live listeners, ambiguous dial errors and changed stale inodes are rejected. Only ECONNREFUSED plus matching inode/type/owner under the lock permits stale removal. Cleanup preserves replacement files; lock files intentionally remain. Close is idempotent.
- Observer HTTP requires exactly `Host: artel-observer`, no Origin or absolute URL, a scoped bearer, strict UTF-8 JSON (16 KiB, no missing/unknown/duplicate fields), and a fixed method/path allowlist (no mux redirects or filesystem endpoint). Header/body/write deadlines are two seconds. Reports are limited to four attempts per rolling second per authenticated observer/runtime with bounded limiter storage.
- Both credential directions are tested, including owner snapshot/history/SSE/control endpoints. Revocation is checked before parsing/routing and again inside mutations. SHA-256 verifiers alone enter SQLite. Observer errors are fixed, secret-free codes. Owner transport errors retain the existing owner middleware representation; both error shapes are explicit in the shared OpenAPI contract.
- Owner controls mount in the existing owner router. `api/openapi.yaml` references the observer contract's five owner paths rather than duplicating DTOs. Existing manual snapshot/history/event semantics are unchanged.

## CLI and artifacts

All commands emit public JSON on success. Exit codes: 0 success, 1 rejected/unavailable/unsafe operation, 2 invalid usage. Errors are fixed local terminal diagnostics, not server body echoes.

```text
artelctl observer provision --name NAME --observer-socket ABSOLUTE_PATH --output ABSOLUTE_CONFIG [--endpoint URL] [--token-file OWNER_TOKEN]
artelctl observer provision --resume ABSOLUTE_CONFIG [--endpoint URL] [--token-file OWNER_TOKEN]
artelctl observer list --json [--endpoint URL] [--token-file OWNER_TOKEN]
artelctl observer bind --id OBSERVER_UUID --runtime-id RUNTIME_UUID --project-id PROJECT_UUID --expected-version N --idempotency-key RETRY_UUID [owner options]
artelctl observer unbind|revoke|prune --id OBSERVER_UUID --expected-version N --idempotency-key RETRY_UUID [owner options]
```

An omitted mutation key is generated locally; supply an explicit key to replay an uncertain bind/unbind/revoke/prune from another CLI process. Provisioning always persists its generated key and canonical payload for restart recovery.

- Provisioning creates a private `CONFIG.token` sidecar and fsynced atomic pending config before PUT. Initial publication never overwrites existing artifacts. Token and config containing directories are synced. Resume verifies the saved token/verifier, original label/payload/key and database identity; new provisioning arguments are refused.
- Only an acknowledged response with expected observer/database IDs permits atomic confirmed publication. Failure before that publication leaves pending state resumable; confirmed resume also replays the same receipt. No token is printed, supplied in argv or placed in a receipt/projection.
- A Go artifact reader and TypeScript consumer config gate reject pending, unsafe and mismatched-token artifacts. The TypeScript gate is prerequisite code for Task 4, not an already running extension: lifecycle/network wiring remains Task 4.
- Scoped credentials are not a sandbox against arbitrary code with the same Unix UID. Private directories and cooperating lifetime locks are the filesystem trust boundary; arbitrary same-UID concurrent filesystem tampering is not isolation.

## Validation of the exact Task 3 implementation

Executed successfully:

```text
make lint test
make test-race build
make observer-check
go test ./cmd/arteld ./cmd/artelctl ./internal/local ./internal/observerapi -count=3
git diff --check
```

Tool versions reported by preflight: Go 1.26.3, Qt 6.12.0, clang-format 23.1.3, golangci-lint 2.10.1. Observer dependency preflight uses pinned Pi 1.5.2 and Node 22.23.2. No packages installed or services activated.

Coverage includes:

- Actual daemon subprocess: initial startup, SIGKILL with leftover socket, same-runtime daemon restart (preserved database UUID, stale until a new report), subsequent graceful shutdown/restart, authentication over both actual transports, test-owned process reaping and socket cleanup.
- Actual CLI subprocess restarts: lost successful provisioning response (including Go transport idempotent retries), pending gate, canonical saved payload/key, successful resume, resume after confirmed publication, no duplicate principal and no secret output. Independent injected post-ACK config-write failure remains pending and resumes successfully. Replacement database is refused.
- Owner HTTP bind lost-response replay, CAS conflict, old generation rejection, unbind/prune/retired replay and byte-equivalent manual snapshot before/after observation. CLI list/unbind/revoke/prune and invalid bind arguments have exit/output checks.
- Register immutable replay/conflict, reports/duplicates/sequence conflict/rate limit, foreign runtime and revoked token; strict JSON, Host/Origin/absolute URL/content-type/query/method/path rejection, error and successful response schema validation.
- Socket unsafe-file/symlink/directory/mode/parent/live-listener/lock/length fixtures, synthetic foreign UID check without chown privileges, simultaneous lock acquisition, stale recovery and replacement preservation. Thirty-two socket open/close cycles leave the same `/proc/self/fd` count; persistent lock files are deliberate.
- Mandatory existing Go, Qt, documentation/delivery/privacy tests remain green. Observer SDK and PTY probe gates pass with zero skips. The live extension/full-stack observer UI, remote CI, installed artifact acceptance and actual desktop/agterm smoke are not claimed here.

Local command logs: `/tmp/artel-task3-check.log`, `/tmp/artel-task3-race-build.log`, `/tmp/artel-task3-observer-check.log`, `/tmp/artel-task3-repeat.log` (ephemeral, not delivery artifacts). Process-crash recovery tests do not establish power-loss durability.
