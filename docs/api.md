# API

The API is `steward.delivery.v1`, in [`proto/steward/delivery/v1`](../proto/steward/delivery/v1),
with the Go stubs committed in `gen/go`. Access to every RPC is enforced at the gateway; delivery
records the user ids a request names. Errors carry an `ErrorInfo` ([error codes](error-codes.md)).

| RPC | Does |
| --- | --- |
| `GetRenderedContent` | Renders a version's content to HTML (the rendering contract in `rendering.proto` and `internal/render`). |
| `GetDiff` | Returns core's section diff between two versions. |
| `CreateMagicLink` | Issues a link to a version: 30 days, or 48 hours for a sensitive one (configurable). |
| `RevokeMagicLink` | Ends a link at once; revoking twice succeeds. |
| `ResolveMagicLink` | Returns the version a live link opens. A sensitive link needs `viewer_email` and always requires the watermark. Expired, revoked and unknown links each have their own code. |
| `RequestPDFExport` | Records a pending job and creates its [`PdfRender`](pdf-render.md); returns the job id. `PDF_EXPORT_DISABLED` while export is off. |
| `GetPDFDownloadLink` | A presigned download URL for a done job (15 minutes by default). `PDF_EXPORT_NOT_READY` while it renders, `PDF_EXPORT_FAILED` if the renderer failed. |

The export watermark follows the policy, never the caller: a version is exported as sensitive if
its content says so or its policy is classified sensitive in core.

The server also serves `grpc.health.v1` and reflection. On `grpc.health.v1`, `liveness` reports the
process only, and the empty name and `readiness` fail while Postgres, RabbitMQ or core is down.
Every `Health/Check` answer carries the `steward-version`, `steward-commit`, `steward-dep-<name>`
and `steward-depstate-<name>` headers. `/livez` and `/readyz` serve the same over HTTP on
`PROBE_PORT`; see the [runbook](runbook.md#probes).

## Internal HTTP

`GET /internal/policies/{versionId}/html` on `INTERNAL_HTTP_PORT` returns the rendered version
with its appendices, for the renderer job to print. It has no authentication of its own: allow
only the renderer's pods to reach that port (a NetworkPolicy). An unknown version answers 404, so
the renderer fails the job instead of retrying.

## Events out

**Audit.** Magic links publish a `steward.audit.v1.AuditEvent` (steward-audit's contract) to the
`audit` topic exchange, as protobuf binary with the content type
`application/protobuf; proto=steward.audit.v1.AuditEvent`:

| Action | Tier (routing key) | Subject | Attributes |
| --- | --- | --- | --- |
| `magic_link.created` | `audit` | `policy_version:<id>` | `link`, `sensitive` |
| `magic_link.accessed` | `activity` | `policy_version:<id>` | `link`, `viewer_email`, `sensitive` |
| `magic_link.revoked` | `audit` | `magic_link:<link>` | none |

`link` is the token's fingerprint (the first 16 hex characters of its SHA-256). An event never
carries the token itself.

## Calling other services

Delivery never imports another service's Go module. It pins the protos it uses in
`proto-refs.env` and `scripts/proto-generate.sh` generates them into `gen/go/thirdparty`:

| Service | Pin | Used for |
| --- | --- | --- |
| steward-core | `STEWARD_CORE_REF` | `PolicyService.GetPolicyVersion`, `GetPolicy`, `DiffVersions`; `AppendixService.ListAppendices` |
| steward-audit | `STEWARD_AUDIT_REF` | the `AuditEvent` message it publishes |

To try an unmerged proto change, point `STEWARD_CORE_PROTO_DIR` or `STEWARD_AUDIT_PROTO_DIR` at a
local `proto/` directory and run `task proto`. Bump a pin and commit the regenerated `gen/` in the
same change.
