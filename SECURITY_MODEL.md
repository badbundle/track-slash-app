# Security model

What each kind of user can reach, the rules every surface follows, and the
trade-offs trackslash accepts on purpose. Read this before changing permissions,
authentication, project access, comments, notifications or realtime. Report a
vulnerability as [SECURITY.md](SECURITY.md) describes, never in a public issue.

## Who can see what

`ProjectPermissionsForUser` in `internal/store/permissions.go` is the single
source of a user's rights in a project.

| Who | Reads | Writes |
| --- | --- | --- |
| Site admin, project owner | Everything | Everything, including members, access and deleting the project |
| Member | Everything, including members-only comments and deleted issues | Issues, comments, sprints, context, whiteboard |
| Read-only member | Everything, including members-only comments and deleted issues | Nothing |
| Public viewer (`public`, `public_issues`) | Live issues that are not private, and their shared comments | Files issues in `public_issues` only |
| Reporter of a private issue (a non-member of a public project) | That issue, as a `ReporterIssue`, and its shared comments | Replies on it |
| Help-desk reporter (non-member of a `helpdesk` project) | Only issues they reported, as a `ReporterIssue`, and their shared comments | Files issues (title and description) and replies |
| Blocked user | Nothing: 403 everywhere in the project | Nothing |

- **Help-desk reporters get no project-level read at all.** Only the reporter
  paths open (see `helpdesk.go`). Every route that names an issue goes through
  `issueRouteAccess`, which answers another reporter's issue exactly as it would
  a missing one.
- **Public projects are link-only.** Anyone with the link can read one, but
  lists name a project only to its owner and members: the Projects page and an
  owner's projects page, the `/` redirect, Me, the new-issue project picker,
  `GET /api/v1/projects` and `track_list_projects`. Site admins get the same
  lists; their wider access works by link. Recents and Favorites record the
  user's own history, so a public project opened by link can appear there.
- **Private issues are the members' and their reporter's.** An issue marked
  private is left out, for everyone else, of every list, board, count, chart,
  sub-issue list, link, file listing, changelog entry, realtime event and push
  notification, and every route that names it answers as a missing issue does
  (404, MCP `not_found`, a realtime subscribe that fails as for a missing
  topic). Store lists take `IncludePrivate`, false unless the reader is a
  member (`readsPrivateIssues`); `ProjectPermissions.ForIssue` narrows a
  reader's rights to one issue. A non-member reporter follows their private
  issue the way a help-desk reporter does. A sub-issue is never more visible
  than its parent: one filed under a private issue is private, making an issue
  private makes its sub-issues private, and a sub-issue can't be made public
  under a private parent. A repeating issue's next repetition keeps its
  private flag, and an issue's `repeat.previous`/`repeat.next` name a private
  repetition only on a private issue, since only members read one in full.
- **Members-only data stays with members.** That covers members-only comments,
  block history, deleted issues and the files only they hold, the changelog
  about them, and deleted comments' previews. Members-only comments never reach
  anyone else, whether through the API, MCP, the UI, the changelog, push
  notifications or realtime.
- **Emails are never shown outside the account itself.** Member lists, pickers,
  comment authors and the reporter view name people by display name or
  username. Member search matches emails only for someone who manages members.
  The access log records each request's path without its query string, so a
  searched email stays out of it, and the settings page sends a browser's push
  endpoint, a capability URL, in a header rather than the URL.

## Rules every surface follows

- **REST, MCP and the UI apply the same check for the same action.** A new route
  or tool resolves its entity through the existing helpers (`issueFromRoute`,
  `mcpIssue`, `uiIssueFromRoute`, `projectFromRoute`, and so on) and then checks
  the right permission on the project that entity belongs to.
- **Something the caller may not see is missing, not forbidden.** This applies to
  a hidden comment, to another reporter's issue and to a private issue. A
  signed-out visitor is not sent to sign in for an issue ref on a public
  project, missing or private, so both get the same 404.
- **A dead credential is refused, not ignored.** Over REST, only a request with
  no `Authorization` header reads a public project as a signed-out visitor. An
  expired, revoked or malformed token gets 401 with `WWW-Authenticate`, as it
  does on MCP, so its client knows to sign in again or refresh.
- **Credentials are the account's own.** A connector (OAuth) token cannot read
  or change tokens, email, password, password login, passkeys, saved GitHub
  tokens, or site-admin account administration. See [OAUTH.md](OAUTH.md).
- **A deleted account has no way back in.** Deleting a user revokes its API
  tokens, sessions, connector grants and codes, password and passkeys in the
  same transaction (`DeleteUser`), so none of them rests on the `deleted_at`
  filter alone. `trackd -create-admin-token` refuses an email that belongs to a
  deleted account rather than restoring it.
