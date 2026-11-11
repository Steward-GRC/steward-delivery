# Error codes

Every coded gRPC error from the delivery service carries an `ErrorInfo` with the symbol as
its reason, the domain `delivery` and the code in `codeNum`. Only user-safe messages reach
the caller; every other code is sent as `Code N: Internal Error`. Delivery owns 8000 to 8099.

| Code | Symbol | Area | Cause | User-safe |
| --- | --- | --- | --- | --- |
| 8000 | `INTERNAL` | delivery | an uncoded failure inside the delivery service | no |
| 8001 | `STORE_UNAVAILABLE` | delivery store | a delivery Postgres read or write failed; the op metadata names it, the cause is only logged | no |
| 8002 | `CORE_UNAVAILABLE` | core | a call to steward-core failed; the op metadata names it, the cause is only logged | no |
| 8003 | `POLICY_VERSION_NOT_FOUND` | policy version | core has no policy version with that id | yes |
| 8004 | `MAGIC_LINK_NOT_FOUND` | magic link | no magic link has that token | yes |
| 8005 | `MAGIC_LINK_EXPIRED` | magic link | the magic link is past its expiry | yes |
| 8006 | `MAGIC_LINK_REVOKED` | magic link | the magic link was revoked | yes |
| 8007 | `VIEWER_EMAIL_REQUIRED` | magic link | a sensitive magic link was opened without the viewer's email address | yes |
| 8008 | `PDF_EXPORT_DISABLED` | PDF export | PDF export is off, or the service has no Kubernetes API to create the render on | yes |
| 8009 | `PDF_EXPORT_NOT_FOUND` | PDF export | no PDF export job has that id, or the caller did not start it | yes |
| 8010 | `PDF_EXPORT_NOT_READY` | PDF export | the PDF is still rendering | yes |
| 8011 | `PDF_EXPORT_FAILED` | PDF export | the renderer reported a failure; the job row holds its message | yes |
| 8012 | `INVALID_REQUEST` | request | a required request field is empty | yes |
