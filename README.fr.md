# tenant-core

[🇬🇧 English](README.md) · 🇫🇷 Français

> Toolkit multi-tenant pour Go — résolvez, isolez et observez vos tenants nativement dans votre stack HTTP.

[![Go Reference](https://pkg.go.dev/badge/github.com/sylvinhio676-ux/tenant-core.svg)](https://pkg.go.dev/github.com/sylvinhio676-ux/tenant-core)
[![Go Report Card](https://goreportcard.com/badge/github.com/sylvinhio676-ux/tenant-core)](https://goreportcard.com/report/github.com/sylvinhio676-ux/tenant-core)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

## Pourquoi tenant-core ?

La plupart des frameworks Go traitent le multi-tenant comme une préoccupation de niveau applicatif. tenant-core fait du tenant un citoyen de première classe dès la couche HTTP — le rate limiting, le cache, les métriques et le RBAC deviennent tous tenant-aware par défaut, sans jamais imposer de stratégie d'isolation des données.

## Getting started

Nouveau sur tenant-core ? [GETTING_STARTED.md](GETTING_STARTED.md) (en anglais ;
version française : [GETTING_STARTED.fr.md](GETTING_STARTED.fr.md)) vous guide
pas à pas dans l'intégration à votre propre projet, de la résolution de votre
premier tenant jusqu'au RBAC, au rate limiting, à l'Admin API et aux
adaptateurs de framework (Gin/Echo/Chi) — avec du code fonctionnel et
copiable-collable à chaque étape.

## Statut

🚧 **Développement actif** — architecture centrale complète et testée (résolution, store/cache, propagation de bannissement en temps réel, rate limiting, RBAC, métriques, adaptateurs de framework pour net/http/Gin/Echo/Chi, Admin API, propagation multi-instance basée sur Redis, helpers de test). Pas encore benchmarké ni durci pour la production — la validation de performance, le profiling et les tests de charge restent à faire. Pas encore taggé/publié.

## Installation

\`\`\`bash
go get github.com/sylvinhio676-ux/tenant-core
\`\`\`

## Démarrage rapide

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

Voir [GETTING_STARTED.md](GETTING_STARTED.md) pour le guide complet — relire le tenant dans un handler, tester avec curl, le cache, le RBAC, le rate limiting, l'Admin API, et les autres adaptateurs de framework.

## Documentation

Pour la documentation technique complète (architecture, décisions de conception, concurrence, limitations connues), voir [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) (en anglais ; version française : [docs/ARCHITECTURE.fr.md](docs/ARCHITECTURE.fr.md)).

## Licence

MIT — voir [LICENSE](LICENSE).
