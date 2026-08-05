// CLAUDE:SUMMARY Tests du parcours guidé d'intégration d'un propriétaire (V3c) :
// activation par lien (sans mot de passe), jeton consommé/rejoué/expiré, re-onboard.
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/membership"
	"github.com/hazyhaar/assokit/pkg/parcelle"
)

func adminOnboardReq(method, target string, form url.Values) *http.Request {
	var r *http.Request
	if form != nil {
		r = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	return r.WithContext(setAdminUser(r.Context()))
}

func onboardDeps(t *testing.T) app.AppDeps {
	t.Helper()
	db := newTestDB(t)
	return app.AppDeps{
		DB: db, Logger: newAccountDeps(t).Logger, Mailer: nil,
		Config: config.Config{BaseURL: "https://demoapp.test", CookieSecret: []byte("0123456789abcdef0123456789abcdef")},
	}
}

func validOnboardForm() url.Values {
	form := url.Values{}
	form.Set("display_name", "Jean Berger")
	form.Set("email", "jean.berger@example.com")
	form.Set("period_start", "2026-01-01")
	form.Set("period_end", "2026-12-31")
	form.Set("amount_cents", "5000")
	form.Set("parcelle_commune", "09061")
	form.Set("parcelle_section", "ZD")
	form.Set("parcelle_numero", "0007")
	form.Set("parcelle_surface", "12000")
	form.Set("parcelle_nature", "pleine_propriete")
	return form
}

// TestOnboard_NominalCascade : intégration sans mot de passe, jeton d'activation émis.
func TestOnboard_NominalCascade(t *testing.T) {
	deps := onboardDeps(t)
	defer deps.DB.Close()
	ctx := context.Background()

	r := adminOnboardReq(http.MethodPost, "/admin/onboard-proprietaire", validOnboardForm())
	w := httptest.NewRecorder()
	handleAdminOnboardProprietaire(deps).ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("attendu 303 après POST, obtenu %d", w.Code)
	}

	user, err := (&identity.Store{DB: deps.DB}).GetUserIDByEmail(ctx, "jean.berger@example.com")
	if err != nil {
		t.Fatalf("membre absent: %v", err)
	}

	var hash string
	deps.DB.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, user).Scan(&hash)
	if hash != "" {
		t.Errorf("password_hash = %q, attendu vide (activation par lien)", hash)
	}

	var tokenCount int
	deps.DB.QueryRow(`SELECT COUNT(*) FROM login_magic_tokens WHERE email = ?`, "jean.berger@example.com").Scan(&tokenCount)
	if tokenCount != 1 {
		t.Fatalf("attendu 1 jeton d'activation, obtenu %d", tokenCount)
	}

	cotis, _ := (&membership.Store{DB: deps.DB}).ListForUser(ctx, user)
	if len(cotis) != 1 {
		t.Fatalf("adhésion: %+v", cotis)
	}
	parcs, _ := (&parcelle.Store{DB: deps.DB}).ListForUser(ctx, user)
	if len(parcs) != 1 {
		t.Fatalf("parcelle: %+v", parcs)
	}
}

// TestOnboard_NoPasswordField : le formulaire GET ne propose plus de champ mot de passe.
func TestOnboard_NoPasswordField(t *testing.T) {
	deps := onboardDeps(t)
	defer deps.DB.Close()
	r := adminOnboardReq(http.MethodGet, "/admin/onboard-proprietaire", nil)
	w := httptest.NewRecorder()
	handleAdminOnboardProprietaire(deps).ServeHTTP(w, r)
	body := w.Body.String()
	if strings.Contains(body, `name="password"`) || strings.Contains(body, "Mot de passe initial") {
		t.Fatalf("le formulaire ne doit plus proposer de mot de passe: %s", body)
	}
}

