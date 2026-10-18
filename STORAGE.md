# Object Storage

track-slash stores object metadata in Postgres and object bytes in a configured storage backend. This keeps the API and access-control model in the database while allowing local disk today and bucket-style backends later.

## Current Backends

V1 supports `local` and S3-compatible storage backends.

Local runtime config, with defaults:

```bash
TRACK_SLASH_STORAGE_BACKEND=local
TRACK_SLASH_STORAGE_LOCAL_ROOT=data/storage
TRACK_SLASH_STORAGE_BUCKET=local
TRACK_SLASH_STORAGE_MAX_UPLOAD_BYTES=52428800
```

`TRACK_SLASH_STORAGE_LOCAL_ROOT` is a directory on the machine running `trackd`. The default, `data/storage`, is project-relative and gitignored; it is deliberately separate from Air's disposable `tmp/air` build directory, so files survive `make run` restarts. Set it to an absolute path or another relative path to keep local assets elsewhere. Production deployments should set an explicit durable path.

When upgrading from the former `tmp/storage` default, `trackd` moves that directory to `data/storage` at startup when the new default is in use. If both directories already exist, startup stops rather than merging or overwriting files; move the files manually before restarting. Assets previously deleted from `tmp/storage` cannot be recovered.

S3-compatible runtime config:

```bash
TRACK_SLASH_STORAGE_BACKEND=s3
TRACK_SLASH_STORAGE_BUCKET=track-slash
TRACK_SLASH_STORAGE_S3_ENDPOINT=http://garage:3900
TRACK_SLASH_STORAGE_S3_REGION=us-east-1
TRACK_SLASH_STORAGE_S3_FORCE_PATH_STYLE=true
TRACK_SLASH_STORAGE_MAX_UPLOAD_BYTES=52428800
AWS_ACCESS_KEY_ID=...
AWS_SECRET_ACCESS_KEY=...
AWS_SESSION_TOKEN=... # optional
```

`TRACK_SLASH_STORAGE_S3_REGION` defaults to `us-east-1`; for S3-compatible services such as Garage this is primarily the SigV4 signing region. For AWS S3, set the real bucket region. `TRACK_SLASH_STORAGE_S3_FORCE_PATH_STYLE` defaults to `true` for compatibility with Garage and similar services. The app does not create buckets or access keys; create them in the storage service first. Garage's admin/API endpoint is not used by `trackd`; only the S3 endpoint is required.

Google Cloud Storage can be used through the same S3-compatible backend by using the Cloud Storage XML API and GCS HMAC keys:

```bash
TRACK_SLASH_STORAGE_BACKEND=s3
TRACK_SLASH_STORAGE_BUCKET=track-slash-main
TRACK_SLASH_STORAGE_S3_ENDPOINT=https://storage.googleapis.com
TRACK_SLASH_STORAGE_S3_REGION=auto
TRACK_SLASH_STORAGE_S3_FORCE_PATH_STYLE=true
TRACK_SLASH_STORAGE_MAX_UPLOAD_BYTES=52428800

AWS_ACCESS_KEY_ID=...        # GCS HMAC access ID
AWS_SECRET_ACCESS_KEY=...    # GCS HMAC secret
AWS_REQUEST_CHECKSUM_CALCULATION=when_required
AWS_RESPONSE_CHECKSUM_VALIDATION=when_required
```

GCS HMAC keys are separate from Google service account JSON credentials. The current `s3` backend does not use `GOOGLE_APPLICATION_CREDENTIALS`; it signs S3/XML API requests with the `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` environment variables. Keep the GCS bucket private. Downloads continue to flow through authenticated track-slash routes, so browser-facing signed URLs and GCS CORS rules are not required.

When the configured endpoint is `storage.googleapis.com`, the S3 backend automatically applies the GCS XML API interoperability rules: `Accept-Encoding` is excluded from SigV4 because Google's frontend rewrites it in transit, and S3's `If-None-Match: *` precondition is omitted from `PUT` requests because GCS rejects mixing AWS-signed `x-amz-*` headers with its `x-goog-*` generation precondition. Object keys are UUID-derived, so collisions remain negligibly unlikely. Other S3-compatible backends retain the create-only precondition.

