package handlers

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/pkg/identity"
)

// TestPasswordResetFlow_EndToEnd remplace l'ancien test CDP rouge (t.Skip
// "flow non implémenté", périmé) par un vrai test httptest du cycle complet :
// forgot -> token en DB -> reset -> mot de passe changé -> ancien refusé,
// jeton non réutilisable. Aucun chromium requis.
func TestPasswordResetFlow_EndToEnd(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	// auth.Register assigne le rôle member (FK roles).
	if _, err := db.Exec(`INSERT OR IGNORE INTO roles(id,label) VALUES('member','member')`); err != nil {
		t.Fatal(err)
	}
	store := &identity.Store{DB: db}
	const email = "membre@example.org"
	if _, err := store.Register(ctx, email, "ancien-mdp-123", "Membre"); err != nil {
		t.Fatalf("register: %v", err)
	}

	deps := app.AppDeps{
		DB:     db,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config: config.Config{CookieSecret: []byte("0123456789abcdef0123456789abcdef")},
	}

	// 1. POST /forgot — crée un jeton (Mailer nil : le jeton est créé quand même).
	post(t, handleForgotSubmit(deps), "/forgot", "email="+email)

	var token string
	if err := db.QueryRow(
		`SELECT token FROM password_reset_tokens WHERE user_id=(SELECT id FROM users WHERE email=?)`,
		email).Scan(&token); err != nil {
		t.Fatalf("jeton non créé en DB: %v", err)
	}

	// 2. POST /reset — change le mot de passe.
	const newPwd = "nouveau-mdp-secure-2026"
	post(t, handleResetSubmit(deps), "/reset",
		"token="+token+"&password="+newPwd+"&password_confirm="+newPwd)

	// 3. Le nouveau mot de passe fonctionne, l'ancien non.
	if _, err := store.Authenticate(ctx, email, newPwd); err != nil {
		t.Fatalf("nouveau mot de passe refusé: %v", err)
	}
	if _, err := store.Authenticate(ctx, email, "ancien-mdp-123"); err == nil {
		t.Fatal("ancien mot de passe encore accepté")
	}

	// 4. Le jeton est marqué utilisé → non réutilisable.
	var usedAt sql.NullString
	db.QueryRow(`SELECT used_at FROM password_reset_tokens WHERE token=?`, token).Scan(&usedAt) //nolint:errcheck
	if !usedAt.Valid {
		t.Fatal("jeton non marqué used_at après reset")
	}
}

func post(t *testing.T, h http.HandlerFunc, path, body string) {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "192.0.2.10:1234"
	h(httptest.NewRecorder(), req)
}
