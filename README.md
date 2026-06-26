# assokit

**assokit** is an open-source (MIT) Go toolkit for building the website of any
**member-based community** — associations, clubs, collectives, cooperatives,
teams. It ships as a single self-contained binary: server-rendered HTML, a built
SQLite database, and a native **MCP endpoint** so an LLM can drive every feature
a human can.

> One instance = one community. Multi-community is achieved by replicating
> instances, never by a `tenant_id` column.

## Highlights

- **Single binary, zero external services required.** Pure-Go SQLite
  (`modernc.org/sqlite`, `CGO_ENABLED=0`), embedded migrations, server-rendered
  views (templ + Alpine.js + HTMX). No bundler, no SPA.
- **LLM-parity as a hard invariant.** Every human action goes through a single
  action registry and is therefore automatically exposed as an MCP tool. The
  LLM of an operator can do anything a human can — and a test suite guards all
  ~58 actions behaviorally.
- **Tenant-agnostic core.** The core never reads the environment; the database,
  secrets, branding and instance identity are injected at the boundary
  (`api.New(Options{...})`). Easy to embed, test, and replicate.
- **Batteries included.** Members & RBAC, authentication (password, magic-link,
  OAuth 2.1 provider + Dynamic Client Registration, social login), forum, CMS
  pages, full-text search, private messaging, events, memberships, newsletter,
  donations (HelloAsso), video rooms (LiveKit), email (SMTP/Resend), and an
  admin Setup dashboard.

## Quick start

```go
package main

import (
	"context"
	"os"

	"github.com/hazyhaar/assokit/pkg/api"
)

func main() {
	app, err := api.New(api.Options{
		DBPath:        "assokit.db",
		Port:          "8080",
		BaseURL:       "https://my-community.org",
		BrandingFS:    os.DirFS("./config"),
		AdminEmail:    "admin@my-community.org",
		AdminPassword: "change-me",
		CookieSecret:  cookieSecret, // 32+ bytes; required in prod
	})
	if err != nil {
		panic(err)
	}
	_ = app.ListenAndServe(context.Background())
}
```

See `examples/minimal-asso/` for a runnable demonstration.

## Configuration

All configuration is passed through `api.Options` — the core reads no
environment variables. The reference binary `cmd/assokit` maps environment
variables to `Options` at the boundary (`PORT`, `DB_PATH`, `BASE_URL`,
`COOKIE_SECRET`, `ADMIN_EMAIL`/`ADMIN_PASSWORD`, `SMTP_*`/`RESEND_API_KEY`,
`GOOGLE_CLIENT_ID`/`SECRET`, `ASSOKIT_MASTER_KEY` for the connector vault,
`WEBHOOK_URL`/`WEBHOOK_SECRET` for outgoing events, `TRUST_PROXY_HEADERS`,
`APP_ENV=production`). The admin **Setup** screen (`/admin/setup`) diagnoses, per
feature, what is configured and what is still missing.

In production (`APP_ENV=production`), a missing `COOKIE_SECRET` is fatal — a
random secret would silently invalidate every session on restart.

## Architecture

- `pkg/api` — stable public surface (`New`, `Options`, `App`).
- `pkg/actions` — the single action registry; `MountHTTP` and `MountMCP` iterate
  the same registry, guaranteeing LLM-parity.
- `pkg/horui/*` — UI, auth, RBAC, theme, forum, search, messaging, setup.
- `pkg/connectors/*` — HelloAsso, LiveKit, webhooks, the encrypted vault.
- `internal/chassis` — a single embedded migration runner (no global state).
- The recursive node engine is extracted into a standalone MIT module,
  [`nodetree`](https://github.com/hazyhaar/nodetree); forum, pages and
  categories are thin layers over it.

## Connectors

Connectors are optional and configured through an encrypted vault (enabled with
`ASSOKIT_MASTER_KEY`):

- **HelloAsso** — donations and memberships, with HMAC-verified webhooks.
- **LiveKit** — video rooms; assokit mints access tokens and serves the room UI,
  the LiveKit server runs alongside.

Outgoing domain events (`member.signup`, `feedback.created`,
`forum.post.created`, `webhook.received`) are emitted to a pluggable
`EventSink`: a no-op by default, a signed generic HTTP webhook if `WEBHOOK_URL`
is set, or a custom sink injected at the boundary.

## Development

```sh
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go test ./...
CGO_ENABLED=1 go test -race ./...   # race detector
go vet ./...
```

See `CODE_PATTERNS_GO.md` for the project's Go conventions and `CONTRIBUTING.md`
for the contribution workflow.

## License

MIT. See [LICENSE](LICENSE).
