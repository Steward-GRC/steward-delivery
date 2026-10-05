# PdfRender

A PDF export is a `PdfRender` resource. Delivery creates it; steward-pdf-renderer owns the CRD,
runs one job per resource with headless Chromium, and reports back in its status. This page is the
contract between the two; `internal/pdfrender` pins it in tests.

- **API group and version:** `renders.steward-grc.com/v1alpha1`, kind `PdfRender`, resource
  `pdfrenders`, namespaced (`POD_NAMESPACE`).
- **Name:** the job id, `pdf-<version id>-<nanoseconds>`. The informer matches status to the
  `pdf_jobs` row by name.
- **Labels:** `app.kubernetes.io/managed-by: delivery`, `app.kubernetes.io/part-of: steward`.

## Spec (delivery writes)

| Field | Value |
| --- | --- |
| `policyVersionId` | The version to print. |
| `fetchURL` | `INTERNAL_BASE_URL` + `/internal/policies/<version id>/html`. |
| `outputBucket` | `S3_BUCKET`. |
| `outputKey` | `artifacts/<version id>/<job id>.pdf`. |
| `sensitivity` | `standard` or `sensitive`; `sensitive` prints the watermark. |
| `requestedBy` | The requesting user's id. |
| `trace` | Optional trace context. |

## Status (the renderer writes)

| Field | Meaning |
| --- | --- |
| `phase` | Empty until the renderer sees the resource, then `Pending`, `Running`, `Succeeded` or `Failed`. |
| `outputURL` | Where the PDF went; delivery uses `spec.outputKey` and falls back to this. |
| `error` | The failure, shown to operators only. |

`Running` moves the job to `processing`, `Succeeded` to `done` with the key, `Failed` to `failed`
with the error. A resync every 5 minutes repairs a row that drifted.

## Access delivery needs

Its service account needs `create`, `get`, `list` and `watch` on `pdfrenders` in its namespace.