For failed S3-compatible requests, the backend adds bounded diagnostics to the internal server error log: operation, HTTP request shape, status, API code, request IDs, SigV4 credential scope and signed-header names, and the GCS `Details`, `CanonicalRequest`, and `StringToSign` fields when present. Authorization values, access IDs, signatures, session tokens, cookies, and customer encryption keys are not logged. Error response capture is limited to 64 KiB and is never returned to API clients.

## Data Model

`storage_objects` is the metadata table. Each row belongs to exactly one owner scope:

- Project-owned objects have `project_id`, no `owner_user_id`, and a project-local public ref such as `object-1`.
- User-owned objects have `owner_user_id`, no `project_id`, and no public `object-N` ref.

Important fields:

- `project_id`, `owner_user_id`, `number`: exactly one owner scope plus project-local numbering for project objects.
- `backend`, `bucket`, `object_key`: backend locator. Project keys look like `projects/{project_id}/objects/{object_id}`. User profile image keys look like `users/{user_id}/profile-images/{object_id}/{variant}`.
- `filename`, `content_type`, `byte_size`, `sha256`: download metadata and integrity metadata.
- `created_by_id`, timestamps, `deleted_at`: audit and soft-delete state.
- `copied_from_id`: set on a copy, the row it was copied from (see below).

Each upload has a backend key of its own. Copies are the one exception: when a repeating issue's next repetition is created, each attachment its description refers to is copied as a new row, with its own `object-N` and attachment link, that shares the original's backend key and bytes. Nothing is copied in the backend, so a completion never does storage I/O. The unique index on `(backend, bucket, object_key)` covers rows that aren't copies. While the copy is made, the originals are held `FOR SHARE SKIP LOCKED`: an original can't be deleted until the copy is committed, and one already being deleted is passed over rather than waited on.

The storage_object_deletions table is the durable backend-deletion queue. A Postgres trigger inserts a pending job in the same transaction whenever a live storage_objects row becomes soft-deleted. Jobs copy the backend, bucket, and object key needed after the object disappears from normal reads. The migration also backfills jobs for objects that were already soft-deleted.

Project object rows are listed and fetched only when both the project and object are live. Project soft-delete makes objects inaccessible but does not physically remove backend bytes. User-owned rows are fetched only through feature-specific routes, not project object routes.

Profile images are account-wide user-owned objects. Each user can reference one original image row in `users.profile_image_object_id` and one generated thumbnail row in `users.profile_image_thumbnail_object_id`. Both rows must be live `storage_objects` rows owned by the same user.

Project images are project-owned objects referenced by `projects.image_object_id` and `projects.image_thumbnail_object_id`. Both rows must be live objects owned by that project. Although they retain internal project object numbers, current project-image rows are excluded from generic `object-N` listing, metadata, content, attachment, and delete flows; they are available only through the project-image routes.

## API

Project readers, including anonymous readers of public projects, can list object metadata and download object content. Owners, admins, and members with write access can also upload and delete objects. A project-level user block overrides public read access for that signed-in account:

- `POST /api/v1/{owner}/projects/{key}/objects` with multipart field `file`.
- `GET /api/v1/{owner}/projects/{key}/objects`.
- `GET /api/v1/{owner}/projects/{key}/objects/{objectRef}`.
- `GET /api/v1/{owner}/projects/{key}/objects/{objectRef}/content`.
- `DELETE /api/v1/{owner}/projects/{key}/objects/{objectRef}`.

Project readers can fetch the current project image. Project writers can replace or remove it:

- `POST /api/v1/{owner}/projects/{key}/image` with multipart field `file`.
- `DELETE /api/v1/{owner}/projects/{key}/image`.
- `GET /api/v1/{owner}/projects/{key}/image/content`.
- `GET /api/v1/{owner}/projects/{key}/image/thumbnail/content`.

Downloads stream backend bytes and set `Content-Type`, `Content-Length`, `ETag` from SHA-256, and `Content-Disposition` with the stored filename. They have no overall request deadline; one stops only when a backend read or a write to the client makes no progress for 30 seconds.

