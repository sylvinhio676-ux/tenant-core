# tenant-core
# tenant-core

> Multi-tenancy toolkit for Go — resolve, isolate, and observe tenants natively in your HTTP stack.

[![Go Reference](https://pkg.go.dev/badge/github.com/sylvinhio676-ux/tenant-core.svg)](https://pkg.go.dev/github.com/sylvinhio676-ux/tenant-core)
[![Go Report Card](https://goreportcard.com/badge/github.com/sylvinhio676-ux/tenant-core)](https://goreportcard.com/report/github.com/sylvinhio676-ux/tenant-core)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

## Why tenant-core?

Most Go frameworks treat multi-tenancy as an application-level concern. tenant-core makes the tenant a first-class citizen at the HTTP layer — rate limiting, caching, metrics, and RBAC all become tenant-aware by default, without ever imposing a data isolation strategy.

## Status

🚧 Early development — not yet ready for production use. Following an open build-in-public roadmap.

## Installation

\`\`\`bash
go get github.com/sylvinhio676-ux/tenant-core
\`\`\`

## Quick start

\`\`\`go
tm := tenant.New(
    tenant.WithResolver(resolver.Subdomain()),
)
\`\`\`

## License

MIT — see [LICENSE](LICENSE).