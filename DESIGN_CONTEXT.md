# Design Context

Use this as lightweight product/design memory alongside `MANIFESTO.md` and `COMPONENTS.md`.

## Responsive Shell

- Below the `md` breakpoint, use an off-canvas navigation drawer opened from a persistent mobile app bar. Do not reserve space for a permanent icon rail on narrow screens.
- Keep mobile page gutters compact and consistent while preserving the established desktop content width and spacing.
- Dense issue rows should reflow into stacked, readable metadata on narrow screens and return to the compact column layout at `sm` and above.
- Keep primary tabs on one line. On narrow project pages, keep `Sprint`, `Planned`, and `All` visible (`In progress` and `All` when sprints are disabled) and move `Context`, `Whiteboard`, and `About` into the project overflow menu instead of wrapping or scrolling the tab bar. `Changelog` always lives in project overflow.

## Sidebar

- Below `Me` and `Projects`, the sidebar has two titled sections, each behind a divider: `Favorites` (favorited projects) and `Recents` (the ten issues the user opened most recently, newest first). Each has a small uppercase heading, and an empty section is not shown at all.
- Recents rows are the issue key badge plus a one-line title. Opening an issue moves it to the top without duplicating it, and the list refreshes out of band on htmx navigation. The full title shows in the app tooltip only when it is cut off.
- Recents history is stored per user on the server (the store keeps the 50 newest views), so it follows the user across devices. It never lists an issue the user can no longer read, and signed-out visitors have none.
- The collapsed icon rail hides Recents entirely rather than squeezing issue keys into icons. On an issue page, the issue's Recents entry is the active destination; with the sidebar collapsed, the page's favorited project is highlighted instead.

## Brand

- The login page's backdrop, icon and colours are the trackslash brand, and the user asked for that brand on every page, not just the front door. Every full page (app shell, login, signup, OAuth consent and error, legal pages) shares the `brand-head` links, a `brand-backdrop`, and the icon with the `trackslash` wordmark. Do not strip the backdrop from in-app pages to get back to a flat `slate-50` page.
- The backdrop comes in two strengths. The full, animated scene sits behind the single centered card of the auth and OAuth pages; it animates only `transform` and `opacity`, and every animation is gated on `prefers-reduced-motion: no-preference`. The `ambient` variant sits behind the app shell and legal pages, which people keep open for hours, and never moves: the user found the motion too much there and asked to keep only the grid and the colour flare. It has dimmer orbs, a grid that fades out under the page header, and no card halo or beams. Keep the motion on the sign-in pages only.
- Signed-in people can recolour the ambient backdrop behind the app from Profile → Appearance: **Indigo** (the brand's own, and everyone's default), **Slate** (the grid with no colour), **Emerald**, **Sky** and **Amber**. It's stored per user, so it follows them across devices and renders on the server without a flash. A preset changes only the wash and the orbs, in light and dark. Cards, chrome, primary buttons, the navigation progress bar and the `theme-color` meta stay indigo. Sign-in, OAuth and legal pages always use the default. Don't add a preset where red or rose dominates (rose means errors), and each preset is a class in `frontend/tailwind.css`, never a style attribute (CSP).
- The backdrop shows in gutters and empty space; content never sits on it directly. `#main` has no background of its own, but every card, panel, and chrome surface stays opaque. A card that sits straight on the page and carries a status tint uses `statusSurface` or `statusCard`, which lay the translucent tint over an opaque page-colour base. Only rows inside an opaque list card use the translucent `statusRow` tints.
- The icon and wordmark lockup (`brand-mark`: the 28px `static/icon.svg` plus `trackslash` at `text-base font-semibold tracking-tight`) sits in the sidebar head, the mobile app bar, and the legal header. The collapsed sidebar hides it with the other wide-only labels.
- Navigating inside the app shows a thin indigo→violet→sky progress bar along the top of the viewport, with a highlight sweeping through it at the icon's slash angle. It appears only after a short delay, so fast navigations never flash it.
- Type: the default Tailwind sans stack everywhere; there is no brand font file, so do not add one from a font CDN. Headings are `font-semibold`, and page titles use the wordmark's display tracking (`text-2xl font-semibold tracking-tight`), as do Markdown `h1`/`h2`.
- Colour: slate neutrals with indigo as the one accent. Primary buttons are `bg-indigo-600 hover:bg-indigo-500 shadow-sm shadow-indigo-600/30 font-semibold`, matching the login button. Errors and destructive actions use rose (never red, which is reserved for the P0 priority and the red tag colour), success uses emerald, and selected text picks up indigo. Violet, sky, and rose appear as brand colours only in the backdrop and the progress bar.
- The backdrop and the progress bar are the only gradients. Keep the rest of the "quiet work tool" guidance: no hero sections, decorative gradients, or ornament inside page content.

