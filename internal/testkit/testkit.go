// Package testkit fournit une instance assokit ephemere DEJA authentifiee
// (compte admin), sans navigateur, pour les tests d'integration.
//
// Motivation : la voie de verification E2E native (examples/demoapp) exige
// un binaire Chromium et un parcours de login par formulaire. Pour un test qui
// n'a besoin QUE d'un client HTTP porteur d'une session admin valide, ce detour
// est couteux et fragile (faux 403, WAL orphelin, port squatte). Ce helper
// demarre la meme facade de bordure api.New, monte un httptest.Server et pose le
// cookie de session PROGRAMMATIQUEMENT via le secret d'instance — aucun
// navigateur, aucun formulaire.
//
// Reutilisation stricte (aucun recodage) :
//   - api.New(Options{...}) : provisionnement de l'instance (DB, migrations,
//     bootstrap admin idempotent via Options.AdminEmail/AdminPassword).
//   - identity.Store.Authenticate : preuve reelle des identifiants admin
//     (verification bcrypt) et source de l'AdminUserID.
//   - middleware.SetSessionCookie : signature HMAC du cookie assokit_session,
//     exactement celle que le middleware d'auth verifie a l'entree.
//
// Import-clean (frontiere d'export open-source) : stdlib + modernc.org/sqlite +
// deps assokit existantes, aucune dependance horos55.
package testkit

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite" // driver SQLite pour la connexion de lecture de l'AdminUserID

	"github.com/hazyhaar/assokit/pkg/api"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
)

// Defauts d'identite d'instance de test. Valeurs locales, jamais des secrets de
// production : injectees a la bordure comme run-local injecte les siennes.
const (
	defaultAdminEmail    = "admin@testkit.local"
	defaultAdminPassword = "testkit-admin-pwd-9f3c"
)

// Options configure l'instance de test. Valeur zero = tout par defaut.
type Options struct {
	// DBPath : chemin de la DB. Vide (defaut) → un fichier dans t.TempDir(),
	// nettoye automatiquement par le framework de test. Si un chemin fichier
	// hors t.TempDir est fourni, le teardown supprime explicitement les
	// sidecars WAL (.db-wal, .db-shm) laisses par le mode journal WAL.
	DBPath string
	// AdminEmail / AdminPassword : identifiants du compte admin bootstrappe.
	// Vides → valeurs de test par defaut.
	AdminEmail    string
	AdminPassword string
}

// Option applique une mutation aux Options (patron fonctionnel).
type Option func(*Options)

// WithDBPath fixe un chemin de DB explicite (fichier). Utile pour inspecter la
// DB apres coup ; le teardown nettoie alors les sidecars WAL.
func WithDBPath(path string) Option { return func(o *Options) { o.DBPath = path } }

// WithAdmin surcharge l'email et le mot de passe du compte admin bootstrappe.
func WithAdmin(email, password string) Option {
	return func(o *Options) { o.AdminEmail = email; o.AdminPassword = password }
}

// Instance est une instance assokit ephemere authentifiee.
type Instance struct {
	// Server est le httptest.Server servant app.Handler().
	Server *httptest.Server
	// Client porte un cookie de session admin valide (assokit_session) pose
	// programmatiquement — toute requete via ce client est authentifiee admin.
	Client *http.Client
	// AdminUserID est l'identifiant du compte admin bootstrappe (source :
	// identity.Authenticate, donc identifiants reellement verifies).
	AdminUserID string
	// AdminEmail est l'email du compte admin bootstrappe.
	AdminEmail string
	// Secret est le CookieSecret genere puis injecte a api.New — expose pour
	// forger d'autres cookies (ex. session d'un membre non-admin) au besoin.
	Secret []byte
}

