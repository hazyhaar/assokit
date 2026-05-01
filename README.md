# assokit

Boîte à outils Go pour sites d'associations loi 1901 et sites communautaires sobres.
Composants `templ` réutilisables, paquet d'authentification, file d'attente d'emails,
recherche FTS5, gestion d'arbre de contenus.

## Périmètre

| Paquet | Rôle |
|---|---|
| `pkg/horui/layout` | `Base`, `Header`, `Footer`, `FlashBanner`, `Breadcrumb`, `Sidebar`, `ErrorPage` |
| `pkg/horui/components` | `Button`, `Card`, `Form`, `TextField`/`EmailField`/`PasswordField`/`TextArea`/`Select`, `Modal`, `SearchBar`, `Badge`, `Tabs`, `Table`, `NodeCard` |
| `pkg/horui/forum` | Index + Thread + ThreadView **récursif** + ReplyForm |
| `pkg/horui/pages` | Pages clé en main paramétrables : StaticPage, Thematique, Participer, SignupForm, Contact, Donate, Login, Register, Search, Merci, NotFound |
| `pkg/horui/auth` | `Store` : Register, Authenticate, GetByID — bcrypt + sessions |
| `pkg/horui/middleware` | Sessions cookie signé, Flash, Theme middleware, Auth |
| `pkg/horui/perms` | RBAC nœuds × rôles |
| `pkg/horui/tree` | Hiérarchie nœuds (folder/page/post/form/doc), markdown→HTML via goldmark |
| `pkg/horui/search` | Wrapper FTS5 SQLite |
| `pkg/horui/theme` | Charte graphique (palette + fonts + nom du site) |
| `pkg/mailer` | Outbox + worker SMTPS (port 465) ou Resend HTTP, backoff exponentiel |
| `schema/` | DDL SQLite (users, roles, nodes, permissions, signups, email_outbox, activation_tokens, FTS5) |
| `static/css/horui.css` | Feuille de style ~510 lignes (palette + composants) |

## Stack

- Go 1.26
- `github.com/a-h/templ` v0.3+
- `github.com/go-chi/chi/v5`
- `modernc.org/sqlite` (pure Go, FTS5 inclus)
- `github.com/yuin/goldmark`

## Démarrage rapide

```go
import (
    "github.com/hazyhaar/assokit/pkg/horui/layout"
    "github.com/hazyhaar/assokit/pkg/horui/pages"
    "github.com/hazyhaar/assokit/pkg/horui/theme"
)

func handleHome(w http.ResponseWriter, r *http.Request) {
    th := theme.Defaults()
    th.SiteName = "Mon Asso"
    nav := []layout.NavItem{{Label: "Accueil", Href: "/"}}
    layout.Base(th, "Accueil", nav, pages.StaticPage("Bienvenue", "", "<p>Hello</p>")).Render(r.Context(), w)
}
```

## Versionnement

Versions majeures uniquement quand l'API change. Patches/correctifs dans la branche
`main`. Tag `vX.Y.Z` à chaque release.

## Licence

MIT. Voir [LICENSE](LICENSE).
