package identity_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/hazyhaar/assokit/pkg/identity"
)

func TestCreateAccount_WithoutPassword(t *testing.T) {
	db := newTestDB(t)
	s := &identity.Store{DB: db}
	ctx := context.Background()

	u, err := s.CreateAccount(ctx, identity.CreateAccountOpts{
		Email: "pending@example.org", DisplayName: "En attente", Active: true,
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if u.ID == "" {
		t.Fatal("id vide")
	}

	var hash string
	if err := db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, u.ID).Scan(&hash); err != nil {
		t.Fatalf("select hash: %v", err)
	}
	if hash != "" {
		t.Errorf("password_hash = %q, attendu vide", hash)
	}

	_, err = s.Authenticate(ctx, "pending@example.org", "anything")
	if !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Errorf("Authenticate avec hash vide doit refuser: %v", err)
	}
}

func TestCreateAccountOrGet_ReOnboardNoDuplicate(t *testing.T) {
	db := newTestDB(t)
	s := &identity.Store{DB: db}
	ctx := context.Background()

	first, created, err := s.CreateAccountOrGet(ctx, identity.CreateAccountOpts{
		Email: "dup@example.org", DisplayName: "Premier", Active: true,
	})
	if err != nil || !created {
		t.Fatalf("première création: created=%v err=%v", created, err)
	}

	second, created2, err := s.CreateAccountOrGet(ctx, identity.CreateAccountOpts{
		Email: "dup@example.org", DisplayName: "Second", Active: true,
	})
	if err != nil {
		t.Fatalf("re-onboard: %v", err)
	}
	if created2 {
		t.Fatal("re-onboard ne doit pas recréer")
	}
	if second.ID != first.ID {
		t.Fatalf("ids divergents %s vs %s", first.ID, second.ID)
	}

	var n int
	db.QueryRow(`SELECT COUNT(*) FROM users WHERE email = ?`, "dup@example.org").Scan(&n)
	if n != 1 {
		t.Fatalf("doublon en base: count=%d", n)
	}
}

func TestIssueMagicToken_ActivationTTL(t *testing.T) {
	db := newTestDB(t)
	s := &identity.Store{DB: db}
	ctx := context.Background()

	u, err := s.CreateAccount(ctx, identity.CreateAccountOpts{
		Email: "act@example.org", DisplayName: "Act", Active: true,
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	tok, err := s.IssueMagicToken(ctx, u.Email, u.ID, "/", "", identity.ActivationTokenTTL)
	if err != nil {
		t.Fatalf("IssueMagicToken: %v", err)
	}
	if len(tok.Token) != 64 {
		t.Fatalf("token len = %d", len(tok.Token))
	}

	var expires string
	var userID sql.NullString
	if err := db.QueryRow(`SELECT expires_at, user_id FROM login_magic_tokens WHERE token = ?`, tok.Token).
		Scan(&expires, &userID); err != nil {
		t.Fatalf("lookup token: %v", err)
	}
	if !userID.Valid || userID.String != u.ID {
		t.Errorf("user_id = %v, want %s", userID, u.ID)
	}
	if expires == "" {
		t.Error("expires_at vide")
	}
}
