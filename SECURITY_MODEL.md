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
| Member | Everything, including members-only comments, deleted issues and forms | Issues, comments, sprints, context, whiteboard, forms |
| Read-only member | Everything, including members-only comments, deleted issues and forms | Nothing, except answering a form addressed to them |
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
- **Forms are the members'.** A form holds the questions an agent asks a
  person and that person's answers. The project's members read it, read-only
  ones included, and nobody else does: not a public viewer, a help-desk
  reporter or the reporter of a private issue. Every form route and tool
  checks membership before it looks for the form. Someone who can read the
  project without being a member is answered as for a form that doesn't exist
  (404, MCP `not_found`), whether or not it does, and someone who can't read
  the project is refused as on any of its routes. Creating a form, picking one
  up and cancelling one need write access. Only the form's assignee saves its
  answers, submits it or reopens it. The assignee must be a member; a
  read-only member can be asked, because answering their own form is not a
  project write. The Inbox (`/inbox`, `GET /api/v1/me/inbox`, `track_list_inbox`)
  lists only the forms addressed to the caller, and only those in live projects
  where they are still the owner or a member and not blocked. A site admin gets
  no extra forms there, and removing or blocking someone drops their forms from
  the list and the count at once.
  An issue names its forms (the form notice, the Details `Forms` row and
  `open_forms` on `track_get_issue` and the REST issue) to members only, and
  reporter views never do.
  A form's changelog entries (asked, submitted, reopened, picked up,
  cancelled) are members-only like the form, name no issue, and carry the
  form's title and reference only: never a question, an answer, the description
  or the comment. The push notification for a new form goes to its assignee
  alone, even when they asked for it themselves, and is checked again when it
  is sent: the form must still be open and theirs, and they must still be a
  member who is not blocked, or nothing is sent. The payload is the form's
  reference, its project, its title and a link, and nothing from the questions
  or answers.
  Creating a form returns a wait URL (`/api/v1/form-waits/{key}`), so the
  agent that asked can learn the form was answered without the person telling
  it. The key in it is the only credential the URL takes, because an agent's
  background command holds no account token, and all it buys is the form's
  status: `open`, `submitted`, `acknowledged` or `cancelled`. It shows no
  title, question, answer or project, and it changes nothing. The agent reads
  the answers through `track_get_form` or the REST route as before, which check
  membership. The key stands for the member who created the form, and that is
  checked on every read: once they are no longer a member, are blocked or are
  deleted, or the project is gone, the URL answers 404 exactly as for a key
  that was never issued. The server keeps only the key's SHA-256 and shows the
  key once, in the response that creates the form. `track_get_form` with
  `wait_seconds` waits the same way with the caller's own credentials, and
  checks their membership again on each read.
  A form can carry files (images, audio, video, anything). They are the form's
  and its members': a file reaches a member only through its form
  (`/forms/{formRef}/attachments/{objectRef}/content`, REST and UI), which
  checks membership first and looks for the file on that form only, so an
  `object-N` that is pending, on another form or not attached is 404. A file
  that belongs to a form, and a pending upload no form has claimed yet, are
  left out of every generic object route and tool for everyone, members
  included (`/objects`, `track_list_objects`, `track_get_object`,
  `track_read_object_content`, the `track://object-content` resource, and the
  project and issue attachment routes). A pending upload belongs to whoever
  made it: only a form created by that same person can claim it, and a ref that
  names someone else's, a deleted one or one that is not a pending upload is
  refused with one message, and nothing is claimed. Uploads that no form claims
  within a day are deleted, and the files of a form that was picked up or
  cancelled are deleted 30 days later (the form keeps `media_removed_at`).
  An upload URL (`/api/v1/form-uploads/{key}`) is how an agent gets files in,
  because a tool call can't carry a file and the agent's shell holds no account
  token. Like the wait URL it carries its own secret. All it buys is adding a
  file to its creator's pending uploads, for 30 minutes, 20 files and 25 MB a
  file, and the key is checked before the body is read. The key stands for the
  member who made it and needs write access, checked on every use: once they are
  no longer a member, are blocked, made read-only or deleted, or the project is
  gone, it answers 404 as for a key that was never issued. The server keeps only
  the key's SHA-256 and shows the key once.
- **Emails are never shown outside the account itself.** Member lists, pickers,
  comment authors and the reporter view name people by display name or
  username. Member search matches emails only for someone who manages members.

## Rules every surface follows

