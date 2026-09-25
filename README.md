# trackslash

**The open issue tracker your coding agents can actually use.**

trackslash is a fast, self-hostable issue tracker built as a single Go application backed by PostgreSQL. Its HTTP API, built-in MCP server, and project context give developers and coding agents access to the same project work.

> [!WARNING]
> trackslash is under active development and is not ready for production or commercial use. Interfaces, migrations, and deployment requirements may change. If you run it yourself, you are responsible for backups, upgrades, and recovery.

## What it does

- Tracks projects, issues, sub-issues, comments, links, tags, priorities, due dates, and sprints. Sprints are optional per project: without them, work is picked up one issue at a time.
- Keeps project and issue context addressable from the UI, HTTP API, and MCP.
- Exposes MCP tools, resources, and prompts for issue work, planning, context, attachments, users, and tokens.
- Supports project membership, read-only roles, public project views, passkeys, password login, and API tokens.
- Streams realtime changes through PostgreSQL-backed events.
- Stores attachments locally or in S3-compatible object storage.
- Ships as one Go application plus PostgreSQL, with frontend assets and migrations embedded in the production image.

## Development preview

The [trackslash project](https://trackslash.com/badbundle/projects/TRACK) is publicly readable as a development preview. It demonstrates the current product while trackslash remains experimental; it is not a hosted product offering or a production-readiness claim.

## Connect a coding agent with MCP

Create an API token from **Tokens** in the trackslash UI, then put it in an environment variable rather than a committed configuration file:

```sh
export TRACKSLASH_TOKEN='<your-token>'
```

The MCP endpoint is `https://<host>/mcp`. For a local instance, use `http://localhost:8080/mcp`.

An API token is the simplest way to connect a CLI agent and is used by every command below. trackslash also speaks OAuth 2.1, so clients that prefer to discover and authorize for themselves — including `claude mcp add --transport http` — work without a token being pasted anywhere. See [Add trackslash as a custom connector](#add-trackslash-as-a-custom-connector).

### Codex

```sh
codex mcp add trackslash \
  --url https://<host>/mcp \
  --bearer-token-env-var TRACKSLASH_TOKEN
```

### Claude Code

```sh
claude mcp add-json trackslash \
  '{"type":"http","url":"https://<host>/mcp","headers":{"Authorization":"Bearer ${TRACKSLASH_TOKEN}"}}'
```

### Cursor

Add this server to `.cursor/mcp.json` for one project or `~/.cursor/mcp.json` globally:

```json
{
  "mcpServers": {
    "trackslash": {
      "type": "http",
      "url": "https://<host>/mcp",
      "headers": {
        "Authorization": "Bearer ${env:TRACKSLASH_TOKEN}"
      }
    }
  }
}
```

Restart the client after changing its MCP configuration. Keep the token out of source control and revoke it from **Tokens** when it is no longer needed.

## Add trackslash as a custom connector

Hosted clients such as Claude.ai connect over OAuth rather than a pasted token,
and ask for a client ID and secret. Register one from **Tokens → Connectors**:

1. Choose **Register connector** and give it a name.
2. Enter the redirect URI the client will use, one per line and exactly as the
   client writes it. For Claude that is `https://claude.ai/api/mcp/auth_callback`.
3. Copy the **client ID** and **client secret**. The secret is stored only as a
   hash and is shown once; if it is lost, register a new connector.
4. In the client, add a custom connector pointing at `https://<host>/mcp` and
   paste in the ID and secret.

Each person who connects signs in to trackslash and approves the connector for
themselves, and it then acts with their permissions — the same access an API
token they created would have. Revoking a connector from **Tokens** disconnects
it immediately for everyone who approved it.

trackslash supports the authorization code flow with PKCE, and refresh tokens.
It does not implement dynamic client registration, which is why the client asks
for an ID and secret, and it has no client credentials grant: every token
belongs to a person. Set `TRACK_SLASH_PUBLIC_ORIGIN` in production so the
discovery documents advertise a stable address.

## Self-hosting

trackslash can be self-hosted under the MIT License with PostgreSQL and either local or S3-compatible object storage. For a local source-based instance, you will need Go 1.26.3, Node.js 20 or newer, and Docker:

```sh
cp .env.example .env
make up
make run
```

The app will be available at [http://localhost:8080](http://localhost:8080). Run `make seed` to add demo data.

## Readiness caveat

Self-hosting is available for development and evaluation, not as a production-readiness promise. Interfaces, migrations, storage behaviour, and deployment requirements may change. Back up both PostgreSQL and object storage, test restores, review migrations before upgrades, and keep your own recovery plan.

## Development commands

Useful commands:

```sh
make test
make build
make assets
make assets-check
make down
```

Frontend dependencies are pinned in `package-lock.json`. Generated CSS and JavaScript are committed under `internal/server/static` so the Go binary and Docker image do not need Node.js at runtime. Run `make assets` after changing templates, Tailwind source, or frontend dependencies; CI runs `make assets-check` to catch stale output.

## Deployment reference

The [deployment notes](DEPLOYMENT.md) document the current container image, migration job, origin and proxy settings, session limits, development-preview terms flag, request limits, and object-storage configuration. Treat them as an evolving operator reference, not a production support commitment.

## Security and legal

- Report vulnerabilities through the [security policy](SECURITY.md), published for the preview at [trackslash.com/security](https://trackslash.com/security), not a public issue.
- The Bad Bundle-operated development preview is governed by its [Preview Terms](legal/TERMS.md) and [Privacy Notice](legal/PRIVACY.md), published at [trackslash.com/terms](https://trackslash.com/terms) and [trackslash.com/privacy](https://trackslash.com/privacy). Those documents do not govern independent self-hosted installations.
- Use of the trackslash name and branding is described in the [trademark policy](TRADEMARKS.md).

## License

[MIT](LICENSE)

## Issue tracking

Issues are tracked in the public [trackslash development preview](https://trackslash.com/badbundle/projects/TRACK), not GitHub Issues.
