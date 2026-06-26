package handlers

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/pkg/identity"
)

// TestLoginGuard_LocksAfterThreshold : après maxFails échecs, l'identité est
// verrouillée ; le reset (login réussi) la déverrouille.
func TestLoginGuard_LocksAfterThreshold(t *testing.T) {
	g := newLoginGuard()
	const email = "Cible@Example.org"

	// Avant tout échec : non verrouillé.
	if locked, _ := g.Locked(email); locked {
		t.Fatal("verrouillé sans échec")
	}

	// maxFails-1 échecs : pas encore verrouillé.
	for i := 0; i < g.maxFails-1; i++ {
		g.RecordFailure(email)
	}
	if locked, _ := g.Locked(email); locked {
		t.Fatalf("verrouillé après %d échecs (seuil %d)", g.maxFails-1, g.maxFails)
	}

	// Le maxFails-ième échec verrouille.
	g.RecordFailure(email)
	locked, retry := g.Locked(email)
	if !locked {
		t.Fatalf("non verrouillé après %d échecs", g.maxFails)
	}
	if retry <= 0 {
		t.Fatalf("retry attendu > 0, got %v", retry)
	}

	// La clé est insensible à la casse / aux espaces.
	if locked, _ := g.Locked("  cible@example.org  "); !locked {
		t.Fatal("la clé doit être normalisée (casse/espaces)")
	}

	// Reset (connexion réussie) déverrouille.
	g.Reset(email)
	if locked, _ := g.Locked(email); locked {
		t.Fatal("toujours verrouillé après reset")
	}
}

// TestLoginGuard_WindowResetsCount : des échecs hors fenêtre ne s'accumulent pas.
func TestLoginGuard_WindowResetsCount(t *testing.T) {
	g := newLoginGuard()
	const email = "u@example.org"
	// Force une première frappe ancienne (hors fenêtre).
	g.RecordFailure(email)
	g.mu.Lock()
	g.fails[loginKey(email)].firstAt = g.fails[loginKey(email)].firstAt.Add(-2 * g.window)
	g.mu.Unlock()

	// Les échecs suivants repartent d'une nouvelle fenêtre → pas de verrou immédiat.
	for i := 0; i < g.maxFails-1; i++ {
		g.RecordFailure(email)
	}
	if locked, _ := g.Locked(email); locked {
		t.Fatal("verrouillé alors que les échecs sont répartis sur des fenêtres distinctes")
	}
}

// TestLoginSubmit_LockoutIntegration : via le handler HTTP, après 5 mauvais mots
// de passe l'identité est verrouillée — même le BON mot de passe est alors refusé
// (pas de session posée). Prouve le câblage anti-bruteforce dans handleLoginSubmit.
func TestLoginSubmit_LockoutIntegration(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO roles(id,label) VALUES('member','member')`); err != nil {
		t.Fatal(err)
	}
	store := &identity.Store{DB: db}
	const email, good = "victime@example.org", "bon-mot-de-passe-2026"
	if _, err := store.Register(context.Background(), email, good, "Victime"); err != nil {
		t.Fatal(err)
	}

	globalLoginGuard = newLoginGuard() // isole l'état partagé
	deps := app.AppDeps{
		DB:     db,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config: config.Config{CookieSecret: []byte("0123456789abcdef0123456789abcdef")},
	}
	h := handleLoginSubmit(deps)

	// 5 tentatives avec un mauvais mot de passe.
	for i := 0; i < 5; i++ {
		post(t, h, "/login", "email="+email+"&password=faux")
	}

	// Désormais verrouillé : le BON mot de passe ne doit PAS poser de session.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/login", strings.NewReader("email="+email+"&password="+good))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == "assokit_session" && c.Value != "" {
			t.Fatal("session posée alors que le compte est verrouillé")
		}
	}
}
