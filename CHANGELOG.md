# Changelog

## [v0.2.0] - 2026-05-01

### Added
- `assokit` CLI binaire standalone (Go 1.26 + SQLite + Chi v5 + Templ)
- `/sitemap.xml` handler (standard sitemap XML)
- `/robots.txt` handler (User-agent: * Allow: /)
- Theme singleton via `pkg/horui/theme` (chargé depuis `branding.toml`)
- Pages markdown depuis `BrandingFS` (`/pages/*.md`)
- Mailer SMTP avec AUTH LOGIN + STARTTLS + fallback outbox
- Multi-profils d'adhésion (/adhérer/lanceur, /adhérer/observateur, etc.)
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
- Gralceful shutdown sur SIGINT/SIGTERM (timeout 10s)

### Testing
- `go test -race ./...` : 0 leak, 0 data race
- `go test -tags=integration_cdp ./internal/handlers/...` : CDP crawler + full E2E
- Cross-platform builds : linux/amd64 (6.6MB), linux/arm64 (à tester)
