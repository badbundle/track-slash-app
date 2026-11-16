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

When a repeating issue's next repetition is created (TRACK-103), each attachment the completed repetition's description shows is copied to the new issue: a new object and link that share the original's bytes (see `STORAGE.md`). "Shows" means what rendering resolves: an `object-N` that is a whole link or image destination, with or without a title, or a reference definition. Those refs are rewritten to the copies' numbers. Prose, URLs and code that merely contain `object-N` are left alone. Attachments the description doesn't show aren't copied, and neither are deleted ones, one being deleted at that moment, or any copy that would take the description over its length limit. Deleting a copy or the original removes only that object; the bytes go with the last one.

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

## Form attachments

A form (see `SECURITY_MODEL.md`) can carry files: images, audio, video or anything else. They are shown with a question, with one of its options, or with the form as a whole. Unlike the attachments above they have no description Markdown, and they are for the project's members only.

`form_attachments` has the same shape as `issue_attachments` with `form_id` in place of `issue_id` (no `version` or `updated_at`, because forms have no realtime events). It stores `project_id`, `form_id`, `storage_object_id` and `created_by_id`, one row per storage object, and its rows go when the form or the project does. Object metadata stays in `storage_objects`.

The file is uploaded before the form exists, so it goes through three steps:

1. `POST /api/v1/{owner}/projects/{key}/forms/upload-urls` (or `track_create_form_upload_url`) makes an upload URL: `{"upload_url","expires_at","max_files","max_file_bytes"}`. It needs write access.
2. `curl -sS -F file=@PATH "$upload_url"` uploads one file and returns `{"object":"object-N","filename","content_type","byte_size"}`. The URL is `/api/v1/form-uploads/{key}`. It takes no account credentials: the key is the credential. Members with write access can also upload with their token to `POST /api/v1/{owner}/projects/{key}/forms/uploads`. The file is a pending upload (`form_uploads`) of the person who made the URL.
3. `POST .../forms` (or `track_create_form`) names pending uploads in a question's `media` (up to 6), an option's `media` (one) and the form's `attachments`. Creating the form claims them: each moves from `form_uploads` to `form_attachments`, in the transaction that creates the form. A form takes at most 20 distinct files, counting all three places, and a file named twice counts once. A ref that is not one of the creator's pending uploads in this project (someone else's, already on another form, deleted, a generic object, an issue attachment or missing) fails the whole create with a 400 that names it, and every upload stays pending.

A storage object belongs to at most one attachment flow. A pending upload is a flow of its own until a form claims it.

Routes that serve a form's files, both for members only (a reader who is not a member gets 404):

- `GET /api/v1/{owner}/projects/{key}/forms/{formRef}/attachments/{objectRef}/content`
- `GET /{owner}/projects/{key}/forms/{formRef}/attachments/{objectRef}/content` (cookie session)

`object-N` in a form resolves only against that form's attachments: a file on another form, a pending upload or a missing one is 404. Content is served by `streamFormMediaContent`, used by these two routes only, with `http.ServeContent`: `Accept-Ranges: bytes`, `206` with `Content-Range`, `416` for an unsatisfiable range, and `If-Range` and `If-None-Match` honoured. A backend body that can seek is used directly; otherwise the object is read into memory first (form media is capped at 25 MiB). A safe image and these audio and video types are served inline with their own type: `audio/mpeg`, `audio/mp4`, `audio/x-m4a`, `audio/aac`, `audio/wav`, `audio/x-wav`, `audio/ogg`, `audio/webm`, `video/mp4`, `video/quicktime`, `video/webm`. That is safe because audio and video are passive, and the response keeps `nosniff`, the `default-src 'none'; sandbox` policy, `Cross-Origin-Resource-Policy: same-origin` and the SHA-256 `ETag`, for members only. Any other type is an octet-stream download in a sandbox, whatever was declared. Every other content route (issue, project, sprint, context, generic object, images) is unchanged: no ranges and no inline audio or video.

A form upload that declares no type, or `application/octet-stream` (what `curl -F file=@clip.m4a` sends), takes its type from the extension: `.mp3` audio/mpeg, `.m4a` audio/mp4, `.aac` audio/aac, `.wav` audio/wav, `.ogg`, `.oga` and `.opus` audio/ogg, `.mp4` and `.m4v` video/mp4, `.mov` video/quicktime, `.webm` video/webm. Any other extension is sniffed as before, and a type the uploader declares is kept. `kind` follows what is served: audio or video only for the types above, so audio/flac is a `file`.

On the form page (a member's view) a question's `media` sits between its help and its inputs, an option's `media` inside its card, `object-N` in Markdown (description, section description, help) resolves against the form's files, and files nothing places are listed under the description.

A form response lists its files in `attachments`: `object`, `filename`, `content_type`, `byte_size`, `kind` (`image`, `audio`, `video` or `file`) and `url`, the content route above, absolute when the public origin is set. A list of forms carries none.

Limits are settings in `server.Options.FormMedia`: the largest file (25 MiB, and never more than the storage limit), how long an upload URL works (30 minutes), and how many files it takes (20).

## Security Constraints

- Raw HTML in Markdown remains disabled.
- Markdown links allow safe external URL schemes only, plus same-origin absolute paths and fragments.
- Markdown images render inline only for attached safe images or single-slash same-origin absolute paths. External `http`/`https` and protocol-relative destinations render as no-referrer links instead of images. Unsafe schemes such as `javascript:` and `data:` remain inert.
- `object-N` refs never cross project, issue, or sprint description boundaries.
- Context-page `object-N` refs resolve only against attachments on that context page, including when the page is viewed from an issue.
- Inline rendering is limited to safe image content types; downloads use attachment disposition.
- Form files are never listed, fetched or deleted by the generic object routes, and no Markdown resolves an `object-N` against them.