Signed-in users can use profile image routes:

- `POST /api/v1/me/profile-image` with multipart field `file`.
- `DELETE /api/v1/me/profile-image`.
- `GET /api/v1/users/{id}/profile-image/content`.
- `GET /api/v1/users/{id}/profile-image/thumbnail/content`.

Any authenticated user may read any live user's profile image content. Profile image objects are not exposed through project `object-N` listing, metadata, content, or delete routes.

## Write And Delete Flow

Uploads write bytes to the backend first, then insert metadata in Postgres. If the metadata insert fails, the server deletes the just-written backend object. When metadata exists but a later attachment-link transaction fails, cleanup soft-deletes the object, transactionally queues backend deletion, and also attempts immediate removal.

Deletes soft-delete the Postgres row and enqueue storage_object_deletions work in the same transaction. This avoids live metadata pointing at missing bytes and preserves a retry path across request failures, process crashes, and restarts. HTTP and MCP delete responses describe the committed logical deletion; they do not fail merely because the immediate best-effort backend removal failed.

Backend bytes are deleted only once no live row uses them, because a copy may share them. The deletion worker checks before each backend delete and completes the job without deleting while another live row has the key, counting it as `Kept`. The immediate best-effort deletes after a soft-delete (`deleteRemovedStorageBytes`) make the same check, and leave the bytes to the worker if the check fails. Bytes no row was ever written for, such as an upload whose metadata insert failed, are deleted directly, because nothing can share them. No new row can start using a key once the check finds none, because a copy is only made from a live row.

Every running trackd binary processes due jobs. Workers use leased FOR UPDATE SKIP LOCKED claims, so multiple replicas can cooperate without intentionally processing the same live lease. Missing backend bytes count as success. Transient failures retry up to eight attempts with exponential backoff starting at five seconds and capped at five minutes. A processing lease becomes reclaimable after one minute, and each backend delete has a ten-second timeout.

After the final attempt, the job remains queryable with status failed, its bounded error text and failure timestamp are persisted, and trackd logs a terminal failure. Operators can inspect failures with:

    SELECT storage_object_id, backend, bucket, object_key, attempt_count, last_error, failed_at
    FROM storage_object_deletions
    WHERE status = 'failed'
    ORDER BY failed_at;

After correcting the backend or configuration problem, requeue selected failures with:

    UPDATE storage_object_deletions
    SET status = 'pending', next_attempt_at = now(), locked_at = NULL, failed_at = NULL, updated_at = now()
    WHERE status = 'failed'
      AND storage_object_id = '<object UUID>';

Profile image replacement writes the original and generated thumbnail to the backend first, creates user-owned metadata rows, then updates the user profile pointers in one transaction. Previous profile image rows are soft-deleted and queued in that transaction. Removing a profile image clears both user pointers and queues both old objects the same way.

Project image replacement follows the same lifecycle with project-owned metadata rows and transactional project pointers. Project image keys use `projects/{project_id}/images/{object_id}/{variant}`. Replacement and removal soft-delete and queue the previous pair transactionally.

The server accepts decodable PNG, JPEG, GIF, WebP, and BMP profile and project-image uploads. SVG, AVIF, non-images, corrupt images, oversized files, and unreasonable dimensions are rejected before image metadata is linked. The generated thumbnail is a centered square `128x128` PNG; animated formats use the decoded first frame. User images render as circles; project images retain the square crop with a small corner radius.

## Remote Backend Path

Remote backends implement `internal/storage.Backend` and keep the same metadata table:

- `backend`: stable backend name such as `s3`.
- `bucket`: remote bucket/container name.
- `object_key`: opaque backend key generated by the storage service, not user input.

Do not store raw object bytes in Postgres. Product features such as project/issue/sprint description attachments or image fields should reference `storage_objects.id` from their own tables.

Project, issue, sprint description, and project-context page attachments are documented in `ATTACHMENTS.md`. Attachment links store only parent/object/audit data; object metadata such as filename, content type, byte size, hash, backend, bucket, and object key remains in `storage_objects`.
