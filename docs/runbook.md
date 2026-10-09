# Runbook

## Start-up

The service applies the baseline migration, connects to Postgres and RabbitMQ, dials steward-core
and serves gRPC, the probes and the internal HTTP port. A bad setting stops it with every problem
listed. PDF export is optional: with `PDF_EXPORT_ENABLED=false` or outside a cluster,
`RequestPDFExport` answers `PDF_EXPORT_DISABLED`. Export also stays off, and answers the same,
until `S3_ENDPOINT`, `S3_BUCKET` and `INTERNAL_BASE_URL` are all set: without them a render has no
bucket to write to and no URL to fetch from. Without `S3_ENDPOINT`, `GetPDFDownloadLink` answers
`PDF_EXPORT_DISABLED` too. Rendering, diffs and magic links still work.

## Probes

Readiness follows go-buildinfo's dependency checker. Each check has a 2-second timeout, and a
result is reused for 5 seconds.

| Dependency | Required | When it's down |
| --- | --- | --- |
| `postgres` | yes | Not ready: links and jobs can't be read or written. |
| `rabbitmq` | yes | Not ready: magic links can't be audited. |
| `core` | yes | Not ready: nothing can be rendered or diffed. Checked through core's own health answer, which also gives its version. |
| `objectstore` | no, reported when `S3_ENDPOINT` is set | Degraded, still ready: only PDF downloads fail. |
| `jwks` | yes, while service-to-service authentication is on | Not ready: no caller can be verified. A good fetch keeps it up for a minute; a failure is retried on the next probe. The verifier keeps its last good key set either way. |
| `workloadauth` | no, reported only with `WORKLOAD_AUTH=disabled` | Always degraded: every caller that reaches the port is served. Never run like this outside local development. |
| `pdfexport` | no, reported only while `PDF_EXPORT_ENABLED` is on but export is off | Always degraded, still ready: the message names what is missing (the in-cluster config, `S3_ENDPOINT` and `S3_BUCKET`, or `INTERNAL_BASE_URL`). |
| `kubernetes` | no, reported while PDF export is on | Degraded, still ready: only new exports fail. Fails too when the CRD is missing or the service account can't list `pdfrenders`. |

- **HTTP on `PROBE_PORT` (8080):** `GET /livez` is 200 while the process is up and never checks a
  dependency. `GET /readyz` is 200 while ready and 503 while a required dependency is down; its
  JSON body lists every dependency with its state and error class. There's no plain `/health`.
- **gRPC on `GRPC_PORT`:** `grpc.health.v1` with the service name `liveness` reports the process
  only. The empty name and `readiness` follow readiness. Every `Health/Check` answer carries
  `steward-version`, `steward-commit`, `steward-dep-postgres`, `steward-dep-core` and
  `steward-depstate-<name>` (`ok`, `degraded` or `down`).
- Never point liveness at a dependency: a database outage would restart every replica.
- Readiness recovers on its own once the dependency is back.

## Common problems

| Symptom | Look at |
| --- | --- |
| `Code 8001: Internal Error` | A store call failed. The log line with the same trace id names the `op`. |
| `CORE_UNAVAILABLE` | steward-core is down or unreachable at `CORE_GRPC_ADDR`; `steward-depstate-core`. |
| `PDF_EXPORT_DISABLED` | The `pdfexport` entry in `/readyz` (what is missing), or `PDF_EXPORT_ENABLED=false`. |
| Exports stay `PDF_EXPORT_NOT_READY` | The renderer isn't reconciling: `kubectl get pdfrenders`, the renderer's logs, and whether it can reach `INTERNAL_BASE_URL`. |
| `PDF_EXPORT_FAILED` | `pdf_jobs.error_msg` for the job, or the resource's `status.error`. |
| Delivery won't start: `WORKLOAD_TOKEN_FILE` | The projected token isn't mounted, or `WORKLOAD_TOKEN_FILE` points elsewhere. |
| Calls to core fail with `read WORKLOAD_TOKEN_FILE` | The token mount went away; delivery won't call core without it. |
| Core refuses delivery: `Unauthenticated` or `PermissionDenied` | core's `WORKLOAD_ALLOWED_SERVICEACCOUNTS` must list `steward/steward-delivery`, and the token's audience must be `steward`. |
| `Unauthenticated: workload token rejected` on delivery's API | The log line `caller token rejected` gives the reason: wrong `iss` or `aud`, expired, or a service account missing from `WORKLOAD_ALLOWED_SERVICEACCOUNTS`. |
| `PermissionDenied: caller not allowed on this method` | Only the gateway may call delivery's API; the `rpc.denied` audit event names the caller. |
| `Unavailable: workload verifier unavailable` | No JWKS has loaded since start: `steward-depstate-jwks`, then the `JWKS refresh failed` log line. Every call needing a token is refused (the internal render port answers 503) and the pod stays not ready until a fetch succeeds. A `status 401` there means the API server refused `WORKLOAD_OIDC_BEARER_FILE`: it must hold a token with the API server's own audience, not the `steward` caller token. |
| The renderer's HTML fetch gets 401 or 403 | 401: the renderer Job sent no token or a rejected one (its `WORKLOAD_TOKEN_FILE` mount, audience `steward`, and `steward/steward-pdf-renderer` in `WORKLOAD_ALLOWED_SERVICEACCOUNTS`). 403: the token belongs to another caller. |
| No events reach audit | `steward-depstate-rabbitmq` or `/readyz`, then the `audit` exchange and its binding to audit's queue. |

## Backups

Back up Postgres. The PDFs in the bucket can be rendered again; magic links can't be re-created
with the same token.
