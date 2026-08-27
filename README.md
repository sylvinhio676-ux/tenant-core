# tenant-core

🇬🇧 English · [🇫🇷 Français](README.fr.md)

> Multi-tenancy toolkit for Go — resolve, isolate, and observe tenants natively in your HTTP stack.

[![Go Reference](https://pkg.go.dev/badge/github.com/sylvinhio676-ux/tenant-core.svg)](https://pkg.go.dev/github.com/sylvinhio676-ux/tenant-core)
[![Go Report Card](https://goreportcard.com/badge/github.com/sylvinhio676-ux/tenant-core)](https://goreportcard.com/report/github.com/sylvinhio676-ux/tenant-core)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

## Why tenant-core?

Most Go frameworks treat multi-tenancy as an application-level concern. tenant-core makes the tenant a first-class citizen at the HTTP layer — rate limiting, caching, metrics, and RBAC all become tenant-aware by default, without ever imposing a data isolation strategy.

## Getting started

New to tenant-core? [GETTING_STARTED.md](GETTING_STARTED.md) walks through
integrating it into your own project step by step, from resolving your first
tenant to RBAC, rate limiting, the Admin API, and framework adapters
(Gin/Echo/Chi) — with copy-pasteable, working code at each step.

## Status

🚧 **Active development** — core architecture complete and tested (resolution, store/cache, real-time ban propagation, rate limiting, RBAC, metrics, framework adapters for net/http/Gin/Echo/Chi, Admin API, Redis-based multi-instance propagation, test helpers). Not yet benchmarked or hardened for production — performance validation, profiling, and load testing are still ahead. Not tagged/released yet.

## Installation

\`\`\`bash
go get github.com/sylvinhio676-ux/tenant-core
\`\`\`

## Quick start

\`\`\`go
package main

import (
	"context"
	"log"
	"net/http"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/resolver"
	"github.com/sylvinhio676-ux/tenant-core/store"

	nethttp "github.com/sylvinhio676-ux/tenant-core/middleware/nethttp"
)

func main() {
	memStore := store.NewMemoryStore()
	_ = memStore.Create(context.Background(), &tenant.Tenant{ID: "acme", State: tenant.Active})

	manager := tenant.New(
		tenant.WithResolver(resolver.NewSubdomainResolver("localhost")),
		tenant.WithStore(memStore),
	)

	handler := nethttp.Wrap(manager, http.DefaultServeMux)
	log.Fatal(http.ListenAndServe(":8080", handler))
}
\`\`\`

See [GETTING_STARTED.md](GETTING_STARTED.md) for the full walkthrough — reading the tenant back in a handler, testing with curl, caching, RBAC, rate limiting, the Admin API, and other framework adapters.

## Documentation

For complete technical documentation (architecture, design decisions, concurrency, known limitations), see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## License

MIT — see [LICENSE](LICENSE).