## Login page

- The signed-out auth pages, `/login` and its sibling `/signup`, are the brand's front door and show its full-strength version. Do not "fix" them back to a plain card. The OAuth consent and error pages reuse the same shell.
- All four share the `auth-page-open`/`auth-page-close` shell in `login.html`: the full `brand-backdrop` (soft indigo/violet/sky/rose gradient orbs, a masked grid, a breathing halo behind the card, and light beams at the icon's slash angle), then one centered card with the 64px `static/icon.svg` icon and a large `trackslash` wordmark, then the legal links.
- The wordmark uses the same treatment as `brand-mark`, the default Tailwind sans stack at `font-semibold` with display tracking, only larger.
- The backdrop is pure CSS in `frontend/tailwind.css` under the `.brand-backdrop` classes and is rendered by the `brand-backdrop` template in `brand.html`. The shapes are gradients rather than `filter: blur()`. CSP forbids inline styles, so keep it as classes in the compiled stylesheet. Keep the card surface near-opaque with no `backdrop-filter`, so text stays readable and nothing re-blurs every frame.
- Login is passkey-first. `Log in with passkey` is the primary button and the first focusable control. `Log in with password` is a small native `<details>` disclosure below it that holds the username and password fields; it stays collapsed until asked for. It renders open after a failed password attempt, and `auth.js` opens it when the browser has no WebAuthn. Opening it moves focus to the username field.

## User Identity

- Render profile images and initials fallbacks as circles everywhere. The shared `user-avatar` component owns the crop shape so individual screens cannot diverge.
- Keep profile and project image selection, upload, and removal in the shared image-picker modal. Owning panels show only the current image and a compact Add/Change action.
- In the project About `Details` card, the image row puts the project image on the leading edge and the Change action on the trailing edge, vertically centred on the image.
- Identify the signed-in account as `@username` in the profile overlay instead of showing a generic role label such as `Member` or `Admin`.

## Account Pages

- A signed-in user's own settings live on four focused account pages, not one general Settings page: `Profile` (`/settings/profile`: profile image, display name, email), `Login` (`/settings/login`: password and passkeys), `Notifications` (`/settings/notifications`: browser push), and `Tokens` (`/tokens`: API tokens, connectors, GitHub tokens, web sessions). `/settings` redirects to Profile and keeps its query string.
- The `Login` account page manages credentials for a signed-in user. It is a normal in-app page, separate from the signed-out `/login` sign-in page described under "Login page" above, so it gets the in-app ambient brand rather than the sign-in card.
- The four pages live only in the account menu that opens from the avatar at the bottom of the sidebar; the sidebar itself does not list them. Each menu item pairs its Lucide icon with its label, loads into `#main` like other navigation, and is highlighted while it is the current page. The menu renders from `uiAccountPages`, so add or reorder pages there.
- `Sign out` sits alone at the bottom of the account menu, below a divider, in bold rose (destructive) text with a `log-out` icon.
- Every account page uses the Tokens page frame and header so moving between them does not shift the column, and ends with the shared `account-footer` legal links.
- Account page cards stack in one column, each spanning the page, so their edges line up; do not put them in a side-by-side grid. Inside a card, short paired fields (display name and email, current and new password) share a row from `sm` up and stack below it, with the submit button beneath them.
- Password and passkeys stay together on Login: the password login toggle reauthenticates through the passkeys panel, and changing a passkey can ask for the current password.
- When email is configured, the email leaves the profile form for its own `Email` card below `Profile`: the address in effect with a `Verified` or `Not verified` badge, a change still waiting for its link with `Resend`, and a `Change email` form that asks for the password or a passkey first. A new address takes effect only once confirmed from the link it was sent. With email off there is no card and the profile form keeps its email field.
- Web sessions stay on Tokens for now; moving them to Login is an open question.
- The Tokens page lists only live API tokens. A revoked token leaves the list as soon as it is revoked; its row stays in `auth_tokens`, and the API and MCP token listings still return it.

## Controls

- Every icon-only action needs a concise, action-oriented `aria-label` and the shared app tooltip on pointer hover and keyboard focus. Do not show redundant tooltips while equivalent text is visibly rendered, and do not rely on native `title` tooltips for interactive controls.

## Forms

- Create forms stack their fields in one left-aligned column, each labelled above its control with even spacing, so tab order follows the visual order. Free-text fields (title, description) span the card; short metadata fields (priority, people, dates) share a narrower column width. On New issue the order is Priority, Worker, Reporter, Assignee, Due date, then Repeat. Worker, Reporter, Assignee and Repeat are for members, so a public submission skips them.

## Issue Detail

- When Sub-issues and Linked issues are both empty they share one row in equal halves, so their headers never wrap and their edges line up with the Description card. A section with items takes the full width.
- Values in the Details sidebar share the label's left edge. Clickable badges (priority) pull back their hit-area padding so the badge itself lines up.

- Keep the issue-title edit action attached to the title's final word at every viewport width. The word and action form one unit that wraps together, and the action must never become an orphaned line by itself. Only a final word too long for a line breaks, and the action stays with its last piece. The action must not change the title's line height or the final word's baseline.

## Project View

- Render project images as squares with a small corner radius everywhere, using the shared project icon component and a project-initial fallback. Keep them visually distinct from circular user avatars.
- Treat the project page as a focused planning console, not a place to introduce new workflow controls by default.
- Prefer the stronger hierarchy of the issue detail page: clear title card, compact metadata, purposeful cards, and restrained section language.
- Keep the project header cohesive. Project identity, actions, and tabs should feel like one unit, with the tab bar close to the project title and flush to the bottom of the header.
- Keep `Deleted issues` in the project actions menu, not in the primary tab bar.
- Project charts live in a dedicated `Insights` view at `/{owner}/projects/{key}/insights`, rendered under the standard project header. The primary tab bar stays fixed, so `Insights` sits in the project actions menu directly above `Sprint history`. The About page carries only a compact `Insights` card that links there; do not put charts back on About.
- Insights charts follow issue status colours: neutral slate for scope and to-do work, blue for started work, emerald for completed work, and the indigo accent for arrivals and single-series marks. One date-range control (`2 weeks`, `30 days`, `90 days`, `All time`) above the charts scopes every chart; each chart shows exact values on hover, tap, and keyboard focus, has legend toggles, and keeps a `Data table` disclosure. Sprint burn-up and velocity follow the project's sprint mode: when sprints are off they are hidden and a one-line note says so.
- Keep `Sprint history` in the project actions menu immediately above `Changelog`. It is a read-only, newest-first list of completed sprints with compact schedule/completion metadata and explicit pagination. Each row shows frozen `Done` and `Cancelled` issue counts plus a compact Markdown description preview with its own `See more` expansion. Retain a separate sprint disclosure control that lazy-loads the complete, paginated issue membership captured atomically when that sprint completed.
- Open project member management from the project actions menu as a full project page with its own URL, not as a modal.
- Keep `Delete project` at the bottom of the project actions menu, below a divider and in destructive colour. It opens a confirmation dialog that names the consequences and requires the project key to be typed. Only the project owner and site admins see it. The same owner-or-admin rule governs `DELETE /projects/{key}` and `track_delete_project`, so no surface can delete a project the others would refuse.
- Use the wide-layout project tabs `Sprint`, `Planned`, `All`, `Context`, `Whiteboard`, and `About`; with sprints disabled, `In progress` replaces `Sprint` and `Planned`. `Sprint` is singular; use a human/running-style Lucide icon when available, and show it only when the project has sprints enabled. Below `lg`, show only the work tabs (`Sprint`, `Planned`, `All`, or `In progress` and `All`) and expose `Context`, `Whiteboard`, and `About` from project actions. Keep `Changelog` in project overflow at every breakpoint.
- Show assignee filters only where they apply. Do not preserve or display assignee filters on `About`.
- The `All` tab is the triage and discovery surface. It should feel dense and scan-friendly, with all current, past, completed, planned, and unplanned issues available through one list.
- In sprint mode, each `All` row whose issue is in a sprint shows a small `S` badge (`issue-sprint-badge`). Hovering it names the sprint (`In sprint-3 · UI polish (active)`), so sprinted and unplanned work are told apart at a glance. Rows not in a sprint show nothing, and with sprints off no row shows it. Views already grouped by sprint (the `Sprint` board, `Planned`, and `Sprint history`) don't repeat it. The page loads its sprints in one query (`ListSprintsByID`), so the infinite-scroll pages carry the badge too.
- Flat issue lists — project `All` and both `Me` views — default to open work (`To do` and `In progress`). A list of everything ever filed answers "what is on this project" worse than a list of what is left. Completed and cancelled work stays one click away behind the `Any` status filter, and `?status=any` is the URL that says so. The sprint board is the exception: its columns are the statuses, so it keeps all of them.
- Keep `All` page controls in one coherent section. Avoid loose chip clusters; group filters in aligned rows and separate sort controls visually while keeping them in the same control shell.
- For filters, support multi-select where it helps scanning. Statuses, priorities, and assignees use OR semantics within each group, while different groups combine together.
- Put project tag management in the project About details sidebar, parallel to issue tag management. Keep it out of the project overflow menu.
- GitHub tokens belong to the user, not the project. They are saved, renamed, replaced, and removed in the `GitHub tokens` section of the Tokens account page. The About page's `Connect GitHub repository` dialog picks one of the user's saved tokens, and pasting a new token there saves it to the account rather than to the project. Each connected repository names only the viewer's own token (`your token “Personal”`); anyone else's is just `saved token`.
- Show project access settings at a glance near the top of the About sidebar in an `Access` card. Each row is an `access-badge` with a one-line explanation under it: `Visibility` (`Public` or `Private`), `Issue creation` (`Any signed-in user` or `Members only`), then `Sprints` (`Enabled` or `Disabled`). The open or on state is tinted and the restricted or off state is neutral, so the rows read as a set. The card is read-only for everyone; owners and admins get a compact settings action that opens the members page, where the settings are edited.
- Above `Access`, a `Your access` card tells the viewer where they stand. It has a `Role` row, an `access-badge` of `Owner`, `Site admin`, `Member`, `Read-only`, `Not a member` or `Signed out` with a one-line summary, then a `Permissions` list: view, create issues, edit, manage access and settings, delete the project. Each item carries a check or lock icon and screen-reader `Allowed`/`Not allowed` text. The card is built by `uiProjectViewerAccessFor` from the same `store.ProjectPermissions` the handlers enforce, so it never promises an action the server refuses. A one-line hint says how to get more access where that helps: `Sign in` links for signed-out visitors, and `Ask @owner …` for read-only members and non-members. We chose one summary card over per-section padlock icons.
- Keep visual changes layout-focused unless the user explicitly asks for new creation, editing, drag/drop, or planning workflow controls.

## Sprint Mode

- Sprints are a per-project choice, disabled for new projects. The `Sprints` toggle lives on the members page beside the access settings and follows their owner-or-admin rule. The same rule governs `PATCH /projects/{key}/sprint-mode` and `track_update_project_sprint_mode`.
- In sprint mode the project opens on the `Sprint` board. With sprints disabled it opens on `All`, the `Sprint` tab is hidden, and a request for the sprint board redirects to `All` rather than showing an empty board.
- With sprints disabled there is nothing to plan, so the `Planned` tab is hidden and `In progress` takes its place (see below). `/planned` redirects to `/progress`, and with sprints enabled `/progress` redirects to the `Sprint` board, which shows the same work. Disabling sprints never deletes anything: planned sprints keep their issues and reappear under `Planned` when sprints are turned back on, `Sprint history` stays in the project actions menu, and completed history is never rewritten.
- A stale `Activate sprint` form submitted after sprints were disabled lands on `In progress`, which says why nothing started in one compact notice, with an `Enable sprints` link for people who can change it.
- While a sprint is active the toggle is locked and says `Complete the active sprint to disable sprints.` The store enforces the same rule under the project row lock, and starting a sprint takes that lock too, so the two cannot race.

## In Progress

- `In progress` (`/{owner}/projects/{key}/progress`) is the project's "what is happening now" view when sprints are off. It has two sections: `In progress`, every top-level issue in progress with the highest priority first, and `Recently completed`, top-level issues completed within a chosen window, most recently completed first, each showing when it was completed. Completed means Done or Closed: won't-do, invalid, and duplicate issues are listed too, with their close reason.
- The window is a `range-control` on the trailing edge of the `Recently completed` header: `24 hours`, `7 days` (default), `14 days`, and `30 days`, carried in `?completed_within=1d|7d|14d|30d`. Completion time is the issue's last move to Done or Closed in the changelog, including automatic duplicate closes, so a reopened issue counts from when it was finished again.
- Each section keeps its own count badge and empty state (`No issues in progress.`, `Nothing completed in the last 7 days.`). There are no filters; `All` is the place to slice issues.
- `GET /api/v1/{owner}/projects/{key}/progress?completed_within=7d` and `track_get_project_progress` return the same `ProjectProgress` payload in either sprint mode, under the project read check, so public projects work signed out.

## Sprint Descriptions

- Active, planned, and completed sprint-history rows retain a compact, vertically cropped Markdown preview. “See more” expands the full Markdown description and attachment rows; sprint issues remain an independent disclosure.
- Planned and completed sprint cards keep identity and metadata in a stable header, render the description at full card width, and place the issue disclosure in the bottom-right footer with explicit `Show issues` / `Hide issues` text.
- Sprint description editing uses the same attachment dropzone, preview rows, Markdown-copy, download, and removal behavior as issue descriptions.

## Context IA

- Treat project context as a top-level project view. Do not duplicate it in the Project About details sidebar.
- Use `/{owner}/projects/{key}/context` as an integrated project tab with the standard project header, a flat ordered page list, and one selected document.
- Project pages use explicit Markdown edit/save/cancel behavior, support `.md`, `.markdown`, and `.txt` import, and use compact move-up/move-down controls rather than drag-and-drop.
- Keep page rows compact and show body content only for the selected page. Page attachments use the shared description attachment behavior and resolve `object-N` only within that page.
- Keep linked-issue counts out of project context page rows. Show the complete linked-issue list in its own section below, but visually separate from, the selected document card, with the count beside the section title.
- Keep the project Context Pages sidebar content-height; it must not stretch to match the selected document pane.
- Use an integrated issue Context manager rather than a modal. It should mirror the project Context list/document layout, keep the issue identity and breadcrumb return path visible, and use addressable selected-page URLs.
- Linked project pages render their Markdown and page attachments in the issue Context manager; issue-only context remains escaped pre-wrapped text.
- Linked project pages are read-only in the issue Context manager; edit them from the project Context tab so their project-wide scope stays explicit.
- Use user-facing titles for finding and attaching context. Do not expose refs such as `context-1` as visible row labels or search/link inputs; refs may remain in URLs/API mechanics.
- Keep issue Context actions explicit: one action creates issue-scoped context and one attaches existing project context. Project page creation, ordering, deletion, attachments, and linked-issue management stay in the project Context tab.

## Whiteboard

- The Whiteboard is a project's scratch space: free-form Markdown notes and ideas that come and go. It is deliberately lighter than Context and never links to issues — no linked-issue section, no attach action, no issue counts. Promote anything that becomes real work into an issue.
- Use `/{owner}/projects/{key}/whiteboard` as an integrated project tab with the standard project header, the same list/document layout as Context, and `/whiteboard/{whiteboard-N}` for the selected page. The tab opens on the most recently updated page.
- List pages most recently updated first, titles only; only the selected page renders its body. There is no manual ordering and no attachment or import support.
- Creating a page is one click from the list header (or the empty state) and takes a title plus optional Markdown. Editing uses the same explicit edit/save/cancel Markdown behavior as Context pages. Deleting is one action with a lightweight `hx-confirm`; pages are soft-deleted and their refs are never reused.
- Writers see the create, edit, and delete controls. Read-only members and public readers see the same pages with none of them. The empty state reads `No whiteboard pages yet` and offers `New page` to writers only.

## Tag IA

- Treat issue tags as lightweight issue-detail metadata. Show them near the issue title, not buried in the Details sidebar.
- Use a modal for issue-scoped tag attach/detach from issue detail. The modal should preserve the user's place on the issue, avoid URL pushes, support quick repeated changes, and expose search over existing project tags.
- Keep broader tag taxonomy work in the project tag manager. Issue tag modals should link out to that manager for create/edit/delete rather than becoming a second fullscreen management surface.
- Ticket numbers are identifiers, not prose. Render issue identifiers with the shared compact monospace `issue-key` treatment, including modal/header contexts.
