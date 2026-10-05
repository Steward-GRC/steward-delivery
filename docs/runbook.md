# Runbook

## Start-up

The service applies the baseline migration, connects to Postgres and RabbitMQ, dials steward-core
and serves gRPC, the probes and the internal HTTP port. A bad setting stops it with every problem
listed. PDF export is optional: with `PDF_EXPORT_ENABLED=false` or outside a cluster,
`RequestPDFExport` answers `PDF_EXPORT_DISABLED`; without `S3_ENDPOINT`, so does
`GetPDFDownloadLink`. Rendering, diffs and magic links still work.

## Probes

Readiness follows go-buildinfo's dependency checker. Each check has a 2-second timeout, and a
result is reused for 5 seconds.

| Dependency | Required | When it's down |
| --- | --- | --- |
| `postgres` | yes | Not ready: links and jobs can't be read or written. |
| `rabbitmq` | yes | Not ready: magic links can't be audited. |
| `core` | yes | Not ready: nothing can be rendered or diffed. Checked through core's own health answer, which also gives its version. |
| `objectstore` | no, reported when `S3_ENDPOINT` is set | Degraded, still ready: only PDF downloads fail. |
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
| `PDF_EXPORT_DISABLED` | `PDF_EXPORT_ENABLED`, the in-cluster config, and `S3_ENDPOINT` for downloads. |
| Exports stay `PDF_EXPORT_NOT_READY` | The renderer isn't reconciling: `kubectl get pdfrenders`, the renderer's logs, and whether it can reach `INTERNAL_BASE_URL`. |
| `PDF_EXPORT_FAILED` | `pdf_jobs.error_msg` for the job, or the resource's `status.error`. |
| No events reach audit | `steward-depstate-rabbitmq` or `/readyz`, then the `audit` exchange and its binding to audit's queue. |

## Backups

Back up Postgres. The PDFs in the bucket can be rendered again; magic links can't be re-created
with the same token.
