# Changelog

## [v0.3.0] - 2026-05-02

### Added
- **Feedback module** (F1/F2/F3) : collecte anonyme stricte, widget HTMX public, interface admin triage paginée
  - `pkg/horui/admin/feedback_list.templ` — liste + détail + formulaire triage
  - `internal/handlers/admin_feedbacks.go` — GET list, GET detail, POST triage (whitelist status)
  - Migration v2 : table `feedbacks` (ip_hash, fingerprint, source, status, triaged_by)
- **RBAC module** (Sprint 1, briques 1–3) :
  - RBAC-1 : schema Goose `permissions`, `grades`, `grade_permissions`, `grade_inherits` DAG, `user_grades`, `user_effective_permissions`, `rbac_audit` + `Store` complet
  - RBAC-2 : `Service{Store, Cache}` — `Can()` hot-path L1→L2, `Recompute()` TX atomique, hooks mutation (`AssignGrade`, `GrantPerm`, `AddInherit`, …)
  - `Cache` L1 in-memory version-based (`sync.Map` + `atomic.Uint64`), invalidation par `BumpVersion()`, bound 10k users
  - RBAC-3 : middleware chi `RBAC(svc)` — injection service+userID en ctx ; helpers `perms.Required(perm)`, `perms.Has(ctx, perm)`, `perms.IfHas(ctx, perm, component)` pour rendu conditionnel templ
- `Store.GradeDescendants(ctx, id)` — CTE récursive descendante pour propagation cache sur mutation grade parent
- `pkg/horui/perms` : helpers contexte RBAC (`ContextWithService`, `ServiceFromContext`, `ContextWithUserID`, `UserID`)

### Fixed
- Tests CDP (`integration_cdp_test.go`) portés vers API post-Lot2 (`SeedNodes` → `app.Bootstrap`, `theme.Defaults` supprimé)
- `recomputeGradeUsers` : closure descendante manquante — GrantPerm sur grade B ne recomputait pas les users du grade A (A inherits B) ; corrigé via `GradeDescendants` CTE

### Testing
- Suite complète `go test ./...` verte — aucune régression
- Tests gardiens RBAC : `TestService_TransitiveClosure_DAG`, `TestCan_HotPath_NoDBQueryAfterCacheHit`, `TestService_GrantPermOnAncestorGradeRecomputesDescendantUsers`, `TestService_AddInheritCascadesDescendantRecompute`, `TestStore_GradeDescendants_RecursiveCTE`
- Tests gardiens RBAC-3 : `TestPerms_Required403WithoutPerm_NextWithPerm`, `TestPerms_IfHasRendersContentOnlyWhenAllowed`, `TestPerms_CtxWithoutUserReturnsForbidden`
- Benchmark `BenchmarkCan_CacheHit` < 1µs (hot path L1)

### Migration notes (v0.2 → v0.3)
- Ajouter `middleware.RBAC(svc)` après `middleware.Auth(...)` dans la chaîne chi si RBAC activé
- `perms.Required("my.perm")` remplace les checks manuels `if !user.HasPerm(...)` dans les routes
- Run `chassis.Run(db)` inclut maintenant les migrations RBAC (v3) — idempotent, pas d'action manuelle

---

## [v0.2.0] - 2026-05-01

### Added
- `assokit` CLI binaire standalone (Go 1.26 + SQLite + Chi v5 + Templ)
- `/sitemap.xml` handler (standard sitemap XML)
- `/robots.txt` handler (User-agent: * Allow: /)
- Theme singleton via `pkg/horui/theme` (chargé depuis `branding.toml`)
- Pages markdown depuis `BrandingFS` (`/pages/*.md`)
- Mailer SMTP avec AUTH LOGIN + STARTTLS + fallback outbox
  - `/signup` avec profils configurables (lanceur, observateur, etc.) via `profils.toml`
- Forum hiérarchique (threads/reply) avec permissions
- Search via `pkg/horui/search` (grep nodes/posts)

### Changed
- Public API repensée : `pkg/api.New()` + `ListenAndServe()` + `Handler()`
- `Theme` devient singleton global au lieu de paramètre par requête
- Branding externalisé dans `branding.toml` (nom, couleurs, liens)
- Routing chi v5 avec middleware auth/session/flash centralisés

### Removed
- Packages legacy NPS-specific supprimés
- Dépendances frontend compilé (tout rendu server-side Templ)
- Config in-code remplacée par TOML + env vars

### Migration notes (v0.1 → v0.2)
1. Remplacez vos imports legacy par `github.com/hazyhaar/assokit/pkg/api`
2. Créez un dossier `config/` avec `branding.toml` + `pages/` + `assets/`
3. Passez la config via `api.Options{DBPath, BaseURL, Port, BrandingFS, AdminEmail, CookieSecret}`
4. Le theme est maintenant chargé automatiquement au `New()` — plus de paramètre `theme` à passer aux handlers
5. Les pages statiques sont des fichiers markdown dans `BrandingFS`, plus de strings hardcodées

### Fixes
- `api.ListenAndServe` honore maintenant `opts.Port` (était hardcodé à 8080)
- Graceful shutdown sur SIGINT/SIGTERM (timeout 10s)

### Testing
- `go test -race ./...` : 0 leak, 0 data race
- `go test -tags=integration_cdp ./internal/handlers/...` : CDP crawler + full E2E
- Cross-platform builds : linux/amd64 (6.6MB), linux/arm64 (6.1MB) — both built from CGO=0 static, validated via `file` ELF header
