// Exemple minimal d'application asso utilisant assokit.
//
// Démontre :
//   - Init DB SQLite via schema.Run
//   - Layout horui + composants templ
//   - Auth (register/login/logout) via pkg/horui/auth + middleware
//   - Pages clé en main (home custom, contact, login, 404)
//   - Mailer (mode désactivé sans SMTP/APIKey, emails restent en outbox)
//   - Static files + theme custom
//
// Lancement :
//
//	cd examples/minimal-asso
//	go run .
//	# puis ouvrir http://localhost:8080/
package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	chimid "github.com/go-chi/chi/v5/middleware"
	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/layout"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/pages"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
	"github.com/hazyhaar/assokit/pkg/mailer"
	"github.com/hazyhaar/assokit/schema"
)

const cookieSecret = "demo-cookie-secret-32bytes-padded000"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// 1. DB éphémère in-memory partagée — en prod : file:./data.db?...
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(1)")
	must(err, "open db")
	defer db.Close()
	db.SetMaxOpenConns(1)
	must(schema.Run(db), "schema")

	// 2. Theme custom
	th := theme.Defaults()
	th.SiteName = "Mon Asso"
	th.SiteTagline = "Démo assokit — exemple minimal"

	// 3. Mailer (sans backend → emails dans outbox status=pending)
	m := &mailer.Mailer{DB: db, From: "noreply@example.org", AdminTo: "admin@example.org", Logger: logger}

	// 4. Router + middlewares
	r := chi.NewRouter()
	r.Use(chimid.Recoverer)
	r.Use(middleware.Flash)
	r.Use(middleware.Auth(db, []byte(cookieSecret)))

	nav := []layout.NavItem{
		{Label: "Accueil", Href: "/"},
		{Label: "Contact", Href: "/contact"},
		{Label: "Connexion", Href: "/login"},
	}

	// 5. Routes
	r.Get("/", renderPage(th, nav, "Accueil", homeContent()))
	r.Get("/contact", renderPage(th, nav, "Contact", pages.Contact()))
	r.Post("/contact", handleContact(m, "admin@example.org", logger))
	r.Get("/login", renderPage(th, nav, "Connexion", pages.Login()))
	r.Post("/login", handleLogin(db, []byte(cookieSecret), logger))
	r.Post("/logout", handleLogout)
	r.Get("/merci", renderPage(th, nav, "Merci", pages.Merci("Merci", "Votre message a bien été reçu.")))
	r.NotFound(renderPage(th, nav, "404", pages.NotFound()))
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port
	logger.Info("listening", "addr", addr, "site", th.SiteName)
	srv := &http.Server{Addr: addr, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	must(srv.ListenAndServe(), "listen")
}

// homeContent : exemple de page d'accueil custom (pas dans le kit).
func homeContent() templ.Component {
	return pages.StaticPage("Bienvenue", "Démo", `
		<p>Cette page est rendue par <code>pages.StaticPage</code>.</p>
		<p>Allez voir <a href="/contact">Contact</a> ou <a href="/login">Connexion</a>.</p>
	`)
}

func renderPage(th theme.Theme, nav []layout.NavItem, title string, content templ.Component) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		layout.Base(th, title, nav, content).Render(r.Context(), w) //nolint:errcheck
	}
}

func handleContact(m *mailer.Mailer, adminEmail string, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		nom := r.FormValue("nom")
		email := r.FormValue("email")
		msg := r.FormValue("message")
		if nom == "" || email == "" || msg == "" {
			middleware.PushFlash(w, "error", "Champs requis manquants.")
			http.Redirect(w, r, "/contact", http.StatusSeeOther)
			return
		}
		body := "De : " + nom + " <" + email + ">\n\n" + msg
		_ = m.Enqueue(r.Context(), adminEmail, "[Contact] "+nom, body, body)
		logger.Info("contact enqueued", "from", email)
		http.Redirect(w, r, "/merci", http.StatusSeeOther)
	}
}

func handleLogin(db *sql.DB, secret []byte, logger *slog.Logger) http.HandlerFunc {
	store := &auth.Store{DB: db}
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		u, err := store.Authenticate(r.Context(), r.FormValue("email"), r.FormValue("password"))
		if err != nil {
			middleware.PushFlash(w, "error", "Identifiants invalides.")
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		middleware.SetSessionCookie(w, u.ID, secret, false)
		logger.Info("login", "user", u.ID)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	middleware.ClearSessionCookie(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func must(err error, what string) {
	if err != nil {
		panic(what + ": " + err.Error())
	}
}

var _ = context.Background // keep import for clarity in handlers if needed