// NewAuthedInstance demarre une instance assokit ephemere, bootstrappe le compte
// admin, monte un httptest.Server et retourne un client HTTP porteur d'une
// session admin valide. Le teardown (Shutdown + fermeture DB, nettoyage des
// sidecars WAL si DBPath fichier fourni) est enregistre via t.Cleanup.
//
// Toute erreur de provisionnement est fatale (t.Fatal) : le helper ne rend
// jamais une instance a moitie construite.
func NewAuthedInstance(t *testing.T, opts ...Option) *Instance {
	t.Helper()

	cfg := Options{}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.AdminEmail == "" {
		cfg.AdminEmail = defaultAdminEmail
	}
	if cfg.AdminPassword == "" {
		cfg.AdminPassword = defaultAdminPassword
	}

	// DBPath : fichier fourni (nettoyage sidecar au teardown) ou fichier dans
	// t.TempDir (nettoye par le framework, pas de sidecar a purger).
	customDB := cfg.DBPath != ""
	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = filepath.Join(t.TempDir(), "testkit.db")
	}

	// Secret d'instance : 32 octets aleatoires. Le helper doit connaitre CE
	// secret pour signer le cookie que le middleware d'auth verifiera.
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatalf("testkit: generation du secret de cookie: %v", err)
	}

	app, err := api.New(api.Options{
		DBPath:        dbPath,
		Port:          "0",
		BaseURL:       "http://127.0.0.1",
		AdminEmail:    cfg.AdminEmail,
		AdminPassword: cfg.AdminPassword,
		CookieSecret:  secret,
	})
	if err != nil {
		t.Fatalf("testkit: api.New (provisionnement instance): %v", err)
	}

	srv := httptest.NewServer(app.Handler())

	// AdminUserID : voie reelle — verification bcrypt des identifiants admin par
	// le manager d'identite, sur une connexion de lecture dediee a la DB de
	// l'instance. Aucun INSERT, aucun recodage : identity.Authenticate est la
	// meme porte que le formulaire /login.
	adminID := readAdminUserID(t, dbPath, cfg.AdminEmail, cfg.AdminPassword)

	// Cookie de session admin, pose programmatiquement via le secret d'instance.
	// SetSessionCookie ecrit dans un ResponseRecorder ; on en extrait le cookie
	// signe pour le charger dans le jar du client (secure=false : httptest sert
	// en HTTP clair).
	rec := httptest.NewRecorder()
	middleware.SetSessionCookie(rec, adminID, secret, false)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("testkit: cookie jar: %v", err)
	}
	srvURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("testkit: parse url serveur: %v", err)
	}
	jar.SetCookies(srvURL, rec.Result().Cookies())
	client := &http.Client{Jar: jar}

	t.Cleanup(func() {
		srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Shutdown arrete le serveur interne (non demarre ici) et ferme la DB.
		if err := app.Shutdown(ctx); err != nil {
			t.Logf("testkit: shutdown: %v", err)
		}
		if customDB {
			// Sidecars WAL laisses par journal_mode(WAL) : hors t.TempDir, le
			// framework ne les nettoie pas — suppression explicite.
			for _, suffix := range []string{"-wal", "-shm"} {
				_ = os.Remove(dbPath + suffix)
			}
		}
	})

	return &Instance{
		Server:      srv,
		Client:      client,
		AdminUserID: adminID,
		AdminEmail:  cfg.AdminEmail,
		Secret:      secret,
	}
}

// readAdminUserID ouvre une connexion de lecture sur la DB de l'instance et
// verifie les identifiants admin via identity.Authenticate (bcrypt), en
// retournant l'identifiant reel du compte. La connexion est fermee avant retour.
func readAdminUserID(t *testing.T, dbPath, email, password string) string {
	t.Helper()

	dsn := "file:" + dbPath + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("testkit: ouverture DB lecture: %v", err)
	}
	defer db.Close()

	store := &identity.Store{DB: db}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u, err := store.Authenticate(ctx, email, password)
	if err != nil {
		t.Fatalf("testkit: authentification admin (%s): %v", email, err)
	}
	return u.ID
}
