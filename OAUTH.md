# OAuth connectors

trackslash is an OAuth 2.1 authorization server for one purpose: letting a
client such as a Claude.ai custom connector reach `/mcp` as a trackslash user,
without that user pasting a token into someone else's product.

API tokens still work and are unaffected. They remain the simplest option for a
CLI agent; OAuth exists for clients that discover and authorize for themselves.

## The flow

1. The client fetches `/.well-known/oauth-protected-resource/mcp` (RFC 9728) and
   `/.well-known/oauth-authorization-server` (RFC 8414). An unauthenticated
   `POST /mcp` answers `401` with a `WWW-Authenticate` header naming the first
   of those, which is how a client finds them in the first place.
2. It sends the user to `GET /oauth/authorize` with `client_id`, an exactly
   registered `redirect_uri`, `state`, and a PKCE `code_challenge`.
3. trackslash signs the user in through its own login page if needed, then shows
   the consent screen. Approving mints a single-use code, delivered by redirect.
4. The client exchanges the code at `POST /oauth/token`, authenticating with its
   client ID and secret and proving it started the flow with `code_verifier`.
5. The access token is an ordinary bearer token. `POST /oauth/revoke` (RFC 7009)
   disconnects.

Only `S256` is accepted for PKCE; `plain` offers no protection against an
intercepted code. The only scope is `mcp`, which grants the approving user's own
permissions — the same authority an API token of theirs carries. Subdividing it
would imply an enforcement boundary that does not exist elsewhere in trackslash.

## What is stored, and where

| Table | Holds |
| --- | --- |
| `oauth_clients` | Registered connectors: public `client_id`, hashed secret, exact redirect URIs, owner |
| `oauth_client_consents` | One row per (client, user) who approved, so a reconnect skips the prompt |
| `oauth_authorization_codes` | Short-lived single-use codes and their PKCE challenge |
| `oauth_refresh_tokens` | Refresh tokens and their rotation lineage |
| `auth_tokens` | Access tokens, as `kind = 'oauth'` with an `oauth_client_id` |

Lifetimes: authorization codes 1 minute, access tokens 1 hour, refresh tokens
90 days with rotation on every use. PKCE challenges and verifiers are held to
the 43-to-128-character range RFC 7636 section 4.1 requires, in Go as well as by
the column constraint, so a malformed one is reported to the client rather than
failing an insert.

Consent is remembered per (client, user, scope). The scope is part of the match
because an approval is for what the consent screen showed; a request for
anything else has to be approved on its own terms.

## Decisions worth knowing

**Client secrets are hashed, never encrypted.** trackslash only ever verifies a
client secret; it never replays one to a third party, which is what forces the
AES-GCM envelope used for GitHub tokens in `internal/githubintegration`. Hashing
means no new key to manage and nothing usable in a database leak. The secret is
shown once at registration and cannot be read back.

**Access tokens live in `auth_tokens`; refresh tokens deliberately do not.** The
API auth middleware accepts any row in that table as a bearer credential without
filtering on kind, so a refresh token stored there would authenticate REST calls
it was never meant to. Access tokens belong there precisely because they *should*
behave like any other token the user holds.

**Adding the `oauth` token kind needed two migrations.** `auth_token_kind` is a
Postgres enum. `ALTER TYPE ... ADD VALUE` may run inside a transaction, which is
what goose wraps each migration in, but the new value cannot be *used* in that
same transaction — so `0042` adds it alone and `0043` builds on it.

**Replay revokes rather than refuses.** A reused authorization code, or a
refresh token that has already been rotated away, means a copy leaked. Both
revoke every token that (client, user) pair holds before reporting the failure.
The revocation is committed before the error is returned; failing the
transaction instead would roll back the very thing that makes detection useful.

**A code is looked up by client, not merely checked against one.** Both
`ConsumeOAuthAuthorizationCode` and `RotateOAuthRefreshToken` take the
authenticated client and match on it in the locking `SELECT`. Checking the
binding after consuming would let any registered client — and on this instance
any signed-in user can register one — burn a code belonging to someone else.
The rightful client's exchange would then look like a replay, and replay revokes
the whole grant. Matching in the query means a foreign code is simply not found
and nothing is mutated.

**A connector cannot manage credentials.** `requireFirstPartyToken` (and
`requireMCPFirstPartyToken` on the MCP surface) refuses the token endpoints to a
token whose kind is `oauth`. A connector that could mint an API token would
escape its own revocation: `CreateAuthToken` records no `oauth_client_id`, so
nothing that revokes a connector can reach such a token, and an unexpiring one
would outlive the consent it was created under. `CreateAuthToken` also refuses
the `oauth` kind outright, so the only way an `oauth` row exists is through the
authorization server, which always records the client behind it.

**A connector's token is not a browser session.** `uiAuthMiddleware` rejects the
`oauth` kind. The UI's CSRF token is derived from whatever value sits in the
session cookie, so anything holding a raw access token could otherwise drive the
whole signed-in UI — including registering a second connector for that user,
which revoking the first would not touch.

**Redirect URIs are matched byte for byte.** They decide where an authorization
code is delivered, so there is no normalisation, case folding, or prefix match.
Nothing at all is sent to an unregistered address — not even an error — because
that would be an open redirector carrying the victim's `state`.

Registration is correspondingly strict. Userinfo is refused, per RFC 6749
section 3.1.2 — browsers disagree about whether to strip or prompt on it, and
trackslash would be placing a fresh authorization code next to a password in a
`Location` header. The host is restricted to the characters a real host name or
IP literal needs: Go's URL parser accepts `;`, `,`, `*` and `'` in a host, and
the consent page splices that origin into its `Content-Security-Policy`, where a
`;` would add a whole directive and a `,` would split the header into two
policies. `oauthSafeHost` enforces the same allowlist again at the point of
use, so the property does not rest on registration alone.

**`form-action` is widened on exactly two responses.** The global CSP is
`form-action 'self'`, and Chromium and WebKit apply it across a form
submission's whole redirect chain, so the hand-off from the consent screen to
the client would otherwise be blocked. `allowOAuthFormAction` adds the one
origin the client registered, to that response only, after the redirect URI has
been matched. Every other page keeps the strict policy.

**Discovery and token endpoints answer any origin.** They are public documents
or authenticated by a client secret in the request, never by cookie, so a
wildcard gives a hostile page nothing. They are routed around the shared CORS
middleware, which would otherwise refuse preflights from origins outside the
configured allow list.

**Failures are budgeted, not traffic.** Hosted clients refresh from a small pool
of shared egress addresses, so a per-IP budget spent by successful refreshes
would throttle exactly the traffic that is working. The rate limiter is consumed
only when client authentication or a grant fails.

**No dynamic client registration, and no client credentials grant.** Omitting
`registration_endpoint` from the metadata is what tells a client to ask the
operator for an ID and secret. A client credentials grant would mean a token
with nobody behind it, which trackslash's per-user permission model has no way
to answer.

## Operating notes

Set `TRACK_SLASH_PUBLIC_ORIGIN` in production. Without it the discovery
documents derive their URLs from the request `Host`, which is right for a
localhost instance but wrong behind a proxy that rewrites it.

Revoking a connector scrubs its secret hash, forgets every remembered approval,
and revokes all its access and refresh tokens in one transaction.
