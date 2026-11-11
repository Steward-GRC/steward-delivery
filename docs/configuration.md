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
| `INTERNAL_HTTP_PORT` | `8081` | The policy HTML the renderer fetches. Needs the renderer's workload token (see below). |
| `INTERNAL_BASE_URL` | empty | The in-cluster URL of that port, the prefix of every render's fetch URL. |
| `GRPC_TLS_CERT_FILE`, `GRPC_TLS_KEY_FILE`, `GRPC_TLS_CLIENT_CA_FILE` | empty | Set all three to serve mTLS (TLS 1.3, client certificates required). |
| `WORKLOAD_TOKEN_FILE` | required | Delivery's projected service-account token (audience `steward`, mounted at `/var/run/secrets/steward/token`), sent to steward-core on every call. Optional only with `WORKLOAD_AUTH=disabled`. |
| `WORKLOAD_OIDC_ISSUER` | required | The cluster's service-account token issuer, an `https` URL that must equal the token's `iss`. |
| `WORKLOAD_OIDC_JWKS_URL` | discovered | The issuer's JWKS, when it isn't at the `jwks_uri` of `<issuer>/.well-known/openid-configuration`. `https` only. |
| `WORKLOAD_OIDC_CA_FILE` | system roots | Extra PEM CA trusted for the discovery and JWKS fetch (the cluster CA). |
| `WORKLOAD_OIDC_BEARER_FILE` | empty | A token sent on the discovery and JWKS fetch, re-read on every fetch (the pod's API token). |
| `WORKLOAD_AUDIENCE` | `steward` | The audience a caller's token must carry. |
| `WORKLOAD_ALLOWED_SERVICEACCOUNTS` | required | Comma list of `<namespace>/<serviceaccount>` that may call delivery at all: `steward/steward-gateway,steward/steward-pdf-renderer`. |
| `WORKLOAD_AUTH` | enabled | `disabled` turns caller authentication off, for local runs only. Nothing else turns it off, and it can't be combined with `WORKLOAD_OIDC_ISSUER`. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Traces and metrics. |
| `MAGIC_LINK_NONSENSITIVE_TTL` | `720h` | A magic link's lifetime. |
| `MAGIC_LINK_SENSITIVE_TTL` | `48h` | A sensitive magic link's lifetime. |
| `PDF_LINK_TTL` | `15m` | A PDF download link's lifetime. |
| `PDF_EXPORT_ENABLED` | `true` | `false` when steward-pdf-renderer isn't deployed. Export is on only in a cluster, with `S3_ENDPOINT`, `S3_BUCKET` and `INTERNAL_BASE_URL` set; otherwise readiness reports `pdfexport` degraded with what is missing. |
| `POD_NAMESPACE` | `default` | Where `PdfRender` resources are created (set from the downward API). |
| `S3_ENDPOINT`, `S3_BUCKET` | empty | The bucket the renderer writes PDFs to; set both or neither. Without them PDF export and download links are off. |
| `S3_REGION` | `us-east-1` | |
| `S3_ACCESS_KEY`, `S3_SECRET_KEY` | empty | Static credentials. |
| `S3_FORCE_PATH_STYLE` | `true` | Path-style addressing (RustFS and most self-hosted stores). |
| `LOG_LEVEL`, `LOG_FORMAT` | go-log's | `trace` and `console` locally; clusters log JSON. |

## Service-to-service authentication

- **Calling core:** every call to steward-core carries `authorization: Bearer <token>` from
  `WORKLOAD_TOKEN_FILE`. The file is read again on every call, so a token the kubelet rotates is
  picked up. If the file is missing at start-up, delivery doesn't start; if it goes missing later,
  each call fails with an error naming `WORKLOAD_TOKEN_FILE` instead of going out without a token.
- **Its own API:** every call to delivery's gRPC API must carry the caller's token. Delivery
  verifies it against the issuer's JWKS, maps `<namespace>/steward-<name>` to the caller `<name>`,
  and checks the per-method allow-list in `internal/grpcsvc/callers.go`. The gateway is the only
  caller, on behalf of the signed-in user, on every method. The signed-in user's actor
  (go-grpc-actor) is trusted only from that verified on-behalf caller, so with
  `WORKLOAD_AUTH=disabled` no call carries a user and PDF download links are refused.
- **The internal HTTP port:** `GET /internal/policies/{versionId}/html` needs
  `Authorization: Bearer <token>` with the same check, and only `pdf-renderer` is allowed. A
  missing or rejected token is 401, another verified caller 403, and a verifier with no key set
  yet 503; each is audited as `rpc.denied` with the route as the method. The probes are on
  `PROBE_PORT` and need no token.
- **Refusals:** a missing or rejected token is `Unauthenticated`, a caller the method doesn't list
  is `PermissionDenied`, and a verifier that hasn't loaded a key set answers `Unavailable`. Each
  refusal is logged and audited as `rpc.denied`. `grpc.health.v1` and server reflection need no
  token.
- **Off switch:** `WORKLOAD_AUTH=disabled` is the only way to run without authentication, for
  local runs. Delivery logs a warning at start-up and every 5 minutes, and readiness reports
  `workloadauth` degraded. With neither `WORKLOAD_OIDC_ISSUER` nor `WORKLOAD_AUTH=disabled`,
  delivery doesn't start.
- **The shared code:** `internal/workloadauth` is a byte-identical copy of steward-core's at the
  commit `STEWARD_CORE_REF` pins in `proto-refs.env`. `scripts/workloadauth-check.sh` (the
  "Workload auth copy" check) fails on any difference. Never edit the copy here: change it in
  steward-core, move the pin, and copy the files again.

## Build arguments

The image takes `VERSION` (the image tag) and `COMMIT` (the full source SHA) and stamps them into
go-buildinfo with `-ldflags -X github.com/Bugs5382/go-buildinfo.Version=${VERSION} -X github.com/Bugs5382/go-buildinfo.Commit=${COMMIT}`.
Unstamped builds report `dev` and fall back to the Go VCS stamp, then `unknown`.