- **Guesses are budgeted.** Password sign-in, password changes, reauthentication,
  sign-up and OAuth client authentication have per-address and per-identifier
  budgets (`auth_rate_limit.go`). Each purpose keys its identifier budgets
  under its own prefix, so spending one never spends another, and sign-up is
  budgeted by the normalized username. A successful passkey sign-in spends
  nothing, since it cannot be a guess.
- **Push deliveries reach only public addresses.** A browser names its own
  push endpoint, so saving one on `localhost` or a loopback, private,
  link-local, unique-local, carrier-grade NAT or unspecified address fails
  (`PushEndpointAddressAllowed`). The sender checks each resolved address again
  as it connects and never follows a redirect.
- **Signing out stops a browser's notifications.** The sign-out form names
  the browser's push subscription, which is disabled with the session, and
  app.js unsubscribes it too. The response sends
  `Clear-Site-Data: "cache", "storage"`, which removes the service worker and
  its subscription along with htmx's history cache of pages. "Sign out
  everywhere" sends the same header, and it and a password change disable push
  on every browser of the account, including the one that changed the password.
  Each browser turns push back on from its own Notifications page.
- **Uploads are inert.** Only safe raster images are served inline and with
  their own type. Everything else is sent as an octet-stream download with a
  sandbox CSP.
- **Personal responses are not cached.** Pages rendered for a signed-in user
  and every stored file, on a public project too, are sent
  `Cache-Control: private, no-store`. Assets under `/static` are shared and
  keep their own caching.

## Accepted trade-offs

These are known and left as they are. Change one only with that in mind.

- **Existence can be probed.** A signed-in outsider gets 403 for a private
  project or issue that exists and 404 for one that does not.
  `/{owner}/projects` answers 404 for an unknown username. Project keys and
  usernames are not secrets. Help-desk reporters and private issues are the
  exception: another reporter's issue, and a private issue on a public
  project, are always 404 to someone outside it. Issue numbers are still
  sequential, so a gap shows that something had that number, just as a
  deleted issue leaves one.
- **Help-desk refs reveal counts.** Issue numbers run per project and comment
  numbers per issue. A reporter's own refs therefore show roughly how many
  issues the project has, and gaps show that members-only comments exist. Refs
  have to stay stable and shareable.
- **Public projects list their members.** Anyone who can read a project can
  list its members' usernames, names and roles over REST and MCP, though only
  managers see the members page. Usernames already appear on issues.
- **GitHub metadata follows the project's visibility.** If a public project
  connects a private repository, its readers see the repository name and the
  linked branch and pull request titles.
- **Images are stored as uploaded,** metadata included.
- **Session cookies do not use the `__Host-` prefix.** They are HttpOnly,
  SameSite=Lax, Secure over https and have no Domain. Renaming them would sign
  everyone out.
- **Ending sessions leaves API tokens and connectors alive.** "Sign out
  everywhere" and a password change end web sessions and disable push
  subscriptions, nothing more. API tokens and connected apps are listed on
  Tokens and ended there.
- **A sign-out without script can't name its push subscription,** and nothing
  records which session made one, so the server keeps sending to it.
  `Clear-Site-Data` still removes the subscription from a browser that
  supports the header, so nothing is shown there, and the push service's
  rejection of the next delivery disables it.
- **Creating a token or registering a connector needs no reauthentication,**
  and the token can outlive the session that made it.
- **Passkey sign-count regressions are recorded, not refused.** Synced passkeys
  report zero.
- **Emails are not verified.** Setting an email another account holds fails
  with a conflict, which tells the caller that the email is registered.
  `trackd -create-admin-token` will not promote an existing non-admin who holds
  the email unless `-promote-existing` is passed.
- **Storage object JSON names its backend, bucket and key.** These are
  locations, not credentials.
- **Realtime access is checked once, on subscribe.** Events carry only IDs, and
  every refetch checks access again. Changes to access, blocks or membership,
  and deleting a user, disconnect every client so it has to reauthorize. A token
  revoked mid-connection keeps its socket until it reconnects. With several
  replicas, a disconnect reaches only the replica that handled the change.
- **Rate limits live in each process,** so several replicas multiply the
  budgets.
- **Anyone can spend a username's sign-in budget.** Ten failed password
  attempts in five minutes hold up that account's password sign-in for the rest
  of the window, though passkey sign-in still works. This is the cost of a
  per-account budget against distributed guessing.
- **The changelog keeps edit history.** The before and after previews of an
  edited issue or comment stay visible to whoever can read the changelog.
- **Blocks do not apply to site admins.**
- **In public projects, comments are shared by default,** as they always were.
  Help desks default to members-only.
