package handlers

import (
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/connectors/livekit"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/salon"
)

// newSalonVisioTestEnv prépare un DB en mémoire avec un salon et des membres,
// et retourne une implémentation bouchon du connecteur LiveKit.
func newSalonVisioTestEnv(t *testing.T) (db *sql.DB, deps app.AppDeps, ownerID, nonMemberID, slug string) {
	t.Helper()
	rawDB := newTestDB(t)
	seedRoles(t, rawDB)

	deps = app.AppDeps{
		DB:     rawDB,
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	// Créer deux utilisateurs.
	ownerID = "user-owner-" + t.Name()
	nonMemberID = "user-nonmember-" + t.Name()
	for _, uid := range []string{ownerID, nonMemberID} {
		if _, err := rawDB.Exec(
			`INSERT INTO users(id, email, password_hash, display_name) VALUES(?,?,?,?)`,
			uid, uid+"@test.example", "x", "Test User",
		); err != nil {
			t.Fatalf("seed user %s: %v", uid, err)
		}
	}

	// Créer un salon avec visio_enabled=1.
	st := &salon.Store{DB: rawDB}
	sl, err := st.Create(t.Context(), "Test Salon Visio", ownerID, "", true)
	if err != nil {
		t.Fatalf("salon.Create: %v", err)
	}
	slug = sl.Slug
	return rawDB, deps, ownerID, nonMemberID, slug
}

// buildSalonVisioRouter construit un router chi pour les tests de garde.
// conn peut être nil pour simuler le Vault absent (503).
func buildSalonVisioRouter(deps app.AppDeps, conn *livekit.Connector) *chi.Mux {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return next
	})
	// Montage direct du handler sans passer par MountSalonVisioRoute
	// afin de pouvoir injecter le connecteur bouchon ou nil.
	r.Get("/salon/{slug}/visio", handleSalonVisio(deps, conn))
	// Route consentement RGPD (hôte-only, indépendante du connecteur).
	r.Post("/salon/{slug}/visio/transcription", handleVisioTranscriptionConsent(deps))
	return r
}

// injectUser ajoute un utilisateur dans le contexte de la requête (simule le middleware auth).
func injectUser(r *http.Request, u *identity.User) *http.Request {
	return r.WithContext(middleware.ContextWithUser(r.Context(), u))
}

func TestSalonVisio_NonMembre_403(t *testing.T) {
	rawDB, deps, _, nonMemberID, slug := newSalonVisioTestEnv(t)
	defer rawDB.Close()

	// Connecteur factice (inutile ici car on attend 403 avant la forge).
	r := buildSalonVisioRouter(deps, nil)

	req := httptest.NewRequest(http.MethodGet, "/salon/"+slug+"/visio", nil)
	req = injectUser(req, &identity.User{ID: nonMemberID, Email: nonMemberID + "@test.example"})
	rw := httptest.NewRecorder()
	r.ServeHTTP(rw, req)

	// Non membre + connecteur nil = on attend 403 (membre check avant conn check).
	if rw.Code != http.StatusForbidden {
		t.Errorf("non-membre: attendu 403, got %d", rw.Code)
	}
}

func TestSalonVisio_VisioDisabled_403(t *testing.T) {
	rawDB, deps, ownerID, _, _ := newSalonVisioTestEnv(t)
	defer rawDB.Close()

	// Créer un second salon avec visio_enabled=0.
	st := &salon.Store{DB: rawDB}
	sl2, err := st.Create(t.Context(), "Salon Sans Visio", ownerID, "", false)
	if err != nil {
		t.Fatalf("salon.Create: %v", err)
	}

	r := buildSalonVisioRouter(deps, nil)

	req := httptest.NewRequest(http.MethodGet, "/salon/"+sl2.Slug+"/visio", nil)
	req = injectUser(req, &identity.User{ID: ownerID, Email: ownerID + "@test.example"})
	rw := httptest.NewRecorder()
	r.ServeHTTP(rw, req)

	if rw.Code != http.StatusForbidden {
		t.Errorf("visio_enabled=0: attendu 403, got %d", rw.Code)
	}
}

func TestSalonVisio_MembreVisioEnabled_ConnAbsent_503(t *testing.T) {
	rawDB, deps, ownerID, _, slug := newSalonVisioTestEnv(t)
	defer rawDB.Close()

	// conn=nil = Vault absent.
	r := buildSalonVisioRouter(deps, nil)

	req := httptest.NewRequest(http.MethodGet, "/salon/"+slug+"/visio", nil)
	req = injectUser(req, &identity.User{ID: ownerID, Email: ownerID + "@test.example"})
	rw := httptest.NewRecorder()
	r.ServeHTTP(rw, req)

	if rw.Code != http.StatusServiceUnavailable {
		t.Errorf("vault absent: attendu 503, got %d", rw.Code)
	}
}

func TestSalonVisio_TranscriptionConsent_NonOwner_403(t *testing.T) {
	rawDB, deps, ownerID, _, slug := newSalonVisioTestEnv(t)
	defer rawDB.Close()

	// Ajouter un membre non-owner.
	memberID := "user-member-" + t.Name()
	if _, err := rawDB.Exec(
		`INSERT INTO users(id, email, password_hash, display_name) VALUES(?,?,?,?)`,
		memberID, memberID+"@test.example", "x", "Membre",
	); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	st := &salon.Store{DB: rawDB}
	if err := st.Join(t.Context(), slug, memberID, ""); err != nil {
		t.Fatalf("join: %v", err)
	}
	_ = ownerID

	r := buildSalonVisioRouter(deps, nil)

	req := httptest.NewRequest(http.MethodPost, "/salon/"+slug+"/visio/transcription", nil)
	req = injectUser(req, &identity.User{ID: memberID, Email: memberID + "@test.example"})
	rw := httptest.NewRecorder()
	r.ServeHTTP(rw, req)

	if rw.Code != http.StatusForbidden {
		t.Errorf("non-owner: attendu 403, got %d", rw.Code)
	}
}

func TestSalonVisio_TranscriptionConsent_Owner_Toggle(t *testing.T) {
	rawDB, deps, ownerID, _, slug := newSalonVisioTestEnv(t)
	defer rawDB.Close()

	r := buildSalonVisioRouter(deps, nil)

	// POST consent=1 par le owner.
	body := strings.NewReader("consent=1")
	req := httptest.NewRequest(http.MethodPost, "/salon/"+slug+"/visio/transcription", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = injectUser(req, &identity.User{ID: ownerID, Email: ownerID + "@test.example"})
	rw := httptest.NewRecorder()
	r.ServeHTTP(rw, req)

	// Attendu : redirection 303 vers la page visio.
	if rw.Code != http.StatusSeeOther {
		t.Errorf("owner toggle: attendu 303, got %d", rw.Code)
	}

	// Vérifier la persistance.
	st := &salon.Store{DB: rawDB}
	ok, err := st.ShouldTranscribe(t.Context(), slug)
	if err != nil {
		t.Fatalf("ShouldTranscribe: %v", err)
	}
	if !ok {
		t.Fatal("ShouldTranscribe doit être true après toggle owner (visio active + consent)")
	}
}
