# CLAUDE.md

## Project Overview

Mailroom is a Go task processor and web service for RapidPro/TextIt messaging platforms. It handles flow execution, contact management, campaign events, message coordination, IVR calls, and LLM integration. Compiles to a single binary that runs as both a web service and background task processor.

## Build & Test

```bash
# Build main executable
go build -v -o mailroom github.com/nyaruka/mailroom/cmd/mailroom

# Test (requires services: Postgres 15, Valkey 8.0, Elasticsearch 8.13.4, LocalStack)
go test -p=1 -coverprofile=coverage.text -covermode=atomic ./...

# Test a single package
go test -p=1 ./core/models/...
```

Tests run serially (`-p=1`) and require external services configured in `.github/workflows/ci.yml`.

## Project Structure

- `cmd/` - CLI entrypoints (`mailroom`, `mrllmtests`)
- `core/models/` - Database models and ORM layer
- `core/runner/` - Flow execution engine with `handlers/` and `hooks/`
- `core/tasks/` - Background task definitions (30+ types)
- `core/crons/` - Scheduled jobs
- `core/search/` - Elasticsearch contact indexing
- `web/` - HTTP handlers organized by domain (contact, flow, msg, etc.)
- `services/` - External integrations (IVR providers, LLM providers, airtime)
- `runtime/` - Runtime configuration and initialization
- `testsuite/` - Test infrastructure, fixtures, and `testdb` constants
- `utils/` - Utility packages (queues, crons, logs)

## Code Conventions

- **Formatting**: standard `gofmt`, 120 char line limit
- **Private fields**: exported with `_` suffix if necessary for JSON marshaling, accessed via methods (e.g., `UUID_` field, `UUID()` accessor if they need to avoid conflicts with interface method names)
- **Typed IDs**: `ContactID`, `OrgID`, `ChannelID` with `Nil*` zero constants (e.g., `NilContactID = ContactID(0)`)
- **Registration via init()**: event handlers, tasks, and web routes all register in `init()` functions
  - `runner.RegisterEventHandler(events.TypeX, handlerFunc)`
  - `tasks.RegisterType(TypeName, func() Task { return &TaskType{} })`
  - `web.InternalRoute(method, pattern, handler)` / `web.PublicRoute(...)`
- **Error handling**: wrap with `fmt.Errorf("context: %w", err)`, structured logging via `log/slog`
- **Test packages**: use external test packages (e.g., `package models_test`)
- **Test setup**: `ctx, rt := testsuite.Runtime(t)` for integration tests; use `testdb.*` constants for fixture data

## Configuration

Environment variables prefixed with `MAILROOM_` (e.g., `MAILROOM_DB`, `MAILROOM_VALKEY`, `MAILROOM_ELASTIC`). Config precedence: config file < env vars < CLI params.

## Key Dependencies

- `goflow` - Flow engine; `gocommon` - Shared utilities (both Nyaruka)
- `lib/pq` (Postgres), `go-elasticsearch/v8`, `redigo` (Valkey/Redis)
- AWS SDK v2 (S3, DynamoDB, CloudWatch)
- `stretchr/testify` for test assertions
