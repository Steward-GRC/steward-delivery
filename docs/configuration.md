# Configuration

Every setting is an environment variable. A bad value stops the service at start-up with every
problem listed. `.env.example` has local defaults.

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_DSN` | required | The service's Postgres database. |
| `MIGRATE_DSN` | `DATABASE_DSN` | A direct connection for the migration, when `DATABASE_DSN` goes through a pooler. |
| `MIGRATIONS_DIR` | `migrations` | Where the baseline lives (`/migrations` in the image). |
| `RABBITMQ_URL` | none | The broker for audit events. |
| `CORE_GRPC_ADDR` | required | steward-core's gRPC address. |
| `GRPC_PORT` | `9090` | The gRPC port. |
| `PROBE_PORT` | `8080` | `/livez` and `/readyz`. |
| `INTERNAL_HTTP_PORT` | `8081` | The policy HTML the renderer fetches. Reachable from the renderer only. |
| `INTERNAL_BASE_URL` | empty | The in-cluster URL of that port, the prefix of every render's fetch URL. |
| `GRPC_TLS_CERT_FILE`, `GRPC_TLS_KEY_FILE`, `GRPC_TLS_CLIENT_CA_FILE` | empty | Set all three to serve mTLS (TLS 1.3, client certificates required). |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Traces and metrics. |
| `MAGIC_LINK_NONSENSITIVE_TTL` | `720h` | A magic link's lifetime. |
| `MAGIC_LINK_SENSITIVE_TTL` | `48h` | A sensitive magic link's lifetime. |
| `PDF_LINK_TTL` | `15m` | A PDF download link's lifetime. |
| `PDF_EXPORT_ENABLED` | `true` | `false` when steward-pdf-renderer isn't deployed. Outside a cluster export is off anyway. |
| `POD_NAMESPACE` | `default` | Where `PdfRender` resources are created (set from the downward API). |
| `S3_ENDPOINT`, `S3_BUCKET` | empty | The bucket the renderer writes PDFs to; set both or neither. Without them download links are off. |
| `S3_REGION` | `us-east-1` | |
| `S3_ACCESS_KEY`, `S3_SECRET_KEY` | empty | Static credentials. |
| `S3_FORCE_PATH_STYLE` | `true` | Path-style addressing (RustFS and most self-hosted stores). |
| `LOG_LEVEL`, `LOG_FORMAT` | go-log's | `trace` and `console` locally; clusters log JSON. |

## Build arguments

The image takes `VERSION` (the image tag) and `COMMIT` (the full source SHA) and stamps them into
go-buildinfo with `-ldflags -X github.com/Bugs5382/go-buildinfo.Version=${VERSION} -X github.com/Bugs5382/go-buildinfo.Commit=${COMMIT}`.
Unstamped builds report `dev` and fall back to the Go VCS stamp, then `unknown`.