- **REST, MCP and the UI apply the same check for the same action.** A new route
  or tool resolves its entity through the existing helpers (`issueFromRoute`,
  `mcpIssue`, `uiIssueFromRoute`, `projectFromRoute`, and so on) and then checks
  the right permission on the project that entity belongs to.
- **Something the caller may not see is missing, not forbidden.** This applies to
  a hidden comment, to another reporter's issue and to a private issue. A
  signed-out visitor is not sent to sign in for an issue ref on a public
  project, missing or private, so both get the same 404.
- **Credentials are the account's own.** A connector (OAuth) token cannot read
  or change tokens, email, password, password login, passkeys, saved GitHub
  tokens, or site-admin account administration. See [OAUTH.md](OAUTH.md).
- **A person answers a form.** A connector (OAuth) token can create a form,
  read it and mark it picked up, but it cannot save answers, submit or reopen
  one, even for its own user, and MCP has no tool for any of the three. A
  session or an API token is the person's own, so REST accepts those. The
  form records how it was submitted (`web` or `api`). See [OAUTH.md](OAUTH.md).
- **A wait is bounded.** A wait URL and `track_get_form` hold a request for 50
  seconds at most, read the form again every 2 seconds and keep no database
  connection in between. At most 4 requests wait on one form and 256 in all;
  one more is refused with 429 (MCP `rate_limited`). A plain status check never
  waits, so it is never refused (`form_wait.go`).
- **Guesses are budgeted.** Password sign-in, password changes, reauthentication
  and OAuth client authentication share per-address and per-identifier failure
  budgets (`auth_rate_limit.go`).
- **Uploads are inert.** Only safe raster images are served inline and with
  their own type. Everything else is sent as an octet-stream download with a
  sandbox CSP. The one exception is a form's own content routes (the two
  `/forms/{formRef}/attachments/{objectRef}/content` routes, REST and UI),
  which also serve these audio and video types inline with their own type, and
  answer byte ranges, so a browser can play and seek them (Safari plays
  nothing without ranges): `audio/mpeg`, `audio/mp4`, `audio/x-m4a`,
  `audio/aac`, `audio/wav`, `audio/x-wav`, `audio/ogg`, `audio/webm`,
  `video/mp4`, `video/quicktime`, `video/webm`. That is safe because audio and
  video are passive media a browser plays and never runs, the response keeps
  `X-Content-Type-Options: nosniff`, the added `Content-Security-Policy:
  default-src 'none'; sandbox`, `Cross-Origin-Resource-Policy: same-origin`
  and the SHA-256 `ETag`, and the route is for members only. Any other type,
  however it was declared or named (HTML, SVG, script, FLAC, ...), is still an
  octet-stream download there. Every other route that serves an upload is
  unchanged: no inline audio or video, no ranges (tests pin this).
- **A file reaches people only through what owns it.** A form's file is
  served by its form's route and by no generic object route, and a file
  nothing owns yet (a pending upload) is served by none. Any new flow that
  stores files must be left out of the generic object routes the same way.

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
- **A form's answers are plain text every member can read.** The tool
  descriptions tell agents never to ask for a password, key or token in one,
  but nothing stops a person typing one into an answer.
- **A form's origin link is its creator's word.** `origin_url` must be an
  `https` link with no credentials in it, but nothing checks where it leads.
  Only members with write access create forms, and only members see them.
- **A form's wait URL is a secret in a URL.** Whoever holds it can see the
  form's status, and nothing else, until its creator loses the project. It
  outlives the token that created the form, and it can't be revoked on its
  own: cancelling the form leaves it answering `cancelled`. The server's own
  request log leaves the key out, but a proxy in front of it may log the full
  path. A status is all it gives away, so that is accepted.
- **A form's upload URL is a secret in a URL.** Whoever holds it can add files
  to its creator's pending uploads until it expires (30 minutes), up to its
  limits (20 files, 25 MB each), and it can't be revoked on its own. It is
  write-only: it reads nothing, and a file it adds does nothing until its
  creator puts it in a form. The files count as the creator's. A pending upload
  that no form claims is deleted after a day, and a closed form's files go 30
  days after it is picked up or cancelled. The server's own request log leaves the key out, but a
  proxy in front of it may log the full path.
- **Images are stored as uploaded,** metadata included.
- **Session cookies do not use the `__Host-` prefix.** They are HttpOnly,
  SameSite=Lax, Secure over https and have no Domain. Renaming them would sign
  everyone out.
- **Ending sessions leaves API tokens and connectors alive.** "Sign out
  everywhere" and a password change end web sessions only. API tokens and
  connected apps are listed on Tokens and ended there.
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
