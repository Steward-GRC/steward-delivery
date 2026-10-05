# steward-delivery 🐹

> 🧭 Rendering, diff, shared links and PDF export service for Steward

The delivery service is how a Steward policy version leaves the editor: rendered, compared, shared
and printed.

- **Render:** a version's Lexical content becomes HTML through the server renderer, the reference
  for the rendering contract every client follows.
- **Diff:** the section diff between two versions, from steward-core.
- **Magic links:** read-only share links to one version, with a shorter life, a watermark and the
  viewer's email for sensitive policies.
- **PDF export:** each request creates a `PdfRender` resource that steward-pdf-renderer renders;
  delivery tracks the job and hands out a short-lived download link.

Delivery calls steward-core and publishes steward-audit's `AuditEvent`.

## 🚀 Run

```bash
cp .env.example .env   # a local Postgres, RabbitMQ and steward-core
task run
```

Or build the image with `docker build --build-arg VERSION=dev --build-arg COMMIT=$(git rev-parse HEAD) -t steward-delivery .`.
Settings are in [configuration](docs/configuration.md). The version and commit show up in the
`steward-version` and `steward-commit` health headers and on `/readyz`; the probes are in the
[runbook](docs/runbook.md#probes).

## 📚 Docs

- [API](docs/api.md): the gRPC service, the events out, and calling other services.
- [PdfRender](docs/pdf-render.md): the resource delivery creates for the renderer.
- [Configuration](docs/configuration.md).
- [Runbook](docs/runbook.md).
- [Error codes](docs/error-codes.md).

## 🛠 Develop

```bash
task build       # go build ./...
task test        # go test ./... (store and readiness tests start containers)
task test-race   # the same with the race detector
task lint        # gofmt check + golangci-lint + yamllint
task proto       # fetch the pinned callee protos, buf lint, regenerate gen/
task license     # check Apache-2.0 headers (golic)
```

Set `DATABASE_TEST_DSN` to run the store tests against an existing Postgres instead of a container.

## ⚖️ License

Apache-2.0 (c) 2026 The Steward Authors
