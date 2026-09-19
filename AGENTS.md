# Agent guide — track-slash

## Product manifesto

Read `MANIFESTO.md` before making product, API, data model, or frontend decisions. It defines the long-term direction for track-slash.
Read `DESIGN_CONTEXT.md` before making frontend or product-layout decisions. It captures recent user preferences and design memory that should guide future UI passes.
Read `STORAGE.md` before changing object storage, attachment, image, or file-upload behavior. It documents the metadata/backend split and local storage contract.
Read `ATTACHMENTS.md` before changing issue attachments, description Markdown rendering, `object-N` references, or attachment UI behavior.
Read `OAUTH.md` before changing OAuth connectors, the `/oauth/*` endpoints, the discovery documents, MCP bearer authentication, or the `auth_tokens` kinds.

## Frontend design principles

track-slash UI should feel like a fast work tool: quiet, direct, and consistent with the existing Tailwind-based templates. Consistency wins over almost everything else. Prefer reuse over novelty: simple components that compose should carry the interface.

- Read `COMPONENTS.md` before adding or changing reusable UI. Prefer the documented components in `internal/server/templates/components.html`, and update the reference when adding a reusable component.
- Prefer simple, recognizable Lucide icons paired with concise text labels over custom graphics, decorative treatments, or verbose copy.
- Reuse existing components and Tailwind utility patterns from nearby templates before introducing new structure or visual language.
- When adding repeatable UI, extract or extend a shared template/component first so future screens inherit the same behavior, spacing, and states.
- Keep navigation controls predictable: tabs for sibling views, icon+text links for movement between pages, and compact buttons for concrete actions.
- Keep cards purposeful. Use them for bounded content like project headers, forms, lists, and repeated items; do not nest cards or add decorative wrappers.
- Preserve dense, scannable layouts with clear hierarchy, restrained color, and readable spacing. Avoid marketing-style hero sections, gradients, and ornamental UI.
- Keep page content widths consistent across sibling app views so navigation does not cause the main content column to jump.
- Present ticket numbers and project keys consistently as compact bordered badges so keys remain visually distinct from titles and metadata.
- Match dark-mode classes and interactive states (`hover`, active, focus) when adding or changing controls.

## Test coverage policy

**Aim for 100% branch coverage on business logic.** Branch coverage, not just line coverage — every condition, every error mapping, every state transition must be exercised.

What counts as business logic:

- Store CRUD and transactional flows (`internal/store/`)
- HTTP handlers and request validation (`internal/server/`)
- Domain enums, transitions, invariants (`internal/model/`)
- Realtime event routing (`internal/realtime/event.go`, `hub.go`)

What is exempt:

- `cmd/trackd/main.go` wiring
- `internal/config/` env parsing
- `internal/migrations/migrations.go` goose glue
- Pure defensive `return err` after a successful no-rows / known-pgcode check, where the only way to trigger it is a DB outage. Document these with a one-line comment so a reviewer can see they were considered.

When adding a feature:

1. Before merging, run `make test` with `DATABASE_URL` set and inspect `go tool cover -func=...` on the touched packages.
2. Every new branch in a handler, store method, or model transition needs a test case.
3. New error mappings (pg codes → sentinel errors) need a test that triggers the mapped code path through the public API, not the underlying pgx error directly.
4. New realtime entity/topic combinations need both a hub unit test and a listener integration test.

## How tests are organised

- Unit tests: `*_test.go` next to the code, no DB.
- Integration tests: `*_integration_test.go`, gated on `TEST_DATABASE_URL` or `DATABASE_URL`. Skip when env var is unset so `go test ./...` stays green without infra.
- Server tests use `httptest.NewServer(srv.Router())` against a real store and Postgres.
- Realtime tests subscribe to topics via `Hub.Subscribe` and assert events arrive via the same path production clients use.

## Running tests locally

```bash
make up                                     # boot Postgres on $POSTGRES_PORT
DATABASE_URL='postgres://track:track@localhost:5436/track?sslmode=disable' \
  go test -count=1 -coverprofile=/tmp/cover.out ./internal/...
go tool cover -func=/tmp/cover.out | sort
```

The repo's docker-compose maps `5432` on the container; `POSTGRES_PORT` in `.env` controls the host port (default `5432`, dev typically `5436`).

## Conventions worth knowing

- Sentinel errors `store.ErrNotFound` and `store.ErrConflict`; `writeStoreError` maps them to 404/409.
- Postgres error codes: `23503` (FK), `23505` (unique), `23514` (CHECK). Map these in the store, never let pgx errors leak to handlers.
- One transaction per store call when crossing more than one table or doing read-then-write under contention.
- Goose migrations are `+goose StatementBegin/End` per logical block; never inline multiple `CREATE`s without statement markers.
- Realtime events are emitted by the `track_emit_event` Postgres trigger. New tables that need realtime need a trigger and a topic. Sprint events ride the existing channel — see `0003_sprints.sql` for the pattern.

## When in doubt

Read the existing tests for the closest analogous feature. `internal/store/sprints_integration_test.go` and `internal/server/sprints_integration_test.go` are the current reference for full-coverage feature tests.
