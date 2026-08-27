# tenant-core — Architecture et documentation technique complète

[🇬🇧 English](ARCHITECTURE.md) · 🇫🇷 Français

> Toolkit multi-tenant natif pour Go : résolution, isolation de contexte, cache, propagation de bannissement en temps réel, rate limiting, RBAC, métriques, Admin API, et propagation multi-instance — distribué comme une bibliothèque middleware compatible avec les routeurs Go existants.

---

## Table des matières

1. [Vue d'ensemble](#1-vue-densemble)
2. [Principes fondamentaux](#2-principes-fondamentaux)
3. [Vue d'ensemble de l'architecture](#3-vue-densemble-de-larchitecture)
4. [Étape 1 — Fondations](#4-étape-1--fondations)
5. [Étape 2 — Store et cache](#5-étape-2--store-et-cache)
6. [Étape 3 — Bannissement en temps réel](#6-étape-3--bannissement-en-temps-réel)
7. [Étape 4 — RateLimiter et CacheKeyer](#7-étape-4--ratelimiter-et-cachekeyer)
8. [Étape 5 — RBAC et Metrics](#8-étape-5--rbac-et-metrics)
9. [Étape 6 — Adaptateurs de framework](#9-étape-6--adaptateurs-de-framework)
10. [Étape 7 — API Admin et EventBus Redis](#10-étape-7--api-admin-et-eventbus-redis)
11. [Étape 8 — Outils de test (tenanttest)](#11-étape-8--outils-de-test-tenanttest)
12. [Architecture complète finale](#12-architecture-complète-finale)
13. [Flux de données](#13-flux-de-données)
14. [Concurrence et thread-safety](#14-concurrence-et-thread-safety)
15. [Testabilité](#15-testabilité)
16. [Limitations et évolutions futures](#16-limitations-et-évolutions-futures)
17. [Arborescence des packages](#17-arborescence-des-packages)
18. [Décisions / points à clarifier](#18-décisions--points-à-clarifier)

---

## 1. Vue d'ensemble

### Objectif de tenant-core

`tenant-core` est un toolkit Go dont le but est de résoudre un problème récurrent dans les applications SaaS multi-entreprises :

> Quand une requête arrive, l'application doit toujours savoir à quel tenant elle appartient, et empêcher que le contexte d'un tenant ne soit jamais mélangé avec celui d'un autre.

```text
                 YOUR APPLICATION
                       │
          ┌────────────┼────────────┐
          ▼            ▼            ▼
       Company A     Company B     Company C
          │            │            │
       Users A       Users B       Users C
       Data A        Data B        Data C
```

### Problème résolu

Sans toolkit dédié, chaque équipe réinvente sa propre gestion du multi-tenant : résoudre le tenant depuis la requête, le propager manuellement (`handler(request, tenant)`, `service(request, tenant)`, `repository(request, tenant)`...), isoler le cache, les quotas, les permissions — avec le risque constant de fuite de données entre tenants.

`tenant-core` répond à cela avec deux opérations fondamentales :

1. **Résolution du tenant** — à partir d'une requête HTTP, déterminer à quel tenant elle appartient.
2. **Propagation du contexte tenant** — transmettre cette information à chaque couche de l'application sans paramètre explicite supplémentaire.

```text
GET https://company-a.example.com/users

tenant-core must understand:

This request
     ↓
belongs to
     ↓
Tenant A

Then propagate this information:

Request
   │
   ▼
TenantResolver
   │
   │ "It's Tenant A"
   ▼
ContextInjector
   │
   │ context = Tenant A
   ▼
Middleware
   │
   ▼
Handler
```

### Cas d'usage

- Une plateforme SaaS où chaque client (entreprise) ne doit voir que ses propres données, et jamais accéder à celles d'un autre.
- Un système où bannir un tenant (fraude, abus) doit s'appliquer immédiatement, sur toutes les instances du serveur.
- Une application déployée derrière n'importe quel routeur Go (`net/http`, Gin, Echo, Chi) sans dupliquer la logique multi-tenant pour chacun.
- Un besoin de quotas et permissions différenciés par tenant, sans configuration globale rigide.

### Philosophie générale

`tenant-core` n'essaie pas de réinventer un serveur HTTP, ni d'imposer une stratégie d'isolation des données (`tenant_id` partagé, schéma séparé, base de données séparée). Il se positionne comme un toolkit qui traite le **tenant comme un citoyen de première classe** à chaque couche — résolution, cache, quotas, permissions, métriques — sans jamais imposer comment les données elles-mêmes sont isolées au niveau du stockage.

### Pourquoi context.Context ?

Plutôt que de faire voyager le tenant comme paramètre explicite à travers toute la pile applicative :

```go
handler(request, tenant)
service(request, tenant)
repository(request, tenant)
```

`tenant-core` s'appuie sur le `context.Context` standard de Go :

```text
Request
   │
   ▼
context.Context
   │
   ├── Tenant A
   ├── deadline
   └── cancellation
```

Le tenant devient ainsi accessible à tout composant qui en a besoin, sans changer la signature de chaque fonction intermédiaire.

---

## 2. Principes fondamentaux

Ces principes ont été appliqués de façon constante tout au long des huit étapes de construction du toolkit.

### 2.1 Séparation des responsabilités

Chaque composant a une responsabilité unique, clairement délimitée. Le `Resolver` ne connaît pas le `Store` ; le `Store` ne connaît pas l'`EventBus` ; l'`EventBus` ne connaît pas ses consommateurs.

### 2.2 Interfaces minimales

> Une interface ne devrait exposer que ce dont son consommateur a réellement besoin.

C'est le principe qui a motivé la séparation entre `tenant.Store` (lecture, consommée par `Manager`) et `tenant.AdminStore` (écriture, consommée par `admin.Service`) — plutôt qu'une seule interface `Store` surchargée avec `Create`, `Update`, `Ban`, `Disable`, etc.

### 2.3 Le typage structurel de Go

Go n'exige pas qu'un type déclare explicitement `implements InterfaceX`. Dès qu'un type possède les méthodes qu'une interface attend, il la satisfait automatiquement :

```text
             tenant.Store
                  ▲
       ┌──────────┼───────────┐
       │          │           │
       ▼          ▼           ▼
 MemoryStore CachedStore  DBStore (future)
```

Ce mécanisme permet à `SubdomainResolver`, `MemoryStore`, `CachedStore`, `RedisEventBus`, `MemoryMetrics`, etc. de satisfaire les contrats définis dans le package `tenant` sans jamais avoir besoin d'importer ce package en retour — évitant les dépendances circulaires.

### 2.4 Agnosticisme

Le cœur du toolkit ne dépend d'aucune technologie d'infrastructure particulière :

- pas un framework HTTP (Gin, Echo, Chi) ;
- pas Redis ;
- pas Prometheus ;
- pas un moteur de base de données particulier.

```text
             tenant
               │
       ┌───────┴────────┐
       │                │
   Interface       Interface
       │                │
       ▼                ▼
Implementation A   Implementation B
```

### 2.5 Fail-fast

Une erreur de **configuration du programme** (un composant requis manquant, une connexion Redis inaccessible au démarrage) doit être détectée **immédiatement**, généralement via un `panic` ou une erreur retournée au démarrage — plutôt que découverte silencieusement en production. Une erreur survenant pendant le **traitement d'une requête**, en revanche, est toujours gérée via le mécanisme standard `error`.

### 2.6 Testabilité

Chaque composant est conçu pour être testé indépendamment, sans dépendance à une infrastructure réelle (base de données, Redis, framework HTTP complet). Le package `tenanttest` étend ce principe pour les utilisateurs externes du toolkit.

### 2.7 Isolation multi-tenant

> Toute ressource partagée doit être explicitement scopée par `TenantID`.

Ce principe s'applique de façon transversale : au stockage (`TenantID → Tenant`), au cache (`TenantID → Cache Key`), au rate limiting (`TenantID → Rate Limit Bucket`), aux permissions (`TenantID → Roles → Permissions`).

### 2.8 Concurrence sûre

Le toolkit est destiné à des applications HTTP qui gèrent naturellement des requêtes concurrentes. Chaque composant à état partagé est protégé par le mécanisme de synchronisation adapté à son profil d'accès (voir [section 14](#14-concurrence-et-thread-safety)).

### 2.9 Observabilité

Le toolkit permet de mesurer son propre comportement (requêtes, erreurs, latence, refus RBAC, refus de rate-limit) sans imposer de backend de métriques particulier.

### 2.10 Séparation de la logique métier et du transport

La logique métier (`Manager`, `admin.Service`, `RBAC`) ne sait jamais quel protocole de transport l'invoque (HTTP, CLI, futur gRPC, tests). C'est le rôle des adaptateurs de faire le pont entre les deux.

---

## 3. Vue d'ensemble de l'architecture

### Le principe central

> À partir d'une requête HTTP, identifier le tenant, récupérer son état, puis appliquer les différents mécanismes de protection et d'isolation.

```text
                    HTTP REQUEST
                         │
                         ▼
               ┌──────────────────┐
               │    Resolver      │
               │                  │
               │ Request → ID     │
               └────────┬─────────┘
                        │
                  TenantID
                        │
                        ▼
               ┌──────────────────┐
               │      Store       │
               │                  │
               │ ID → *Tenant     │
               └────────┬─────────┘
                        │
                    *Tenant
                        │
                        ▼
               ┌──────────────────┐
               │   ContextInjector│
               │    tenantctx     │
               │                  │
               │ *Tenant → Context│
               └────────┬─────────┘
                        │
                        ▼
                   CONTEXT
```

### La séparation fondamentale des packages

```text
tenant
│
├── defines the contracts and business model (the "what")
│
├── tenantctx        → carries the tenant via context.Context
├── resolver         → concrete resolution (the "how")
├── store            → persistence and cache
├── eventbus         → event propagation
├── ratelimit        → per-tenant quotas
├── cachekey         → cache key isolation
├── rbac             → per-tenant permissions
├── metrics          → observability
├── middleware       → router adapters
│   ├── net/http
│   ├── gin
│   ├── echo
│   └── chi
├── admin            → tenant administration
├── eventbus/redis   → multi-instance propagation
└── tenanttest       → test tools
```

> **Le package `tenant` définit le « quoi » ; les sous-packages définissent le « comment ».**

Cette organisation permet :

- un **faible couplage** — chaque sous-package ne dépend que du contrat qu'il implémente, jamais d'autres implémentations ;
- des **interfaces minimales** — chaque composant n'expose que ce dont son consommateur a besoin ;
- l'**agnosticisme aux frameworks** — le cœur ne connaît ni Gin, ni Echo, ni Chi, ni Redis, ni Prometheus ;
- la **testabilité** — chaque contrat peut être satisfait par une implémentation fictive dans les tests ;
- l'**extensibilité** — une nouvelle implémentation (`PostgresStore`, `RedisRateLimiter`, `PrometheusMetrics`) peut être ajoutée sans modifier le cœur ;
- des **implémentations interchangeables** — passer de `MemoryEventBus` à `RedisEventBus` ne change aucun contrat, seulement l'adaptateur utilisé.

---

## 4. Étape 1 — Fondations

### 4.1 Objectif

Avant Redis, avant les middlewares Gin/Echo/Chi, avant l'Admin API ou le RBAC, une question fondamentale devait trouver réponse :

> Comment une requête HTTP est-elle associée à un tenant, et comment garantit-on que les données de ce tenant restent isolées ?

```text
                    HTTP Request
                         │
                         ▼
                ┌─────────────────┐
                │    Resolver     │
                │ "Which tenant?" │
                └────────┬────────┘
                         │
                    TenantID
                         │
                         ▼
                ┌─────────────────┐
                │ Tenant Context  │
                │ "Which tenant   │
                │ for this        │
                │ request?"       │
                └────────┬────────┘
                         │
                         ▼
                  Business handler
                         │
                         ▼
                 tenantctx.FromContext()
                         │
                         ▼
                    *Tenant
```

### 4.2 Les types fondamentaux

**`TenantID`**

```go
type TenantID string
```

Un type nommé dédié plutôt qu'un simple `string`, pour exprimer l'intention et bénéficier de la sécurité de typage : `var id tenant.TenantID` est conceptuellement différent de `var email string`. Le compilateur refuse de mélanger les deux, même si les deux ne sont « que des strings » en interne.

**`State`**

```go
type State string

const (
    Active   State = "active"
    Disabled State = "disabled"
    Banned   State = "banned"
)
```

Les trois états ont des significations métier distinctes :

- **`Active`** — le tenant peut accéder normalement au système.
- **`Disabled`** — le tenant est désactivé (ex. fin d'abonnement). La désactivation peut se propager avec un léger délai, notamment via un cache (cohérence *eventual*).
- **`Banned`** — le tenant est banni pour fraude ou abus. Contrairement à `Disabled`, un bannissement doit se propager **immédiatement** — ce qui justifie l'introduction ultérieure de `BanChecker` et de l'`EventBus` (étape 3).

**`Tenant`**

```go
type Tenant struct {
    ID    TenantID
    State State
    Roles []Role
}
```

`Role` (comme `TenantID`) est un type nommé `string` dédié plutôt qu'un simple `string` — ajouté en v0.3.0 pour la même raison de sécurité de typage, et vivant dans ce package racine (pas dans `rbac`, son principal consommateur) précisément parce que `Tenant.Roles` en a besoin ici, et que la direction de dépendance établie est `tenant → rbac`, jamais l'inverse.

```text
Tenant
 ├── ID
 ├── State
 └── Roles
```

Le champ `Roles` a été prévu dès le départ pour permettre l'intégration ultérieure du RBAC (étape 5).

### 4.3 Le contrat Resolver

Une décision architecturale importante : le contrat vit dans le package racine `tenant`, pas dans le package `resolver`.

```go
type Resolver interface {
    Resolve(r *http.Request) (TenantID, error)
}
```

Cette interface répond à une seule question : *à quel tenant appartient cette requête HTTP ?* Elle ne dit rien sur **comment** le tenant est trouvé.

```text
tenant
   │
   └── defines the contract
          │
          ▼
      Resolver

resolver/
   └── SubdomainResolver (implementation)
```

### 4.4 SubdomainResolver

La première implémentation concrète, basée sur le sous-domaine de la requête :

```text
tenant-a.example.com
        │
        ▼
SubdomainResolver
        │
        ▼
TenantID("tenant-a")
```

```text
https://school-a.example.com/users
                   │
                   ▼
              tenant-a
```

### 4.5 Pourquoi SubdomainResolver n'est pas dans `tenant`

```text
tenant
 │
 └── Resolver
       ▲
       │
       │ automatically satisfied by
       │
resolver
 │
 └── SubdomainResolver
```

Grâce au typage structurel de Go, `SubdomainResolver` n'a jamais besoin d'écrire `implements tenant.Resolver`. Il lui suffit d'avoir la méthode `Resolve(*http.Request) (tenant.TenantID, error)`.

### 4.6 L'injecteur de contexte — `tenantctx`

Une fois le tenant identifié, il doit être transmis aux couches suivantes. Le package `tenantctx/` gère le stockage du tenant dans le `context.Context` standard :

```go
ctx := tenantctx.WithTenant(ctx, tenant)
```

```text
context.Context
       │
       └── Tenant
            ├── ID
            ├── State
            └── Roles
```

Le code métier récupère ensuite le tenant avec :

```go
t := tenantctx.FromContext(ctx)
```

Une fonction métier n'a donc pas besoin de connaître le sous-domaine, HTTP, Gin, Echo, Chi, Redis, ni comment le tenant a été résolu — elle reçoit simplement un `context.Context`.

### 4.7 Pourquoi context.Context

Le contexte permet à l'identité du tenant de voyager à travers les couches, sans avoir à ajouter `tenantID string` à chaque signature :

```text
HTTP
 │
 ▼
Middleware
 │
 ▼
Service
 │
 ▼
Repository
 │
 ▼
Database
```

### 4.8 Isolation entre tenants

Il ne suffisait pas de pouvoir identifier un tenant : il fallait aussi garantir que le contexte de requête du tenant A ne puisse jamais être accidentellement réutilisé pour le tenant B.

```text
Request A
tenant-a.example.com
       │
       ▼
Context A
TenantID = tenant-a

Request B
tenant-b.example.com
       │
       ▼
Context B
TenantID = tenant-b
```

Les deux contextes doivent rester complètement indépendants.

### 4.9 Le test critique d'isolation

L'isolation a été traitée comme une propriété à tester **explicitement**, jamais simplement supposée.

```text
Create context A
      │
      ▼
inject tenant-A
      │
      ▼
Create context B
      │
      ▼
inject tenant-B
      │
      ▼
verify A == tenant-A
verify B == tenant-B
```

Le but visé, en particulier, est de détecter une mauvaise implémentation qui utiliserait une variable globale plutôt que `context.Context` :

```go
var currentTenant *Tenant // ❌ dangerous
```

```text
Goroutine A
tenant-A
     │
     ▼
globalTenant = A

Goroutine B
tenant-B
     │
     ▼
globalTenant = B

Goroutine A
     │
     ▼
gets B back ❌
```

Le contexte standard, en revanche, est **immuable** — `WithTenant()` ne modifie jamais un contexte existant, il en crée un nouveau qui l'enveloppe. Deux contextes créés depuis des branches différentes ne peuvent jamais se marcher dessus. Ce mécanisme évite précisément ce type de partage implicite dangereux.

**Deux tests concrets ont validé cette propriété** :

- Un test **structurel** : injecter deux tenants dans deux contextes séparés, vérifier qu'ils restent distincts, et que muter le tenant récupéré depuis l'un n'affecte jamais l'autre contexte.
- Un test sous **concurrence réelle** : une centaine de goroutines simulant des requêtes simultanées alternant entre deux tenants, systématiquement exécuté avec `go test -race`, pour garantir qu'aucune goroutine ne voit jamais le tenant d'une autre.

### 4.10 La clé de contexte privée

Un détail technique important : la clé utilisée par `context.WithValue` pour stocker le tenant n'est **jamais** un simple `string`. Une clé `string` comme `"tenant"` pourrait entrer en collision avec n'importe quelle autre bibliothèque tierce utilisant la même clé, avec un vrai risque d'écrasement silencieux.

La solution choisie est un type de clé **privé, non exporté** :

```go
type contextKey int

const tenantContextKey contextKey = 0
```

Puisque `contextKey` est un type non exporté, aucun autre package ne peut créer une valeur de ce type — même en connaissant son nom. Et même si un autre package définissait lui aussi un `type contextKey int` avec la valeur `0`, ce serait un type Go **différent** (les types sont comparés par identité complète package + nom), donc `context.WithValue` ne les confondrait jamais. C'est le pattern officiellement documenté par la bibliothèque standard de Go elle-même.

### 4.11 Architecture des packages après l'étape 1

```text
tenant-core/
│
├── tenant.go
│   ├── TenantID
│   ├── State
│   ├── Tenant
│   └── Resolver
│
├── tenantctx/
│   └── tenant context
│
└── resolver/
    └── SubdomainResolver
```

| Package | Responsabilité |
|---|---|
| `tenant` | Concepts et contrats fondamentaux |
| `tenantctx` | Transporte le tenant via `context.Context` |
| `resolver` | Résolution concrète du tenant |
| `SubdomainResolver` | Identification depuis le sous-domaine |

### 4.12 Le principe architectural établi dès l'étape 1

```text
                CONTRACTS
                   │
                   ▼
               tenant package
                   │
        ┌──────────┼──────────┐
        ▼          ▼          ▼
    resolver     store     eventbus
        │          │          │
        ▼          ▼          ▼
 implementation implementation implementation
```

Ce principe s'est répété constamment dans le reste du toolkit : `tenant.Resolver` ← `SubdomainResolver`, `tenant.Store` ← `MemoryStore`/`CachedStore`, `eventbus.EventBus` ← `MemoryEventBus`/`RedisEventBus`.

**Résumé de l'étape** : identifier → représenter → transporter → isoler le tenant. Cette fondation a permis de rester agnostique aux frameworks et de construire les adaptateurs Gin/Echo/Chi sans jamais modifier le cœur du système.

---

## 5. Étape 2 — Store et cache

### 5.1 Objectif

L'étape 1 répondait à *« à quel tenant correspond cette requête ? »*, mais seulement avec son identifiant. La question devenait maintenant : *« quelles sont les informations de ce tenant, et dans quel état est-il ? »* — c'est le rôle du `Store`.

```text
                    HTTP Request
                         │
                         ▼
                   ┌──────────┐
                   │ Resolver │
                   └────┬─────┘
                        │
                     TenantID
                        │
                        ▼
                 ┌─────────────┐
                 │ CachedStore │
                 └──────┬──────┘
                        │
                 cache hit?
                  /           \
                yes            no
                 │              │
                 │              ▼
                 │        ┌────────────┐
                 │        │ MemoryStore│
                 │        └──────┬─────┘
                 │               │
                 └───────┬───────┘
                         ▼
                      *Tenant
```

### 5.2 Le contrat `tenant.Store`

```go
type Store interface {
    Get(ctx context.Context, id TenantID) (*Tenant, error)
    IsBanned(ctx context.Context, id TenantID) (bool, error)
}
```

- **`Get`** — récupère un tenant complet.
- **`IsBanned`** — une vérification spécialisée et rapide de bannissement, qui devient particulièrement importante avec `BanChecker` (étape 3).

Cette séparation compte : le chemin de résolution normal n'a pas besoin de connaître les opérations d'administration (voir étape 7, `AdminStore`).

### 5.3 Pourquoi `Store` est une interface

```text
             tenant.Store
                  ▲
       ┌──────────┼───────────┐
       │          │           │
       ▼          ▼           ▼
 MemoryStore CachedStore  DBStore (future)
```

Le cœur du toolkit doit pouvoir remplacer `MemoryStore` par `PostgreSQLStore`, `MySQLStore`, `RedisStore`, ou `APIStore`, sans jamais modifier `Manager`.

### 5.4 MemoryStore

```go
type MemoryStore struct {
    mu      sync.RWMutex
    tenants map[tenant.TenantID]*tenant.Tenant
}
```

```text
MemoryStore
│
├── mu
│   └── RWMutex
│
└── tenants
    └── map[TenantID]*Tenant
```

### 5.5 Pourquoi `sync.RWMutex`

Le store est accédé simultanément par plusieurs goroutines HTTP :

```text
Request A ──────┐
Request B ──────┤
Request C ──────┼──► MemoryStore
Request D ──────┤
Request E ──────┘
```

Une simple map Go n'est pas sûre pour un accès concurrent impliquant des écritures. `RWMutex` permet deux types de verrouillage :

```text
Reader A ── RLock ──►
Reader B ── RLock ──►
Reader C ── RLock ──►

Writer
   │
   ▼
 Lock()
   │
   ▼
 modification
   │
   ▼
Unlock()
```

Plusieurs lecteurs peuvent lire en même temps ; une écriture reste exclusive. Ce profil convient particulièrement bien à un `Store`, puisque les lectures sont bien plus fréquentes que les écritures.

### 5.6 Le piège du pointeur partagé

Une subtilité importante rencontrée lors de la conception : la map contient `map[TenantID]*Tenant`, c'est-à-dire des **pointeurs**, pas des copies.

```text
Map
 │
 └── *Tenant ──────────┐
                       │
                       ▼
                    Tenant
                    State
```

Si `Get()` renvoie `t` directement (le pointeur interne), l'appelant obtient un accès direct à l'objet réellement stocké dans le store. Faire `t.State = tenant.Disabled` en dehors du verrou, pendant qu'une autre goroutine lit ce même champ via `Get()`, provoque une véritable **data race** — détectable par `go test -race`.

```text
Goroutine A                 Goroutine B

t.State = Banned
       │
       │                 Get()
       │                   │
       ▼                   ▼
   write                 read
```

> **Protéger seulement la map ne suffit pas quand les valeurs de la map sont des pointeurs mutables.**

**La solution adoptée** :

- **`Get()` renvoie toujours une copie**, jamais le pointeur interne. Le consommateur externe ne peut donc jamais muter l'état interne du store via le pointeur qu'il a reçu.
- Les opérations d'écriture (`SetState`, `Create`, `Update`) modifient l'objet interne **directement, sous un verrou exclusif (`Lock`)** — jamais via un aller-retour lecture + modification + réécriture, qui recréerait une fenêtre de *lost update*.

```text
MemoryStore
    │
    ▼
internal *Tenant
    │
    │ copy
    ▼
returned *Tenant
```

Une **primitive d'écriture interne** (`set`, non exportée) est toujours utilisée en interne par `Create`/`Update`/`SetState`, mais n'est jamais exposée publiquement — le contrat d'écriture public passe exclusivement par ces trois méthodes explicites, jamais par une écriture brute.

### 5.7 `Get()`

```text
TenantID
   │
   ▼
MemoryStore
   │
   ├── look up the tenant
   │
   ├── check it exists
   │
   └── return the tenant (copy)
```

Si le tenant n'existe pas, une erreur sentinelle explicite est renvoyée : `ErrTenantNotFound`. Cela permet aux couches supérieures de distinguer un tenant qui n'existe réellement pas d'une autre erreur technique.

### 5.8 `IsBanned()`

```text
TenantID
   │
   ▼
Store
   │
   ▼
State == Banned?
   │
   ├── yes → true
   └── no → false
```

### 5.9 Changement d'état — `Disable()` / `SetState()`

Un tenant peut passer de `Active` à `Disabled` (ex. fin d'abonnement) :

```text
subscription ended
       │
       ▼
Disable()
       │
       ▼
State = Disabled
```

Ce changement est protégé par le même mécanisme de synchronisation que les autres écritures :

```text
Disable()
   │
   ▼
Lock()
   │
   ▼
modify tenant
   │
   ▼
Unlock()
```

### 5.10 Pourquoi un TTL est nécessaire

Une base de données distante peut être bien plus lente qu'une lecture en mémoire. Sans cache :

```text
Request
   │
   ▼
Store
   │
   ▼
Database
   │
   ▼
Tenant
```

Si des milliers de requêtes demandent en continu le même tenant, cela devient coûteux. D'où l'introduction d'un cache devant le store.

### 5.11 CachedStore

```text
store/
├── memory.go
└── cached.go
```

`CachedStore` n'est pas un remplacement de `Store` : il l'**enveloppe**.

```text
CachedStore
     │
     └── source Store
             │
             ▼
        MemoryStore
```

Le champ `source` repose sur l'**interface** `tenant.Store`, jamais sur une implémentation concrète — une décision importante : le cache ne dépend d'aucune implémentation particulière, ce qui lui permet d'envelopper n'importe quel futur `Store` (Postgres, Redis, etc.) sans modification.

### 5.12 Fonctionnement du cache

**Cache HIT**

```text
Get("tenant-a")
       │
       ▼
   Cache found
       │
       ▼
   still valid?
       │
       ▼
     Tenant
```

**Cache MISS**

```text
Get("tenant-a")
       │
       ▼
   Cache absent/expired
       │
       ▼
 source.Get(...)
       │
       ▼
    Tenant
       │
       ▼
 store in cache
       │
       ▼
    return
```

### 5.13 Le TTL

Chaque entrée du cache a une durée de validité (ex. 30 secondes). Après expiration, l'entrée est considérée invalide et le store sous-jacent est de nouveau interrogé.

```text
Cache
 │
 ├── tenant-a
 │      expired ❌
 │
 ▼
source.Get()
```

### 5.14 Pourquoi accepter une légère incohérence est acceptable pour `Disabled`

Le TTL convient particulièrement bien à l'état `Disabled` : pendant la fenêtre de validité du cache, une instance peut encore considérer un tenant désactivé comme actif. C'est une incohérence temporaire **acceptée**.

```text
Disabled  → eventual propagation (TTL acceptable)
Banned    → immediate propagation (requires an event — Step 3)
```

Cette distinction se retrouve dans la propriété de `MemoryStore.IsBanned()` : contrairement à un `Get()` classique, `IsBanned` (et plus tard, dans `CachedStore`, son équivalent) contourne systématiquement le cache pour interroger directement la source de vérité.

### 5.15 Protection contre les appels dupliqués — `singleflight`

Un problème d'efficacité (pas de sécurité) subsiste malgré le `RWMutex` : si 500 requêtes concurrentes pour le même tenant arrivent exactement au moment d'un cache miss, elles peuvent toutes observer simultanément l'absence de l'entrée avant que l'une d'elles n'ait eu le temps de la remplir — provoquant 500 appels dupliqués vers la source de vérité (un phénomène connu sous le nom de *cache stampede* ou *thundering herd*).

La solution adoptée est `golang.org/x/sync/singleflight`, qui garantit qu'un seul appel réel part vers la source pour une clé donnée, les appelants concurrents attendant et recevant le même résultat :

```go
v, err, _ := cs.group.Do(string(id), func() (interface{}, error) {
    t, err := cs.source.Get(ctx, id)
    // ...
    return t, nil
})
```

### 5.16 Architecture complète de l'étape 2

```text
tenant-core/
│
├── tenant.go
│   │
│   ├── Tenant
│   └── Store
│
└── store/
    │
    ├── memory.go
    │   └── MemoryStore
    │
    └── cached.go
        └── CachedStore
```

```text
                  tenant.Store
                       ▲
                       │
              ┌────────┴────────┐
              │                 │
        MemoryStore        CachedStore
                                │
                                │ source
                                ▼
                           tenant.Store
```

Configuration typique :

```text
                 Manager
                    │
                    ▼
              CachedStore
                    │
                    ▼
              MemoryStore
```

### 5.17 Résumé de l'étape 2

| Élément | Responsabilité |
|---|---|
| `tenant.Store` | Contrat de lecture pour les tenants |
| `MemoryStore` | Stockage en mémoire |
| `RWMutex` | Protection des accès concurrents |
| `Get()` | Récupère un tenant (copie, jamais le pointeur interne) |
| `IsBanned()` | Vérification spécialisée de bannissement |
| `Disable()` / `SetState()` | Changement d'état, atomique sous `Lock()` |
| `CachedStore` | Ajoute un cache devant un `Store` |
| `TTL` | Expiration des entrées du cache |
| `singleflight` | Déduplication des appels concurrents sur un cache miss |
| `source Store` | Découple le cache de l'implémentation concrète |
| `ErrTenantNotFound` | Identification explicite d'un tenant inexistant |

> **L'étape 1 a permis d'identifier le tenant ; l'étape 2 permet de récupérer son état de façon sûre et efficace, tout en préparant les problématiques de concurrence et de cache.**

---

## 6. Étape 3 — Bannissement en temps réel

### 6.1 Objectif

Le cache de l'étape 2 était délibérément à cohérence *eventual* pour la désactivation. Mais pour un bannissement lié à une fraude ou un abus, ce comportement n'est pas acceptable :

```text
Instance A
    │
    ▼
tenant-A = Banned

Instance B (cache not expired)
    │
    ▼
tenant-A = Active   ❌
```

Le but de cette étape est d'introduire un `EventBus`, un `MemoryEventBus`, un `BanChecker`, et la règle selon laquelle `Ban()` est **synchrone**.

```text
              BAN
               │
               ▼
        state change
               │
               ▼
        publish event
               │
        ┌──────┴──────┐
        ▼             ▼
    Instance A     Instance B
        │             │
        ▼             ▼
   BanChecker     BanChecker
        │             │
        ▼             ▼
   immediate blocking
```

### 6.2 Pourquoi le TTL seul ne suffit pas

```text
TTL = 30 seconds

12:00:00 → tenant-A = Active
12:00:05 → Admin bans tenant-A

Another instance still holds:
tenant-A = Active (expires 12:00:30)
```

Sans mécanisme supplémentaire, cette instance pourrait accepter le tenant jusqu'à 12:00:30. Acceptable pour `Disabled`, inacceptable pour `Banned`.

```text
Disabled → eventual consistency → TTL acceptable
Banned   → near-immediate consistency → event required
```

### 6.3 `TenantEvent`

```go
type TenantEvent struct {
    TenantID  tenant.TenantID
    State     tenant.State
    Timestamp time.Time
}
```

```text
TenantEvent
├── TenantID
├── State
└── Timestamp
```

`TenantID` et `State` sont le strict minimum fonctionnel pour qu'un abonné sache quoi faire. `Timestamp` a été ajouté délibérément : sans lui, un futur composant (audit, logging) ne pourrait même pas répondre à *« quand ce changement a-t-il eu lieu ? »* — et, plus important encore, il devient indispensable pour résoudre un problème de cohérence temporelle (voir 6.9).

L'événement ne dit pas comment le changement doit être traité — il dit simplement : *« le tenant tenant-A est maintenant dans l'état Banned. »*

### 6.4 L'interface `EventBus`

```go
type EventBus interface {
    Publish(ctx context.Context, event TenantEvent) error
    Subscribe(handler func(TenantEvent)) error
}
```

```text
Publish
   │
   ▼
send an event

Subscribe
   │
   ▼
receive events
```

### 6.5 Pourquoi `EventBus` est une interface

```text
                 EventBus
                    ▲
                    │
          ┌─────────┴─────────┐
          │                   │
          ▼                   ▼
 MemoryEventBus          RedisEventBus
```

Le cœur du toolkit ne devrait connaître que `eventbus.EventBus` — pas Redis, NATS, Kafka, ou RabbitMQ (implémentations futures possibles). Même principe que `tenant.Store`.

### 6.6 MemoryEventBus

Une implémentation entièrement en mémoire, utilisée pour développer le mécanisme, le tester, et éviter d'avoir besoin de Redis dès les premières étapes.

```text
MemoryEventBus
      │
      ├── subscribers (handlers)
      │
      └── Publish()
```

```text
Publish(TenantEvent)
       │
       ▼
MemoryEventBus
       │
       ├───────────────┐
       ▼               ▼
 Subscriber A      Subscriber B
       │               │
       ▼               ▼
    handler()        handler()
```

### 6.7 Isoler les handlers par goroutine

Un détail d'implémentation important : ne **jamais** exécuter les handlers séquentiellement dans la même goroutine. Une mauvaise approche serait :

```go
for _, handler := range handlers {
    handler(event) // ❌ a slow handler blocks all the following ones
}
```

Si un handler est lent (`time.Sleep`) ou panique, tous les suivants sont retardés ou ne s'exécutent jamais. Le principe adopté :

```text
Publish
  │
  ├──► goroutine Handler A
  │
  ├──► goroutine Handler B
  │
  └──► goroutine Handler C
```

Chaque handler est isolé et démarre en parallèle des autres.

**Un second problème d'efficacité a été identifié** : la première version de `Publish()` retenait un `RLock()` pendant toute la durée d'exécution des handlers, bloquant tout appel concurrent à `Subscribe()`. Le correctif adopté : copier la liste des handlers sous `RLock`, relâcher le verrou immédiatement, puis lancer les goroutines depuis la copie — `Subscribe()` n'attend donc plus jamais les handlers en cours d'exécution.

### 6.8 Protection contre les panics — `recover()`

Un handler fourni par l'utilisateur ne doit jamais pouvoir faire tomber tout le processus avec un simple `panic(...)`. Chaque handler est donc exécuté avec un mécanisme de récupération :

```go
defer func() {
    if r := recover(); r != nil {
        // log
    }
}()
```

> **Un handler qui échoue ne doit jamais empêcher les autres handlers de recevoir l'événement.**

Point crucial pour un toolkit destiné à des applications externes : `recover()` ne fonctionne qu'**au sein de la même goroutine** que le `panic()` — il doit donc être placé à l'intérieur de la fonction lancée par `go`, jamais autour de l'appel à `Publish()` lui-même (qui est déjà retourné bien avant que le handler ne s'exécute réellement).

**Un trade-off accepté** : puisque chaque handler s'exécute dans sa propre goroutine, `Publish()` ne peut plus remonter directement les erreurs des handlers à l'appelant. `Publish()` retournant `nil` signifie donc *« j'ai réussi à démarrer la diffusion vers les handlers »*, pas *« tous les handlers ont traité l'événement avec succès »*.

### 6.9 Le BanChecker

L'EventBus transporte l'événement, mais il faut un composant qui **réagit** au bannissement.

```text
EventBus
   │
   │ TenantEvent{State: Banned}
   ▼
BanChecker
   │
   ▼
update its local state
```

```text
BanChecker
    │
    └── banned
         ├── tenant-A
         ├── tenant-C
         └── tenant-F
```

### 6.10 Pourquoi BanChecker existe en plus du Store

Le `Store` reste la source de vérité. `BanChecker` répond à une question beaucoup plus spécialisée : *« ce tenant est-il actuellement banni ? »*, avec une exigence de rapidité extrême.

```text
Request
   ↓
IsBanned(tenant-A)
   ↓
RAM (BanChecker)
   ↓
true/false
```

Si `IsBanned()` devait systématiquement appeler la source de vérité, 10 000 requêtes pour le même tenant produiraient 10 000 accès à la source. Avec `BanChecker`, cela devient 10 000 lectures RAM — la source n'est interrogée que lorsqu'un changement d'état doit être propagé (modèle **push**, par opposition à un modèle pull) :

```text
Source
   │
   │ "tenant-A is now Banned"
   ▼
EventBus
   │
   ▼
BanChecker
   │
   ▼
tenant-A → true
```

### 6.11 Le principe de priorité du bannissement

`Banned` doit être prioritaire sur le cache normal. Par exemple, si `CachedStore` affiche encore `Active` alors que `BanChecker` sait déjà `Banned`, le système doit traiter le tenant comme banni. `BanChecker` devient une sorte de barrière de sécurité placée devant le chemin normal :

```text
                 HTTP Request
                      │
                      ▼
                  Resolver
                      │
                      ▼
                   TenantID
                      │
                      ▼
                BanChecker
                      │
                ┌─────┴─────┐
                │           │
             Banned       Not banned
                │           │
                ▼           ▼
             Reject      CachedStore
                            │
                            ▼
                          Tenant
```

### 6.12 Résolution des conflits par timestamp — ordre causal

Un problème de cohérence plus profond a été identifié : charger un **snapshot initial** au démarrage d'une instance (nécessaire dans un environnement multi-instance, pour connaître l'état des bannissements passés avant de s'abonner) peut entrer en conflit avec un **événement récent** reçu entre-temps.

**Scénario problématique** : un tenant est débanni (`Active`) juste avant qu'un snapshot obsolète (démarré avant le débannissement, mais dont l'écriture arrive après l'événement) n'écrase cette information avec `Banned` — la donnée en mémoire redeviendrait alors incorrecte.

**La solution adoptée** : chaque entrée de `BanChecker` (pas seulement un booléen `banned`) est associée à un **timestamp de dernière mise à jour**. Une écriture n'est appliquée que si son timestamp est **plus récent** (ou égal) à celui déjà stocké — garantissant qu'une information obsolète ne peut jamais écraser une information plus fraîche, quel que soit l'ordre d'exécution réel des goroutines.

**Règle également établie** : `Subscribe()` doit toujours être appelé **avant** le chargement du snapshot initial, jamais l'inverse — sinon un événement publié entre-temps pourrait être manqué (jamais reçu par aucun mécanisme).

### 6.13 Ban() synchrone

Une distinction essentielle entre synchrone et asynchrone :

**Synchrone (adopté)**

```text
Ban()
 │
 ├── state change
 │
 ├── publish event
 │
 └── return
```

La fonction ne retourne qu'une fois les opérations qu'elle garantit effectuées.

**Asynchrone (rejeté)**

```text
Ban()
 │
 └── start goroutine
          │
          └── publish later

Ban() returns immediately
```

Le problème d'une version asynchrone : l'appelant ne saurait jamais si le bannissement a réellement été publié.

### 6.14 Pourquoi Ban() doit être synchrone

```go
err := Ban(ctx, tenantID)
```

`err == nil` doit signifier *« l'opération de bannissement a été réalisée avec succès selon les garanties de cette couche »*, et `err != nil` doit signifier que l'opération n'a pas pu être correctement effectuée — permettant à l'appelant de réagir immédiatement (par exemple en signalant l'échec).

### 6.15 Le flux de Ban()

```text
Ban(tenant-A)
      │
      ▼
change state
      │
      ▼
Tenant = Banned
      │
      ▼
Publish(TenantEvent)
      │
      ▼
return
```

### 6.16 Le problème de non-atomicité (identifié dès cette étape)

`SetState()` suivi de `Publish()` n'est **pas** une transaction atomique. Deux scénarios problématiques :

```text
SetState() → SUCCESS
Publish()  → FAILURE
```

La source de vérité dit `Banned`, mais l'`EventBus` n'a transmis aucun événement — d'autres instances peuvent ne pas savoir immédiatement que le tenant est banni.

```text
Publish()  → SUCCESS
SetState() → FAILURE
```

Encore plus dangereux : d'autres instances croiraient le tenant banni, alors que la source de vérité dit encore `Active` — un **événement mensonger**.

**Décision adoptée** : `SetState → Publish` (jamais l'inverse), avec l'idée qu'un mécanisme plus robuste (Outbox) pourrait plus tard résoudre la durabilité et l'atomicité (voir étape 7, section 10.8, et [limitations futures](#16-limitations-et-évolutions-futures)).

### 6.17 Pourquoi l'Outbox n'était pas nécessaire à cette étape

> Construire d'abord le contrat et le comportement corrects, puis renforcer progressivement la fiabilité.

Les fondations construites ici (`TenantEvent`, `EventBus`, `MemoryEventBus`, `BanChecker`, `Ban()`) ont permis plus tard, à l'étape 7, d'introduire `RedisEventBus` comme un simple **changement d'adaptateur** — pas une réécriture du cœur métier.

### 6.18 Architecture complète de l'étape 3

```text
tenant-core/
│
├── tenant.go
│   ├── Tenant
│   ├── State
│   ├── TenantID
│   └── Store
│
├── eventbus/
│   ├── event.go
│   │   └── TenantEvent
│   │
│   ├── eventbus.go
│   │   └── EventBus
│   │
│   └── memory.go
│       └── MemoryEventBus
│
└── banchecker/
    └── BanChecker
```

```text
                  tenant
                    │
                    │ TenantID / State
                    ▼
               TenantEvent
                    │
                    ▼
                EventBus
                    │
                    ▼
             MemoryEventBus
                    │
                    ▼
               BanChecker
```

### 6.19 Architecture distribuée déjà préparée

```text
Today
              Instance A
                  │
             MemoryEventBus
                  │
                  ▼
             BanChecker A

Tomorrow (with Redis)
Instance A                         Instance B
    │                                  │
    ▼                                  ▼
Ban()                            BanChecker
    │                                  ▲
    ▼                                  │
RedisEventBus ─────── Redis ───────────┘
```

Le contrat `EventBus` ne change jamais — seule l'implémentation passe de `MemoryEventBus` à `RedisEventBus`.

### 6.20 Résumé de l'étape 3

| Élément | Responsabilité |
|---|---|
| `TenantEvent` | Représente un changement d'état |
| `EventBus` | Contrat publish/subscribe |
| `MemoryEventBus` | `EventBus` local, en mémoire |
| `Publish()` | Diffuse un événement |
| `Subscribe()` | Enregistre un handler |
| Une goroutine par handler | Isolation et non-blocage entre handlers |
| `recover()` | Empêche un panic de tuer le traitement |
| `BanChecker` | Maintient une connaissance immédiate des tenants bannis |
| `Ban()` | Déclenche le changement de bannissement et sa propagation, de façon synchrone |
| Résolution par timestamp | Empêche un snapshot obsolète d'écraser un événement plus récent |
| TTL | Toujours acceptable pour `Disabled`, mais insuffisant pour `Banned` |
| Redis | Sera la future implémentation distribuée (étape 7) |

> **L'étape 2 acceptait une propagation retardée via TTL ; l'étape 3 introduit un canal d'événements qui transforme un bannissement en une information active, propagée immédiatement.**

Principe architectural fondamental établi :

- **Store** = « Quelle est la vérité sur le tenant ? »
- **Cache** = « Comment éviter de relire cette vérité trop souvent ? »
- **EventBus** = « Comment annoncer qu'elle vient de changer ? »
- **BanChecker** = « Comment appliquer immédiatement la règle critique de bannissement ? »

---

## 7. Étape 4 — RateLimiter et CacheKeyer

### 7.1 Objectif

Cette étape ajoute deux mécanismes transversaux qui renforcent le toolkit sans le rendre dépendant d'une technologie particulière, en conservant le principe établi depuis l'étape 1 : *le package `tenant` définit les contrats, les sous-packages fournissent les implémentations.*

Deux protections manquaient encore :

**Protection n°1 — abus de requêtes.** Un tenant pourrait envoyer un volume disproportionné de requêtes (`tenant-A → 10 000 requêtes/seconde`) et monopoliser les ressources du serveur.

**Protection n°2 — isolation du cache.** Une clé de cache applicative naïve comme `"user:123"` pourrait provoquer une collision entre deux tenants ayant chacun un utilisateur d'ID `123` :

```text
tenant-A + user-123
tenant-B + user-123
```

Une clé globale `user:123` créerait une fuite de données entre tenants — exactement le genre de bug catastrophique qu'un système multi-tenant doit structurellement empêcher.

### 7.2 Architecture générale

```text
                    ┌──────────────────────┐
                    │     Application      │
                    └──────────┬───────────┘
                               │
                               ▼
                     ┌───────────────────┐
                     │ Tenant Middleware │
                     └─────────┬─────────┘
                               │
                               ▼
                       ┌──────────────┐
                       │    Tenant    │
                       │   Context    │
                       └──────┬───────┘
                              │
              ┌───────────────┴────────────────┐
              │                                │
              ▼                                ▼
      ┌───────────────┐                ┌───────────────┐
      │ RateLimiter   │                │  CacheKeyer   │
      └───────┬───────┘                └───────┬───────┘
              │                                │
              ▼                                ▼
       Per-tenant limit                 Isolated key
              │                                │
              └───────────────┬────────────────┘
                              │
                              ▼
                       Business application
```

`RateLimiter` et `CacheKeyer` sont deux composants indépendants : aucun ne devrait connaître Redis directement, ce qui permet ensuite différentes implémentations (`MemoryRateLimiter`, un futur `RedisRateLimiter` ; `DefaultCacheKeyer`).

### 7.3 RateLimiter — responsabilité

`RateLimiter` répond à une question très simple : *« ce tenant est-il encore autorisé à faire cette requête ? »* Il ne décide ni quel tenant est utilisé, ni comment il est résolu ou stocké, ni comment répondre en HTTP — il se concentre uniquement sur la limitation.

### 7.4 Pourquoi RateLimiter est lié au tenant

Une limite globale naïve pénaliserait les tenants les uns contre les autres :

```text
Tenant A ─┐
Tenant B  ├──► same counter (❌ bad)
Tenant C ─┘
```

L'approche correcte isole chaque compteur par tenant :

```text
Tenant A → counter A → 100 req/min
Tenant B → counter B → 100 req/min
Tenant C → counter C → 100 req/min
```

La clé logique du rate limiting est donc le `TenantID`.

### 7.5 Fonctionnement — exemple

Avec une limite de 5 requêtes/minute pour `tenant-A` :

```text
Request 1 → ALLOW
Request 2 → ALLOW
Request 3 → ALLOW
Request 4 → ALLOW
Request 5 → ALLOW
Request 6 → DENY
```

`tenant-B`, avec son propre compteur indépendant (`0 / 5` utilisé), reste autorisé.

### 7.6 Implémentation en mémoire

```text
MemoryRateLimiter
       │
       ▼
┌───────────────────────────┐
│ map[TenantID]*bucket      │
├───────────────────────────┤
│ tenant-A → counter        │
│ tenant-B → counter        │
│ tenant-C → counter        │
└───────────────────────────┘
```

Puisque plusieurs goroutines HTTP peuvent accéder simultanément à cette structure, son état partagé doit être protégé — le même principe qu'avec `MemoryStore`.

L'implémentation concrète adoptée repose sur une clé (`TenantID`) associée à un limiteur individuel de type *token bucket*, chaque tenant ayant son propre « seau de jetons » (voir [section 14](#14-concurrence-et-thread-safety) pour les détails de concurrence, notamment l'utilisation de `LoadOrStore` pour garantir qu'un seul limiteur survit par tenant même sous accès concurrent).

**Le principe métier — deux grands modèles conceptuels de rate limiting**, tels que présentés dans la documentation d'introduction :

| Modèle | Principe | Cas d'usage |
|---|---|---|
| **Token Bucket** | Un seau se remplit de jetons à taux constant ; chaque requête consomme un jeton ; seau vide → bloqué | Idéal pour des rafales modérées |
| **Leaky Bucket** | Un seau qui fuit à taux constant ; les requêtes arrivent en rafale mais partent à rythme régulier | Idéal pour lisser les pics de trafic |

### 7.7 Fenêtre temporelle / TTL

Le `RateLimiter` doit aussi savoir quand une limite se réinitialise. Selon l'algorithme choisi, cela peut être implémenté avec une fenêtre fixe, une fenêtre glissante, un token bucket, ou un leaky bucket. Pour une première implémentation, une stratégie simple et déterministe reste préférable.

### 7.8 Pourquoi RateLimiter n'est pas immédiatement intégré à Manager

`Manager` reste principalement responsable de `Request → Resolver → TenantID → Store → Tenant`. Le rate limiting est une responsabilité **additionnelle**, qui pourrait être intégrée au pipeline (avant ou après la résolution complète du tenant), mais `Manager` ne doit pas devenir un objet conscient de chaque préoccupation du toolkit — chaque middleware ou composant appelant reste libre de l'invoquer explicitement là où c'est pertinent.

### 7.9 CacheKeyer — responsabilité

Transformer une clé applicative logique en une clé réellement isolée par tenant :

```text
logical key: "user:123"
        ↓
tenant-A:user:123
```

### 7.10 Contrat CacheKeyer

```go
type CacheKeyer interface {
    Key(id TenantID, key string) string
}
```

Reçoit un `TenantID` et une clé applicative, renvoie une clé isolée :

```text
keyer.Key("tenant-A", "users:123")
        ↓
"tenant-A:users:123"
```

### 7.11 CacheKeyer ne stocke rien

Il ne fait ni `Get`, ni `Set`, ni `Delete` — seulement la construction de clés :

```text
CacheKeyer
    │
    └── key construction

Cache
    │
    └── data storage
```

Ces deux responsabilités restent strictement séparées :

```text
                    Application
                         │
                         ▼
                 ┌──────────────┐
                 │ CacheKeyer   │
                 └──────┬───────┘
                        │
                        ▼
               tenant-A:user:123
                        │
                        ▼
                  ┌──────────┐
                  │  Cache   │
                  └──────────┘
```

Le cache peut être une implémentation en mémoire, Redis, Memcached, ou autre — `CacheKeyer` reste identique.

### 7.12 Isolation : le principe fondamental de cette étape

```text
                    TenantID
                       │
        ┌──────────────┼──────────────┐
        │              │              │
        ▼              ▼              ▼
      Store        CacheKeyer     RateLimiter
        │              │              │
        ▼              ▼              ▼
     Tenant       Isolated cache  Isolated limit
```

> **Toute ressource partagée doit être explicitement scopée par TenantID.**

C'est ce qui empêche un tenant de consommer accidentellement le quota d'un autre, de lire des données mises en cache pour un autre tenant, de provoquer des collisions de clés, ou de contourner l'isolation logique du système.

### 7.13 Architecture globale après cette étape

```text
                         HTTP Request
                              │
                              ▼
                     ┌─────────────────┐
                     │    Resolver     │
                     └────────┬────────┘
                              │
                         TenantID
                              │
                              ▼
                     ┌─────────────────┐
                     │  RateLimiter    │
                     └────────┬────────┘
                              │
                    ┌─────────┴─────────┐
                    │                   │
                  DENY                ALLOW
                    │                   │
                    ▼                   ▼
                 HTTP 429           Store
                                        │
                                        ▼
                                     Tenant
                                        │
                         ┌──────────────┴──────────────┐
                         │                             │
                         ▼                             ▼
                    BanChecker                    tenantctx
                         │                             │
                         ▼                             ▼
                  tenant state                Application
                         │
                         ▼
                    Banned?
                         │
                  ┌──────┴──────┐
                  │             │
                 YES            NO
                  │             │
                  ▼             ▼
               Reject       Continue


       ┌─────────────────────────────────────┐
       │             Cache layer              │
       │                                     │
       │ TenantID + logical key              │
       │             │                       │
       │             ▼                       │
       │        CacheKeyer                   │
       │             │                       │
       │             ▼                       │
       │     tenant-A:users:123              │
       │             │                       │
       │             ▼                       │
       │           Cache                     │
       └─────────────────────────────────────┘
```

### 7.14 Principe d'agnosticisme (rappel)

```text
             tenant
               │
       ┌───────┴────────┐
       │                │
   Interface       Interface
       │                │
       ▼                ▼
MemoryRateLimiter   CacheKeyer
       │
       ▼
Implementation

Then later:

RateLimiter
     │
     ├── MemoryRateLimiter
     │
     └── RedisRateLimiter (ratelimit/redis, see §7.16)
```

### 7.15 Tests à prévoir

**RateLimiter** — vérifier au minimum : la première requête est autorisée ; les requêtes jusqu'à la limite sont autorisées ; une requête dépassant la limite est rejetée ; une nouvelle fenêtre redevient autorisée ; un tenant ne bloque jamais un autre ; l'accès concurrent ne produit aucune condition de course (`go test -race`).

**CacheKeyer** — vérifier que `tenant-A + users:123` et `tenant-B + users:123` produisent des clés différentes (`tenant-A:users:123 != tenant-B:users:123`) — un test d'isolation fondamental.

### 7.16 RedisRateLimiter — l'alternative distribuée

`ratelimit.TenantRateLimiter` (décrit ci-dessus) conserve son état de quota en mémoire locale du processus via `sync.Map` — extrêmement rapide (quelques centaines de nanosecondes par appel, voir `ratelimit/ratelimit_bench_test.go`), sans aucune dépendance d'infrastructure, mais chaque instance de l'application applique son propre quota indépendant. Dans un déploiement multi-instance, un tenant limité à 100 req/min pourrait en principe envoyer jusqu'à `100 × N` requêtes par minute à travers `N` instances avant qu'un seul limiteur local d'instance ne rejette quoi que ce soit.

`ratelimit/redis` (un sous-module Go séparé, même logique que `middleware/gin`/`eventbus/redis` — aucun consommateur du cœur n'est forcé d'embarquer un client Redis) fournit `RedisRateLimiter`, satisfaisant la même interface `ratelimit.RateLimiter` (`Allow(t *tenant.Tenant) bool`) mais stockant le compteur dans Redis, de sorte que le quota est réellement partagé entre toutes les instances pointant vers le même serveur Redis.

**C'est un trade-off délibéré, pas un remplacement strictement meilleur** :

- **Latence.** `TenantRateLimiter.Allow()` s'exécute en environ 320-460ns. `RedisRateLimiter.Allow()` mesuré à environ 816µs par appel même contre `miniredis` (un faux Redis en mémoire, sur boucle TCP locale) — un vrai Redis distant ajouterait encore de la latence réseau par-dessus. C'est de l'ordre de 2 000x plus lent. `RedisRateLimiter` n'est pas un remplacement performant équivalent ; c'est ce vers quoi on se tourne spécifiquement quand un quota partagé compte plus que ce coût.
- **Précision de l'algorithme.** `RedisRateLimiter` implémente un compteur à fenêtre fixe (une clé Redis par tenant par fenêtre, incrémentée atomiquement et avec TTL par un unique script Lua), pas une fenêtre glissante ni un véritable token bucket distribué. Une conséquence connue et acceptée : un tenant peut légalement envoyer jusqu'à la limite juste avant une limite de fenêtre et de nouveau la même limite juste après, donc dans le pire cas environ 2x le quota configuré peut passer sur une courte période chevauchant deux fenêtres. C'est un simple V1 à un aller-retour — pas une prétention à un rate limiting distribué parfaitement précis.
- **Mode d'échec.** Quand Redis est inaccessible, `RedisRateLimiter` bascule vers une `FailurePolicy` explicite et configurable : `FailOpen` (par défaut — autorise la requête, privilégiant la disponibilité) ou `FailClosed` (la refuse, privilégiant l'application stricte). `TenantRateLimiter` n'a pas de mode d'échec équivalent puisqu'il n'a aucune dépendance externe susceptible d'échouer.

**Recommandation** : utilisez `TenantRateLimiter` par défaut. Ne vous tournez vers `RedisRateLimiter` que si vous faites tourner plusieurs instances et que le quota d'un tenant doit réellement être appliqué comme un seul budget partagé entre toutes — et acceptez, en connaissance de cause, à la fois le coût de latence et l'approximation à fenêtre fixe documentés ci-dessus.

### 7.17 Résumé de l'étape 4

| Composant | Responsabilité | Ne doit pas connaître |
|---|---|---|
| `RateLimiter` | Limiter les requêtes par tenant | HTTP, Redis |
| `MemoryRateLimiter` | Implémentation locale du rate limiting | Logique HTTP |
| `CacheKeyer` | Construire des clés isolées | Le stockage du cache |
| `Cache` | Stocker les données | La logique de construction de clés |
| `TenantID` | Identifier le tenant | L'infrastructure |
| `Manager` | Résoudre/récupérer le tenant | Les détails du cache |
| `BanChecker` | Vérifier un bannissement | Le transport HTTP |

> **La règle fondamentale de cette étape : toute ressource partagée doit être explicitement scopée par TenantID.**

---

## 8. Étape 5 — RBAC et Metrics

### 8.1 Objectif

Après l'étape 4, le toolkit sait identifier le tenant, récupérer son état, gérer le cache, détecter un bannissement, limiter les requêtes, et construire des clés de cache isolées. Deux questions restaient ouvertes :

**Question 1 — Autorisation.** *« Ce tenant est-il autorisé à effectuer cette opération ? »* — c'est le rôle du **RBAC**.

**Question 2 — Observabilité.** *« Combien de requêtes sont traitées ? Combien sont rejetées ? Combien de tenants sont actifs ? Combien de bannissements surviennent ? »* — c'est le rôle de **Metrics**.

### 8.2 Architecture générale

```text
                         HTTP Request
                              │
                              ▼
                         Resolver
                              │
                              ▼
                           Tenant
                              │
                    ┌─────────┴─────────┐
                    │                   │
                    ▼                   ▼
              RateLimiter             RBAC
                    │                   │
                    │             Authorization
                    │                   │
                    └─────────┬─────────┘
                              │
                              ▼
                         BanChecker
                              │
                              ▼
                       Application
                              │
                              │
                    ┌─────────┴─────────┐
                    │                   │
                    ▼                   ▼
                 Metrics           Prometheus
```

RBAC (sécurité/autorisation) et Metrics (observabilité) sont indépendants et ne doivent jamais être mélangés.

### 8.3 Partie 1 — RBAC

**Principe.** *Role-Based Access Control.* Plutôt que de coder en dur *« Sylvinhio peut faire X »*, on définit `Rôle → Permissions`, et un tenant a alors un ou plusieurs rôles — via le champ `Roles []Role` déjà présent sur `Tenant` depuis l'étape 1.

**Exemple concret**

```text
Tenant A          Tenant B
Roles: admin      Roles: viewer

admin
 ├── tenant.read
 ├── tenant.write
 ├── user.read
 └── user.write

viewer
 └── tenant.read

Tenant A + tenant.write → ALLOW
Tenant B + tenant.write → DENY
```

### 8.4 Séparer le rôle de la permission

Il ne faut surtout jamais coder en dur `if tenant.Roles[0] == "admin" { ... }` dans toute l'application. À la place :

```text
Tenant
  │
  └── Roles
       │
       ▼
      RBAC
       │
       ▼
Permission
       │
       ├── ALLOW
       └── DENY
```

L'application demande simplement : *« ce tenant a-t-il cette permission ? »*, et le composant RBAC s'occupe du reste.

### 8.5 Contrat minimal — `Authorizer` / `Can`

```go
type Authorizer interface {
    Can(t *Tenant, permission string) bool
}
```

Le contrat exprime seulement : *« ce tenant peut-il effectuer cette action ? »* — sans aucune connaissance de HTTP, Gin, Echo, Redis, PostgreSQL, ou Prometheus.

**Implémentation adoptée** — les définitions de rôles/permissions sont organisées **par tenant** (pas une table de rôles globale unique partagée par tout le monde), les permissions d'un rôle étant représentées comme un **ensemble** (`map[string]struct{}`) plutôt qu'une simple liste, pour des recherches en O(1) au lieu d'une recherche linéaire :

```text
Tenant A
   │
   ├── admin
   │     ├── users:read
   │     ├── users:write
   │     └── users:delete
   │
   └── viewer
         └── users:read
```

Cette organisation par tenant est fondamentale : deux tenants peuvent avoir un rôle avec le **même nom** mais des permissions **complètement différentes** — le rôle `admin` du tenant A n'implique rien sur ce que signifie `admin` pour le tenant B.

### 8.6 Pourquoi Tenant est fourni à RBAC

RBAC ne devrait pas faire une seconde requête au Store pour connaître les rôles — `Manager` a déjà récupéré le `*Tenant` complet, y compris `Roles`. Cela évite une seconde lecture inutile.

```text
Manager
   │
   ▼
Tenant
   │
   ├── ID
   ├── State
   └── Roles
          │
          ▼
        RBAC
```

### 8.7 Exemple d'utilisation

```go
allowed := rbac.Can(tenant, "users.create")
```

Le toolkit reste agnostique sur la façon dont l'application traduit un refus :

```text
RBAC
 │
 └── false
       │
       ├── HTTP → 403 Forbidden
       ├── gRPC → PermissionDenied
       └── CLI → error message
```

Même principe d'agnosticisme que `AdminService` (voir étape 7).

### 8.8 RBAC et multi-tenancy — deux questions distinctes

- **Tenant** répond à : *« de quel espace isolé provient cette requête ? »*
- **RBAC** répond à : *« que peut faire cet acteur dans cet espace ? »*

```text
Resolver → Tenant A → RBAC → Permission
```

RBAC ne remplace jamais le mécanisme de tenant ; il s'y ajoute.

### 8.9 Évolutivité — Rôle → Permissions → Action

Une mauvaise conception fige les capacités dans un `if role == "admin"`. Une architecture évolutive relie un rôle à une liste de permissions, qui peut être étendue sans toucher à la logique applicative :

```text
admin
    users.read
    users.create
    users.update
    users.delete

teacher
    users.read

viewer
    users.read
```

### 8.10 Partie 2 — Metrics

Un toolkit multi-tenant de production doit pouvoir répondre à des questions comme : combien de requêtes sont reçues ? combien sont rejetées ? combien sont bloquées par le rate limiter ? combien de tenants sont bannis ? combien de temps prend la résolution du tenant ? combien d'erreurs le Store produit-il ? C'est le rôle de Metrics, avec Prometheus envisagé comme backend d'exposition.

### 8.11 Pourquoi une abstraction Metrics

Il serait néfaste que `Manager` contienne directement des types `prometheus.CounterVec`/`prometheus.HistogramVec`, rendant le cœur dépendant de `github.com/prometheus/client_golang` — perdant l'agnosticisme.

```text
tenant
 │
 └── Metrics contract
        │
        ├── NoopMetrics / MemoryMetrics (dev)
        │
        └── PrometheusMetrics (production)
```

### 8.12 Contrat Metrics (conceptuel) et implémentation adoptée

Le contrat conceptuel minimal initialement envisagé :

```go
type Metrics interface {
    IncRequest()
    IncRBACDenied()
}
```

**L'interface réellement adoptée et implémentée**, plus proche des besoins réels exprimés dans le cahier des charges (exigence fonctionnelle #5 — latence, RPS, taux d'erreur), expose trois opérations paramétrées par tenant :

```go
type MetricsCollector interface {
    IncRequests(ctx context.Context, tenantID tenant.TenantID)
    ObserveLatency(ctx context.Context, tenantID tenant.TenantID, duration time.Duration)
    IncErrors(ctx context.Context, tenantID tenant.TenantID)
}
```

Une implémentation `MemoryMetrics` maintient, **par tenant**, des compteurs `requests`, `errors`, `latencySum`, et `latencyCount` (permettant de calculer la latence moyenne), combinant deux niveaux de concurrence (voir [section 14](#14-concurrence-et-thread-safety)) : `sync.Map` pour la collection dynamique de tenants, et `atomic.Int64` pour chaque compteur individuel.

### 8.13 Types de métriques (modèle Prometheus)

**Counter** — une valeur qui ne fait qu'augmenter (`tenant_requests_total`). Utilisé pour compter les requêtes, erreurs, refus RBAC, refus RateLimiter, bannissements.

**Histogram** — mesure une distribution (`tenant_resolution_duration_seconds`), permettant de détecter une dégradation de performance.

**Gauge** — une valeur qui peut monter et descendre (`tenants_active`).

### 8.14 Exemple de métriques utiles

```text
tenant_requests_total
tenant_requests_rejected_total
tenant_ratelimit_rejected_total
tenant_rbac_denied_total
tenant_resolution_errors_total
tenant_resolution_duration_seconds
tenant_banned_total
```

Le but n'est pas de créer des centaines de métriques, mais de privilégier un petit nombre de métriques réellement utiles.

### 8.15 Attention : cardinalité des labels Prometheus

Un point particulièrement important dans un système multi-tenant : chaque combinaison de labels crée une série temporelle Prometheus distincte. Utiliser `tenant_id` directement comme label pour une plateforme comptant des dizaines de milliers de tenants peut créer une explosion de cardinalité.

```text
Bad idea
tenant_requests_total{tenant_id="..."} for every tenant without thought

Preferable
tenant_requests_total{status="success", source="api"}
tenant_rbac_denied_total{permission="users.read"}
```

> **Règle adoptée : ne jamais utiliser une donnée utilisateur à forte cardinalité comme label Prometheus sans justification — particulièrement vrai pour `TenantID`.**

### 8.16 Séparation RBAC / Metrics

`RBAC → Prometheus` directement ne devrait jamais se produire. RBAC fait son travail (`Can(...)`), puis une couche supérieure enregistre le résultat dans les métriques :

```text
             ┌──────────────┐
             │     RBAC     │
             └──────┬───────┘
                    │
                 result
                    │
                    ▼
             ┌──────────────┐
             │    Metrics   │
             └──────┬───────┘
                    │
                    ▼
                Prometheus
```

Sinon RBAC deviendrait dépendant de Prometheus, brisant l'agnosticisme.

### 8.17 Architecture des packages

```text
tenant-core/
│
├── tenant.go
│
├── tenantctx/
├── resolver/
├── store/
├── eventbus/
│
├── middleware/
│   ├── nethttp/
│   ├── gin/
│   ├── echo/
│   └── chi/
│
├── admin/
├── tenanttest/
├── rbac/
│
└── metrics/
    └── prometheus/ (envisioned adapter)
```

### 8.18 Relation avec les interfaces Go (typage structurel, rappel)

```go
type Metrics interface {
    IncRequest()
    IncRBACDenied()
}
```

Une implémentation `PrometheusMetrics` possédant les bonnes méthodes satisfait automatiquement `tenant.Metrics` sans déclaration explicite — même mécanisme que `tenant.Store`, `tenant.Resolver`, `tenant.AdminStore`, `eventbus.EventBus`.

### 8.19 Tests

**RBAC** — tester `admin + users.read → ALLOW`, `admin + users.delete → ALLOW`, `viewer + users.read → ALLOW`, `viewer + users.delete → DENY`, un tenant sans rôle → `DENY`, plusieurs rôles → comportement correct, et surtout vérifier qu'un tenant n'obtient jamais les permissions d'un autre (tenant inconnu, rôle inconnu).

**Metrics** — vérifier qu'une requête incrémente le bon compteur, qu'un refus RBAC incrémente son compteur dédié, qu'un refus RateLimiter incrémente le sien. Pour Prometheus, vérifier aussi que les métriques produites sont correctement exposées dans le format attendu.

### 8.20 Ce que cette étape ajoute au toolkit

```text
Before                          After Step 5
Toolkit                        Toolkit
   │                              │
   ├── Identification             ├── Identification (Resolver)
   ├── Storage                    ├── Isolation (Store, CacheKeyer, tenantctx)
   ├── Cache                      ├── Security (BanChecker, RateLimiter, RBAC)
   ├── Ban                        └── Observability (Metrics → Prometheus)
   └── Rate limiting
```

### 8.21 Principes architecturaux adoptés

1. **RBAC ne connaît pas HTTP** — RBAC produit une décision d'autorisation ; HTTP la traduit en 403.
2. **Metrics ne connaît pas la logique métier** — Metrics mesure ; Prometheus collecte.
3. **Le cœur ne dépend pas de Prometheus** — `tenant → contrat Metrics → adaptateur Prometheus`.
4. **Le tenant reste la frontière d'isolation** — `TenantID → Store / RateLimiter / Cache / RBAC`.
5. **Les interfaces restent minimales** — chaque composant n'expose que ce dont son consommateur a besoin.

### 8.22 Résumé de l'étape 5

| Composant | Responsabilité |
|---|---|
| `RBAC` | Vérifie les permissions d'un tenant |
| `Role` | Regroupe des permissions |
| `Permission` | Représente une capacité métier |
| `Authorizer` / `Can` | Contrat d'autorisation |
| `Metrics` / `MetricsCollector` | Contrat d'observabilité |
| `MemoryMetrics` / `PrometheusMetrics` | Implémentations du contrat |
| `Counter` | Compte des événements |
| `Histogram` | Mesure des durées/distributions |
| `Gauge` | Mesure une valeur variable |

> **RBAC décide « qui peut faire quoi », tandis que Metrics permet de savoir « ce qui se passe réellement dans le système ».**

Progression cohérente à travers les cinq premières étapes : identification → données → sécurité → protection des ressources → autorisation + observabilité.

---

## 9. Étape 6 — Adaptateurs de framework

### 9.1 Le problème à résoudre

Le cœur de `tenant-core` contient une logique indépendante du framework :

```text
HTTP Request
     │
     ▼
Identify the tenant
     │
     ▼
Retrieve the tenant
     │
     ▼
Put the tenant in the context
     │
     ▼
Application handler
```

Mais chaque framework Go construit ses middlewares différemment :

```text
net/http    func(next http.Handler) http.Handler
Gin         func(c *gin.Context)
Echo        func(next echo.HandlerFunc) echo.HandlerFunc
Chi         func(next http.Handler) http.Handler
```

L'objectif : surtout, ne jamais réécrire la logique multi-tenant quatre fois. D'où les **adaptateurs de framework**.

### 9.2 Architecture générale

```text
                       ┌──────────────────────┐
                       │     Application      │
                       │   Go + HTTP Framework │
                       └──────────┬───────────┘
                                  │
                    ┌─────────────┴─────────────┐
                    │                           │
                 Middleware                 Middleware
                    │                           │
          ┌─────────┼─────────┬─────────┬──────┘
          │         │         │         │
          ▼         ▼         ▼         ▼
       net/http    Gin       Echo      Chi
          │         │         │         │
          └─────────┴─────────┼─────────┘
                              │
                              ▼
                       tenant.Manager
                              │
                              ▼
                    ┌──────────────────┐
                    │    Resolver      │
                    │ "Which tenant?"  │
                    └────────┬─────────┘
                             │
                             ▼
                    ┌──────────────────┐
                    │      Store       │
                    │ "What is its     │
                    │      state?"     │
                    └────────┬─────────┘
                             │
                             ▼
                       *tenant.Tenant
                             │
                             ▼
                    tenantctx.WithTenant()
                             │
                             ▼
                       HTTP Request
                             │
                             ▼
                     Business handler
```

> **Les frameworks vivent en dehors du cœur du toolkit. Le cœur ne connaît ni Gin, ni Echo, ni Chi.**

### 9.3 Le cœur : `tenant.Manager`

```go
type Manager struct {
    resolver Resolver
    store    Store
}

func (m *Manager) Resolve(r *http.Request) (*Tenant, error)
```

```text
HTTP Request
     │
     ▼
Resolver.Resolve()
     │
     ▼
TenantID
     │
     ▼
Store.Get()
     │
     ▼
*Tenant
```

`Manager` n'a absolument aucune idée si la requête vient de Gin, Echo, Chi, ou `net/http` — et c'est **délibéré**.

**Note de conception importante** : `Manager.Resolve()` ne construit **pas** lui-même un `context.Context`. Faire dépendre `tenant.go` (package racine) de `tenantctx` créerait une dépendance circulaire (`tenant → tenantctx → tenant`, puisque `tenantctx` dépend déjà de `tenant` pour le type `*Tenant`). C'est donc la responsabilité de chaque adaptateur de framework de combiner `Manager.Resolve()` et `tenantctx.WithTenant()`.

**Fail-fast à la construction** : `tenant.New(options...)` panique si `Resolver` ou `Store` ne sont pas fournis après application des options — une dépendance requise manquante est une erreur de configuration du programme, détectée immédiatement, pas une erreur de traitement de requête gérée via `error`.

### 9.4 Le rôle de tenantctx

Une fois que `Manager` fournit `*tenant.Tenant`, cette information doit être transmise aux handlers via le `context.Context` standard :

```go
ctx := tenantctx.WithTenant(r.Context(), t)
```

Puis remplacer la requête par ce nouveau contexte. Le handler peut ensuite faire :

```go
t := tenantctx.FromContext(r.Context())
```

```go
func GetUsers(w http.ResponseWriter, r *http.Request) {
    t := tenantctx.FromContext(r.Context())
    // use t...
}
```

Cette logique métier fonctionne à l'identique derrière les quatre adaptateurs.

### 9.5 Adaptateur net/http

**Fichier** : `middleware/nethttp.go`

**Signature** : `func Wrap(m *tenant.Manager, next http.Handler) http.Handler`

```text
Request
   │
   ▼
m.Resolve(r)
   │
   ├── error → HTTP 404
   │
   ▼
Tenant
   │
   ▼
tenantctx.WithTenant(...)
   │
   ▼
r.WithContext(...)
   │
   ▼
next.ServeHTTP(...)
```

**Code essentiel**

```go
ctx := tenantctx.WithTenant(r.Context(), t)
next.ServeHTTP(w, r.WithContext(ctx))
```

C'est l'adaptateur de référence, utilisant directement les primitives HTTP standard de Go. Si `Manager.Resolve()` échoue, la requête est rejetée avec un statut `404` **avant** d'atteindre `next` — `next.ServeHTTP` n'est **jamais** appelé dans ce cas (comportement explicitement vérifié par un test).

**Détail important : pourquoi `r.WithContext(ctx)`, et pas `r` directement ?** Le `context.Context` est immuable en Go — `WithTenant()` crée un nouveau contexte, il ne modifie jamais l'ancien. De même, `r.WithContext()` ne modifie pas `r` en place : il renvoie une **copie** de la requête portant le nouveau contexte. Sans cet appel, le handler suivant recevrait toujours l'ancien contexte (sans le tenant), et `tenantctx.FromContext` ne trouverait jamais rien.

### 9.6 Adaptateur Gin

**Fichier** : `middleware/gin/gin.go` — sous-module Go séparé (son propre `go.mod`, dépendance à `github.com/gin-gonic/gin`).

Gin a son propre `*gin.Context`, mais celui-ci contient toujours une requête HTTP standard accessible via `c.Request`.

```go
t, err := m.Resolve(c.Request)
ctx := tenantctx.WithTenant(c.Request.Context(), t)
c.Request = c.Request.WithContext(ctx)
c.Next()
```

```text
gin.Context
     │
     ▼
c.Request
     │
     ▼
Manager.Resolve()
     │
     ▼
Tenant
     │
     ▼
tenantctx
     │
     ▼
c.Request = new Request
     │
     ▼
c.Next()
```

En cas d'échec de résolution, `c.AbortWithStatus(http.StatusNotFound)` est utilisé — l'équivalent Gin de « ne jamais appeler le handler suivant ».

**Pourquoi ne pas utiliser `c.Set("tenant", t)` ?** Cela créerait un mécanisme de propagation spécifique à Gin. Le choix adopté (`tenantctx.WithTenant`) garantit que le tenant reste accessible avec la **même API partout**, quel que soit le framework — cohérence transversale essentielle pour un toolkit destiné à des milliers de développeurs sur différentes stacks.

**Note de mise en place** : le sous-module `middleware/gin` utilise une directive `replace github.com/sylvinhio676-ux/tenant-core => ../..` dans son `go.mod`, pour pointer vers le code local pendant le développement (avant que le module racine n'ait une version taguée publiée). Cette directive devra être supprimée une fois une version stable publiée, pour que les utilisateurs récupèrent la vraie dépendance depuis le dépôt public.

### 9.7 Adaptateur Echo

**Fichier** : `middleware/echo/echo.go` — sous-module Go séparé (dépendance à `github.com/labstack/echo/v4`).

Echo a `echo.Context`, mais la requête HTTP s'obtient via une **méthode**, `c.Request()`, pas un champ direct.

**Signature** : `func Middleware(m *tenant.Manager) echo.MiddlewareFunc`

```text
echo.Context
     │
     ▼
c.Request()
     │
     ▼
Manager.Resolve()
     │
     ▼
Tenant
     │
     ▼
tenantctx.WithTenant()
     │
     ▼
c.Request().WithContext()
     │
     ▼
c.SetRequest(...)
     │
     ▼
next(c)
```

**Détail important** : `c.SetRequest(c.Request().WithContext(ctx))`. Contrairement à Gin, on ne peut pas simplement faire `c.Request = ...` — Echo expose la requête via une méthode d'accès, pas un champ public.

**Gestion des erreurs** : Echo propage les erreurs via la valeur de retour `error` de chaque handler, pas en écrivant directement dans le `ResponseWriter` :

```go
return echo.NewHTTPError(http.StatusNotFound, "tenant not found")
```

Pour arrêter la chaîne de middlewares en cas de rejet, `c.Next()` (en fait, `next(c)`) n'est simplement jamais atteint — la fonction renvoie l'erreur avant cela.

### 9.8 Adaptateur Chi

**Fichier** : `middleware/chi/chi.go` — sous-module Go séparé (dépendance à `github.com/go-chi/chi/v5`).

Chi est le plus proche de `net/http` : il consomme `http.Handler` directement, sans type de contexte propre.

**Signature** : `func Middleware(m *tenant.Manager) func(http.Handler) http.Handler`

```text
http.Request
     │
     ▼
Manager.Resolve()
     │
     ▼
Tenant
     │
     ▼
tenantctx.WithTenant()
     │
     ▼
r.WithContext()
     │
     ▼
next.ServeHTTP()
```

**Code essentiel**

```go
ctx := tenantctx.WithTenant(r.Context(), t)
r = r.WithContext(ctx)
next.ServeHTTP(w, r)
```

Parce que Chi repose directement sur `http.Handler`, il n'a besoin d'aucun système de contexte supplémentaire — le code est presque identique à l'adaptateur `net/http`.

### 9.9 Comparaison des quatre adaptateurs

| Framework | Accès à la requête | Injection | Continuer |
|---|---|---|---|
| `net/http` | `r` | `r.WithContext()` | `next.ServeHTTP()` |
| Gin | `c.Request` | `c.Request.WithContext()` | `c.Next()` |
| Echo | `c.Request()` | `c.SetRequest()` | `next(c)` |
| Chi | `r` | `r.WithContext()` | `next.ServeHTTP()` |

Malgré ces différences syntaxiques, le résultat architectural est identique :

```text
                 ┌───────────────────────┐
                 │    Manager.Resolve()  │
                 └───────────┬───────────┘
                             │
                             ▼
                         *Tenant
                             │
                             ▼
                  tenantctx.WithTenant()
                             │
                             ▼
                     new Context
                             │
                             ▼
                      Next handler
```

### 9.10 Pourquoi quatre adaptateurs

```go
// Gin
router.Use(gin.Middleware(manager))

// Echo
e.Use(echo.Middleware(manager))

// Chi
r.Use(chi.Middleware(manager))

// net/http alone
handler := nethttp.Wrap(manager, myHandler)
```

La logique métier du toolkit ne change jamais. C'est exactement le rôle d'un *adaptateur* : traduire l'interface spécifique d'un framework vers l'interface générique du cœur.

### 9.11 Ce que les adaptateurs ne font PAS

C'est une frontière délibérément stricte, documentée pour prévenir toute dérive future :

- ❌ décider comment un tenant fonctionne
- ❌ interroger directement la base de données
- ❌ vérifier les rôles RBAC
- ❌ appliquer le RateLimiter
- ❌ gérer les métriques
- ❌ gérer directement les bannissements
- ❌ connaître la logique métier

Un adaptateur fait exclusivement :

```text
Framework
    ↓
extract *http.Request
    ↓
Manager.Resolve()
    ↓
inject Tenant into Context
    ↓
Framework
```

Rien de plus.

### 9.12 Architecture complète du toolkit après l'étape 6

```text
                         ┌─────────────────────┐
                         │    HTTP Request     │
                         └──────────┬──────────┘
                                    │
                 ┌──────────────────┼──────────────────┐
                 │                  │                  │
                 ▼                  ▼                  ▼
              Resolver          Middleware          ...
                 │                  │
                 │                  ▼
                 │             Manager
                 │                  │
                 │          ┌───────┴───────┐
                 │          │               │
                 ▼          ▼               ▼
             TenantID     Resolver        Store
                            │               │
                            └───────┬───────┘
                                    │
                                    ▼
                              *Tenant
                                    │
                                    ▼
                              tenantctx
                                    │
                                    ▼
                            Business handler
                                    │
               ┌────────────────────┼──────────────────┐
               │                    │                  │
               ▼                    ▼                  ▼
          BanChecker            RateLimiter          RBAC
               │                    │                  │
               └────────────────────┼──────────────────┘
                                    │
                                    ▼
                                Metrics
```

```text
                 APPLICATION
                      │
        ┌─────────────┼─────────────┐
        │             │             │
      Gin           Echo          Chi
        │             │             │
        └─────────────┼─────────────┘
                      │
                      ▼
                tenant.Manager
                      │
                      ▼
                Tenant Context
```

> **Le cœur de notre toolkit s'exprime en abstractions Go (Manager, Resolver, Store, context.Context). Les adaptateurs de framework se contentent de traduire les conventions de chaque framework vers ces abstractions. C'est ce qui permet à notre code multi-tenant de rester indépendant du framework tout en restant très facile à intégrer.**

---

## 10. Étape 7 — API Admin et EventBus Redis

Cette étape avait un double objectif : **l'administration des tenants** via une API HTTP, et **la propagation inter-instances**, pour qu'un changement de tenant (notamment un bannissement) soit immédiatement connu par toutes les instances du serveur grâce à Redis Pub/Sub.

> **Le cœur métier ne connaît ni HTTP, ni Redis, ni aucun framework particulier. Les adaptateurs dépendent du cœur, jamais l'inverse.**

### 10.1 Architecture globale de cette étape

```text
                         APPLICATION
                              │
                    ┌─────────┴─────────┐
                    │                   │
              Admin API             Normal request
                    │                   │
                    ▼                   ▼
             admin.HTTPHandler      Manager.Resolve()
                    │                   │
                    ▼                   ▼
              admin.Service       Resolver + Store
                    │
             ┌──────┴──────┐
             │             │
             ▼             ▼
        AdminStore      EventBus
             │             │
             ▼             ▼
        MemoryStore    RedisEventBus
                           │
                           ▼
                      Redis Pub/Sub
                           │
              ┌────────────┼────────────┐
              ▼            ▼            ▼
           Instance A   Instance B   Instance C
              │            │            │
              ▼            ▼            ▼
         BanChecker    BanChecker    BanChecker
```

`admin.Service` ne sait pas qu'il utilise Redis — il connaît seulement `eventbus.EventBus`. De même, il ne connaît pas `MemoryStore` ni aucune base SQL particulière — il connaît `tenant.AdminStore`.

### 10.2 Étendre tenant.Store — pourquoi une interface séparée

L'interface `Store` existante, dédiée au chemin de lecture normal :

```go
type Store interface {
    Get(ctx context.Context, id TenantID) (*Tenant, error)
    IsBanned(ctx context.Context, id TenantID) (bool, error)
}
```

... n'a **pas** été enrichie avec des opérations d'administration. À la place :

```go
type AdminStore interface {
    Create(ctx context.Context, t *Tenant) error
    Update(ctx context.Context, t *Tenant) error
    SetState(ctx context.Context, id TenantID, state State) error
}
```

**Pourquoi ?** Parce que le principe des interfaces minimales devait être respecté : `Manager` n'a absolument aucun besoin de pouvoir bannir un tenant, il ne doit donc pas dépendre d'une interface contenant `Ban()`/`Disable()`/`Activate()`. Cela évite de transformer progressivement `Store` en une gigantesque interface CRUD.

### 10.3 AdminStore : pourquoi pas Ban() / Disable() / Activate() directement

`AdminStore` n'expose délibérément que `Create`, `Update`, `SetState` — jamais `Ban()`, `Disable()`, `Activate()` directement, parce que ces opérations ne sont pas de simples modifications locales : un bannissement doit **aussi** produire un événement.

```text
Tenant A
   │
   ├── local state → Banned
   │
   └── event → TenantEvent
```

Si `AdminStore` avait `Ban()`, un développeur pourrait appeler `store.Ban(ctx, id)` et **oublier** de publier l'événement, créant une incohérence :

```text
Instance A: Tenant = BANNED     ❌ event not published
Instance B: Tenant = ACTIVE
```

C'est précisément le problème de cohérence que l'architecture voulait éviter — publier l'événement ne doit jamais être une étape optionnelle laissée à la discrétion de l'appelant.

### 10.4 MemoryStore et le problème du pointeur (rappel détaillé)

Ce problème a été identifié précisément lors de l'ajout de `SetState`, et était déjà documenté à l'étape 2 (section 5.6) — restitué ici avec le cas d'usage spécifique de `SetState`.

```text
map[TenantID]*Tenant
```

Quand `t, _ := store.Get(...)` renvoie `*Tenant`, ce pointeur réfère au **même objet** présent dans la map — ce n'est pas une copie. Faire `t.State = tenant.Banned` en dehors du verrou peut déclencher :

```text
Goroutine A                 Goroutine B

t.State = Banned
       │
       │                 Get()
       │                   │
       ▼                   ▼
   write                 read
```

... une véritable data race.

> **Protéger seulement la map ne suffit pas quand les valeurs de la map sont des pointeurs mutables.**

Les opérations d'écriture restent donc correctement protégées par le mécanisme de synchronisation du store : `Get()` renvoie une copie, `SetState`/`Create`/`Update` opèrent directement sur l'objet interne sous un `Lock()` exclusif.

### 10.5 admin.Service : le cœur métier de l'administration

```text
admin/
├── admin.go
├── admin_test.go
└── http.go
```

```go
type Service struct {
    store tenant.AdminStore
    bus   eventbus.EventBus
}
```

Seulement deux dépendances requises. Constructeur simple, sans options fonctionnelles :

```go
func NewAdminService(store tenant.AdminStore, bus eventbus.EventBus) *Service
```

**Pourquoi pas d'options fonctionnelles ici, contrairement à `Manager` ?** `Service` n'a que deux dépendances requises et aucune configuration optionnelle prévue. `NewAdminService(store, bus)` est plus simple et plus lisible que `NewAdminService(WithStore(...), WithEventBus(...))` pour un nombre de paramètres aussi petit et fixe — le pattern des options fonctionnelles n'est utile que lorsqu'il apporte une réelle valeur d'extensibilité, pas comme réflexe systématique.

### 10.6 La méthode `transition()`

`Ban()`, `Disable()`, `Activate()` partagent exactement le même mécanisme :

1. changer l'état ;
2. construire le `TenantEvent` ;
3. publier l'événement ;
4. logger en cas d'échec de publication.

Plutôt que de dupliquer cette logique trois fois, une méthode privée commune la factorise :

```go
func (s *Service) transition(ctx context.Context, id tenant.TenantID, state tenant.State) error
```

```go
func (s *Service) Ban(...) error {
    return s.transition(ctx, id, tenant.Banned)
}

func (s *Service) Disable(...) error {
    return s.transition(ctx, id, tenant.Disabled)
}

func (s *Service) Activate(...) error {
    return s.transition(ctx, id, tenant.Active)
}
```

> **Une seule implémentation de la logique partagée, plusieurs opérations métier explicites.** Cela garantit aussi que le comportement (y compris le logging en cas d'échec) reste identique à travers les trois transitions, sans risque de divergence accidentelle.

### 10.7 Le flux d'un bannissement

```text
service.Ban(ctx, "tenant-A")

                 AdminService.Ban()
                         │
                         ▼
                  transition()
                         │
                         ▼
             AdminStore.SetState()
                         │
                         ▼
                 State = Banned
                         │
                         ▼
              create TenantEvent
                         │
                         ▼
                EventBus.Publish()
                         │
                         ▼
                event propagation
```

```go
eventbus.TenantEvent{
    TenantID:  id,
    State:     tenant.Banned,
    Timestamp: time.Now(),
}
```

### 10.8 Pourquoi SetState → Publish (et pas l'inverse)

**Décision architecturale, non-atomicité acceptée.**

```text
SetState() → success
Publish()  → failure
```

L'état local devient `BANNED`, mais les autres instances ne reçoivent pas l'événement — une incohérence, mais **acceptée** pour cette version.

**Pourquoi pas `Publish → SetState` ?** Parce qu'alors `Tenant A → BANNED` pourrait être publié pendant que `SetState()` échoue ensuite — l'événement annoncerait un état qui, en fin de compte, n'existe jamais dans le Store. C'est strictement pire : un **événement mensonger**.

**Décision adoptée** : `SetState → Publish`, avec une limitation explicitement documentée directement dans le code lui-même :

```text
// Known limitation: SetState and Publish are not atomic with each other (they
// are two distinct systems). The order SetState → Publish guarantees that we
// never publish an event for a state that was not actually
// applied to the Store — but if Publish fails after a successful SetState,
// the event may be lost until manual resynchronization or a
// future durable-delivery mechanism (Outbox pattern).
```

### 10.9 Journaliser l'incohérence

Quand `SetState()` réussit mais que `Publish()` échoue, le service **journalise explicitement** l'anomalie, avec le contexte complet (le tenant concerné, l'état cible, l'erreur rencontrée) :

```text
ERROR
tenant state changed but event publication failed
tenant_id=tenant-A state=banned error=redis connection refused
```

Cela permet à un opérateur de savoir : ⚠ l'état local a changé, ⚠ l'événement n'a pas été propagé, ⚠ une resynchronisation est potentiellement nécessaire.

**Nuance importante adoptée** : le log ne remplace jamais l'erreur renvoyée à l'appelant — les deux sont faits, parce que l'appelant seul (recevant juste une erreur Redis générique) ne saurait pas nécessairement qu'une opération métier a *partiellement* réussi (le Store a bel et bien été modifié) — information que seul le `Service` possède.

**Évolution future identifiée** : un pattern **Outbox** (changement d'état et événement à publier écrits dans la même transaction de stockage, avec un *worker* asynchrone responsable de la publication réelle et des tentatives en cas d'échec) rendrait la publication durable. Ce mécanisme n'a délibérément **pas** été construit à cette étape.

### 10.10 API Admin — couche HTTP

```go
type HTTPHandler struct {
    mux     *http.ServeMux
    service *Service
}
```

**Choix architectural important : `net/http` pur, ni Gin, ni Echo, ni Chi.**

**Pourquoi ?** Parce que l'API Admin est une **API de commande** pour le toolkit, pas un middleware destiné à être branché sur différents frameworks applicatifs. Elle reste donc indépendante du framework utilisé par l'application consommant le toolkit — n'importe quel serveur Go capable de monter un `http.Handler` peut l'intégrer, quel que soit le choix de framework de l'application pour le reste.

### 10.11 Routage moderne avec `http.ServeMux` (Go 1.22+)

```go
h.mux.HandleFunc("PATCH /tenants/{id}/ban", h.handleBan)
h.mux.HandleFunc("PATCH /tenants/{id}/disable", h.handleDisable)
h.mux.HandleFunc("PATCH /tenants/{id}/activate", h.handleActivate)
```

Grâce au support moderne de motifs de `ServeMux` (méthodes HTTP + wildcards), le handler récupère directement :

```go
id := r.PathValue("id")
```

```text
PATCH /tenants/tenant-A/ban
                 │
                 ▼
             PathValue
                 │
                 ▼
             tenant-A
```

Cela évite le parsing manuel (`strings.Split`, `strings.TrimPrefix`, `switch`), réduisant le risque de reconstruire progressivement un mini-routeur maison.

### 10.12 Pourquoi seulement trois endpoints

Délibérément, **pas** de `POST /tenants` ni de `GET /tenants/{id}`, même si `AdminStore` a `Create()` et `Store` a `Get()`.

> **L'API HTTP doit suivre le contrat métier du Service, pas exposer automatiquement chaque méthode du Store.**

Actuellement, `Service` n'expose que `Ban()`, `Disable()`, `Activate()` — donc l'API expose exactement `PATCH /tenants/{id}/ban`, `/disable`, `/activate`, pas un CRUD générique. Cela protège l'architecture contre une dérive du type *« Store → toutes les méthodes → endpoints HTTP »*.

Si la création ou la lecture devaient un jour faire partie de l'API Admin, l'approche consiste à d'abord enrichir le contrat métier (`Service.Create(...)`, `Service.Get(...)`), **et seulement ensuite** exposer les endpoints correspondants — jamais l'inverse.

### 10.13 Architecture de l'API Admin — flux complet

```text
HTTP
 │
 │ PATCH /tenants/tenant-A/ban
 ▼
HTTPHandler
 │
 │ service.Ban(...)
 ▼
AdminService
 │
 ├───────────────┐
 ▼               ▼
AdminStore     EventBus
 │               │
 ▼               ▼
State=Banned   TenantEvent
                 │
                 ▼
             propagation
```

Le `HTTPHandler` ne connaît rien de Redis, de `MemoryStore`, de la façon dont l'état est stocké, ni de la façon dont les événements sont transportés.

### 10.14 Limitations actuelles de l'API Admin (documentées honnêtement)

**Authentification** — l'API n'a actuellement **aucune** authentification ni autorisation. Elle ne doit donc pas être exposée directement sur Internet en production. Un point critique à traiter plus tard.

**Gestion des erreurs** — `writeError(...)` renvoie systématiquement `500 Internal Server Error`, quelle que soit la cause réelle (tenant introuvable, store indisponible, etc.). Une évolution future devrait distinguer `404` (tenant absent), `500` (erreur interne), `503` (dépendance indisponible). Cette limitation vient notamment du fait qu'il n'existe pas encore d'erreur sentinelle exportée pour « tenant introuvable » au niveau de l'interface générique `AdminStore` — contrairement à `store.ErrTenantNotFound`, spécifique à `MemoryStore`.

### 10.15 Pourquoi Redis

Jusqu'ici, `MemoryEventBus` fonctionne très bien sur une seule instance :

```text
Instance A
   │
MemoryEventBus
   │
local handlers
```

Mais avec plusieurs instances, chacune a sa propre mémoire :

```text
Instance A
   │
Ban tenant-A
   │
MemoryEventBus
   │
   └── only A
```

B et C ne voient rien.

### 10.16 Redis Pub/Sub — RedisEventBus

```text
eventbus/redis/
└── redis.go
```

Utilise `github.com/redis/go-redis/v9`. Le package `eventbus` lui-même ne connaît **jamais** Redis — une règle architecturale essentielle :

```text
eventbus
   │
   │ defines
   ▼
EventBus interface
   ▲
   │ implements
   │
eventbus/redis
```

Grâce au typage structurel de Go, `RedisEventBus` satisfait automatiquement `eventbus.EventBus`.

```go
type RedisEventBus struct {
    client  *goredis.Client
    channel string

    mu      sync.Mutex
    pubsubs []*goredis.PubSub // tracked so Stop can close them
}
```

```text
RedisEventBus
├── Redis Client
├── Redis Channel
└── pubsubs (one *redis.PubSub per Subscribe call, for Stop)
```

**Note de mise en place** : un sous-module Go séparé (`eventbus/redis/go.mod`), avec la même directive locale `replace` que les adaptateurs de framework, pour les mêmes raisons.

**La résilience réseau est gérée nativement par go-redis — vérifié depuis le code source, pas supposé.** Le type `*redis.PubSub` de `go-redis/v9` documente lui-même qu'il se reconnecte et se réabonne automatiquement à ses canaux en cas d'erreur réseau, et cela a été confirmé en lisant directement `pubsub.go` (v9.22.0) : la goroutine derrière `pubsub.Channel()` appelle `Receive()` en boucle et ne s'arrête (en fermant le canal Go) que lorsque `Close()` a été appelé explicitement (renvoyant `pool.ErrClosed`) ; toute autre erreur — y compris une connexion perdue — est retentée de façon transparente, avec un réabonnement automatique aux mêmes canaux et un ping de vérification de santé périodique (toutes les 3s par défaut) pour détecter les déconnexions silencieuses. Concrètement, cela signifie :

- La boucle `for msg := range pubsub.Channel()` à l'intérieur de `Subscribe()` ne se termine jamais à cause d'un incident réseau — les messages reprennent simplement une fois que go-redis s'est reconnecté.
- Construire un mécanisme de backoff/retry/réabonnement maison par-dessus dupliquerait une logique que go-redis fournit déjà, sans aucun bénéfice — et cela a été envisagé puis délibérément rejeté pour cette raison exacte.
- `RedisEventBus.Stop()` existe pour un but différent : un arrêt propre et intentionnel. Il ferme chaque `*redis.PubSub` créé par `Subscribe`, ce qui met fin à leur boucle `Channel()` et à la goroutine associée. Cela n'a rien à voir avec la reconnexion et peut être appelé plusieurs fois, ou même si `Subscribe` n'a jamais été appelé.
- **Course entre `Stop()` et un `Subscribe()` en cours** : `pubsub.Receive(ctx)` de `Subscribe()` peut bloquer un moment contre un Redis lent ou dégradé, donc `Stop()` pourrait s'exécuter — et fermer chaque abonnement qu'il connaît actuellement — avant que cet appel à `Subscribe()` ne se termine. Pour éviter de faire fuiter silencieusement cet abonnement (enregistré après que `Stop()` a déjà tourné, et donc jamais fermé), `RedisEventBus` maintient un drapeau `stopped` : une fois `Receive()` réussi, `Subscribe()` revérifie `stopped` sous le même verrou avant d'enregistrer l'abonnement ; si `Stop()` a déjà tourné, il ferme immédiatement son propre `pubsub` et renvoie `ErrStopped`. Cela fait de `Stop()` un arrêt véritablement final — un `RedisEventBus` ne peut pas être ranimé par un appel à `Subscribe()` simplement en retard.

### 10.17 Transformation TenantEvent ↔ JSON

Redis ne connaît pas `eventbus.TenantEvent` — il transporte des octets/messages bruts. JSON a été choisi.

**Publication**

```text
TenantEvent
     │
     ▼
json.Marshal()
     │
     ▼
[]byte
     │
     ▼
Redis PUBLISH
```

```json
{
  "TenantID": "tenant-A",
  "State": "banned",
  "Timestamp": "..."
}
```

**Réception**

```text
Redis
 │
 │ JSON message
 ▼
RedisEventBus
 │
 ▼
json.Unmarshal()
 │
 ▼
TenantEvent
 │
 ▼
handler(event)
```

Transformation symétrique :

```text
             Publish
TenantEvent ────────→ JSON ────────→ Redis
                                      │
                                      │
TenantEvent ←────── JSON ←───────────┘
             Subscribe
```

**Pourquoi JSON** : standard, lisible, simple, indépendant du langage, directement supporté par la bibliothèque standard de Go.

### 10.18 Subscribe() et la goroutine dédiée

Redis Pub/Sub fonctionne avec un abonnement **continu** :

```go
for msg := range pubsub.Channel() {
    // ...
}
```

Cette boucle peut vivre pendant toute la durée de vie du serveur. Si elle s'exécutait directement à l'intérieur de `Subscribe()`, la fonction ne retournerait jamais, bloquant tout le code appelant :

```text
Subscribe()
   │
   ▼
infinite loop
   │
   ├── never returns
   │
   └── calling code blocked
```

**Solution adoptée** :

```text
Subscribe()
   │
   ├── configuration
   │
   ├── confirmation
   │
   └── launch goroutine
             │
             ▼
       reading loop
```

La boucle de réception vit dans une seule goroutine dédiée et permanente — distincte des goroutines ensuite lancées pour chaque handler individuel (voir 10.19).

### 10.19 Confirmation synchrone avec `pubsub.Receive()` — fail-fast

Simplement faire `pubsub := client.Subscribe(...)` ne garantit **pas** immédiatement que Redis a confirmé l'abonnement (une opération asynchrone côté connexion). `pubsub.Receive(ctx)` est utilisé **avant** de lancer la goroutine de traitement, pour bloquer jusqu'à confirmation, ou remonter une erreur concrète si Redis est inaccessible :

```text
Subscribe()
    │
    ▼
Redis SUBSCRIBE
    │
    ▼
Receive()
    │
    ├── error → Subscribe returns error
    │
    └── confirmation
           │
           ▼
       goroutine
           │
           ▼
       messages
```

Cela suit le principe **fail-fast** pour les erreurs de configuration : un développeur qui configure mal Redis (mauvaise adresse, identifiants invalides) le découvre immédiatement au démarrage de son serveur, plutôt que silencieusement en production, des heures plus tard.

### 10.20 Protection contre les handlers qui paniquent (rappel + application à Redis)

Même comportement que `MemoryEventBus` (étape 3) : chaque événement reçu est traité dans sa propre goroutine, protégée par `recover()` :

```text
Redis message
      │
      ▼
JSON decode
      │
      ▼
go safeCall(handler, event)
```

```go
func safeCall(handler func(TenantEvent), event TenantEvent) {
    defer func() {
        if r := recover(); r != nil {
            // log
        }
    }()
    handler(event)
}
```

```text
Handler A → panic → recover()
Handler B → continues normally
Handler C → continues normally
```

Un handler qui échoue ne doit jamais faire tomber le processus.

### 10.21 Message Redis malformé

Si `json.Unmarshal(...)` échoue, le message invalide est journalisé puis **ignoré**, sans `panic(...)` ni `return` qui arrêterait toute la consommation :

```text
invalid message
      │
      ▼
log error
      │
      ▼
continue
```

### 10.22 Architecture multi-instance finale

```text
                 Redis
              Pub/Sub Channel
                   │
       ┌───────────┼───────────┐
       │           │           │
       ▼           ▼           ▼
 Instance A   Instance B   Instance C
       │           │           │
 RedisEventBus RedisEventBus RedisEventBus
       │           │           │
 BanChecker   BanChecker   BanChecker
```

Un bannissement effectué sur l'instance A se propage à toutes les autres :

```text
                    Instance A
                        │
                   Admin API
                        │
                        ▼
                  AdminService
                        │
                  SetState(Banned)
                        │
                        ▼
                  Publish(Event)
                        │
                        ▼
                      Redis
                        │
             ┌──────────┼──────────┐
             ▼          ▼          ▼
            A           B          C
             │          │          │
             ▼          ▼          ▼
         handlers    handlers   handlers
             │          │          │
             ▼          ▼          ▼
        BanChecker  BanChecker  BanChecker
```

### 10.23 Stratégie de test — pourquoi miniredis plutôt qu'un vrai Redis

Pour tester `RedisEventBus` sans nécessiter un vrai serveur Redis pendant `go test` (ni pour le développeur local, ni en CI), la bibliothèque **`miniredis`** (une implémentation Redis purement en mémoire, écrite en Go) a été choisie plutôt que d'installer un vrai Redis dans le workflow CI.

| Critère | miniredis | Redis en CI |
|---|---|---|
| Redis installé localement | ❌ non requis | ❌ non requis |
| Processus externe | ❌ non | ✅ oui |
| Vitesse | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ |
| `go test` immédiat | ✅ | ❌ nécessite une config CI |
| Reproductibilité | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| Tester un vrai Redis | ⚠️ simulation | ✅ oui |

**Décision** : `miniredis` pour l'instant, garantissant que `go test ./...` fonctionne partout sans dépendance externe — cohérent avec le principe de testabilité appliqué depuis le début. Un test d'intégration avec un vrai Redis reste une évolution complémentaire possible, pas un remplacement.

Les tests couvrent : le chemin nominal (publier un événement, le recevoir, vérifier l'aller-retour JSON avec une tolérance sur le timestamp via `assert.WithinDuration`), et le cas fail-fast (`Subscribe()` doit échouer immédiatement si Redis est inaccessible, pas silencieusement).

### 10.24 Architecture complète de l'étape 7

```text
tenant-core/
│
├── tenant.go
│   ├── Tenant
│   ├── TenantID
│   ├── State
│   ├── Resolver
│   ├── Store
│   └── AdminStore
│
├── admin/
│   ├── admin.go
│   │   └── Service
│   │       ├── Ban
│   │       ├── Disable
│   │       ├── Activate
│   │       └── transition
│   │
│   ├── http.go
│   │   └── HTTPHandler
│   │
│   └── admin_test.go
│
├── eventbus/
│   ├── eventbus.go
│   │   └── EventBus
│   │
│   ├── memory.go
│   │   └── MemoryEventBus
│   │
│   └── redis/            (separate Go sub-module)
│       ├── go.mod
│       └── redis.go
│           └── RedisEventBus
│
└── store/
    └── memory.go
        └── MemoryStore
```

### 10.25 Le principe architectural à retenir

```text
                    BUSINESS
                      │
                      ▼
                AdminService
                      │
          ┌───────────┴───────────┐
          ▼                       ▼
      AdminStore              EventBus
          │                       │
          ▼                       ▼
    MemoryStore              MemoryEventBus
                                  │
                                  │
                             RedisEventBus
                                  │
                                  ▼
                                Redis
```

`AdminService` ❌ ne connaît pas HTTP, ❌ ne connaît pas Redis, ❌ ne connaît pas Gin, ❌ ne connaît pas Echo, ❌ ne connaît pas PostgreSQL.

`AdminService` ✅ connaît `AdminStore`, ✅ connaît `EventBus`.

> **Le cœur définit les contrats. Les adaptateurs implémentent ces contrats.** Exactement le même principe appliqué avec les adaptateurs de middleware (étape 6).

### 10.26 Ce qui est volontairement laissé pour plus tard

| Sujet | État actuel | Évolution |
|---|---|---|
| API Admin | Fonctionnelle pour les transitions | Authentification/RBAC |
| Erreurs HTTP | Majoritairement `500` | Mapping `404`/`409`/`500`/`503` |
| SetState → Publish | Non atomique | Pattern Outbox |
| EventBus | Redis Pub/Sub, avec un `Stop()` pour un arrêt propre (la reconnexion/réabonnement sur erreurs réseau est géré nativement par le `*redis.PubSub` de go-redis — voir §10.16) | — |
| Redis | Propagation en temps réel | Monitoring dédié, métriques de latence de propagation |
| Création de tenant | `AdminStore.Create` existe | Ajouter la capacité métier `Service.Create` si nécessaire |
| Lecture admin | `Store.Get` existe | Ajouter `Service.Get` si le besoin métier survient |
| Tests Redis | Couverts via `miniredis` | Tests d'intégration avec un vrai serveur Redis, en complément |

**En une phrase** : l'étape 7 transforme le toolkit d'un système capable de résoudre un tenant en un système capable de gérer son cycle de vie et de propager ses changements d'état à travers plusieurs instances, tout en gardant un cœur métier indépendant de HTTP et de Redis.

---

## 11. Étape 8 — Outils de test (tenanttest)

### 11.1 Le problème résolu

Avant cette étape, pour tester du code applicatif dépendant du tenant courant, il fallait écrire manuellement :

```go
t := &tenant.Tenant{
    ID:    "tenant-abc",
    State: tenant.Active,
}

ctx := tenantctx.WithTenant(context.Background(), t)
```

Cette logique était répétée dans plusieurs tests internes au toolkit (`fakeResolver`, `fakeStore`, `fakeAdminStore` — utiles pour tester les composants internes du toolkit lui-même, mais pas destinés à être exposés). Un **utilisateur externe** qui veut simplement tester son application ne devrait pas avoir à connaître toute cette mécanique interne. C'est exactement le rôle de `tenanttest`.

### 11.2 Pourquoi un package séparé de `tenantctx`

| | `tenantctx` | `tenanttest` |
|---|---|---|
| Responsabilité | Gérer le tenant présent dans le `context.Context` de l'application | Fournir des outils pour construire facilement des contextes de test |
| Chemin | Production | Tests |

```text
Application
    │
    ▼
tenantctx.WithTenant()
    │
    ▼
context.Context
```

```text
Test
 │
 ▼
tenanttest.WithFakeTenant()
 │
 ▼
tenantctx.WithTenant()
 │
 ▼
context.Context
```

Un développeur qui importe `tenantctx` dans son application de production ne récupère donc jamais, via ce même import, des fonctionnalités destinées exclusivement aux tests. Les packages énoncent clairement leur rôle : `tenantctx` = mécanisme de production ; `tenanttest` = ergonomie de test.

### 11.3 Architecture des packages

```text
tenant-core/
│
├── tenantctx/
│   └── ...
│
└── tenanttest/
    ├── tenanttest.go
    └── tenanttest_test.go
```

```text
tenanttest
    │
    ├──────────────► tenant
    │
    └──────────────► tenantctx
```

Aucune logique métier additionnelle — purement de l'ergonomie de test.

### 11.4 `WithFakeTenant`

```go
func WithFakeTenant(
    ctx context.Context,
    id tenant.TenantID,
    state tenant.State,
) context.Context
```

```go
ctx := tenanttest.WithFakeTenant(
    context.Background(),
    "tenant-abc",
    tenant.Active,
)
```

### 11.5 Pourquoi garder une API simple

Le choix a été de garder **délibérément** `WithFakeTenant(ctx, id, state)` plutôt que d'y ajouter progressivement des paramètres (`roles`, `permissions`, ...), ce qui rendrait la fonction difficile à utiliser pour le cas le plus courant : *« j'ai juste besoin d'un tenant dans mon contexte. »*

### 11.6 `WithFakeTenantFull`

Pour les tests nécessitant plus de contrôle (notamment RBAC) :

```go
func WithFakeTenantFull(
    ctx context.Context,
    t *tenant.Tenant,
) context.Context
```

```go
ctx := tenanttest.WithFakeTenantFull(
    context.Background(),
    &tenant.Tenant{
        ID:    "tenant-admin",
        State: tenant.Active,
        Roles: []tenant.Role{"admin", "manager"},
    },
)
```

Particulièrement utile pour tester le RBAC, des rôles spécifiques, des états particuliers, des scénarios métier complexes, ou de futurs champs de `Tenant`.

### 11.7 Factorisation entre les deux helpers

`WithFakeTenant` délègue à `WithFakeTenantFull`, de sorte que la logique de création/injection du contexte n'existe qu'à un seul endroit :

```text
                  tenanttest
                      │
             ┌────────┴────────┐
             │                 │
             ▼                 ▼
    WithFakeTenant      WithFakeTenantFull
             │                 │
             │                 │
             └────────┬────────┘
                      ▼
             tenantctx.WithTenant
                      │
                      ▼
               context.Context
```

### 11.8 Pourquoi ne pas créer de faux Resolver à cette étape

Le besoin actuel de `tenanttest` est *d'injecter directement un tenant*, pas *de simuler tout le pipeline HTTP*. Des helpers comme `NewFakeResolver(...)`, `NewFakeStore(...)`, `NewFakeManager(...)` n'ont délibérément **pas** été créés à cette étape.

> **Ne pas abstraire prématurément ; commencer avec le plus petit contrat qui résout réellement le problème.**

### 11.9 Le contrat tenanttest

```text
Input
  │
  ▼
tenanttest.WithFakeTenant(...)
  │
  ▼
context.Context containing the tenant
  │
  ▼
tenantctx.FromContext(ctx)
  │
  ▼
Tenant
```

> **Tout tenant injecté par `tenanttest` doit pouvoir être récupéré via le mécanisme officiel `tenantctx.FromContext`.**

### 11.10 et 11.11 Tests du package

**`TestWithFakeTenant`** vérifie le helper minimal : `ID`, `State`, `Roles` vide.

**`TestWithFakeTenantFull`** vérifie qu'un tenant complet (y compris `Roles`) est correctement préservé — garantissant que les informations RBAC ne sont pas perdues.

Les deux tests restent délibérément courts : `tenantctx.WithTenant()`/`FromContext()` ont déjà été testés en profondeur à l'étape 1 ; ici, seul le **contrat d'intégration** du helper est vérifié.

### 11.12 Exemple d'utilisation par un utilisateur du toolkit

```go
func GetCurrentTenantName(ctx context.Context) string {
    t := tenantctx.FromContext(ctx)
    if t == nil {
        return ""
    }
    return string(t.ID)
}
```

```go
func TestGetCurrentTenantName(t *testing.T) {
    ctx := tenanttest.WithFakeTenant(
        context.Background(),
        "tenant-abc",
        tenant.Active,
    )

    got := GetCurrentTenantName(ctx)

    if got != "tenant-abc" {
        t.Fatalf("expected tenant-abc, got %s", got)
    }
}
```

Le développeur n'a pas besoin de démarrer Redis, de créer un `MemoryStore`, un `Resolver`, de construire une requête HTTP, de démarrer Gin/Echo/Chi, ou d'utiliser `Manager`. C'est exactement le bénéfice recherché.

### 11.13 Exemple pour RBAC

```go
ctx := tenanttest.WithFakeTenantFull(
    context.Background(),
    &tenant.Tenant{
        ID:    "tenant-abc",
        State: tenant.Active,
        Roles: []tenant.Role{"admin"},
    },
)
```

Permet de tester `tenant → RBAC → permission accordée/refusée` sans aucune infrastructure externe.

### 11.14 Architecture globale après l'étape 8

```text
                    tenant-core
                         │
       ┌─────────────────┼──────────────────┐
       │                 │                  │
       ▼                 ▼                  ▼
    Production        Adapters            Testing
       │                 │                  │
       ▼                 ▼                  ▼
    tenant          middleware          tenanttest
    tenantctx       Gin/Echo/Chi
    Manager         Redis
    Store           ...
    AdminService
```

```text
                    CORE
                     │
          ┌──────────┴──────────┐
          │                     │
          ▼                     ▼
      Production             Testing
          │                     │
          ▼                     ▼
     tenantctx             tenanttest
          │                     │
          └──────────┬──────────┘
                     ▼
               context.Context
```

### 11.15 Principe architectural adopté

> **Les outils de test doivent faciliter l'utilisation du cœur sans le polluer avec de la logique spécifique aux tests.**

Donc : `tenantctx` = mécanisme de production ; `tenanttest` = ergonomie de test — et non pas `tenantctx` = production + mocks + helpers + fake stores + ....

### 11.16 Évolutions possibles (non implémentées)

```text
tenanttest/
│
├── tenanttest.go
├── resolver.go   (future evolution)
├── store.go      (future evolution)
├── manager.go    (future evolution)
└── ...
```

Potentiellement avec `tenanttest.NewFakeResolver(...)`, `tenanttest.NewFakeStore(...)`, `tenanttest.NewManager(...)` — seulement une fois de vrais besoins identifiés, conformément à la règle générale de ne pas abstraire prématurément.

### 11.17 Résumé de l'étape 8

```text
                    tenanttest
                        │
             ┌──────────┴──────────┐
             │                     │
             ▼                     ▼
     WithFakeTenant       WithFakeTenantFull
             │                     │
             └──────────┬──────────┘
                        ▼
               tenantctx.WithTenant
                        │
                        ▼
                 context.Context
                        │
                        ▼
              tenantctx.FromContext
                        │
                        ▼
                     Tenant
```

**Objectif final** : permettre à un développeur de tester facilement du code multi-tenant avec un faux tenant, sans infrastructure, sans HTTP, sans Resolver, sans Store, et sans framework, tout en utilisant exactement le même mécanisme `tenantctx` que le code de production.

---

## 12. Architecture complète finale

```text
                         ┌─────────────────────┐
                         │     Application     │
                         └──────────┬──────────┘
                                    │
                                    ▼
                         ┌─────────────────────┐
                         │ Middleware Adapter  │
                         │ HTTP / Gin / Echo   │
                         │ / Chi               │
                         └──────────┬──────────┘
                                    │
                                    ▼
                         ┌─────────────────────┐
                         │   Tenant Manager    │
                         └──────┬────────┬─────┘
                                │        │
                         Resolver        Store
                                │        │
                                │   ┌────┴───────────┐
                                │   │ Memory / Cache  │
                                │   └────────────────┘
                                │
                                ▼
                         ┌─────────────────────┐
                         │    Tenant Context   │
                         └──────────┬──────────┘
                                    │
                 ┌──────────────────┼──────────────────┐
                 │                  │                  │
                 ▼                  ▼                  ▼
              RBAC             RateLimiter          Metrics
                 │                  │                  │
                 └──────────────────┼──────────────────┘
                                    │
                                    ▼
                         ┌─────────────────────┐
                         │     Application     │
                         └─────────────────────┘
```

**Administration :**

```text
Admin API
    │
    ▼
AdminService
    │
    ├── AdminStore
    │
    └── EventBus
          │
          ├── MemoryEventBus
          │
          └── Redis Pub/Sub
                    │
                    ▼
              Other Instances
                    │
                    ▼
                BanChecker
```

**Vue synthétique de la composition (`tenant.New()`)** :

```text
                    tenant.New()
                         │
          ┌──────────────┼──────────────┐
          │              │              │
          ▼              ▼              ▼
      Resolver         Store         EventBus
          │              │              │
          │              │        ┌─────┴─────┐
          │              │        ▼           ▼
          │              │   BanChecker     Metrics
          │              │
          │              ▼
          │         CachedStore
          │
          └──────────────┬──────────────┘
                         ▼
                    Tenant Core
```

**Pourquoi des options fonctionnelles** — plutôt qu'un énorme constructeur (`New(resolver, store, eventBus, banChecker, rateLimiter, rbac, metrics, cacheKey, ...)`), difficile à lire et à maintenir, l'API adoptée est :

```go
tenant.New(
    tenant.WithResolver(resolver),
    tenant.WithStore(store),
)
```

**Important** : le `Manager` réellement implémenté (étapes 6-7) reste délibérément minimal — il assemble seulement `Resolver` et `Store`, panique si l'un des deux manque, et sa seule méthode `Resolve(r *http.Request) (*Tenant, error)` s'arrête à produire un `*Tenant`, sans construire de `context.Context` (pour éviter une dépendance circulaire avec `tenantctx`). Les autres composants (`BanChecker`, `RateLimiter`, `RBAC`, `Metrics`, `CacheKeyer`, `EventBus`) restent des **briques indépendantes**, que l'application invoque explicitement là où c'est pertinent — le diagramme ci-dessus représente l'écosystème des composants disponibles, **pas** un pipeline unique automatiquement imposé par `Manager` lui-même. Voir la section [Décisions / points à clarifier](#18-décisions--points-à-clarifier) pour le détail de cette nuance entre la vision d'ensemble et l'implémentation réelle de `tenant.New()`.

---

## 13. Flux de données

### 13.1 Requête utilisateur normale

```text
HTTP Request
     ↓
Middleware
     ↓
Resolver
     ↓
Manager
     ↓
Store
     ↓
Tenant
     ↓
tenantctx
     ↓
Application Handler
```

En détail :

```text
Step 1 — Resolver
Request → SubdomainResolver → TenantID("tenant-a")

Step 2 — Store
TenantID("tenant-a") → Store → *Tenant{ID: tenant-a, State: active, Roles: [admin]}

Step 3 — Context
*Tenant → tenantctx.WithTenant(...) → context.Context

Components that need the tenant then call tenantctx.FromContext(ctx).
```

### 13.2 Bannissement

```text
Admin API
    ↓
AdminService.Ban()
    ↓
AdminStore.SetState()
    ↓
Tenant = Banned
    ↓
EventBus.Publish()
    ↓
Redis Pub/Sub
    ↓
Other instances
    ↓
BanChecker
    ↓
Local cache invalidated / state updated (timestamp-based resolution)
```

### 13.3 Test applicatif

```text
tenanttest.WithFakeTenant(...)
          ↓
tenantctx
          ↓
Application code
          ↓
Test
```

---

## 14. Concurrence et thread-safety

Chaque composant à état partagé a été analysé selon son **profil d'accès** (lectures fréquentes vs. écritures fréquentes, collection dynamique de clés vs. valeur unique), avec la primitive de synchronisation adaptée à ce profil spécifique — jamais un seul mécanisme appliqué par habitude.

### 14.1 `sync.RWMutex` — lectures fréquentes, écritures rares

Utilisé par `MemoryStore`, `CachedStore`, `MemoryEventBus` (liste des abonnés), `RBAC` (définitions de rôles/permissions).

```text
Reader A ── RLock ──►
Reader B ── RLock ──►
Reader C ── RLock ──►

Writer
   │
   ▼
 Lock()
   │
   ▼
 modification
   │
   ▼
Unlock()
```

Plusieurs lecteurs accèdent aux données simultanément sans jamais se bloquer mutuellement ; une écriture reste exclusive et attend la fin de toutes les lectures en cours.

### 14.2 Le piège des pointeurs dans une map

```text
map[TenantID]*Tenant
```

Une map protégée par `RWMutex` protège l'accès à la map **elle-même** (ajout, suppression, lecture d'une clé), mais **pas** le contenu pointé par les valeurs qu'elle stocke, si ce contenu est muté directement.

```text
Map
 │
 └── *Tenant ──────────┐
                       │
                       ▼
                    Tenant
                    State
```

**Règle adoptée et appliquée de façon constante** : les méthodes de lecture (`Get`) renvoient toujours une **copie**, jamais le pointeur interne ; les méthodes d'écriture (`SetState`, `Create`, `Update`) modifient l'objet interne **directement, sous un `Lock()` exclusif** — jamais via un aller-retour lecture-modification-écriture, qui recréerait une fenêtre de *lost update*.

### 14.3 `sync.Map` + `LoadOrStore` — collections dynamiques par clé

Utilisé par `BanChecker` (`TenantID → banEntry`), `TenantRateLimiter` (`TenantID → *rate.Limiter`), `MemoryMetrics` (`TenantID → *tenantMetrics`).

**Le problème résolu par `LoadOrStore`** : si deux goroutines arrivent simultanément pour un tenant qui n'a **encore jamais** d'entrée, un simple `Load` puis `Store` séparés pourraient finir par créer et écraser deux valeurs distinctes (par exemple deux `*rate.Limiter` différents pour le même tenant, l'un écrasant l'autre). `LoadOrStore` garantit atomiquement qu'**une seule** valeur devient la référence officiellement partagée, même si les deux goroutines ont chacune préparé leur propre valeur candidate.

```text
Goroutine A                    Goroutine B

Load → absent                  Load → absent
  │                              │
creates limiter A              creates limiter B
  │                              │
LoadOrStore                    LoadOrStore
  │                              │
A gets registered              B sees A already exists
                                  │
                           B is NOT registered

Result: both goroutines use the same *rate.Limiter A
```

### 14.4 `sync/atomic` — compteurs très fréquemment écrits

Utilisé par `MemoryMetrics` pour `requests`, `errors`, `latencySum`, `latencyCount` — des compteurs incrémentés à chaque requête, potentiellement par des dizaines de milliers de goroutines simultanées. `atomic.Int64.Add()` garantit un incrément correct sans jamais avoir besoin d'un verrou explicite.

**Deux niveaux de concurrence combinés** dans `MemoryMetrics` : `sync.Map` protège la collection dynamique de tenants, `atomic.Int64` protège chaque compteur individuel — chacun à l'endroit optimal pour son propre problème.

### 14.5 Isolation par goroutine + `recover()` — EventBus (mémoire et Redis)

Chaque handler abonné à un `TenantEvent` s'exécute dans sa **propre goroutine**, individuellement protégée par un `recover()` :

```go
defer func() {
    if r := recover(); r != nil {
        // log
    }
}()
```

**Pourquoi c'est crucial** : `recover()` ne fonctionne qu'**au sein de la même goroutine** que le `panic()` qu'il intercepte — il doit donc être placé à l'intérieur de la fonction lancée par `go`, jamais autour de l'appel à `Publish()` (qui est déjà retourné avant que le handler ne s'exécute réellement).

**Deux niveaux de goroutines dans `RedisEventBus`**, distincts et à ne pas confondre :

```text
Redis listener goroutine (single, permanent)
        │
        │ sequential message reception
        ▼
   event received
        │
        ├──► handler A goroutine + recover (ephemeral, per event)
        ├──► handler B goroutine + recover
        └──► handler C goroutine + recover
```

### 14.6 Résolution des conflits par timestamp — `BanChecker`

Un problème de concurrence plus subtil qu'une simple protection mémoire : un snapshot initial (chargé au démarrage) et un événement reçu en parallèle peuvent tous deux écrire une information pour le même tenant, sans aucune garantie sur l'ordre d'exécution réel de leurs goroutines respectives. La solution adoptée associe chaque entrée à un **timestamp de dernière mise à jour**, et rejette toute écriture dont le timestamp est **plus ancien** que celui déjà stocké — garantissant qu'une information obsolète ne peut jamais faire régresser une information plus récente, quel que soit l'ordre d'arrivée réel.

### 14.7 `singleflight` — déduplication des appels concurrents

Utilisé par `CachedStore.Get()`. Distinct des mécanismes ci-dessus : ce n'est pas un problème de **sécurité mémoire** (le `RWMutex` protège déjà correctement la map du cache), mais un problème d'**efficacité** — sans `singleflight`, un pic de requêtes simultanées pour le même tenant lors d'un cache miss déclencherait autant d'appels dupliqués vers la source de vérité (*cache stampede*). `singleflight.Group.Do(key, fn)` garantit qu'un seul appel réel part vers la source pour une clé donnée ; les appelants concurrents attendent et reçoivent le même résultat.

### 14.8 Ce que `go test -race` peut détecter

Le détecteur de course de Go instrumente le binaire de test pour surveiller tous les accès mémoire concurrents. Il détecte notamment :

- une lecture et une écriture simultanées sur la même variable/le même champ, sans synchronisation partagée (ce qui aurait été le cas si `Get()` avait continué à renvoyer le pointeur interne de `MemoryStore`, combiné à une écriture directe en dehors du verrou) ;
- une incohérence dans l'utilisation d'une map Go non protégée sous accès concurrent ;
- tout accès non protégé qui *pourrait* corrompre l'état, même si le test ne « voit » pas de valeur incorrecte simplement par chance d'ordonnancement.

`go test -race` a été utilisé systématiquement tout au long de chaque étape, y compris dans la CI GitHub Actions à chaque push, sur le module racine et sur chaque sous-module Go séparé.

---

## 15. Testabilité

### 15.1 Principe général

Chaque composant est conçu pour être testé **indépendamment**, sans besoin d'infrastructure réelle. Ce principe a été appliqué dès l'étape 1 et maintenu jusqu'à l'étape 8.

### 15.2 Fakes internes

Pour tester les composants centraux du toolkit lui-même, des implémentations fictives minimales des interfaces (`fakeResolver`, `fakeStore`, `fakeAdminStore`, `countingStore`) sont écrites directement dans les fichiers de test des packages concernés — jamais exportées publiquement, elles n'existent que pour isoler le composant testé de ses dépendances réelles.

### 15.3 Tests unitaires purs

La grande majorité des composants (`tenantctx`, `store`, `eventbus`, `banchecker`, `ratelimit`, `cachekey`, `rbac`, `metrics`, `admin`) sont testés avec des tests Go standards (`testing` + `testify`), sans dépendance externe.

### 15.4 Tester les middlewares HTTP — `httptest`

Le `httptest` de `net/http` (`httptest.NewRequest`, `httptest.NewRecorder`) est utilisé pour tester l'adaptateur `net/http` et l'adaptateur Chi (qui repose directement sur `http.Handler`), simulant une véritable chaîne de traitement HTTP de bout en bout.

### 15.5 Tester les middlewares de framework — mécanismes spécifiques à chaque framework

- **Gin** — `gin.CreateTestContext(recorder)` construit un `*gin.Context` de test depuis un `*gin.Engine` interne.
- **Echo** — `echo.New()` + `e.NewContext(req, recorder)` construit un `echo.Context` de test.
- **Chi** — repose directement sur `net/http`, donc les mêmes primitives `httptest` suffisent (aucun mécanisme de test spécifique à Chi).

Dans chaque cas, le test appelle le **véritable** handler produit par le middleware (`handler.ServeHTTP(...)`, `handler(c)`), jamais une fonction interne directement — garantissant que le comportement testé correspond exactement à ce qui se passerait en production, y compris le routage lui-même (pour l'API Admin en particulier, utiliser `handler.ServeHTTP()` plutôt que d'appeler directement le handler valide aussi le fait que les déclarations de routes `http.ServeMux` fonctionnent réellement).

### 15.6 Tester Redis — `miniredis`

Voir [section 10.23](#1023-stratégie-de-test--pourquoi-miniredis-plutôt-quun-vrai-redis). Une implémentation Redis purement en mémoire permet de tester `RedisEventBus` sans serveur Redis réel, ni localement ni en CI.

### 15.7 `tenanttest` — testabilité pour les utilisateurs externes

Le package `tenanttest` étend ce principe de testabilité **au-delà** du toolkit lui-même, pour les développeurs qui l'utilisent dans leurs propres applications (voir [étape 8](#11-étape-8--outils-de-test-tenanttest) en détail).

### 15.8 Tests de concurrence — `go test -race`

Chaque composant à état partagé possède au moins un test dédié à la concurrence réelle (plusieurs goroutines simultanées), systématiquement exécuté avec le flag `-race` — que ce soit localement ou dans la CI GitHub Actions. C'est ce mécanisme qui a permis de découvrir et corriger des problèmes de conception (notamment le piège du pointeur partagé dans `MemoryStore`, section 14.2) avant qu'ils ne deviennent des bugs en production.

### 15.9 Pourquoi les composants sont conçus pour être testables indépendamment

Chaque composant expose une **interface minimale** définie dans le package `tenant` (ou dans son propre package, pour les composants sans contrat centralisé pour l'instant). N'importe quelle implémentation, y compris une fictive écrite en quelques lignes dans un fichier de test, peut satisfaire ce contrat grâce au typage structurel de Go — permettant à un composant de niveau supérieur (`Manager`, `admin.Service`, un middleware) d'être testé sans jamais instancier une vraie base de données, un vrai Redis, ou un vrai serveur HTTP complet.

---

## 16. Limitations et évolutions futures

Cette section rassemble toutes les limitations **explicitement documentées** en cours de route, ainsi que les évolutions envisagées mais **non implémentées**.

| Sujet | État actuel (implémenté) | Limitation connue | Évolution future envisagée |
|---|---|---|---|
| `SetState → Publish` (Admin) | Ordre adopté, jamais d'événement mensonger | Non atomique — un `Publish` peut échouer après un `SetState` réussi, événement potentiellement perdu | Pattern Outbox (transaction unique état + événement, worker de publication asynchrone avec retry) |
| API Admin — authentification | Aucune | L'API ne doit pas être exposée directement sur Internet en production | Authentification/autorisation à ajouter |
| API Admin — erreurs HTTP | `writeError` renvoie toujours `500` | Pas de distinction `404`/`409`/`503` | Mapping fin des erreurs, nécessite une erreur sentinelle exportée au niveau `AdminStore` |
| API Admin — endpoints | `Ban`/`Disable`/`Activate` seulement | Pas de `Create`/`Get` HTTP, même si `AdminStore.Create` et `Store.Get` existent | Ajouter d'abord `Service.Create`/`Service.Get`, puis les endpoints correspondants, si le besoin métier survient |
| `EventBus` (Redis) | Pub/Sub fonctionnel, fail-fast sur subscribe, `Stop()` pour un arrêt propre | Aucune — la reconnexion et le réabonnement sur des pannes réseau transitoires sont gérés nativement par le `*redis.PubSub` de go-redis (reconnexion automatique + ping de santé périodique, voir §10.16) ; ceci était listé ici précédemment comme une lacune basée sur une hypothèse non vérifiée, corrigée après lecture du code source de go-redis | n/a |
| `Redis` | Propagation en temps réel opérationnelle | Pas de monitoring dédié ni de métriques de latence de propagation (la résilience de connexion elle-même est déjà gérée par go-redis, voir ci-dessus) | Monitoring dédié, métriques de latence de propagation |
| `MemoryStore.Get()` — copie | Copie superficielle (shallow) du `*Tenant` | Le champ `Roles []Role` partage le même tableau sous-jacent que l'original ; un consommateur mutant `Roles[i]` affecterait quand même l'original | Copie profonde (deep copy) du slice `Roles` si ce risque devient significatif |
| Tests `RedisEventBus` | Couverts via `miniredis` (simulation) | `miniredis` ne garantit pas toutes les subtilités d'un vrai serveur Redis | Test d'intégration avec un vrai Redis, en complément, pas en remplacement |
| `tenanttest` | `WithFakeTenant` / `WithFakeTenantFull` | Pas de simulation complète du pipeline HTTP | `NewFakeResolver`, `NewFakeStore`, `NewFakeManager` — seulement si un vrai besoin survient |
| `Prometheus` (Metrics) | Interface `MetricsCollector` définie + implémentation en mémoire | Aucun adaptateur Prometheus concret construit à ce stade | Implémentation `PrometheusMetrics` satisfaisant le même contrat |
| `RateLimiter` distribué | `RedisRateLimiter` de `ratelimit/redis` existe, satisfaisant la même interface `ratelimit.RateLimiter` (voir §7.16) | Compteur à fenêtre fixe (pas un vrai token bucket) — jusqu'à ~2x le quota configuré peut passer à une limite de fenêtre ; ~2 000x la latence du `TenantRateLimiter` local | Un algorithme distribué à fenêtre glissante ou token-bucket précis, si l'approximation à fenêtre fixe s'avère insuffisante en pratique |
| `go.mod` — directive `replace` (sous-modules) | Utilisée pour le développement local avant publication | Pointe vers un chemin local (`../..`), invalide pour un vrai utilisateur externe | À supprimer une fois le module racine taggé et publié |

> **Règle transversale à retenir** : chaque limitation ci-dessus a été **explicitement documentée dans le code au moment où elle a été identifiée** (commentaires, messages de log), plutôt que laissée implicite — cohérent avec le principe général du projet préférant une incohérence *observable* aujourd'hui à une fausse solution prématurée.

---

## 17. Arborescence des packages

```text
tenant-core/                          (root module)
├── tenant.go                         → TenantID, State, Tenant, Resolver,
│                                        Store, AdminStore, Manager, Option,
│                                        New(), Manager.Resolve()
├── tenant_test.go
│
├── tenantctx/                        → WithTenant(), FromContext()
│   └── context_test.go
│
├── resolver/                         → SubdomainResolver
│   └── subdomain_test.go
│
├── store/                            → MemoryStore, CachedStore,
│   │                                    ErrTenantNotFound
│   ├── memory.go
│   ├── memory_test.go
│   ├── cached.go
│   └── cached_test.go
│
├── eventbus/                         → EventBus, TenantEvent, MemoryEventBus
│   ├── eventbus.go
│   └── memory.go (+ tests)
│
├── banchecker/                       → BanChecker
│   └── banchecker_test.go
│
├── ratelimit/                        → TenantRateLimiter, LimitFunc
│   └── ratelimit_test.go
│
├── cachekey/                         → Key()
│   └── cachekey_test.go
│
├── rbac/                             → RBAC, DefineRole(), Can()
│   └── rbac_test.go
│
├── metrics/                          → MetricsCollector, MemoryMetrics
│   └── memory_test.go
│
├── admin/                            → Service, HTTPHandler
│   ├── admin.go
│   ├── http.go
│   └── admin_test.go / http_test.go
│
├── tenanttest/                       → WithFakeTenant(), WithFakeTenantFull()
│   └── tenanttest_test.go
│
├── middleware/
│   ├── nethttp.go                    → Wrap()                (root module)
│   │
│   ├── gin/                          → Middleware()   (SEPARATE Go sub-module)
│   │   ├── go.mod   (module .../middleware/gin, replace → ../..)
│   │   ├── gin.go
│   │   └── gin_test.go
│   │
│   ├── echo/                         → Middleware()   (SEPARATE Go sub-module)
│   │   ├── go.mod   (module .../middleware/echo, replace → ../..)
│   │   ├── echo.go
│   │   └── echo_test.go
│   │
│   └── chi/                          → Middleware()   (SEPARATE Go sub-module)
│       ├── go.mod   (module .../middleware/chi, replace → ../..)
│       ├── chi.go
│       └── chi_test.go
│
├── eventbus/redis/                   → RedisEventBus (SEPARATE Go sub-module)
│   ├── go.mod        (module .../eventbus/redis, replace → ../..)
│   ├── redis.go
│   └── redis_test.go (miniredis)
│
├── .github/workflows/ci.yml          → Multi-module CI (root + 4 sub-modules)
├── LICENSE (MIT)
├── README.md
└── .gitignore
```

**Statut des sous-modules Go indépendants** — `middleware/gin`, `middleware/echo`, `middleware/chi`, et `eventbus/redis` ont chacun leur **propre `go.mod`**, distinct du module racine. Cette organisation garantit qu'un développeur utilisant seulement `net/http` (ou seulement Gin) n'installe **jamais** les dépendances des frameworks/technologies qu'il n'utilise pas — chaque sous-module se build et se teste indépendamment (`cd middleware/gin && go test ./...`), et la CI GitHub Actions exécute une étape dédiée par sous-module (`working-directory`), en plus de l'étape pour le module racine.

Chaque sous-module référence le module racine via une directive `replace ... => ../..` pendant le développement, lui permettant de pointer vers le code local avant qu'une version taguée ne soit publiée sur le dépôt public.

---

## 18. Décisions / points à clarifier

Cette section signale les endroits où les documents source décrivent des contrats de façon **conceptuelle** (souvent introduits par le mot *« Conceptuellement »* dans les documents originaux), qui diffèrent légèrement de la forme exacte adoptée dans l'implémentation réelle, sans que cela ne remette en cause la décision architecturale sous-jacente — seulement le détail de signature.

### 18.1 RateLimiter — interface conceptuelle vs. implémentation adoptée

Le document source de l'étape 4 présente un contrat conceptuel simplifié :

```go
type RateLimiter interface {
    Allow(ctx context.Context, id TenantID) bool
}
```

L'implémentation réellement adoptée repose sur un type concret `TenantRateLimiter`, dont la méthode `Allow` prend directement un `*Tenant` (pas seulement un `TenantID`), et dont la règle de limite par tenant est **injectée** via une fonction (`LimitFunc`) fournie par l'application — plutôt que fixée dans l'implémentation elle-même — construite sur `golang.org/x/time/rate` (un modèle *token bucket*). Le principe métier (une limite indépendante par tenant, un cœur agnostique de l'infrastructure) reste identique ; seule la forme exacte du contrat diffère de la version conceptuelle présentée dans le document source.

### 18.2 RBAC — Authorizer conceptuel vs. RBAC/Can adopté

Le document source présente un contrat conceptuel :

```go
type Authorizer interface {
    Can(t *Tenant, permission string) bool
}
```

L'implémentation adoptée est un type concret `RBAC` (pas une interface publiée dans `tenant.go`), avec une méthode `DefineRole(tenantID, role, permissions)` pour l'enregistrement, et `Can(t *Tenant, permission string) bool` pour la vérification — les définitions étant organisées **par tenant** (`map[TenantID]map[role]map[permission]struct{}`), comme fidèlement décrit dans le document source (section 8.5 de ce document). Le principe (séparation rôle/permission, indépendance par tenant, aucune dépendance HTTP) est identique. (Depuis la v0.3.0, `permission`/`permissions` sont typés `rbac.Permission` — un type nommé `string`, pour la même raison de sécurité de typage que `tenant.TenantID` — plutôt qu'un simple `string`/`[]string` ; `DefineRole` est aussi devenu variadique, et son paramètre `role` (ainsi que `Tenant.Roles`) est typé `tenant.Role`, défini dans le package racine plutôt que dans `rbac` pour préserver la direction de dépendance `tenant → rbac` déjà établie. Une chaîne littérale comme `"users:write"` ou `"admin"` continue de fonctionner sans changement sur chaque appel grâce à la conversion implicite des constantes non typées de Go. C'est un breaking change pour les appelants qui passent une variable `string`/`[]string` plutôt qu'un littéral — voir `CHANGELOG.md`.)

### 18.3 Metrics — Prometheus mentionné comme fait vs. statut réel

Le titre de l'étape 5 dans les documents source (« RBAC + Metrics (Prometheus) ») et plusieurs passages décrivent une implémentation `PrometheusMetrics` en termes assez concrets. D'après l'avancement réel du projet, seuls l'**interface** `MetricsCollector` et une implémentation **en mémoire** (`MemoryMetrics`, avec `sync.Map` + `atomic.Int64`) ont réellement été construits et testés à ce stade — l'adaptateur Prometheus lui-même reste une **évolution future** listée en section 16, pas un composant déjà livré. Cette distinction est faite ici conformément à la règle *« ne pas transformer des améliorations futures en fonctionnalités déjà implémentées »*.

### 18.4 `tenant.New()` et l'orchestration complète des composants

Le document de synthèse source (sections 11 à 16 du document *resumer.txt*) présente une vision englobante de `tenant.New()` avec des options comme `WithEventBus`, `WithRateLimiter`, `WithRBAC`, `WithMetrics` — orchestrant potentiellement les neuf composants du toolkit. L'implémentation réelle de `Manager`/`New()` reste délibérément **plus limitée** : seuls `Resolver` et `Store` sont assemblés par `New()`, la méthode `Resolve()` s'arrêtant à produire un `*Tenant` (sans construire de `context.Context`, pour éviter une dépendance circulaire avec `tenantctx`). Les autres composants (`BanChecker`, `RateLimiter`, `RBAC`, `Metrics`, `CacheKeyer`, `EventBus`) restent des briques indépendantes que l'application ou les adaptateurs de middleware invoquent explicitement, sans être automatiquement enchaînées par `Manager` lui-même — cohérent avec le principe explicitement énoncé dans le document source : *« ce diagramme représente les composants disponibles dans l'écosystème, pas nécessairement un ordre d'exécution que `tenant.New()` imposera automatiquement »*, et *« `tenant.New()` doit rester propre : il compose des dépendances ; il ne doit pas devenir un middleware géant qui mélange toutes les responsabilités »*.

### 18.5 Nom du package `banchecker`

Un document source (étape 3) place `BanChecker` dans un package nommé `banchecker/`, cohérent avec le reste de la documentation et avec l'implémentation réelle.

---

*Fin de la documentation technique complète de tenant-core.*
