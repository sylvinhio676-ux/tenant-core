# tenant-core — Architecture et documentation technique complète

> Toolkit Go de multi-tenancy natif : résolution, isolation de contexte, cache, bannissement temps réel, rate-limiting, RBAC, métriques, Admin API et propagation multi-instance — distribué comme librairie middleware compatible avec les routers Go existants.

---

## Table des matières

1. [Présentation générale](#1-présentation-générale)
2. [Principes fondamentaux](#2-principes-fondamentaux)
3. [Vue d'ensemble de l'architecture](#3-vue-densemble-de-larchitecture)
4. [Étape 1 — Fondations](#4-étape-1--fondations)
5. [Étape 2 — Store et cache](#5-étape-2--store-et-cache)
6. [Étape 3 — Ban temps réel](#6-étape-3--ban-temps-réel)
7. [Étape 4 — RateLimiter et CacheKeyer](#7-étape-4--ratelimiter-et-cachekeyer)
8. [Étape 5 — RBAC et Metrics](#8-étape-5--rbac-et-metrics)
9. [Étape 6 — Framework adapters](#9-étape-6--framework-adapters)
10. [Étape 7 — Admin API et EventBus Redis](#10-étape-7--admin-api-et-eventbus-redis)
11. [Étape 8 — Helpers de test (tenanttest)](#11-étape-8--helpers-de-test-tenanttest)
12. [Architecture complète finale](#12-architecture-complète-finale)
13. [Flux de données](#13-flux-de-données)
14. [Concurrence et thread-safety](#14-concurrence-et-thread-safety)
15. [Testabilité](#15-testabilité)
16. [Limites et évolutions futures](#16-limites-et-évolutions-futures)
17. [Arbre des packages](#17-arbre-des-packages)
18. [Décisions / points à clarifier](#18-décisions--points-à-clarifier)

---

## 1. Présentation générale

### Objectif de tenant-core

`tenant-core` est un toolkit Go dont l'objectif est de résoudre un problème récurrent des applications SaaS multi-entreprises :

> Quand une requête arrive, l'application doit toujours savoir de quel tenant elle relève, et empêcher que le contexte d'un tenant soit mélangé avec celui d'un autre.

```text
                 TON APPLICATION
                       │
          ┌────────────┼────────────┐
          ▼            ▼            ▼
       Entreprise A  Entreprise B  Entreprise C
          │            │            │
       Users A       Users B       Users C
       Data A        Data B        Data C
```

### Problème résolu

Sans toolkit dédié, chaque équipe réinvente sa propre gestion du multi-tenant : résolution du tenant depuis la requête, propagation manuelle (`handler(request, tenant)`, `service(request, tenant)`, `repository(request, tenant)`...), isolation du cache, quotas, permissions — avec le risque constant de fuite de données entre tenants.

`tenant-core` répond à cela avec deux opérations fondamentales :

1. **La résolution du tenant** — à partir d'une requête HTTP, déterminer à quel tenant elle appartient.
2. **La propagation du contexte tenant** — transmettre cette information à toutes les couches de l'application sans paramètre explicite supplémentaire.

```text
GET https://entreprise-a.example.com/users

tenant-core doit comprendre :

Cette requête
     ↓
appartient à
     ↓
Tenant A

Puis transmettre cette information :

Request
   │
   ▼
TenantResolver
   │
   │ "C'est Tenant A"
   ▼
ContextInjector
   │
   │ contexte = Tenant A
   ▼
Middleware
   │
   ▼
Handler
```

### Cas d'utilisation

- Une plateforme SaaS où chaque client (entreprise) doit voir ses propres données, sans jamais accéder à celles d'un autre.
- Un système où le bannissement d'un tenant (fraude, abus) doit être appliqué immédiatement, sur toutes les instances du serveur.
- Une application déployée derrière n'importe quel router Go (`net/http`, Gin, Echo, Chi) sans dupliquer la logique multi-tenant pour chacun.
- Un besoin de quotas et de permissions différenciés par tenant, sans configuration globale rigide.

### Philosophie générale

`tenant-core` ne cherche pas à réinventer un serveur HTTP, ni à imposer une stratégie d'isolation de données (tenant_id partagé, schéma séparé, base séparée). Il se positionne comme un toolkit qui traite le **tenant comme un citoyen de première classe** à chaque étage — résolution, cache, quotas, permissions, métriques — sans jamais imposer comment les données elles-mêmes sont isolées en base.

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

Le tenant devient ainsi accessible à tout composant qui en a besoin, sans modifier la signature de chaque fonction intermédiaire.

---

## 2. Principes fondamentaux

Ces principes ont été appliqués de façon constante à travers les huit étapes de construction du toolkit.

### 2.1 Séparation des responsabilités

Chaque composant a une responsabilité unique et clairement délimitée. Le `Resolver` ne connaît pas le `Store` ; le `Store` ne connaît pas l'`EventBus` ; l'`EventBus` ne connaît pas ses consommateurs.

### 2.2 Interfaces minimales

> Une interface doit exposer uniquement ce dont son consommateur a réellement besoin.

C'est le principe qui a motivé la séparation entre `tenant.Store` (lecture, consommé par `Manager`) et `tenant.AdminStore` (écriture, consommé par `admin.Service`) — plutôt qu'une seule interface `Store` gonflée avec `Create`, `Update`, `Ban`, `Disable`, etc.

### 2.3 Typage structurel de Go

Go ne requiert pas qu'un type déclare explicitement `implements InterfaceX`. Dès qu'un type possède les méthodes attendues par une interface, il la satisfait automatiquement :

```text
             tenant.Store
                  ▲
       ┌──────────┼───────────┐
       │          │           │
       ▼          ▼           ▼
 MemoryStore CachedStore  DBStore (futur)
```

Ce mécanisme permet à `SubdomainResolver`, `MemoryStore`, `CachedStore`, `RedisEventBus`, `MemoryMetrics`, etc. de satisfaire les contrats définis dans le package `tenant` sans jamais avoir besoin d'importer ce package en sens inverse — évitant ainsi les dépendances circulaires.

### 2.4 Agnosticisme

Le cœur du toolkit ne dépend d'aucune technologie d'infrastructure particulière :

- ni d'un framework HTTP (Gin, Echo, Chi) ;
- ni de Redis ;
- ni de Prometheus ;
- ni d'un moteur de base de données particulier.

```text
             tenant
               │
       ┌───────┴────────┐
       │                │
   Interface       Interface
       │                │
       ▼                ▼
Implémentation A   Implémentation B
```

### 2.5 Fail-fast

Une erreur de **configuration** du programme (composant obligatoire manquant, connexion Redis injoignable au démarrage) doit être détectée **immédiatement**, généralement via `panic` ou une erreur retournée au démarrage — plutôt que découverte silencieusement en production. Une erreur survenant pendant le **traitement d'une requête** est en revanche toujours gérée via le mécanisme standard `error`.

### 2.6 Testabilité

Chaque composant est conçu pour être testable indépendamment, sans dépendance à une infrastructure réelle (base de données, Redis, framework HTTP complet). Le package `tenanttest` prolonge ce principe pour les utilisateurs externes du toolkit.

### 2.7 Isolation multi-tenant

> Toute ressource partagée doit être explicitement dimensionnée par `TenantID`.

Ce principe est appliqué de façon transversale : au stockage (`TenantID → Tenant`), au cache (`TenantID → Cache Key`), au rate limiting (`TenantID → Rate Limit Bucket`), aux permissions (`TenantID → Roles → Permissions`).

### 2.8 Concurrence sûre

Le toolkit est destiné à des applications HTTP qui traitent naturellement des requêtes concurrentes. Chaque composant à état partagé est protégé par le mécanisme de synchronisation adapté à son profil d'accès (voir [section 14](#14-concurrence-et-thread-safety)).

### 2.9 Observabilité

Le toolkit permet de mesurer son propre comportement (requêtes, erreurs, latence, refus RBAC, refus de rate limit) sans imposer un backend de métriques particulier.

### 2.10 Séparation métier / transport

La logique métier (`Manager`, `admin.Service`, `RBAC`) ne connaît jamais le protocole de transport qui l'invoque (HTTP, CLI, gRPC futur, tests). C'est le rôle des adaptateurs de faire le pont.

---

## 3. Vue d'ensemble de l'architecture

### Le principe central

> À partir d'une requête HTTP, identifier le tenant, récupérer son état, puis appliquer les différents mécanismes de protection et d'isolation.

```text
                    REQUÊTE HTTP
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
                   CONTEXTE
```

### La séparation fondamentale des packages

```text
tenant
│
├── définit les contrats et le modèle métier (le "quoi")
│
├── tenantctx        → transport du tenant via context.Context
├── resolver         → résolution concrète (le "comment")
├── store            → persistance et cache
├── eventbus         → propagation d'événements
├── ratelimit        → quotas par tenant
├── cachekey         → isolation des clés de cache
├── rbac             → permissions par tenant
├── metrics          → observabilité
├── middleware       → adaptateurs de routers
│   ├── net/http
│   ├── gin
│   ├── echo
│   └── chi
├── admin            → administration des tenants
├── eventbus/redis   → propagation multi-instance
└── tenanttest       → outils de test
```

> **Le package `tenant` définit le "quoi", les sous-packages définissent le "comment".**

Cette organisation permet :

- **un faible couplage** — chaque sous-package ne dépend que du contrat qu'il implémente, jamais des autres implémentations ;
- **des interfaces minimales** — chaque composant n'expose que ce dont son consommateur a besoin ;
- **l'agnosticisme vis-à-vis des frameworks** — le cœur ignore Gin, Echo, Chi, Redis, Prometheus ;
- **la testabilité** — chaque contrat peut être satisfait par une implémentation factice (fake) en test ;
- **l'extensibilité** — une nouvelle implémentation (`PostgresStore`, `RedisRateLimiter`, `PrometheusMetrics`) peut être ajoutée sans modifier le cœur ;
- **le remplacement des implémentations** — passer de `MemoryEventBus` à `RedisEventBus` ne change aucun contrat, seulement l'adaptateur utilisé.

---

## 4. Étape 1 — Fondations

### 4.1 Objectif

Avant Redis, avant les middlewares Gin/Echo/Chi, avant l'Admin API ou le RBAC, il fallait répondre à une question fondamentale :

> Comment une requête HTTP est-elle associée à un tenant, et comment garantir que les données de ce tenant restent isolées ?

```text
                    Requête HTTP
                         │
                         ▼
                ┌─────────────────┐
                │    Resolver     │
                │ "Quel tenant ?" │
                └────────┬────────┘
                         │
                    TenantID
                         │
                         ▼
                ┌─────────────────┐
                │ Tenant Context  │
                │ "Quel tenant    │
                │ pour cette      │
                │ requête ?"      │
                └────────┬────────┘
                         │
                         ▼
                  Handler métier
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

Un type nommé dédié plutôt qu'un simple `string`, pour exprimer l'intention et bénéficier de la sécurité du typage : `var id tenant.TenantID` est conceptuellement différent de `var email string`. Le compilateur refuse de confondre les deux, même si les deux sont "juste des strings" en interne.

**`State`**

```go
type State string

const (
    Active   State = "active"
    Disabled State = "disabled"
    Banned   State = "banned"
)
```

Les trois états ont une signification métier distincte :

- **`Active`** — le tenant peut normalement accéder au système.
- **`Disabled`** — le tenant est désactivé (ex: abonnement terminé). La désactivation peut être propagée avec un léger délai, notamment via un cache (cohérence *eventual*).
- **`Banned`** — le tenant est banni pour fraude ou abus. Contrairement à `Disabled`, le bannissement doit être propagé **immédiatement** — ce qui justifie l'introduction ultérieure de `BanChecker` et de l'`EventBus` (Étape 3).

**`Tenant`**

```go
type Tenant struct {
    ID    TenantID
    State State
    Roles []string
}
```

```text
Tenant
 ├── ID
 ├── State
 └── Roles
```

Le champ `Roles` a été prévu dès le départ pour permettre l'intégration ultérieure du RBAC (Étape 5).

### 4.3 Le contrat Resolver

Décision architecturale importante : le contrat est placé dans le package racine `tenant`, pas dans le package `resolver`.

```go
type Resolver interface {
    Resolve(r *http.Request) (TenantID, error)
}
```

Cette interface répond à une seule question : *à quel tenant appartient cette requête HTTP ?* Elle ne dit rien sur **comment** le tenant est trouvé.

```text
tenant
   │
   └── définit le contrat
          │
          ▼
      Resolver

resolver/
   └── SubdomainResolver (implémentation)
```

### 4.4 SubdomainResolver

Première implémentation concrète, basée sur le sous-domaine de la requête :

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
       │ satisfait automatiquement
       │
resolver
 │
 └── SubdomainResolver
```

Grâce au typage structurel de Go, `SubdomainResolver` n'a jamais besoin d'écrire `implements tenant.Resolver`. Il suffit qu'il possède la méthode `Resolve(*http.Request) (tenant.TenantID, error)`.

### 4.6 Le Context Injector — `tenantctx`

Une fois le tenant identifié, il faut le transmettre aux couches suivantes. Le package `tenantctx/` gère le stockage du tenant dans le `context.Context` standard :

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

Une fonction métier n'a donc besoin de connaître ni le sous-domaine, ni HTTP, ni Gin, ni Echo, ni Chi, ni Redis, ni la façon dont le tenant a été résolu — elle reçoit simplement un `context.Context`.

### 4.7 Pourquoi context.Context

Le contexte permet de faire voyager l'identité du tenant à travers les couches, sans devoir ajouter `tenantID string` à toutes les signatures :

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

Il ne suffisait pas de réussir à identifier un tenant : il fallait garantir que le contexte d'une requête du tenant A ne peut jamais être accidentellement réutilisé pour le tenant B.

```text
Requête A
tenant-a.example.com
       │
       ▼
Context A
TenantID = tenant-a

Requête B
tenant-b.example.com
       │
       ▼
Context B
TenantID = tenant-b
```

Les deux contextes doivent rester complètement indépendants.

### 4.9 Le test critique d'isolation

L'isolation a été considérée comme une propriété à tester **explicitement**, jamais simplement supposée.

```text
Créer contexte A
      │
      ▼
injecter tenant-A
      │
      ▼
Créer contexte B
      │
      ▼
injecter tenant-B
      │
      ▼
vérifier A == tenant-A
vérifier B == tenant-B
```

L'objectif est notamment de détecter une mauvaise implémentation utilisant une variable globale au lieu du `context.Context` :

```go
var currentTenant *Tenant // ❌ dangereux
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
récupère B ❌
```

Le contexte standard, lui, est **immuable** — `WithTenant()` ne modifie jamais un contexte existant, il en crée un nouveau qui l'enveloppe. Deux contextes créés à partir de branches différentes ne peuvent jamais se marcher dessus. Ce mécanisme évite précisément ce type de partage implicite dangereux.

**Deux tests concrets ont validé cette propriété** :

- Un test **structurel** : injecter deux tenants dans deux contextes distincts, vérifier qu'ils restent bien différents, et que muter le tenant récupéré depuis un contexte n'affecte jamais l'autre contexte.
- Un test **sous concurrence réelle** : une centaine de goroutines simulant des requêtes simultanées alternant entre deux tenants, exécuté systématiquement avec `go test -race`, pour garantir qu'aucune goroutine ne voit jamais le tenant d'une autre.

### 4.10 La clé de contexte privée

Un détail technique important : la clé utilisée par `context.WithValue` pour stocker le tenant n'est **jamais** une simple `string`. Une clé `string` comme `"tenant"` pourrait entrer en collision avec n'importe quelle autre bibliothèque tierce utilisant la même clé, avec un risque réel d'écrasement silencieux.

La solution retenue est un type de clé **privé, non exporté** :

```go
type contextKey int

const tenantContextKey contextKey = 0
```

Comme `contextKey` est un type non exporté, aucun autre package ne peut créer une valeur de ce type — même en connaissant son nom. Et même si un autre package définissait aussi un `type contextKey int` avec la valeur `0`, ce serait un type Go **différent** (les types sont comparés par identité complète package + nom), donc `context.WithValue` ne les confondrait jamais. C'est le pattern documenté officiellement par la stdlib Go elle-même.

### 4.11 Architecture des packages après l'Étape 1

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
│   └── contexte du tenant
│
└── resolver/
    └── SubdomainResolver
```

| Package | Responsabilité |
|---|---|
| `tenant` | Concepts et contrats fondamentaux |
| `tenantctx` | Transport du tenant via `context.Context` |
| `resolver` | Résolution concrète du tenant |
| `SubdomainResolver` | Identification depuis le sous-domaine |

### 4.12 Le principe architectural établi dès l'Étape 1

```text
                CONTRATS
                   │
                   ▼
               package tenant
                   │
        ┌──────────┼──────────┐
        ▼          ▼          ▼
    resolver     store     eventbus
        │          │          │
        ▼          ▼          ▼
 implémentation implémentation implémentation
```

Ce principe s'est retrouvé constamment dans toute la suite du toolkit : `tenant.Resolver` ← `SubdomainResolver`, `tenant.Store` ← `MemoryStore`/`CachedStore`, `eventbus.EventBus` ← `MemoryEventBus`/`RedisEventBus`.

**Résumé de l'étape** : identifier → représenter → transporter → isoler le tenant. Cette base a permis de rester agnostique vis-à-vis des frameworks et de construire les adaptateurs Gin/Echo/Chi sans jamais modifier le cœur du système.

---

## 5. Étape 2 — Store et cache

### 5.1 Objectif

L'Étape 1 répondait à *« quel tenant correspond à cette requête ? »*, mais uniquement avec son identifiant. Il fallait maintenant répondre à : *« quelles sont les informations de ce tenant et dans quel état se trouve-t-il ? »* — c'est le rôle du `Store`.

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
                 cache hit ?
                  /           \
                oui            non
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
- **`IsBanned`** — vérification spécialisée et rapide du bannissement, qui deviendra particulièrement importante avec le `BanChecker` (Étape 3).

Cette séparation est importante : le chemin normal de résolution n'a pas besoin de connaître les opérations d'administration (voir Étape 7, `AdminStore`).

### 5.3 Pourquoi `Store` est une interface

```text
             tenant.Store
                  ▲
       ┌──────────┼───────────┐
       │          │           │
       ▼          ▼           ▼
 MemoryStore CachedStore  DBStore (futur)
```

Le cœur du toolkit doit pouvoir remplacer `MemoryStore` par `PostgreSQLStore`, `MySQLStore`, `RedisStore` ou `APIStore` sans jamais modifier `Manager`.

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

Une map Go classique n'est pas sûre pour des accès concurrents impliquant des écritures. `RWMutex` permet deux types de verrouillage :

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

Plusieurs lecteurs peuvent lire simultanément ; une écriture reste exclusive. Ce profil est particulièrement adapté à un `Store` où les lectures sont beaucoup plus fréquentes que les écritures.

### 5.6 Le piège des pointeurs partagés

Une subtilité importante rencontrée pendant la conception : la map contient `map[TenantID]*Tenant`, donc des **pointeurs**, pas des copies.

```text
Map
 │
 └── *Tenant ──────────┐
                       │
                       ▼
                    Tenant
                    State
```

Si `Get()` retourne directement `t` (le pointeur interne), l'appelant obtient un accès direct à l'objet réellement stocké dans le store. Faire `t.State = tenant.Disabled` hors verrou, pendant qu'une autre goroutine lit ce même champ via `Get()`, provoque une **data race** authentique — détectable par `go test -race`.

```text
Goroutine A                 Goroutine B

t.State = Banned
       │
       │                 Get()
       │                   │
       ▼                   ▼
   écriture              lecture
```

> **Protéger uniquement la map ne suffit pas lorsque les valeurs de la map sont des pointeurs mutables.**

**La solution retenue** :

- **`Get()` retourne toujours une copie**, jamais le pointeur interne. Le consommateur externe ne peut donc jamais muter l'état interne du store via le pointeur reçu.
- Les opérations d'écriture (`SetState`, `Create`, `Update`) modifient l'objet interne **directement, sous verrou exclusif (`Lock`)** — jamais via un aller-retour `Get()` + modification + réécriture, qui recréerait une fenêtre de *lost update* entre deux étapes séparées.

```text
MemoryStore
    │
    ▼
*Tenant interne
    │
    │ copie
    ▼
*Tenant retourné
```

Une **primitive interne d'écriture** (`set`, non exportée) reste utilisée en interne par `Create`/`Update`/`SetState`, mais n'est jamais exposée publiquement — le contrat public d'écriture passe exclusivement par ces trois méthodes explicites, jamais par une écriture brute.

### 5.7 `Get()`

```text
TenantID
   │
   ▼
MemoryStore
   │
   ├── chercher le tenant
   │
   ├── vérifier l'existence
   │
   └── retourner le tenant (copie)
```

Si le tenant n'existe pas, une erreur sentinelle explicite est retournée : `ErrTenantNotFound`. Cela permet aux couches supérieures de distinguer un tenant réellement inexistant d'une erreur technique quelconque.

### 5.8 `IsBanned()`

```text
TenantID
   │
   ▼
Store
   │
   ▼
State == Banned ?
   │
   ├── oui → true
   └── non → false
```

### 5.9 Modification d'état — `Disable()` / `SetState()`

Un tenant peut passer d'`Active` à `Disabled` (par exemple, fin d'abonnement) :

```text
abonnement terminé
       │
       ▼
Disable()
       │
       ▼
State = Disabled
```

Cette modification est protégée par le même mécanisme de synchronisation que les autres écritures :

```text
Disable()
   │
   ▼
Lock()
   │
   ▼
modifier tenant
   │
   ▼
Unlock()
```

### 5.10 Pourquoi un TTL est nécessaire

Une base de données distante peut être beaucoup plus lente qu'une lecture en mémoire. Sans cache :

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

Si des milliers de requêtes demandent continuellement le même tenant, cela devient coûteux. D'où l'introduction d'un cache devant le store.

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

Le champ `source` est basé sur l'**interface** `tenant.Store`, jamais sur une implémentation concrète — décision importante : le cache ne dépend d'aucune implémentation particulière, ce qui lui permet d'envelopper n'importe quel futur `Store` (Postgres, Redis, etc.) sans modification.

### 5.12 Fonctionnement du cache

**Cache HIT**

```text
Get("tenant-a")
       │
       ▼
   Cache trouvé
       │
       ▼
   encore valide ?
       │
       ▼
     Tenant
```

**Cache MISS**

```text
Get("tenant-a")
       │
       ▼
   Cache absent/expiré
       │
       ▼
 source.Get(...)
       │
       ▼
    Tenant
       │
       ▼
 mise en cache
       │
       ▼
    retour
```

### 5.13 Le TTL

Chaque entrée du cache possède une durée de validité (par exemple 30 secondes). Après expiration, l'entrée est considérée invalide et le store sous-jacent est de nouveau interrogé.

```text
Cache
 │
 ├── tenant-a
 │      expired ❌
 │
 ▼
source.Get()
```

### 5.14 Pourquoi accepter une légère incohérence pour `Disabled`

Le TTL est particulièrement adapté à l'état `Disabled` : pendant la fenêtre de validité du cache, une instance peut encore considérer un tenant désactivé comme actif. C'est une incohérence temporaire **acceptée**.

```text
Disabled  → propagation eventual (TTL acceptable)
Banned    → propagation immédiate (nécessite un événement — Étape 3)
```

Cette distinction se retrouve dans la propriété du `MemoryStore.IsBanned()` : contrairement au `Get()` classique, `IsBanned` (et plus tard, dans `CachedStore`, son équivalent) contourne systématiquement le cache pour interroger directement la source de vérité.

### 5.15 Protection contre les appels dupliqués — `singleflight`

Un problème d'efficacité (pas de sécurité) subsiste malgré le `RWMutex` : si 500 requêtes concurrentes du même tenant arrivent au moment exact d'un cache miss, elles peuvent toutes constater simultanément l'absence de l'entrée avant qu'aucune n'ait eu le temps de la remplir — provoquant 500 appels dupliqués vers la source de vérité (phénomène connu sous le nom de *cache stampede* ou *thundering herd*).

La solution retenue est `golang.org/x/sync/singleflight`, qui garantit qu'un seul appel réel part vers la source pour une clé donnée, les appelants concurrents attendant et recevant le même résultat :

```go
v, err, _ := cs.group.Do(string(id), func() (interface{}, error) {
    t, err := cs.source.Get(ctx, id)
    // ...
    return t, nil
})
```

### 5.16 Architecture complète de l'Étape 2

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

### 5.17 Résumé de l'Étape 2

| Élément | Responsabilité |
|---|---|
| `tenant.Store` | Contrat de lecture des tenants |
| `MemoryStore` | Stockage en mémoire |
| `RWMutex` | Protection des accès concurrents |
| `Get()` | Récupération d'un tenant (copie, jamais le pointeur interne) |
| `IsBanned()` | Vérification spécialisée du bannissement |
| `Disable()` / `SetState()` | Modification d'état, atomique sous `Lock()` |
| `CachedStore` | Ajout d'un cache devant un `Store` |
| `TTL` | Expiration des données du cache |
| `singleflight` | Déduplication des appels concurrents en cas de cache miss |
| `source Store` | Découplage du cache de l'implémentation concrète |
| `ErrTenantNotFound` | Identification explicite d'un tenant inexistant |

> **L'Étape 1 permettait d'identifier le tenant ; l'Étape 2 permet de récupérer son état de manière sûre et performante, tout en préparant la gestion de la concurrence et du cache.**

---

## 6. Étape 3 — Ban temps réel

### 6.1 Objectif

Le cache de l'Étape 2 était volontairement *eventual-consistent* pour les désactivations. Mais pour un bannissement pour fraude ou abus, ce comportement n'est pas acceptable :

```text
Instance A
    │
    ▼
tenant-A = Banned

Instance B (cache non expiré)
    │
    ▼
tenant-A = Active   ❌
```

L'objectif de cette étape est d'introduire un `EventBus`, un `MemoryEventBus`, un `BanChecker`, et la règle selon laquelle `Ban()` est **synchrone**.

```text
              BAN
               │
               ▼
        changement d'état
               │
               ▼
        publication event
               │
        ┌──────┴──────┐
        ▼             ▼
    Instance A     Instance B
        │             │
        ▼             ▼
   BanChecker     BanChecker
        │             │
        ▼             ▼
   blocage immédiat
```

### 6.2 Pourquoi le TTL seul ne suffit pas

```text
TTL = 30 secondes

12:00:00 → tenant-A = Active
12:00:05 → Admin bannit tenant-A

Une autre instance conserve :
tenant-A = Active (expiration 12:00:30)
```

Sans mécanisme supplémentaire, cette instance pourrait accepter le tenant jusqu'à 12:00:30. Acceptable pour `Disabled`, inacceptable pour `Banned`.

```text
Disabled → cohérence eventual → TTL acceptable
Banned   → cohérence quasi immédiate → événement nécessaire
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

`TenantID` et `State` sont le strict minimum fonctionnel pour qu'un abonné sache quoi faire. `Timestamp` a été ajouté volontairement : sans lui, un futur composant (audit, log) ne pourrait même pas répondre à *« quand ce changement a-t-il eu lieu ? »* — et, plus important encore, il devient indispensable pour résoudre un problème de cohérence temporelle (voir 6.9).

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
envoyer un événement

Subscribe
   │
   ▼
recevoir les événements
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

Le cœur du toolkit ne doit connaître que `eventbus.EventBus` — pas Redis, ni NATS, ni Kafka, ni RabbitMQ (implémentations futures envisageables). Même principe que `tenant.Store`.

### 6.6 MemoryEventBus

Implémentation entièrement en mémoire, utilisée pour développer le mécanisme, le tester, et éviter d'avoir besoin de Redis pendant les premières étapes.

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

### 6.7 Isolation des handlers par goroutine

Un point important de l'implémentation : ne **jamais** exécuter les handlers séquentiellement dans la même goroutine. Une mauvaise approche serait :

```go
for _, handler := range handlers {
    handler(event) // ❌ un handler lent bloque tous les suivants
}
```

Si un handler est lent (`time.Sleep`) ou panique, tous les suivants sont retardés ou jamais exécutés. Le principe retenu :

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

**Un deuxième problème d'efficacité a été identifié** : la première version de `Publish()` retenait un `RLock()` pendant toute la durée d'exécution des handlers, ce qui bloquait tout appel concurrent à `Subscribe()`. La correction retenue : copier la liste des handlers sous `RLock`, libérer immédiatement le verrou, puis lancer les goroutines à partir de la copie — `Subscribe()` n'attend donc plus jamais la fin des handlers en cours.

### 6.8 Protection contre les panics — `recover()`

Un handler utilisateur ne doit jamais pouvoir faire tomber tout le processus avec un simple `panic(...)`. Chaque handler est donc exécuté avec un mécanisme de récupération :

```go
defer func() {
    if r := recover(); r != nil {
        // log
    }
}()
```

> **Un handler défaillant ne doit jamais empêcher les autres handlers de recevoir l'événement.**

Point crucial pour un toolkit destiné à des applications externes : un `recover()` ne fonctionne qu'à l'intérieur de la **même goroutine** que le `panic()` — il doit donc être placé dans la fonction lancée par `go`, jamais autour de l'appel à `Publish()` lui-même (qui a déjà retourné depuis longtemps quand le handler s'exécute réellement).

**Un compromis assumé** : puisque chaque handler s'exécute dans sa propre goroutine, `Publish()` ne peut plus rapporter directement les erreurs des handlers à l'appelant. Le retour `nil` de `Publish()` signifie donc *« j'ai réussi à lancer la diffusion des handlers »*, pas *« tous les handlers ont traité l'événement avec succès »*.

### 6.9 Le BanChecker

L'EventBus transporte l'événement, mais il faut un composant qui **réagisse** au bannissement.

```text
EventBus
   │
   │ TenantEvent{State: Banned}
   ▼
BanChecker
   │
   ▼
mettre à jour son état local
```

```text
BanChecker
    │
    └── banned
         ├── tenant-A
         ├── tenant-C
         └── tenant-F
```

### 6.10 Pourquoi le BanChecker existe en plus du Store

Le `Store` reste la source de vérité. Le `BanChecker` répond à une question beaucoup plus spécialisée : *« ce tenant est-il actuellement banni ? »*, avec une exigence de vitesse extrême.

```text
Requête
   ↓
IsBanned(tenant-A)
   ↓
mémoire RAM (BanChecker)
   ↓
true/false
```

Si `IsBanned()` devait systématiquement appeler la source de vérité, 10 000 requêtes concernant le même tenant produiraient 10 000 accès à la source. Avec `BanChecker`, ce sont 10 000 lectures RAM — la source n'est sollicitée que lorsqu'un changement d'état doit être propagé (**modèle push**, à l'inverse d'un modèle pull) :

```text
Source
   │
   │ "tenant-A est maintenant Banned"
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

`Banned` doit avoir priorité sur le cache normal. Par exemple, si `CachedStore` indique encore `Active` alors que `BanChecker` sait déjà `Banned`, le système doit considérer le tenant comme banni. `BanChecker` devient une sorte de barrière de sécurité placée devant le chemin normal :

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

### 6.12 Résolution de conflit par timestamp — l'ordonnancement causal

Un problème de cohérence plus profond a été identifié : le chargement d'un **snapshot initial** au démarrage d'une instance (nécessaire en environnement multi-instance, pour connaître l'état des bannissements antérieurs à l'abonnement) peut entrer en conflit avec un **événement récent** reçu entre-temps.

**Scénario du problème** : un tenant est débanni (`Active`) juste avant qu'un snapshot périmé (initié avant l'unban, mais dont l'écriture arrive après l'événement) écrase cette information avec `Banned` — la donnée en mémoire redeviendrait alors incorrecte.

**La solution retenue** : chaque entrée du `BanChecker` (pas seulement un booléen `banned`) est associée à un **timestamp de dernière mise à jour**. Une écriture n'est appliquée que si son timestamp est **plus récent** (ou égal) que celui déjà stocké — garantissant qu'une information périmée ne peut jamais écraser une information plus fraîche, quel que soit l'ordre réel d'exécution des goroutines.

**Règle également établie** : `Subscribe()` doit toujours être appelé **avant** le chargement du snapshot initial, jamais l'inverse — sinon un événement publié entre les deux pourrait être manqué (jamais reçu par aucun mécanisme).

### 6.13 Ban() synchrone

Distinction essentielle entre synchrone et asynchrone :

**Synchrone (retenu)**

```text
Ban()
 │
 ├── changement d'état
 │
 ├── publication événement
 │
 └── retour
```

La fonction ne retourne pas tant que les opérations qu'elle garantit n'ont pas été réalisées.

**Asynchrone (rejeté)**

```text
Ban()
 │
 └── lancer goroutine
          │
          └── publier plus tard

Ban() retourne immédiatement
```

Le problème d'une version asynchrone : l'appelant ne saurait jamais si le bannissement a réellement été publié.

### 6.14 Pourquoi Ban() doit être synchrone

```go
err := Ban(ctx, tenantID)
```

`err == nil` doit signifier *« l'opération de bannissement a été effectuée avec succès selon les garanties de cette couche »*, et `err != nil` doit signifier que l'opération n'a pas pu être menée correctement — permettant à l'appelant de réagir immédiatement (par exemple, signaler l'échec).

### 6.15 Le flux de Ban()

```text
Ban(tenant-A)
      │
      ▼
modifier l'état
      │
      ▼
Tenant = Banned
      │
      ▼
Publish(TenantEvent)
      │
      ▼
retourner
```

### 6.16 Le problème de non-atomicité (identifié dès cette étape)

`SetState()` puis `Publish()` ne constitue **pas** une transaction atomique. Deux scénarios problématiques :

```text
SetState() → SUCCESS
Publish()  → FAILURE
```

La source de vérité dit `Banned`, mais l'`EventBus` n'a transmis aucun événement — les autres instances peuvent ne pas savoir immédiatement que le tenant est banni.

```text
Publish()  → SUCCESS
SetState() → FAILURE
```

Encore plus dangereux : les autres instances croiraient le tenant banni, alors que la source de vérité dit toujours `Active` — un **événement mensonger**.

**Décision retenue** : `SetState → Publish` (jamais l'inverse), avec l'idée qu'un mécanisme plus robuste (Outbox) pourra résoudre l'atomicité et la livraison durable plus tard (voir Étape 7, section 10.8 et [limites futures](#16-limites-et-évolutions-futures)).

### 6.17 Pourquoi l'Outbox n'était pas nécessaire à cette étape

> Construire d'abord le contrat et le comportement correct, puis renforcer la fiabilité progressivement.

Les fondations construites ici (`TenantEvent`, `EventBus`, `MemoryEventBus`, `BanChecker`, `Ban()`) ont ensuite permis, à l'Étape 7, d'introduire `RedisEventBus` comme un simple **changement d'adaptateur** — pas une réécriture du cœur métier.

### 6.18 Architecture complète de l'Étape 3

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
Aujourd'hui
              Instance A
                  │
             MemoryEventBus
                  │
                  ▼
             BanChecker A

Demain (avec Redis)
Instance A                         Instance B
    │                                  │
    ▼                                  ▼
Ban()                            BanChecker
    │                                  ▲
    ▼                                  │
RedisEventBus ─────── Redis ───────────┘
```

Le contrat `EventBus` ne change jamais — seule l'implémentation passe de `MemoryEventBus` à `RedisEventBus`.

### 6.20 Résumé de l'Étape 3

| Élément | Responsabilité |
|---|---|
| `TenantEvent` | Représente un changement d'état |
| `EventBus` | Contrat de publication/abonnement |
| `MemoryEventBus` | `EventBus` local en mémoire |
| `Publish()` | Diffuse un événement |
| `Subscribe()` | Enregistre un handler |
| Goroutine par handler | Isolation et non-blocage entre handlers |
| `recover()` | Empêche un panic de tuer le traitement |
| `BanChecker` | Maintient la connaissance immédiate des tenants bannis |
| `Ban()` | Déclenche le changement de bannissement et sa propagation, de façon synchrone |
| Résolution par timestamp | Empêche un snapshot périmé d'écraser un événement plus récent |
| TTL | Toujours acceptable pour `Disabled`, mais insuffisant pour `Banned` |
| Redis | Sera l'implémentation distribuée future (Étape 7) |

> **L'Étape 2 acceptait une propagation retardée via le TTL ; l'Étape 3 introduit un canal d'événements permettant au bannissement de devenir une information active et immédiatement propagée.**

Principe architectural central établi :

- **Store** = « Quelle est la vérité sur le tenant ? »
- **Cache** = « Comment éviter de relire cette vérité trop souvent ? »
- **EventBus** = « Comment annoncer qu'elle vient de changer ? »
- **BanChecker** = « Comment appliquer immédiatement la règle critique du bannissement ? »

---

## 7. Étape 4 — RateLimiter et CacheKeyer

### 7.1 Objectif

Cette étape ajoute deux mécanismes transversaux qui renforcent le toolkit sans le faire dépendre d'une technologie particulière, en conservant le principe posé depuis l'Étape 1 : *le package `tenant` définit les contrats, les sous-packages fournissent les implémentations.*

Deux protections manquaient jusqu'ici :

**Protection n°1 — abus de requêtes.** Un tenant pourrait envoyer un volume disproportionné de requêtes (`tenant-A → 10 000 requêtes/seconde`) et monopoliser les ressources du serveur.

**Protection n°2 — isolation du cache.** Une clé de cache applicative naïve comme `"user:123"` pourrait provoquer une collision entre deux tenants ayant chacun un utilisateur d'identifiant `123` :

```text
tenant-A + user-123
tenant-B + user-123
```

Une clé globale `user:123` créerait une fuite de données entre tenants — exactement le genre de bug catastrophique qu'un système multi-tenant doit empêcher structurellement.

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
       Limite par tenant                 Clé isolée
              │                                │
              └───────────────┬────────────────┘
                              │
                              ▼
                       Application métier
```

`RateLimiter` et `CacheKeyer` sont deux composants indépendants : ni l'un ni l'autre ne doit connaître Redis directement, ce qui permet ensuite d'envisager différentes implémentations (`MemoryRateLimiter`, `RedisRateLimiter` futur ; `DefaultCacheKeyer`).

### 7.3 RateLimiter — responsabilité

Le `RateLimiter` répond à une question très simple : *« ce tenant a-t-il encore le droit d'effectuer cette requête ? »* Il ne décide ni quel tenant est utilisé, ni comment il est résolu ou stocké, ni comment répondre en HTTP — il se concentre uniquement sur la limitation.

### 7.4 Pourquoi le RateLimiter est lié au tenant

Une limite globale naïve pénaliserait les tenants entre eux :

```text
Tenant A ─┐
Tenant B  ├──► même compteur (❌ mauvais)
Tenant C ─┘
```

La bonne approche isole chaque compteur par tenant :

```text
Tenant A → compteur A → 100 req/min
Tenant B → compteur B → 100 req/min
Tenant C → compteur C → 100 req/min
```

La clé logique du rate limiting est donc le `TenantID`.

### 7.5 Fonctionnement — exemple

Avec une limite de 5 requêtes/minute pour `tenant-A` :

```text
Requête 1 → ALLOW
Requête 2 → ALLOW
Requête 3 → ALLOW
Requête 4 → ALLOW
Requête 5 → ALLOW
Requête 6 → DENY
```

`tenant-B`, avec son propre compteur indépendant (`0 / 5` utilisées), reste autorisé.

### 7.6 Implémentation mémoire

```text
MemoryRateLimiter
       │
       ▼
┌───────────────────────────┐
│ map[TenantID]*bucket      │
├───────────────────────────┤
│ tenant-A → compteur       │
│ tenant-B → compteur       │
│ tenant-C → compteur       │
└───────────────────────────┘
```

Comme plusieurs goroutines HTTP peuvent accéder simultanément à cette structure, son état partagé doit être protégé — même principe qu'avec `MemoryStore`.

L'implémentation concrète retenue s'appuie sur une clé (`TenantID`) associée à un limiteur individuel de type *token bucket*, chaque tenant possédant son propre "seau à jetons" (voir [section 14](#14-concurrence-et-thread-safety) pour les détails de concurrence, notamment l'usage de `LoadOrStore` pour garantir qu'un seul limiteur survit par tenant même sous accès concurrent).

**Le principe métier — deux grands modèles conceptuels de rate limiting**, tels que présentés dans la documentation d'introduction :

| Modèle | Principe | Cas d'usage |
|---|---|---|
| **Token Bucket** | Un seau se remplit de jetons à vitesse constante ; chaque requête consomme un jeton ; seau vide → blocage | Idéal pour des rafales (*bursts*) modérées |
| **Leaky Bucket** | Un seau qui fuit à débit constant ; les requêtes arrivent brutalement mais sortent à rythme régulier | Idéal pour lisser les pics de trafic |

### 7.7 Fenêtre temporelle / TTL

Le `RateLimiter` doit également savoir quand une limite se réinitialise. Selon l'algorithme retenu, cela peut être implémenté avec une fenêtre fixe (*fixed window*), une fenêtre glissante (*sliding window*), un *token bucket*, ou un *leaky bucket*. Pour une première implémentation, une stratégie simple et déterministe reste préférable.

### 7.8 Pourquoi le RateLimiter n'est pas immédiatement intégré dans Manager

Le `Manager` reste responsable principalement de `Request → Resolver → TenantID → Store → Tenant`. Le rate limiting est une responsabilité **supplémentaire**, qui pourrait être intégrée au pipeline (avant ou après la résolution complète du tenant), mais il faut éviter de transformer `Manager` en objet connaissant toutes les préoccupations du toolkit — chaque middleware ou composant appelant reste libre de l'invoquer explicitement là où c'est pertinent.

### 7.9 CacheKeyer — responsabilité

Transformer une clé logique d'application en une clé réellement isolée par tenant :

```text
clé logique : "user:123"
        ↓
tenant-A:user:123
```

### 7.10 Contrat du CacheKeyer

```go
type CacheKeyer interface {
    Key(id TenantID, key string) string
}
```

Reçoit un `TenantID` et une clé applicative, retourne une clé isolée :

```text
keyer.Key("tenant-A", "users:123")
        ↓
"tenant-A:users:123"
```

### 7.11 Le CacheKeyer ne stocke rien

Il ne fait ni `Get`, ni `Set`, ni `Delete` — uniquement la construction de clé :

```text
CacheKeyer
    │
    └── construction de clé

Cache
    │
    └── stockage de donnée
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

Le cache peut être une implémentation mémoire, Redis, Memcached ou autre — le `CacheKeyer` reste identique.

### 7.12 Isolation : principe fondamental de cette étape

```text
                    TenantID
                       │
        ┌──────────────┼──────────────┐
        │              │              │
        ▼              ▼              ▼
      Store        CacheKeyer     RateLimiter
        │              │              │
        ▼              ▼              ▼
     Tenant       Cache isolé    Limite isolée
```

> **Toute ressource partagée doit être explicitement dimensionnée par TenantID.**

C'est ce qui empêche qu'un tenant puisse accidentellement consommer le quota d'un autre, lire une donnée mise en cache pour un autre tenant, provoquer une collision de clés, ou contourner l'isolation logique du système.

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
                  état du tenant              Application
                         │
                         ▼
                    Banned ?
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
Implémentation

Puis plus tard :

RateLimiter
     │
     ├── MemoryRateLimiter
     │
     └── RedisRateLimiter (évolution future)
```

### 7.15 Tests à prévoir

**RateLimiter** — vérifier au minimum : la première requête est autorisée ; les requêtes jusqu'à la limite sont autorisées ; une requête dépassant la limite est refusée ; une nouvelle fenêtre redevient autorisée ; un tenant ne bloque jamais un autre tenant ; l'accès concurrent ne produit aucune race condition (`go test -race`).

**CacheKeyer** — vérifier que `tenant-A + users:123` et `tenant-B + users:123` produisent des clés différentes (`tenant-A:users:123 != tenant-B:users:123`) — un test d'isolation fondamental.

### 7.16 Résumé de l'Étape 4

| Composant | Responsabilité | Ne doit pas connaître |
|---|---|---|
| `RateLimiter` | Limiter les requêtes par tenant | HTTP, Redis |
| `MemoryRateLimiter` | Implémentation locale du rate limiting | Logique HTTP |
| `CacheKeyer` | Construire des clés isolées | Stockage du cache |
| `Cache` | Stocker les données | Logique de construction des clés |
| `TenantID` | Identifier le tenant | Infrastructure |
| `Manager` | Résoudre/récupérer le tenant | Détails du cache |
| `BanChecker` | Vérifier le bannissement | Transport HTTP |

> **La règle fondamentale de cette étape : toute ressource partagée doit être explicitement dimensionnée par TenantID.**

---

## 8. Étape 5 — RBAC et Metrics

### 8.1 Objectif

Après l'Étape 4, le toolkit sait identifier le tenant, récupérer son état, gérer le cache, détecter un bannissement, limiter les requêtes et construire des clés de cache isolées. Deux questions restaient ouvertes :

**Question 1 — Autorisation.** *« Ce tenant a-t-il le droit de faire cette opération ? »* — c'est le rôle du **RBAC**.

**Question 2 — Observabilité.** *« Combien de requêtes sont traitées ? Combien sont refusées ? Combien de tenants sont actifs ? Combien de bannissements se produisent ? »* — c'est le rôle des **Metrics**.

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
                    │             Autorisation
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

**Principe.** *Role-Based Access Control.* Au lieu de coder en dur *« Sylvinhio peut effectuer X »*, on définit `Role → Permissions`, puis un tenant possède un ou plusieurs rôles — via le champ `Roles []string` déjà présent dans `Tenant` depuis l'Étape 1.

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

### 8.4 Séparation rôle / permission

Il ne faut surtout pas coder en dur `if tenant.Roles[0] == "admin" { ... }` partout dans l'application. On préfère :

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

L'application demande simplement : *« ce tenant possède-t-il cette permission ? »*, et le composant RBAC gère le reste.

### 8.5 Contrat minimal — `Authorizer` / `Can`

```go
type Authorizer interface {
    Can(t *Tenant, permission string) bool
}
```

Le contrat exprime uniquement : *« est-ce que ce tenant peut effectuer cette action ? »* — sans connaître HTTP, Gin, Echo, Redis, PostgreSQL ni Prometheus.

**Implémentation retenue** — les définitions de rôles/permissions sont organisées **par tenant** (pas une seule table globale de rôles partagée par tous), avec les permissions d'un rôle représentées comme un **set** (`map[string]struct{}`) plutôt qu'une simple liste, pour une vérification en O(1) au lieu d'une recherche linéaire :

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

Cette organisation par tenant est fondamentale : deux tenants peuvent avoir un rôle du **même nom** avec des permissions **complètement différentes** — le rôle `admin` du tenant A n'implique rien sur ce que `admin` signifie pour le tenant B.

### 8.6 Pourquoi Tenant est fourni au RBAC

Le RBAC ne doit pas refaire une requête vers le Store pour connaître les rôles — le `Manager` a déjà récupéré le `*Tenant` complet, y compris `Roles`. Cela évite une seconde récupération inutile.

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

Le toolkit reste agnostique de la façon dont l'application traduit un refus :

```text
RBAC
 │
 └── false
       │
       ├── HTTP → 403 Forbidden
       ├── gRPC → PermissionDenied
       └── CLI → message d'erreur
```

Même principe d'agnosticisme que pour `AdminService` (voir Étape 7).

### 8.8 RBAC et multi-tenancy — deux questions distinctes

- **Tenant** répond à : *« de quel espace isolé provient cette requête ? »*
- **RBAC** répond à : *« que peut faire cet acteur dans cet espace ? »*

```text
Resolver → Tenant A → RBAC → Permission
```

Le RBAC ne remplace jamais le mécanisme de tenant ; il s'y ajoute.

### 8.9 Évolutivité — Role → Permissions → Action

Un mauvais design fige les capacités dans un `if role == "admin"`. Une architecture évolutive relie un rôle à une liste de permissions, qui peut être étendue sans modifier la logique applicative :

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

Un toolkit multi-tenant en production doit permettre de répondre à des questions comme : combien de requêtes sont reçues ? combien sont rejetées ? combien sont bloquées par le rate limiter ? combien de tenants sont bannis ? combien de temps prend la résolution d'un tenant ? combien d'erreurs produit le Store ? C'est le rôle des Metrics, avec Prometheus comme backend d'exposition envisagé.

### 8.11 Pourquoi une abstraction Metrics

Il serait dommageable que `Manager` contienne directement des types `prometheus.CounterVec`/`prometheus.HistogramVec`, faisant dépendre le cœur de `github.com/prometheus/client_golang` — perdant l'agnosticisme.

```text
tenant
 │
 └── Metrics contract
        │
        ├── NoopMetrics / MemoryMetrics (dev)
        │
        └── PrometheusMetrics (production)
```

### 8.12 Contrat Metrics (conceptuel) et implémentation retenue

Le contrat minimal conceptuel envisagé :

```go
type Metrics interface {
    IncRequest()
    IncRBACDenied()
}
```

**L'interface effectivement retenue et implémentée**, plus proche des besoins réels formulés dans le cahier des charges (besoin fonctionnel #5 — latence, RPS, taux d'erreur), expose trois opérations paramétrées par tenant :

```go
type MetricsCollector interface {
    IncRequests(ctx context.Context, tenantID tenant.TenantID)
    ObserveLatency(ctx context.Context, tenantID tenant.TenantID, duration time.Duration)
    IncErrors(ctx context.Context, tenantID tenant.TenantID)
}
```

Une implémentation `MemoryMetrics` maintient, **par tenant**, des compteurs `requests`, `errors`, `latencySum` et `latencyCount` (permettant de calculer une moyenne de latence), avec deux niveaux de concurrence combinés (voir [section 14](#14-concurrence-et-thread-safety)) : `sync.Map` pour la collection dynamique de tenants, et `atomic.Int64` pour chaque compteur individuel.

### 8.13 Types de métriques (modèle Prometheus)

**Counter** — une valeur qui augmente uniquement (`tenant_requests_total`). Utilisée pour compter des requêtes, erreurs, refus RBAC, refus RateLimiter, bannissements.

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

L'objectif n'est pas de créer des centaines de métriques, mais de privilégier peu de métriques réellement utiles.

### 8.15 Précaution : la cardinalité des labels Prometheus

Point particulièrement important dans un système multi-tenant : chaque combinaison de labels crée une série temporelle Prometheus distincte. Utiliser `tenant_id` directement comme label pour une plateforme avec des dizaines de milliers de tenants peut créer une explosion de cardinalité.

```text
Mauvaise idée
tenant_requests_total{tenant_id="..."} pour chaque tenant sans réflexion

Préférable
tenant_requests_total{status="success", source="api"}
tenant_rbac_denied_total{permission="users.read"}
```

> **Règle retenue : ne jamais utiliser une donnée utilisateur à forte cardinalité comme label Prometheus sans justification — particulièrement vrai pour `TenantID`.**

### 8.16 Séparation RBAC / Metrics

Il ne faut jamais faire `RBAC → Prometheus` directement. Le RBAC effectue son travail (`Can(...)`), puis une couche supérieure enregistre le résultat dans les métriques :

```text
             ┌──────────────┐
             │     RBAC     │
             └──────┬───────┘
                    │
                 résultat
                    │
                    ▼
             ┌──────────────┐
             │    Metrics   │
             └──────┬───────┘
                    │
                    ▼
                Prometheus
```

Sinon le RBAC deviendrait dépendant de Prometheus, brisant l'agnosticisme.

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
    └── prometheus/ (adaptateur envisagé)
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

**RBAC** — tester `admin + users.read → ALLOW`, `admin + users.delete → ALLOW`, `viewer + users.read → ALLOW`, `viewer + users.delete → DENY`, un tenant sans rôle → `DENY`, plusieurs rôles → comportement correct, et surtout vérifier qu'un tenant ne récupère jamais les permissions d'un autre (tenant inconnu, rôle inconnu).

**Metrics** — vérifier qu'une requête incrémente le bon compteur, qu'un refus RBAC incrémente le compteur dédié, qu'un refus RateLimiter incrémente le sien. Pour Prometheus, vérifier également que les métriques produites sont correctement exposées dans le format attendu.

### 8.20 Ce que cette étape apporte au toolkit

```text
Avant                          Après l'Étape 5
Toolkit                        Toolkit
   │                              │
   ├── Identification             ├── Identification (Resolver)
   ├── Stockage                   ├── Isolation (Store, CacheKeyer, tenantctx)
   ├── Cache                      ├── Sécurité (BanChecker, RateLimiter, RBAC)
   ├── Bannissement               └── Observabilité (Metrics → Prometheus)
   └── Rate limiting
```

### 8.21 Principes architecturaux retenus

1. **RBAC ne connaît pas HTTP** — RBAC produit une décision d'autorisation ; HTTP la traduit en 403.
2. **Metrics ne connaît pas le métier** — Metrics mesure ; Prometheus collecte.
3. **Le cœur ne dépend pas de Prometheus** — `tenant → Metrics contract → Prometheus adapter`.
4. **Le tenant reste la frontière d'isolation** — `TenantID → Store / RateLimiter / Cache / RBAC`.
5. **Les interfaces restent minimales** — chaque composant expose uniquement ce dont son consommateur a besoin.

### 8.22 Résumé de l'Étape 5

| Composant | Responsabilité |
|---|---|
| `RBAC` | Vérifier les permissions d'un tenant |
| `Role` | Regrouper des permissions |
| `Permission` | Représenter une capacité métier |
| `Authorizer` / `Can` | Contrat d'autorisation |
| `Metrics` / `MetricsCollector` | Contrat d'observabilité |
| `MemoryMetrics` / `PrometheusMetrics` | Implémentations du contrat |
| `Counter` | Compter les événements |
| `Histogram` | Mesurer les durées/distributions |
| `Gauge` | Mesurer une valeur variable |

> **RBAC décide « qui peut faire quoi », tandis que Metrics permet de savoir « ce qui se passe réellement dans le système ».**

Progression cohérente des cinq premières étapes : identification → données → sécurité → protection des ressources → autorisation + observabilité.

---

## 9. Étape 6 — Framework adapters

### 9.1 Le problème à résoudre

Le cœur `tenant-core` contient une logique indépendante du framework :

```text
Requête HTTP
     │
     ▼
Identifier le tenant
     │
     ▼
Récupérer le tenant
     │
     ▼
Mettre le tenant dans le contexte
     │
     ▼
Handler de l'application
```

Mais chaque framework Go construit ses middlewares différemment :

```text
net/http    func(next http.Handler) http.Handler
Gin         func(c *gin.Context)
Echo        func(next echo.HandlerFunc) echo.HandlerFunc
Chi         func(next http.Handler) http.Handler
```

L'objectif : ne surtout pas réécrire la logique multi-tenant quatre fois. D'où les **Framework Adapters**.

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
                    │ "Quel tenant ?"  │
                    └────────┬─────────┘
                             │
                             ▼
                    ┌──────────────────┐
                    │      Store       │
                    │ "Quel est son    │
                    │      état ?"     │
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
                     Handler métier
```

> **Les frameworks sont à l'extérieur du cœur du toolkit. Le cœur ne connaît ni Gin, ni Echo, ni Chi.**

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

Le `Manager` ne sait absolument pas si la requête vient de Gin, Echo, Chi ou `net/http` — et c'est **volontaire**.

**Note de conception importante** : `Manager.Resolve()` ne construit **pas** de `context.Context` lui-même. Faire dépendre `tenant.go` (package racine) de `tenantctx` créerait une dépendance circulaire (`tenant → tenantctx → tenant`, puisque `tenantctx` dépend déjà de `tenant` pour le type `*Tenant`). C'est donc la responsabilité de chaque adaptateur de framework de combiner `Manager.Resolve()` et `tenantctx.WithTenant()`.

**Fail-fast à la construction** : `tenant.New(options...)` panique si `Resolver` ou `Store` ne sont pas fournis après application des options — une dépendance obligatoire manquante est une erreur de configuration du programme, détectée immédiatement, pas une erreur de traitement de requête gérée via `error`.

### 9.4 Le rôle de tenantctx

Une fois que `Manager` fournit `*tenant.Tenant`, il faut transmettre cette information aux handlers via le `context.Context` standard :

```go
ctx := tenantctx.WithTenant(r.Context(), t)
```

Puis remplacer la requête avec ce nouveau contexte. Le handler peut ensuite faire :

```go
t := tenantctx.FromContext(r.Context())
```

```go
func GetUsers(w http.ResponseWriter, r *http.Request) {
    t := tenantctx.FromContext(r.Context())
    // utiliser t...
}
```

Cette logique métier fonctionne identiquement derrière les quatre adaptateurs.

### 9.5 Adaptateur net/http

**Fichier** : `middleware/nethttp.go`

**Signature** : `func Wrap(m *tenant.Manager, next http.Handler) http.Handler`

```text
Request
   │
   ▼
m.Resolve(r)
   │
   ├── erreur → HTTP 404
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

C'est l'adaptateur de référence, utilisant directement les primitives HTTP standard de Go. Si `Manager.Resolve()` échoue, la requête est rejetée avec un statut `404` **avant** d'atteindre `next` — `next.ServeHTTP` n'est **jamais** appelé dans ce cas (comportement vérifié explicitement par test).

**Détail important : pourquoi `r.WithContext(ctx)`, pas `r` directement ?** `context.Context` est immuable en Go — `WithTenant()` crée un nouveau contexte, il ne modifie jamais l'ancien. De la même façon, `r.WithContext()` ne modifie pas `r` en place : elle retourne une **copie** de la requête portant le nouveau contexte. Sans cet appel, le handler suivant recevrait toujours l'ancien contexte (sans le tenant), et `tenantctx.FromContext` ne trouverait jamais rien.

### 9.6 Adaptateur Gin

**Fichier** : `middleware/gin/gin.go` — sous-module Go séparé (`go.mod` propre, dépendance `github.com/gin-gonic/gin`).

Gin possède son propre `*gin.Context`, mais il contient toujours une requête HTTP standard accessible via `c.Request`.

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
c.Request = nouvelle Request
     │
     ▼
c.Next()
```

En cas d'échec de résolution, `c.AbortWithStatus(http.StatusNotFound)` est utilisé — l'équivalent Gin de "ne jamais appeler le handler suivant".

**Pourquoi ne pas utiliser `c.Set("tenant", t)` ?** Cela créerait un mécanisme de propagation spécifique à Gin. Le choix retenu (`tenantctx.WithTenant`) garantit que le tenant reste accessible avec la **même API partout**, quel que soit le framework — cohérence transversale essentielle pour un toolkit destiné à des milliers de développeurs sur des stacks différentes.

**Note de setup** : le sous-module `middleware/gin` utilise une directive `replace github.com/sylvinhio676-ux/tenant-core => ../..` dans son `go.mod`, pour pointer vers le code local pendant le développement (avant que le module racine n'ait de version taguée publiée). Cette directive devra être retirée au moment de la publication d'une version stable, pour que les utilisateurs récupèrent la vraie dépendance depuis le dépôt public.

### 9.7 Adaptateur Echo

**Fichier** : `middleware/echo/echo.go` — sous-module Go séparé (dépendance `github.com/labstack/echo/v4`).

Echo possède `echo.Context`, mais la requête HTTP est obtenue via une **méthode**, `c.Request()`, pas un champ direct.

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

**Gestion des erreurs** : Echo propage les erreurs par le retour `error` de chaque handler, pas en écrivant directement sur le `ResponseWriter` :

```go
return echo.NewHTTPError(http.StatusNotFound, "tenant not found")
```

Pour arrêter la chaîne de middlewares en cas de rejet, `c.Next()` n'est simplement jamais atteint — la fonction retourne l'erreur avant.

### 9.8 Adaptateur Chi

**Fichier** : `middleware/chi/chi.go` — sous-module Go séparé (dépendance `github.com/go-chi/chi/v5`).

Chi est le plus proche de `net/http` : il consomme directement `http.Handler`, sans type de contexte propre.

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

C'est parce que Chi repose directement sur `http.Handler` qu'il n'a besoin d'aucun système de contexte supplémentaire — le code est quasiment identique à l'adaptateur `net/http`.

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
                     nouveau Context
                             │
                             ▼
                      Handler suivant
```

### 9.10 Pourquoi quatre adaptateurs

```go
// Gin
router.Use(gin.Middleware(manager))

// Echo
e.Use(echo.Middleware(manager))

// Chi
r.Use(chi.Middleware(manager))

// net/http seul
handler := nethttp.Wrap(manager, myHandler)
```

La logique métier du toolkit ne change jamais. C'est exactement le rôle d'un *adapter* : traduire l'interface spécifique d'un framework vers l'interface générique du cœur.

### 9.11 Ce que les adapters ne font PAS

C'est une limite volontairement stricte, documentée pour éviter toute dérive future :

- ❌ déterminer comment fonctionne un tenant
- ❌ interroger directement la base de données
- ❌ vérifier les rôles RBAC
- ❌ appliquer le RateLimiter
- ❌ gérer les métriques
- ❌ gérer les bannissements directement
- ❌ connaître la logique métier

Un adaptateur fait exclusivement :

```text
Framework
    ↓
extraire *http.Request
    ↓
Manager.Resolve()
    ↓
injecter Tenant dans Context
    ↓
Framework
```

Rien de plus.

### 9.12 Architecture complète du toolkit après l'Étape 6

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
                            Handler métier
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

> **Le cœur de notre toolkit parle en abstractions Go (Manager, Resolver, Store, context.Context). Les framework adapters traduisent simplement les conventions de chaque framework vers ces abstractions. C'est ce qui permet à notre code multi-tenant d'être indépendant du framework tout en restant très facile à intégrer.**

---

## 10. Étape 7 — Admin API et EventBus Redis

L'objectif de cette étape était double : **l'administration des tenants** via une API HTTP, et **la propagation inter-instance**, pour qu'un changement de tenant (notamment un bannissement) soit immédiatement connu par toutes les instances du serveur grâce à Redis Pub/Sub.

> **Le cœur métier ne connaît ni HTTP, ni Redis, ni framework particulier. Les adaptateurs dépendent du cœur, jamais l'inverse.**

### 10.1 Architecture globale de l'étape

```text
                         APPLICATION
                              │
                    ┌─────────┴─────────┐
                    │                   │
              Admin API             Requête normale
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

`admin.Service` ne sait pas qu'il utilise Redis — il connaît seulement `eventbus.EventBus`. De même, il ne connaît pas `MemoryStore` ni une base SQL particulière — il connaît `tenant.AdminStore`.

### 10.2 Extension de tenant.Store — pourquoi une interface séparée

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

**Pourquoi ?** Parce que le principe des interfaces minimales devait être respecté : `Manager` n'a absolument pas besoin de pouvoir bannir un tenant, il ne doit donc pas dépendre d'une interface contenant `Ban()`/`Disable()`/`Activate()`. Cela évite de transformer progressivement `Store` en une énorme interface CRUD.

### 10.3 AdminStore : pourquoi pas `Ban()` / `Disable()` / `Activate()` directement

`AdminStore` expose volontairement seulement `Create`, `Update`, `SetState` — jamais `Ban()`, `Disable()`, `Activate()` directement, parce que ces opérations ne sont pas de simples modifications locales : un bannissement doit **aussi** produire un événement.

```text
Tenant A
   │
   ├── état local → Banned
   │
   └── événement → TenantEvent
```

Si `AdminStore` possédait `Ban()`, un développeur pourrait appeler `store.Ban(ctx, id)` et **oublier** de publier l'événement, créant une incohérence :

```text
Instance A: Tenant = BANNED     ❌ événement non publié
Instance B: Tenant = ACTIVE
```

C'est précisément le problème de cohérence que l'architecture voulait éviter — la publication de l'événement ne doit jamais être une étape optionnelle laissée à la discrétion de l'appelant.

### 10.4 MemoryStore et le problème des pointeurs (rappel détaillé)

Ce problème a été identifié précisément lors de l'ajout de `SetState`, et déjà documenté à l'Étape 2 (section 5.6) — reformulé ici avec le cas d'usage spécifique de `SetState` :

```text
map[TenantID]*Tenant
```

Lorsque `t, _ := store.Get(...)` retourne `*Tenant`, ce pointeur correspond au **même objet** que celui présent dans la map — ce n'est pas une copie. Faire `t.State = tenant.Banned` hors verrou peut provoquer :

```text
Goroutine A                 Goroutine B

t.State = Banned
       │
       │                 Get()
       │                   │
       ▼                   ▼
   écriture              lecture
```

... une data race authentique.

> **Protéger uniquement la map n'est pas suffisant lorsque les valeurs de la map sont des pointeurs mutables.**

Les opérations de modification restent donc correctement protégées par le mécanisme de synchronisation du store : `Get()` retourne une copie, `SetState`/`Create`/`Update` opèrent directement sur l'objet interne sous `Lock()` exclusif.

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

Seulement deux dépendances obligatoires. Constructeur simple, sans options fonctionnelles :

```go
func NewAdminService(store tenant.AdminStore, bus eventbus.EventBus) *Service
```

**Pourquoi pas d'options fonctionnelles ici, contrairement à `Manager` ?** `Service` possède seulement deux dépendances obligatoires et aucune configuration optionnelle prévue. `NewAdminService(store, bus)` est plus simple et plus lisible que `NewAdminService(WithStore(...), WithEventBus(...))` pour un si petit nombre de paramètres fixes — le pattern d'options fonctionnelles n'est utile que lorsqu'il apporte une réelle valeur d'extensibilité, pas par réflexe systématique.

### 10.6 La méthode `transition()`

`Ban()`, `Disable()`, `Activate()` partagent exactement le même mécanisme :

1. modifier l'état ;
2. construire `TenantEvent` ;
3. publier l'événement ;
4. logger si la publication échoue.

Plutôt que dupliquer cette logique trois fois, une méthode privée commune la factorise :

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

> **Une seule implémentation de la logique commune, plusieurs opérations métier explicites.** Cela garantit aussi que le comportement (y compris le logging en cas d'échec) reste identique pour les trois transitions, sans risque de divergence accidentelle.

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
              création TenantEvent
                         │
                         ▼
                EventBus.Publish()
                         │
                         ▼
                propagation événement
```

```go
eventbus.TenantEvent{
    TenantID:  id,
    State:     tenant.Banned,
    Timestamp: time.Now(),
}
```

### 10.8 Pourquoi SetState → Publish (et pas l'inverse)

**Décision d'architecture, non-atomicité assumée.**

```text
SetState() → succès
Publish()  → échec
```

L'état local devient `BANNED`, mais les autres instances ne reçoivent pas l'événement — une incohérence, mais **acceptée** pour cette version.

**Pourquoi pas `Publish → SetState` ?** Parce qu'on pourrait alors publier `Tenant A → BANNED` alors que `SetState()` échoue ensuite — l'événement annoncerait un état qui n'existe finalement jamais dans le Store. C'est strictement pire : un **événement mensonger**.

**Décision retenue** : `SetState → Publish`, avec une limite explicitement documentée dans le code lui-même :

```text
// Limite connue : SetState et Publish ne sont pas atomiques entre eux (ce
// sont deux systèmes distincts). L'ordre SetState → Publish garantit qu'on
// ne publie jamais un événement pour un état qui n'a pas réellement été
// appliqué au Store — mais si Publish échoue après un SetState réussi,
// l'événement peut être perdu jusqu'à resynchronisation manuelle ou via un
// futur mécanisme de livraison durable (pattern Outbox).
```

### 10.9 Logging de l'incohérence

Lorsque `SetState()` réussit mais que `Publish()` échoue, le service **loggue explicitement** l'anomalie, avec le contexte complet (tenant concerné, état visé, erreur rencontrée) :

```text
ERROR
tenant state changed but event publication failed
tenant_id=tenant-A state=banned error=redis connection refused
```

Cela permet à un opérateur de savoir : ⚠ état local modifié, ⚠ événement non propagé, ⚠ resynchronisation potentiellement nécessaire.

**Nuance importante retenue** : le log ne remplace jamais l'erreur retournée à l'appelant — les deux sont faits, parce que l'appelant seul (recevant juste une erreur Redis générique) ne saurait pas forcément qu'une opération métier a *partiellement* réussi (le Store a bien été modifié), une information que seul le `Service` connaît.

**Évolution future identifiée** : un pattern **Outbox** (changement d'état et événement à publier écrits dans la même transaction de stockage, avec un *worker* asynchrone chargé de la publication effective et retentant en cas d'échec) rendrait la publication durable. Ce mécanisme n'a volontairement **pas** été construit à cette étape.

### 10.10 Admin API — couche HTTP

```go
type HTTPHandler struct {
    mux     *http.ServeMux
    service *Service
}
```

**Choix architectural important : `net/http` pur, ni Gin, ni Echo, ni Chi.**

**Pourquoi ?** Parce que l'Admin API est une **API de commande** du toolkit, pas un middleware destiné à être branché dans différents frameworks applicatifs. Elle reste donc indépendante du framework utilisé par l'application qui consomme le toolkit — n'importe quel serveur Go capable de monter un `http.Handler` peut l'intégrer, quel que soit son propre choix de framework pour le reste de l'application.

### 10.11 Routage moderne avec `http.ServeMux` (Go 1.22+)

```go
h.mux.HandleFunc("PATCH /tenants/{id}/ban", h.handleBan)
h.mux.HandleFunc("PATCH /tenants/{id}/disable", h.handleDisable)
h.mux.HandleFunc("PATCH /tenants/{id}/activate", h.handleActivate)
```

Grâce au support des patterns modernes de `ServeMux` (méthodes HTTP + wildcards), le handler récupère directement :

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

Cela évite un parsing manuel (`strings.Split`, `strings.TrimPrefix`, `switch`), réduisant le risque de reconstruire progressivement un mini-router maison.

### 10.12 Pourquoi seulement trois endpoints

Volontairement, **pas** de `POST /tenants` ni `GET /tenants/{id}`, même si `AdminStore` possède `Create()` et `Store` possède `Get()`.

> **L'API HTTP doit suivre le contrat métier du Service, pas exposer automatiquement toutes les méthodes du Store.**

Actuellement, `Service` n'expose que `Ban()`, `Disable()`, `Activate()` — donc l'API expose exactement `PATCH /tenants/{id}/ban`, `/disable`, `/activate`, pas un CRUD générique. Cela protège l'architecture contre une dérive du type *« Store → toutes les méthodes → endpoints HTTP »*.

Si la création ou la lecture doivent un jour faire partie de l'Admin API, la démarche à suivre est d'abord d'enrichir le contrat métier (`Service.Create(...)`, `Service.Get(...)`), **puis seulement** d'exposer les endpoints correspondants — jamais l'inverse.

### 10.13 Architecture de l'Admin API — flux complet

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

Le `HTTPHandler` ne connaît ni Redis, ni `MemoryStore`, ni la façon dont l'état est stocké, ni la façon dont les événements sont transportés.

### 10.14 Limites actuelles de l'Admin API (documentées honnêtement)

**Authentification** — l'API n'a actuellement **aucune** authentification ni autorisation. Elle ne doit donc pas être exposée directement à Internet en production. Un point critique à traiter ultérieurement.

**Gestion des erreurs** — `writeError(...)` renvoie systématiquement `500 Internal Server Error`, peu importe la vraie cause (tenant introuvable, store indisponible, etc.). Une évolution future devra distinguer `404` (tenant absent), `500` (erreur interne), `503` (dépendance indisponible). Cette limite vient notamment du fait qu'il n'existe pas encore d'erreur sentinelle exportée pour "tenant introuvable" au niveau de l'interface générique `AdminStore` — contrairement à `store.ErrTenantNotFound`, qui est spécifique à `MemoryStore`.

### 10.15 Pourquoi Redis

Jusqu'ici, `MemoryEventBus` fonctionne très bien en mono-instance :

```text
Instance A
   │
MemoryEventBus
   │
handlers locaux
```

Mais avec plusieurs instances, chacune possède sa propre mémoire :

```text
Instance A
   │
Ban tenant-A
   │
MemoryEventBus
   │
   └── uniquement A
```

B et C ne voient rien.

### 10.16 Redis Pub/Sub — RedisEventBus

```text
eventbus/redis/
└── redis.go
```

Utilise `github.com/redis/go-redis/v9`. Le package `eventbus` lui-même ne connaît **jamais** Redis — règle architecturale essentielle :

```text
eventbus
   │
   │ définit
   ▼
EventBus interface
   ▲
   │ implémente
   │
eventbus/redis
```

Grâce au typage structurel de Go, `RedisEventBus` satisfait automatiquement `eventbus.EventBus`.

```go
type RedisEventBus struct {
    client  *goredis.Client
    channel string
}
```

```text
RedisEventBus
├── Redis Client
└── Redis Channel
```

**Note de setup** : sous-module Go séparé (`eventbus/redis/go.mod`), avec la même directive `replace` locale que les adaptateurs de framework, pour les mêmes raisons.

### 10.17 Transformation TenantEvent ↔ JSON

Redis ne connaît pas `eventbus.TenantEvent` — il transporte des bytes/messages bruts. JSON a été retenu.

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
 │ message JSON
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

**Pourquoi JSON** : standard, lisible, simple, indépendant du langage, directement supporté par la stdlib Go.

### 10.18 Subscribe() et la goroutine dédiée

Le Pub/Sub Redis fonctionne avec un abonnement **continu** :

```go
for msg := range pubsub.Channel() {
    // ...
}
```

Cette boucle peut vivre pendant toute la durée du serveur. Si elle était exécutée directement dans `Subscribe()`, la fonction ne retournerait jamais, bloquant tout le code appelant :

```text
Subscribe()
   │
   ▼
boucle infinie
   │
   ├── ne retourne jamais
   │
   └── code appelant bloqué
```

**Solution retenue** :

```text
Subscribe()
   │
   ├── configuration
   │
   ├── confirmation
   │
   └── lancement goroutine
             │
             ▼
       boucle de lecture
```

La boucle de réception vit dans une goroutine dédiée, unique et permanente — distincte des goroutines lancées ensuite pour chaque handler individuel (voir 10.19).

### 10.19 Confirmation synchrone avec `pubsub.Receive()` — fail-fast

Simplement faire `pubsub := client.Subscribe(...)` ne garantit **pas** immédiatement que Redis a confirmé l'abonnement (opération asynchrone côté connexion). `pubsub.Receive(ctx)` est utilisé **avant** de lancer la goroutine de traitement, pour bloquer jusqu'à confirmation, ou remonter une erreur concrète si Redis est injoignable :

```text
Subscribe()
    │
    ▼
Redis SUBSCRIBE
    │
    ▼
Receive()
    │
    ├── erreur → Subscribe retourne error
    │
    └── confirmation
           │
           ▼
       goroutine
           │
           ▼
       messages
```

Cela respecte le principe de **fail-fast** sur les erreurs de configuration : un développeur qui configure mal Redis (mauvaise adresse, credentials invalides) le découvre immédiatement au démarrage de son serveur, plutôt que silencieusement en production, des heures plus tard.

### 10.20 Protection contre les handlers qui paniquent (rappel + application à Redis)

Même comportement que `MemoryEventBus` (Étape 3) : chaque événement reçu est traité dans sa propre goroutine, protégée par `recover()` :

```text
message Redis
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
Handler B → continue normalement
Handler C → continue normalement
```

Un handler défaillant ne doit jamais faire tomber le processus.

### 10.21 Message Redis malformé

Si `json.Unmarshal(...)` échoue, le message invalide est loggé puis **ignoré**, sans `panic(...)` ni `return` qui arrêterait toute la consommation :

```text
message invalide
      │
      ▼
log erreur
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

Pour tester `RedisEventBus` sans exiger un serveur Redis réel pendant `go test` (ni de la part du développeur local, ni en CI), la bibliothèque **`miniredis`** (implémentation Redis en mémoire pure, en Go) a été retenue plutôt que d'installer un vrai Redis dans le workflow CI.

| Critère | miniredis | Redis en CI |
|---|---|---|
| Redis installé localement | ❌ non requis | ❌ non requis |
| Processus externe | ❌ non | ✅ oui |
| Rapidité | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ |
| `go test` immédiat | ✅ | ❌ nécessite config CI |
| Reproductibilité | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| Test du vrai Redis | ⚠️ simulation | ✅ oui |

**Décision** : `miniredis` maintenant, garantissant que `go test ./...` fonctionne partout sans dépendance externe — cohérent avec le principe de testabilité appliqué depuis le début. Un test d'intégration avec un vrai Redis reste une évolution complémentaire envisageable, pas un remplacement.

Les tests couvrent : le chemin nominal (publier un événement, le recevoir, vérifier le round-trip JSON avec une tolérance sur le timestamp via `assert.WithinDuration`), et le cas fail-fast (`Subscribe()` doit échouer immédiatement si Redis est injoignable, pas silencieusement).

### 10.24 Architecture complète de l'Étape 7

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
│   └── redis/            (sous-module Go séparé)
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
                    MÉTIER
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

> **Le cœur définit les contrats. Les adaptateurs implémentent ces contrats.** Exactement le même principe que celui appliqué avec les adaptateurs de middleware (Étape 6).

### 10.26 Ce qui reste volontairement pour plus tard

| Sujet | État actuel | Évolution |
|---|---|---|
| Admin API | Fonctionnelle pour les transitions | Authentification/RBAC |
| Erreurs HTTP | Principalement `500` | Mapping `404`/`409`/`500`/`503` |
| SetState → Publish | Non atomique | Pattern Outbox |
| EventBus | Redis Pub/Sub | Gestion avancée reconnexion/lifecycle |
| Redis | Propagation temps réel | Résilience/observabilité |
| Création tenant | `AdminStore.Create` existe | Ajouter la capacité métier `Service.Create` si nécessaire |
| Lecture admin | `Store.Get` existe | Ajouter `Service.Get` si le besoin métier apparaît |
| Tests Redis | Couverts via `miniredis` | Tests d'intégration avec un vrai serveur Redis, en complément |

**En une phrase** : l'Étape 7 transforme le toolkit d'un système capable de résoudre un tenant en un système capable de gérer son cycle de vie et de propager ses changements d'état à travers plusieurs instances, tout en conservant un cœur métier indépendant de HTTP et de Redis.

---

## 11. Étape 8 — Helpers de test (tenanttest)

### 11.1 Le problème résolu

Avant cette étape, pour tester du code applicatif dépendant du tenant courant, il fallait écrire manuellement :

```go
t := &tenant.Tenant{
    ID:    "tenant-abc",
    State: tenant.Active,
}

ctx := tenantctx.WithTenant(context.Background(), t)
```

Cette logique se répétait dans plusieurs tests internes au toolkit (`fakeResolver`, `fakeStore`, `fakeAdminStore` — utiles pour tester les composants internes du toolkit lui-même, mais pas destinés à être exposés). Un **utilisateur externe** qui veut simplement tester son application ne devrait pas avoir à connaître toute cette mécanique interne. C'est précisément le rôle de `tenanttest`.

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

Un développeur qui importe `tenantctx` dans son application de production ne récupère donc jamais, dans le même import, des fonctionnalités destinées exclusivement au test. Les packages racontent clairement leur rôle : `tenantctx` = mécanisme de production ; `tenanttest` = ergonomie de test.

### 11.3 Architecture du package

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

Aucune logique métier supplémentaire — uniquement de l'ergonomie de test.

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

### 11.5 Pourquoi conserver une API simple

Le choix a été de **volontairement** garder `WithFakeTenant(ctx, id, state)` plutôt que d'y ajouter progressivement des paramètres (`roles`, `permissions`, ...), ce qui rendrait la fonction difficile à utiliser pour le cas le plus fréquent : *« j'ai simplement besoin d'un tenant dans mon contexte. »*

### 11.6 `WithFakeTenantFull`

Pour les tests nécessitant davantage de contrôle (notamment RBAC) :

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
        Roles: []string{"admin", "manager"},
    },
)
```

Particulièrement utile pour tester le RBAC, des rôles précis, des états particuliers, des scénarios métier complexes, ou de futurs champs de `Tenant`.

### 11.7 Factorisation entre les deux helpers

`WithFakeTenant` délègue à `WithFakeTenantFull`, pour que la logique de création/injection du contexte n'existe qu'à un seul endroit :

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

### 11.8 Pourquoi ne pas créer un faux Resolver dès cette étape

Le besoin actuel de `tenanttest` est *injecter directement un tenant*, pas *simuler tout le pipeline HTTP*. Des helpers comme `NewFakeResolver(...)`, `NewFakeStore(...)`, `NewFakeManager(...)` n'ont volontairement **pas** été créés à cette étape.

> **Ne pas abstraire prématurément ; commencer avec le plus petit contrat qui résout réellement le problème.**

### 11.9 Le contrat de tenanttest

```text
Entrée
  │
  ▼
tenanttest.WithFakeTenant(...)
  │
  ▼
context.Context contenant le tenant
  │
  ▼
tenantctx.FromContext(ctx)
  │
  ▼
Tenant
```

> **Tout tenant injecté par `tenanttest` doit pouvoir être récupéré par le mécanisme officiel `tenantctx.FromContext`.**

### 11.10 et 11.11 Tests du package

**`TestWithFakeTenant`** vérifie le helper minimal : `ID`, `State`, `Roles` vide.

**`TestWithFakeTenantFull`** vérifie qu'un tenant complet (y compris `Roles`) est correctement conservé — garantissant que les informations RBAC ne sont pas perdues.

Ces deux tests restent volontairement courts : `tenantctx.WithTenant()`/`FromContext()` ont déjà été testés en profondeur à l'Étape 1 ; ici, seul le **contrat d'intégration** du helper est vérifié.

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

Le développeur n'a besoin ni de démarrer Redis, ni de créer un `MemoryStore`, ni un `Resolver`, ni de construire une requête HTTP, ni de démarrer Gin/Echo/Chi, ni d'utiliser `Manager`. C'est exactement le gain recherché.

### 11.13 Exemple pour le RBAC

```go
ctx := tenanttest.WithFakeTenantFull(
    context.Background(),
    &tenant.Tenant{
        ID:    "tenant-abc",
        State: tenant.Active,
        Roles: []string{"admin"},
    },
)
```

Permet de tester `tenant → RBAC → permission autorisée/refusée` sans aucune infrastructure externe.

### 11.14 Architecture globale après l'Étape 8

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

### 11.15 Principe architectural retenu

> **Les outils de test doivent simplifier l'utilisation du cœur sans polluer le cœur avec une logique spécifique aux tests.**

Ainsi : `tenantctx` = mécanisme de production ; `tenanttest` = ergonomie de test — et non `tenantctx` = production + mocks + helpers + fake stores + ....

### 11.16 Évolutions possibles (non implémentées)

```text
tenanttest/
│
├── tenanttest.go
├── resolver.go   (évolution future)
├── store.go      (évolution future)
├── manager.go    (évolution future)
└── ...
```

Avec potentiellement `tenanttest.NewFakeResolver(...)`, `tenanttest.NewFakeStore(...)`, `tenanttest.NewManager(...)` — uniquement lorsque des besoins réels apparaîtront, en accord avec la règle générale de ne pas abstraire prématurément.

### 11.17 Résumé de l'Étape 8

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

**But final** : permettre à un développeur de tester facilement du code multi-tenant avec un tenant fictif, sans infrastructure, sans HTTP, sans Resolver, sans Store et sans framework, tout en utilisant exactement le même mécanisme `tenantctx` que le code de production.

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

**Vision synthétique de la composition (`tenant.New()`)** :

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

**Pourquoi les options fonctionnelles** — plutôt qu'un constructeur énorme (`New(resolver, store, eventBus, banChecker, rateLimiter, rbac, metrics, cacheKey, ...)`), difficile à lire et à maintenir, l'API adoptée est :

```go
tenant.New(
    tenant.WithResolver(resolver),
    tenant.WithStore(store),
)
```

**Important** : le `Manager` effectivement implémenté (Étapes 6-7) reste volontairement minimal — il n'assemble que `Resolver` et `Store`, panique si l'un des deux manque, et son unique méthode `Resolve(r *http.Request) (*Tenant, error)` s'arrête à la production d'un `*Tenant`, sans construire de `context.Context` (pour éviter un cycle d'import avec `tenantctx`). Les autres composants (`BanChecker`, `RateLimiter`, `RBAC`, `Metrics`, `CacheKeyer`, `EventBus`) restent des **briques indépendantes**, que l'application invoque explicitement là où c'est pertinent — le diagramme ci-dessus représente l'écosystème des composants disponibles, **pas** un pipeline unique imposé automatiquement par `Manager` lui-même. Voir la section [Décisions / points à clarifier](#18-décisions--points-à-clarifier) pour le détail de cette nuance entre la vision d'ensemble et l'implémentation effective de `tenant.New()`.

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

Détaillé :

```text
Étape 1 — Resolver
Request → SubdomainResolver → TenantID("tenant-a")

Étape 2 — Store
TenantID("tenant-a") → Store → *Tenant{ID: tenant-a, State: active, Roles: [admin]}

Étape 3 — Context
*Tenant → tenantctx.WithTenant(...) → context.Context

Les composants qui ont besoin du tenant font ensuite tenantctx.FromContext(ctx).
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
Autres instances
    ↓
BanChecker
    ↓
Cache local invalidé / état mis à jour (résolution par timestamp)
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

Chaque composant à état partagé a été analysé selon son **profil d'accès** (lecture fréquente vs écriture fréquente, collection dynamique de clés vs valeur unique), avec la primitive de synchronisation adaptée à ce profil précis — jamais un mécanisme unique appliqué par réflexe.

### 14.1 `sync.RWMutex` — lecture fréquente, écriture rare

Utilisé par `MemoryStore`, `CachedStore`, `MemoryEventBus` (liste d'abonnés), `RBAC` (définitions de rôles/permissions).

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

Plusieurs lecteurs accèdent simultanément sans jamais se bloquer entre eux ; une écriture reste exclusive et attend que toutes les lectures en cours se terminent.

### 14.2 Le piège des pointeurs dans une map

```text
map[TenantID]*Tenant
```

Une map protégée par `RWMutex` protège les accès à la map **elle-même** (ajout, suppression, lecture d'une clé), mais **pas** le contenu pointé par les valeurs qu'elle stocke si ce contenu est muté directement.

```text
Map
 │
 └── *Tenant ──────────┐
                       │
                       ▼
                    Tenant
                    State
```

**Règle retenue et appliquée systématiquement** : les méthodes de lecture (`Get`) retournent toujours une **copie**, jamais le pointeur interne ; les méthodes d'écriture (`SetState`, `Create`, `Update`) modifient l'objet interne **directement, sous `Lock()` exclusif** — jamais via un aller-retour lecture-modification-réécriture, qui recréerait une fenêtre de *lost update*.

### 14.3 `sync.Map` + `LoadOrStore` — collections dynamiques par clé

Utilisé par `BanChecker` (`TenantID → banEntry`), `TenantRateLimiter` (`TenantID → *rate.Limiter`), `MemoryMetrics` (`TenantID → *tenantMetrics`).

**Le problème résolu par `LoadOrStore`** : si deux goroutines arrivent simultanément pour un tenant qui n'a **jamais** encore d'entrée, un simple `Load` puis `Store` séparés pourrait faire créer et écraser deux valeurs distinctes (par exemple deux `*rate.Limiter` différents pour le même tenant, l'un écrasant l'autre). `LoadOrStore` garantit atomiquement qu'**une seule** valeur devient la référence officielle partagée, même si les deux goroutines ont chacune préparé leur propre valeur candidate.

```text
Goroutine A                    Goroutine B

Load → absent                  Load → absent
  │                              │
crée limiter A                 crée limiter B
  │                              │
LoadOrStore                    LoadOrStore
  │                              │
A est enregistré               B voit que A existe déjà
                                  │
                           B n'est PAS enregistré

Résultat : les deux goroutines utilisent le même *rate.Limiter A
```

### 14.4 `sync/atomic` — compteurs à écriture très fréquente

Utilisé par `MemoryMetrics` pour `requests`, `errors`, `latencySum`, `latencyCount` — des compteurs incrémentés à chaque requête, potentiellement par des dizaines de milliers de goroutines simultanées. `atomic.Int64.Add()` garantit une incrémentation correcte sans jamais nécessiter de verrou explicite.

**Deux niveaux de concurrence combinés** dans `MemoryMetrics` : `sync.Map` protège la collection dynamique de tenants, `atomic.Int64` protège chaque compteur individuel — chacun à l'endroit optimal pour son propre problème.

### 14.5 Isolation par goroutine + `recover()` — EventBus (mémoire et Redis)

Chaque handler abonné à un `TenantEvent` s'exécute dans sa **propre goroutine**, protégée individuellement par un `recover()` :

```go
defer func() {
    if r := recover(); r != nil {
        // log
    }
}()
```

**Pourquoi ceci est crucial** : `recover()` ne fonctionne qu'à l'intérieur de la **même goroutine** que le `panic()` qu'il intercepte — il doit donc être placé à l'intérieur de la fonction lancée par `go`, jamais autour de l'appel à `Publish()` (qui a déjà retourné avant que le handler s'exécute réellement).

**Deux niveaux de goroutines dans `RedisEventBus`**, distincts et à ne pas confondre :

```text
Redis listener goroutine (unique, permanente)
        │
        │ réception séquentielle des messages
        ▼
   événement reçu
        │
        ├──► handler A goroutine + recover (éphémère, par événement)
        ├──► handler B goroutine + recover
        └──► handler C goroutine + recover
```

### 14.6 Résolution de conflit par timestamp — `BanChecker`

Un problème de concurrence plus subtil que la simple protection mémoire : un snapshot initial (chargé au démarrage) et un événement reçu en parallèle peuvent tous deux écrire une information pour le même tenant, sans garantie sur l'ordre réel d'exécution de leurs goroutines respectives. La solution retenue associe chaque entrée à un **timestamp de dernière mise à jour**, et rejette toute écriture dont le timestamp est **antérieur** à celui déjà stocké — garantissant qu'une information périmée ne peut jamais régresser une information plus récente, indépendamment de l'ordre d'arrivée réel.

### 14.7 `singleflight` — déduplication des appels concurrents

Utilisé par `CachedStore.Get()`. Distinct des mécanismes ci-dessus : ce n'est pas un problème de **sécurité mémoire** (le `RWMutex` protège déjà correctement la map de cache), mais un problème d'**efficacité** — sans `singleflight`, un pic de requêtes simultanées pour un même tenant en cache miss provoquerait autant d'appels dupliqués vers la source de vérité (*cache stampede*). `singleflight.Group.Do(key, fn)` garantit qu'un seul appel réel part vers la source pour une clé donnée ; les appelants concurrents attendent et reçoivent le même résultat.

### 14.8 Ce que `go test -race` permet de détecter

Le détecteur de race conditions de Go instrumente le binaire de test pour surveiller tous les accès mémoire concurrents. Il détecte notamment :

- une lecture et une écriture simultanées sur la même variable/champ, sans synchronisation commune (ce qui aurait été le cas si `Get()` avait continué à retourner le pointeur interne du `MemoryStore`, combiné à une écriture directe hors verrou) ;
- une incohérence dans l'usage d'une map Go non protégée sous accès concurrents ;
- tout accès non protégé qui *pourrait* corrompre l'état, même si le test ne "voit" pas de valeur incorrecte par pur hasard d'ordonnancement.

`go test -race` a été systématiquement utilisé à travers toutes les étapes, y compris dans la CI GitHub Actions à chaque push, sur le module racine et sur chaque sous-module Go séparé.

---

## 15. Testabilité

### 15.1 Principe général

Chaque composant est conçu pour être testé **indépendamment**, sans nécessiter d'infrastructure réelle. C'est un principe appliqué dès l'Étape 1 et maintenu jusqu'à l'Étape 8.

### 15.2 Fakes internes

Pour tester les composants du cœur du toolkit eux-mêmes, des implémentations factices minimales des interfaces (`fakeResolver`, `fakeStore`, `fakeAdminStore`, `countingStore`) sont écrites directement dans les fichiers de test des packages concernés — jamais exportées publiquement, elles n'existent que pour isoler le composant testé de ses dépendances réelles.

### 15.3 Tests unitaires purs

La grande majorité des composants (`tenantctx`, `store`, `eventbus`, `banchecker`, `ratelimit`, `cachekey`, `rbac`, `metrics`, `admin`) sont testés avec des tests Go standards (`testing` + `testify`), sans dépendance externe.

### 15.4 Tests des middlewares HTTP — `httptest`

`net/http.testing` (`httptest.NewRequest`, `httptest.NewRecorder`) est utilisé pour tester l'adaptateur `net/http` et l'adaptateur Chi (qui repose directement sur `http.Handler`), en simulant une vraie chaîne de traitement HTTP de bout en bout.

### 15.5 Tests des middlewares de framework — mécanismes spécifiques

- **Gin** — `gin.CreateTestContext(recorder)` construit un `*gin.Context` de test à partir d'un `*gin.Engine` interne.
- **Echo** — `echo.New()` + `e.NewContext(req, recorder)` construit un `echo.Context` de test.
- **Chi** — repose directement sur `net/http`, donc les mêmes primitives `httptest` suffisent (pas de mécanisme de test spécifique à Chi).

Dans chaque cas, le test appelle le **vrai** handler produit par le middleware (`handler.ServeHTTP(...)`, `handler(c)`), jamais directement une fonction interne — garantissant que le comportement testé correspond exactement à ce qui se passerait en production, y compris le routage lui-même (pour l'Admin API, notamment, l'usage de `handler.ServeHTTP()` plutôt qu'un appel direct au handler valide aussi que la déclaration de route `http.ServeMux` fonctionne réellement).

### 15.6 Tests de Redis — `miniredis`

Voir [section 10.23](#1023-stratégie-de-test--pourquoi-miniredis-plutôt-quun-vrai-redis). Une implémentation Redis en mémoire pure permet de tester `RedisEventBus` sans exiger de serveur Redis réel, ni localement, ni en CI.

### 15.7 `tenanttest` — testabilité pour les utilisateurs externes

Le package `tenanttest` prolonge ce principe de testabilité **au-delà** du toolkit lui-même, pour les développeurs qui l'utilisent dans leurs propres applications (voir [Étape 8](#11-étape-8--helpers-de-test-tenanttest) en détail).

### 15.8 Tests de concurrence — `go test -race`

Chaque composant à état partagé possède au moins un test dédié à la concurrence réelle (multiples goroutines simultanées), systématiquement exécuté avec le flag `-race` — que ce soit en local ou en CI GitHub Actions. C'est le mécanisme qui a permis de découvrir et corriger des problèmes de conception (notamment le piège des pointeurs partagés dans `MemoryStore`, section 14.2) avant qu'ils ne deviennent des bugs en production.

### 15.9 Pourquoi les composants sont conçus pour être testables indépendamment

Chaque composant expose une **interface minimale** définie dans le package `tenant` (ou dans son propre package pour les composants qui n'ont pas encore de contrat centralisé). N'importe quelle implémentation, y compris une implémentation factice écrite en quelques lignes dans un fichier de test, peut satisfaire ce contrat grâce au typage structurel de Go — permettant de tester un composant de haut niveau (`Manager`, `admin.Service`, un middleware) sans jamais instancier de vraie base de données, de vrai Redis, ou de vrai serveur HTTP complet.

---

## 16. Limites et évolutions futures

Cette section regroupe l'ensemble des limites **explicitement documentées** au fil des étapes, ainsi que les évolutions envisagées mais **non implémentées**.

| Sujet | État actuel (implémenté) | Limitation connue | Évolution future envisagée |
|---|---|---|---|
| `SetState → Publish` (Admin) | Ordre retenu, jamais d'événement mensonger | Non atomique — un `Publish` peut échouer après un `SetState` réussi, événement potentiellement perdu | Pattern Outbox (transaction unique état + événement, worker de publication asynchrone avec retry) |
| Admin API — authentification | Aucune | L'API ne doit pas être exposée directement à Internet en production | Authentification/autorisation à ajouter |
| Admin API — erreurs HTTP | `writeError` retourne systématiquement `500` | Pas de distinction `404`/`409`/`503` | Mapping fin des erreurs, nécessite une erreur sentinelle exportée au niveau de `AdminStore` |
| Admin API — endpoints | `Ban`/`Disable`/`Activate` uniquement | Pas de `Create`/`Get` HTTP, même si `AdminStore.Create` et `Store.Get` existent | Ajouter `Service.Create`/`Service.Get` d'abord, puis les endpoints correspondants, si le besoin métier apparaît |
| `EventBus` (Redis) | Pub/Sub fonctionnel, fail-fast à l'abonnement | Pas de gestion avancée de reconnexion/lifecycle en cas de coupure Redis prolongée | Reconnexion automatique, observabilité de la santé de la connexion |
| `Redis` | Propagation temps réel opérationnelle | Résilience et observabilité limitées | Monitoring dédié, métriques de latence de propagation |
| `MemoryStore.Get()` — copie | Copie superficielle (*shallow copy*) du `*Tenant` | Le champ `Roles []string` partage le même tableau sous-jacent que l'original ; une mutation de `Roles[i]` par le consommateur affecterait encore l'original | Copie profonde (*deep copy*) du slice `Roles` si ce risque devient significatif |
| Tests `RedisEventBus` | Couverts via `miniredis` (simulation) | `miniredis` ne garantit pas toutes les subtilités d'un vrai serveur Redis | Test d'intégration avec un vrai Redis, en complément, pas en remplacement |
| `tenanttest` | `WithFakeTenant` / `WithFakeTenantFull` | Pas de simulation du pipeline HTTP complet | `NewFakeResolver`, `NewFakeStore`, `NewFakeManager` — uniquement si un besoin réel apparaît |
| `Prometheus` (Metrics) | Interface `MetricsCollector` définie + implémentation en mémoire | Pas d'adaptateur Prometheus concret construit à ce stade | Implémentation `PrometheusMetrics` satisfaisant le même contrat |
| `RateLimiter` distribué | Implémentation en mémoire (par instance) | Les quotas ne sont pas partagés entre plusieurs instances du serveur | `RedisRateLimiter`, sur le même principe d'agnosticisme que `EventBus` |
| `go.mod` — directive `replace` (sous-modules) | Utilisée pour le développement local avant publication | Pointe vers un chemin local (`../..`), invalide pour un vrai utilisateur externe | À retirer une fois le module racine taggé et publié |

> **Règle transversale à retenir** : chaque limite ci-dessus a été **documentée explicitement dans le code au moment où elle a été identifiée** (commentaires, messages de log), plutôt que laissée implicite — cohérent avec le principe général du projet de préférer une incohérence *observable* aujourd'hui à une fausse solution prématurée.

---

## 17. Arbre des packages

```text
tenant-core/                          (module racine)
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
│   ├── nethttp.go                    → Wrap()                (module racine)
│   │
│   ├── gin/                          → Middleware()   (SOUS-MODULE Go séparé)
│   │   ├── go.mod   (module .../middleware/gin, replace → ../..)
│   │   ├── gin.go
│   │   └── gin_test.go
│   │
│   ├── echo/                         → Middleware()   (SOUS-MODULE Go séparé)
│   │   ├── go.mod   (module .../middleware/echo, replace → ../..)
│   │   ├── echo.go
│   │   └── echo_test.go
│   │
│   └── chi/                          → Middleware()   (SOUS-MODULE Go séparé)
│       ├── go.mod   (module .../middleware/chi, replace → ../..)
│       ├── chi.go
│       └── chi_test.go
│
├── eventbus/redis/                   → RedisEventBus (SOUS-MODULE Go séparé)
│   ├── go.mod        (module .../eventbus/redis, replace → ../..)
│   ├── redis.go
│   └── redis_test.go (miniredis)
│
├── .github/workflows/ci.yml          → CI multi-module (root + 4 sous-modules)
├── LICENSE (MIT)
├── README.md
└── .gitignore
```

**Statut des sous-modules Go indépendants** — `middleware/gin`, `middleware/echo`, `middleware/chi` et `eventbus/redis` possèdent chacun leur **propre `go.mod`**, distinct du module racine. Cette organisation garantit qu'un développeur qui utilise uniquement `net/http` (ou uniquement Gin) n'installe **jamais** les dépendances des frameworks/technologies qu'il n'utilise pas — chaque sous-module se construit et se teste indépendamment (`cd middleware/gin && go test ./...`), et la CI GitHub Actions exécute une étape dédiée par sous-module (`working-directory`), en plus de l'étape sur le module racine.

Chaque sous-module référence le module racine via une directive `replace ... => ../..` pendant le développement, permettant de pointer vers le code local avant qu'une version taguée ne soit publiée sur le dépôt public.

---

## 18. Décisions / points à clarifier

Cette section signale les endroits où les documentations sources décrivent des contrats de **façon conceptuelle** (souvent introduits par le mot *« Conceptuellement »* dans les documents d'origine), qui diffèrent légèrement de la forme exacte retenue dans l'implémentation réelle, sans que cela remette en cause la décision architecturale sous-jacente — uniquement le détail de signature.

### 18.1 RateLimiter — interface conceptuelle vs implémentation retenue

Le document source de l'Étape 4 présente un contrat conceptuel simplifié :

```go
type RateLimiter interface {
    Allow(ctx context.Context, id TenantID) bool
}
```

L'implémentation effectivement retenue repose sur un type concret `TenantRateLimiter`, dont la méthode `Allow` prend directement un `*Tenant` (pas seulement un `TenantID`) et dont la règle de limite par tenant est **injectée** via une fonction (`LimitFunc`) fournie par l'application — plutôt que fixée dans l'implémentation elle-même — s'appuyant sur `golang.org/x/time/rate` (modèle *token bucket*). Le principe métier (une limite indépendante par tenant, cœur agnostique de l'infrastructure) reste identique ; seule la forme exacte du contrat diffère de la version conceptuelle présentée dans le document source.

### 18.2 RBAC — `Authorizer` conceptuel vs `RBAC`/`Can` retenu

Le document source présente un contrat conceptuel :

```go
type Authorizer interface {
    Can(t *Tenant, permission string) bool
}
```

L'implémentation retenue est un type concret `RBAC` (pas une interface publiée dans `tenant.go`), avec une méthode `DefineRole(tenantID, role, permissions)` pour l'enregistrement, et `Can(t *Tenant, permission string) bool` pour la vérification — les définitions étant organisées **par tenant** (`map[TenantID]map[role]map[permission]struct{}`), comme décrit fidèlement dans le document source (section 8.5 de ce document). Le principe (séparation rôle/permission, indépendance par tenant, absence de dépendance HTTP) est identique.

### 18.3 Metrics — Prometheus mentionné comme réalisé vs statut réel

Le titre de l'Étape 5 dans les documents sources (« RBAC + Metrics (Prometheus) ») et plusieurs passages décrivent une implémentation `PrometheusMetrics` de façon assez concrète. D'après le déroulé effectif du projet, seule l'**interface** `MetricsCollector` et une implémentation **en mémoire** (`MemoryMetrics`, avec `sync.Map` + `atomic.Int64`) ont été concrètement construites et testées à ce stade — l'adaptateur Prometheus lui-même reste une **évolution future** listée en section 16, pas un composant déjà livré. Cette distinction est faite ici conformément à la règle *« ne pas transformer les améliorations futures en fonctionnalités déjà implémentées »*.

### 18.4 `tenant.New()` et l'orchestration complète des composants

Le document source de synthèse (section 11 à 16 du document *resumer.txt*) présente une vision englobante de `tenant.New()` avec des options comme `WithEventBus`, `WithRateLimiter`, `WithRBAC`, `WithMetrics` — orchestrant potentiellement l'ensemble des neuf composants du toolkit. L'implémentation effective de `Manager`/`New()` reste volontairement **plus restreinte** : seuls `Resolver` et `Store` sont assemblés par `New()`, la méthode `Resolve()` s'arrêtant à la production d'un `*Tenant` (sans construire de `context.Context`, pour éviter un cycle d'import avec `tenantctx`). Les autres composants (`BanChecker`, `RateLimiter`, `RBAC`, `Metrics`, `CacheKeyer`, `EventBus`) restent des briques indépendantes que l'application ou les adaptateurs de middleware invoquent explicitement, sans être automatiquement enchaînées par `Manager` lui-même — conformément au principe explicitement énoncé dans le document source : *« ce diagramme représente les composants disponibles dans l'écosystème, pas nécessairement un ordre d'exécution que `tenant.New()` imposera automatiquement »*, et *« `tenant.New()` doit rester propre : il compose les dépendances ; il ne doit pas devenir un énorme middleware qui mélange toutes les responsabilités »*.

### 18.5 Nom du package `banchecker`

Un document source (Étape 3) situe `BanChecker` dans un package nommé `banchecker/`, cohérent avec le reste de la documentation et avec l'implémentation effective.

---

*Fin de la documentation technique complète de tenant-core.*