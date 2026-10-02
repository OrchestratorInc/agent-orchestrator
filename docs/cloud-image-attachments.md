# Cloud image attachments

Cloud tasks, chat, steering and agent terminals use attachment IDs. Image bytes
never pass through the control-plane JSON client or Electron IPC. Local daemon
attachments keep their existing storage and limits.

Cloud accepts PNG, JPEG, WebP, GIF and BMP. Each submission allows 8 images,
10 MiB per image and 25 MiB total. Verification checks the decoded format, size
and SHA-256 checksum, and rejects images exceeding 25 million decoded pixels.
SVG, PDF and non-image files are not accepted.

## Storage configuration

Development control planes default to filesystem storage under
`$AO_DATA_DIR/cloud/attachments`, or `~/.ao/cloud/attachments` when unset.
`AO_CLOUD_ATTACHMENT_STORAGE=filesystem` is refused outside development,
local and test environments. Signed HTTP upload and read grants let workers
exercise downloads without a shared filesystem mount. The persisted signing
key must stay private. The local Docker scripts mount this directory under the
AO data directory and run the development control plane as the invoking user.

Hosted control planes leave images disabled unless storage is configured.
To use S3, set:

```text
AO_CLOUD_ATTACHMENT_STORAGE=s3
AO_CLOUD_ATTACHMENT_S3_BUCKET=<private bucket>
AO_CLOUD_ATTACHMENT_S3_REGION=<bucket region>
```

The adapter uses the AWS default credential chain. Deploy it with an IAM role,
not embedded access keys. The role needs object read, write and delete access
for attachment keys. Workers get session-scoped temporary read URLs, not AWS
credentials. Provision the bucket separately with blocked public access,
encryption and POST CORS limited to the desktop renderer origins. POST grants
restrict the key, exact object size, MIME type, checksum and encryption fields.
See the [S3 POST policy contract](https://docs.aws.amazon.com/AmazonS3/latest/API/sigv4-HTTPPOSTConstructPolicy.html).

The desktop build must set `AO_CLOUD_IMAGE_UPLOAD_ORIGINS` to a comma-separated
list of exact HTTPS storage origins. These origins enter `connect-src`; arbitrary
HTTPS upload endpoints are not allowed. This is build-time configuration,
separate from the runtime bucket configuration.

## Authorization and lifecycle

Prepare uploads under `/api/cloud/v1/orgs/{orgId}/attachments`. Before task
creation, the record belongs to its creator and project. Completion verifies
the temporary object and writes those exact bytes to a canonical key that
client grants cannot overwrite. Task and message transactions link only ready
attachments and enforce organization and session access.

Authorized parent/child forwarding adds destination message references while
keeping the source session binding. Destination readers and workers use those
references for preview and restore access. Completion and cleanup hold the same
metadata row lock across storage writes and deletions.

Upload grants last 10 minutes. Read grants last 5 minutes. Unreferenced records
expire after 24 hours. The cleanup loop removes expired objects and temporary
upload objects after the last upload grant expires. Referenced images survive
archive and sandbox replacement. Storage lifecycle rules for `upload-*` keys
provide an additional cleanup backstop. Never log signed URLs or image contents.
Expired preparations cannot renew their grants. Grant issuance holds the metadata
row lock and records the returned expiry before the grant reaches the client.

A worker advertises `attachments.images.v1`. Older workers reject or hold image
work instead of accepting text-only delivery. Workers restore the retained
manifest after checkout and rehydration, before workspace-ready acknowledgement.
Downloads use generated `.ao/attachments/image-<id>.<extension>` paths, bounded
verification and confined atomic writes. Git exclusions are installed first;
checkpoint staging also excludes attachments even if the agent force-staged one.

ACP uses native image blocks when negotiated and otherwise explicitly asks the
agent to read each image with its image-reading tool. Native ACP converts BMP to
PNG without changing the stored image. Codex uses native `localImage` input.
Terminal attachments wait for a current-worker acknowledgement, insert quoted
paths and do not press Enter. Ordinary text paste is unchanged.
Preparation permits worker downloads with a 10-minute lease. A valid path
acknowledgement from the current worker promotes the images to durable retention.
Failed, cancelled and stale-worker preparations stay unretained and expire.

## Development verification and production gate

Focused tests cover signed HTTP uploads, invalid bytes, expired grants, S3 POST
policy construction, transactional links, isolation, image-only submissions,
idempotency, worker downloads, restore, Git exclusions, native agent inputs,
composer retries, ID-only durable drafts and terminal fencing. Account-scoped
attachment caches clear on sign-out.
Failed uploads keep their source File in shared renderer memory across composer
remounts, including failures before prepare succeeds. Durable drafts omit bytes.

`frontend/e2e/performance/cloud-images.html` is a browser verification fixture
with real shared UI and mocked control-plane/storage responses. It is not a
live sandbox or AWS test. Its Playwright test checks image IDs and unsubmitted
terminal paths.

Before enabling production storage, rebuild and deploy the managed worker,
then verify against real AWS: IAM restrictions, POST signatures, exact size and
checksum rejection, renderer CORS, canonical-key protection, grant expiry,
worker downloads and replacement restore. Run live Claude/Cursor/Codex turns,
including image-only prompts and every accepted format. Development tests do
not replace this release gate.