// TestOnboard_ActivationTokenReplayRefused : jeton consommé puis rejoué → refus.
func TestOnboard_ActivationTokenReplayRefused(t *testing.T) {
	deps := onboardDeps(t)
	defer deps.DB.Close()

	r := adminOnboardReq(http.MethodPost, "/admin/onboard-proprietaire", validOnboardForm())
	handleAdminOnboardProprietaire(deps).ServeHTTP(httptest.NewRecorder(), r)

	var token string
	deps.DB.QueryRow(`SELECT token FROM login_magic_tokens WHERE email = ?`, "jean.berger@example.com").Scan(&token)

	req1 := httptest.NewRequest(http.MethodGet, "/login/callback?token="+token, nil)
	w1 := httptest.NewRecorder()
	LoginMagicCallback(deps)(w1, req1)
	if w1.Code != http.StatusFound {
		t.Fatalf("premier callback: %d", w1.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/login/callback?token="+token, nil)
	w2 := httptest.NewRecorder()
	LoginMagicCallback(deps)(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("rejeu attendu 400, obtenu %d", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "déjà") {
		t.Fatalf("message rejeu absent: %s", w2.Body.String())
	}
}

// TestOnboard_ActivationTokenExpiredRefused : jeton expiré → refus.
func TestOnboard_ActivationTokenExpiredRefused(t *testing.T) {
	deps := onboardDeps(t)
	defer deps.DB.Close()

	expired := time.Now().UTC().Add(-time.Hour).Format("2006-01-02 15:04:05")
	token := strings.Repeat("fe", 32)
	deps.DB.Exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES('u-exp','exp@x.com','','Exp')`) //nolint:errcheck
	deps.DB.Exec(`INSERT INTO login_magic_tokens(token,email,user_id,return_url,expires_at) VALUES(?,?,?,?,?)`,
		token, "exp@x.com", "u-exp", "/", expired) //nolint:errcheck

	req := httptest.NewRequest(http.MethodGet, "/login/callback?token="+token, nil)
	w := httptest.NewRecorder()
	LoginMagicCallback(deps)(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expiré attendu 400, obtenu %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "expir") {
		t.Fatalf("message expiration absent: %s", w.Body.String())
	}
}

// TestOnboard_ReOnboardExistingEmail : pas de doublon, message honnête via cascade.
func TestOnboard_ReOnboardExistingEmail(t *testing.T) {
	deps := onboardDeps(t)
	defer deps.DB.Close()
	ctx := context.Background()

	form := validOnboardForm()
	handleAdminOnboardProprietaire(deps).ServeHTTP(httptest.NewRecorder(),
		adminOnboardReq(http.MethodPost, "/admin/onboard-proprietaire", form))

	form.Set("parcelle_numero", "0008")
	w := httptest.NewRecorder()
	handleAdminOnboardProprietaire(deps).ServeHTTP(w,
		adminOnboardReq(http.MethodPost, "/admin/onboard-proprietaire", form))

	var n int
	deps.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE email = ?`, "jean.berger@example.com").Scan(&n)
	if n != 1 {
		t.Fatalf("doublon utilisateur: count=%d", n)
	}

	user, _ := (&identity.Store{DB: deps.DB}).GetUserIDByEmail(ctx, "jean.berger@example.com")
	parcs, _ := (&parcelle.Store{DB: deps.DB}).ListForUser(ctx, user)
	if len(parcs) < 1 {
		t.Fatalf("parcelles après re-onboard: %d", len(parcs))
	}
}

// TestOnboard_ValidationFail_RienCree : période invalide → rien créé.
func TestOnboard_ValidationFail_RienCree(t *testing.T) {
	deps := onboardDeps(t)
	defer deps.DB.Close()
	ctx := context.Background()

	form := validOnboardForm()
	form.Set("period_start", "01/01/2026")
	form.Set("period_end", "31/12/2026")

	handleAdminOnboardProprietaire(deps).ServeHTTP(httptest.NewRecorder(),
		adminOnboardReq(http.MethodPost, "/admin/onboard-proprietaire", form))

	_, err := (&identity.Store{DB: deps.DB}).GetUserIDByEmail(ctx, "jean.berger@example.com")
	if err == nil {
		t.Fatal("validation amont : aucun membre ne devait être créé")
	}
}

// TestOnboard_GET_RendForm : le formulaire guidé rend 200 avec ses sections.
func TestOnboard_GET_RendForm(t *testing.T) {
	deps := onboardDeps(t)
	defer deps.DB.Close()
	r := adminOnboardReq(http.MethodGet, "/admin/onboard-proprietaire", nil)
	w := httptest.NewRecorder()
	handleAdminOnboardProprietaire(deps).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Intégrer un propriétaire") {
		t.Fatal("le formulaire doit porter son titre")
	}
}
