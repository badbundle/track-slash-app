# Design Context

Use this as lightweight product/design memory alongside `MANIFESTO.md` and `COMPONENTS.md`.

## Responsive Shell

- Below the `md` breakpoint, use an off-canvas navigation drawer opened from a persistent mobile app bar. Do not reserve space for a permanent icon rail on narrow screens.
- Keep mobile page gutters compact and consistent while preserving the established desktop content width and spacing.
- Dense issue rows should reflow into stacked, readable metadata on narrow screens and return to the compact column layout at `sm` and above.
- Keep primary tabs on one line. On narrow project pages, keep `Sprint`, `Planned`, and `All` visible and move `Context` and `About` into the project overflow menu instead of wrapping or scrolling the tab bar. `Changelog` always lives in project overflow.

## User Identity

- Render profile images and initials fallbacks as circles everywhere. The shared `user-avatar` component owns the crop shape so individual screens cannot diverge.
- Keep profile and project image selection, upload, and removal in the shared image-picker modal. Owning panels show only the current image and a compact Add/Change action.
- Identify the signed-in account as `@username` in the profile overlay instead of showing a generic role label such as `Member` or `Admin`.

## Controls

- Every icon-only action needs a concise, action-oriented `aria-label` and the shared app tooltip on pointer hover and keyboard focus. Do not show redundant tooltips while equivalent text is visibly rendered, and do not rely on native `title` tooltips for interactive controls.

## Issue Detail

- Keep the issue-title edit action attached to the title's final character at every viewport width. Long titles may wrap before that final character-and-action unit, but the action must never become an orphaned line by itself.

## Project View

- Render project images as squares with a small corner radius everywhere, using the shared project icon component and a project-initial fallback. Keep them visually distinct from circular user avatars.
- Treat the project page as a focused planning console, not a place to introduce new workflow controls by default.
- Prefer the stronger hierarchy of the issue detail page: clear title card, compact metadata, purposeful cards, and restrained section language.
- Keep the project header cohesive. Project identity, actions, and tabs should feel like one unit, with the tab bar close to the project title and flush to the bottom of the header.
- Keep `Deleted issues` in the project actions menu, not in the primary tab bar.
- Keep `Sprint history` in the project actions menu immediately above `Changelog`. It is a read-only, newest-first list of completed sprints with compact schedule/completion metadata and explicit pagination. Each row shows frozen `Done` and `Cancelled` issue counts plus a compact Markdown description preview with its own `See more` expansion. Retain a separate sprint disclosure control that lazy-loads the complete, paginated issue membership captured atomically when that sprint completed.
- Open project member management from the project actions menu as a full project page with its own URL, not as a modal.
- Keep `Delete project` at the bottom of the project actions menu, below a divider and in destructive colour. It opens a confirmation dialog that names the consequences and requires the project key to be typed. Only the project owner and site admins see it. The same owner-or-admin rule governs `DELETE /projects/{key}` and `track_delete_project`, so no surface can delete a project the others would refuse.
- Use the wide-layout project tabs `Sprint`, `Planned`, `All`, `Context`, and `About`. `Sprint` is singular; use a human/running-style Lucide icon when available. Below `lg`, show only the first three as tabs and expose `Context` and `About` from project actions. Keep `Changelog` in project overflow at every breakpoint.
- Show assignee filters only where they apply. Do not preserve or display assignee filters on `About`.
- The `All` tab is the triage and discovery surface. It should feel dense and scan-friendly, with all current, past, completed, planned, and unplanned issues available through one list.
- Flat issue lists — project `All` and both `Me` views — default to open work (`To do` and `In progress`). A list of everything ever filed answers "what is on this project" worse than a list of what is left. Completed and cancelled work stays one click away behind the `Any` status filter, and `?status=any` is the URL that says so. The sprint board is the exception: its columns are the statuses, so it keeps all of them.
- Keep `All` page controls in one coherent section. Avoid loose chip clusters; group filters in aligned rows and separate sort controls visually while keeping them in the same control shell.
- For filters, support multi-select where it helps scanning. Statuses, priorities, and assignees use OR semantics within each group, while different groups combine together.
- Put project tag management in the project About details sidebar, parallel to issue tag management. Keep it out of the project overflow menu.
- Show project access settings at a glance at the top of the About sidebar in an `Access` card: the `project-visibility-badge` with a one-line explanation, then `Issue creation` as `Members only` or `Any signed-in user`. The card is read-only for everyone; owners and admins get a compact settings action that opens the members page, where the settings are edited.
- Keep visual changes layout-focused unless the user explicitly asks for new creation, editing, drag/drop, or planning workflow controls.

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

## Tag IA

- Treat issue tags as lightweight issue-detail metadata. Show them near the issue title, not buried in the Details sidebar.
- Use a modal for issue-scoped tag attach/detach from issue detail. The modal should preserve the user's place on the issue, avoid URL pushes, support quick repeated changes, and expose search over existing project tags.
- Keep broader tag taxonomy work in the project tag manager. Issue tag modals should link out to that manager for create/edit/delete rather than becoming a second fullscreen management surface.
- Ticket numbers are identifiers, not prose. Render issue identifiers with the shared compact monospace `issue-key` treatment, including modal/header contexts.
