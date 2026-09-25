# Description Attachments

Description attachments connect project, issue, and sprint descriptions to project object storage. Markdown source remains in `projects.description`, `issues.description`, or `sprints.goal`; files live in object storage and are linked through `project_attachments`, `issue_attachments`, or `sprint_attachments`.

Project context pages use the same storage contract through `context_attachments`. Markdown remains in `project_context.body`; each attachment row stores `project_id`, `context_id`, `storage_object_id`, and audit/realtime fields. A storage object may belong to only one description or context attachment flow.

## Data Model

`issue_attachments` is a link/audit table. It stores:

- `project_id`: denormalized for same-project foreign keys and realtime routing.
- `issue_id`: live issue being annotated.
- `storage_object_id`: referenced object metadata row.
- `created_by_id`, `created_at`, `updated_at`, `version`: audit and realtime fields.

Do not copy filename, content type, size, hash, backend, bucket, or object key into `issue_attachments`. Those values belong only to `storage_objects`.

`sprint_attachments` has the same shape, replacing `issue_id` with `sprint_id`. The issue and sprint tables enforce same-project parent/object references. Their application adapters share storage lifecycle, Markdown rendering, pagination, and UI behavior while keeping parent-specific constraints explicit.

`project_attachments` uses the same object and audit fields, with `project_id` serving as both the owner scope and description parent. Its adapter shares the same storage lifecycle, Markdown rendering, pagination, and UI behavior.

## Upload Flow

Project, issue, and sprint attachment uploads use multipart field `file`.

1. Server writes bytes to configured object storage backend.
2. Server inserts one `storage_objects` metadata row.
3. Server inserts one parent-specific attachment link to that object.
4. If link creation fails, the server soft-deletes the metadata row, transactionally queues backend deletion, and attempts immediate backend cleanup.

Objects attached through a description upload path are owned by that attachment flow. Removing the attachment removes the link, soft-deletes the object metadata row, and queues backend deletion in the same transaction. A background worker retries backend failures; the delete response reflects the committed logical removal.

## Markdown Resolution

Project, issue, and sprint description Markdown may reference attached files by project-local object ref:

```markdown
![Screenshot](object-3)
[Log file](object-4)
```

Rendering resolves `object-N` only against attachments on the current project, issue, or sprint description. Missing, unattached, and cross-parent object refs render inert text instead of links or images.

Project whiteboard pages render through the same Markdown pipeline but have no attachments, so every `object-N` ref in a whiteboard page renders as inert text and external images follow the rule below.

Issue comments render through the same Markdown pipeline as the issue description. `object-N` refs in a comment resolve against that issue's attachments; comments have no attachment store of their own.

External Markdown image URLs do not render inline. They become inert, no-referrer links using their alt text, for example:

```markdown
![](https://news.ycombinator.com/y18.svg)
```

This preserves an explicit path to the external resource without contacting it when a description is viewed. Attached safe images and single-slash same-origin image paths continue to render inline. Protocol-relative destinations such as `//tracker.example/pixel.png` are external and never render as images.

The shared project/issue/sprint UI shows attachments below the description editor or expanded view. Safe images include a compact preview, and every row has a copy action for the Markdown snippet. Active, planned, and completed sprint-history views start with a vertically cropped, attachment-scoped Markdown preview and load full Markdown plus attachment rows through “See more”. Completed sprint-history attachment rows remain read-only.

Safe image content types render inline through the issue attachment content route:

- `image/png`
- `image/jpeg`
- `image/gif`
- `image/webp`
- `image/avif`
- `image/bmp`

SVG is not rendered inline. Non-image attachments render as download links.

## Routes

Project readers, including readonly members and anonymous readers of public projects, can use the `GET` routes below. Owners, admins, and members with write access can also use the `POST` and `DELETE` routes. A project-level user block overrides public read access for that signed-in account:

- `POST /api/v1/{owner}/projects/{key}/attachments`
- `GET /api/v1/{owner}/projects/{key}/attachments`
- `GET /api/v1/{owner}/projects/{key}/attachments/{objectRef}/content`
- `DELETE /api/v1/{owner}/projects/{key}/attachments/{objectRef}`

Project context page routes are nested under the page:

- `POST /api/v1/{owner}/projects/{key}/context/{contextRef}/attachments`
- `GET /api/v1/{owner}/projects/{key}/context/{contextRef}/attachments`
- `GET /api/v1/{owner}/projects/{key}/context/{contextRef}/attachments/{objectRef}/content`
- `DELETE /api/v1/{owner}/projects/{key}/context/{contextRef}/attachments/{objectRef}`

Issue routes:

- `POST /api/v1/{owner}/issues/{issueRef}/attachments`
- `GET /api/v1/{owner}/issues/{issueRef}/attachments`
- `GET /api/v1/{owner}/issues/{issueRef}/attachments/{objectRef}/content`
- `DELETE /api/v1/{owner}/issues/{issueRef}/attachments/{objectRef}`

Sprint equivalents are nested under the project sprint:

- `POST /api/v1/{owner}/projects/{key}/sprints/{sprintRef}/attachments`
- `GET /api/v1/{owner}/projects/{key}/sprints/{sprintRef}/attachments`
- `GET /api/v1/{owner}/projects/{key}/sprints/{sprintRef}/attachments/{objectRef}/content`
- `DELETE /api/v1/{owner}/projects/{key}/sprints/{sprintRef}/attachments/{objectRef}`

The UI has cookie-authenticated equivalents under:

- `/{owner}/projects/{key}/attachments`
- `/{owner}/projects/{key}/attachments/{objectRef}/content`
- `/{owner}/projects/{key}/attachments/{objectRef}/delete`
- `DELETE /{owner}/projects/{key}/attachments/{objectRef}`

Issue equivalents:

- `/{owner}/issues/{issueRef}/attachments`
- `/{owner}/issues/{issueRef}/attachments/{objectRef}/content`
- `/{owner}/issues/{issueRef}/attachments/{objectRef}/delete`
- `DELETE /{owner}/issues/{issueRef}/attachments/{objectRef}`

Sprint UI equivalents use `/{owner}/projects/{key}/sprints/{sprintRef}/attachments/...` with the same content, delete, and JSON-delete suffixes.

Content responses stream bytes from the backend with stored content type, content length, SHA-256 ETag, content disposition, and `X-Content-Type-Options: nosniff`.

## Security Constraints

- Raw HTML in Markdown remains disabled.
- Markdown links allow safe external URL schemes only, plus same-origin absolute paths and fragments.
- Markdown images render inline only for attached safe images or single-slash same-origin absolute paths. External `http`/`https` and protocol-relative destinations render as no-referrer links instead of images. Unsafe schemes such as `javascript:` and `data:` remain inert.
- `object-N` refs never cross project, issue, or sprint description boundaries.
- Context-page `object-N` refs resolve only against attachments on that context page, including when the page is viewed from an issue.
- Inline rendering is limited to safe image content types; downloads use attachment disposition.
