package auth_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/schema"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := schema.Run(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	db.Exec(`INSERT INTO roles(id,label) VALUES('member','Member') ON CONFLICT DO NOTHING`)
	db.Exec(`INSERT INTO roles(id,label) VALUES('admin','Admin') ON CONFLICT DO NOTHING`)
	return db
}

func TestRegisterAuthenticateRoundtrip(t *testing.T) {
	db := newTestDB(t)
	s := &auth.Store{DB: db}
	ctx := context.Background()

	u, err := s.Register(ctx, "user@example.org", "secret123", "Test User")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if u.ID == "" || u.Email != "user@example.org" {
		t.Errorf("Register: mauvais user %+v", u)
	}
	if len(u.Roles) == 0 || u.Roles[0] != "member" {
		t.Errorf("Register: rôle member attendu, got %v", u.Roles)
	}

	u2, err := s.Authenticate(ctx, "user@example.org", "secret123")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if u2.ID != u.ID {
		t.Errorf("Authenticate: ID mismatch %s vs %s", u.ID, u2.ID)
	}
}

func TestAuthenticateWrongPassword(t *testing.T) {
	db := newTestDB(t)
	s := &auth.Store{DB: db}
	ctx := context.Background()

	s.Register(ctx, "test@nps.fr", "correct", "Test")
	_, err := s.Authenticate(ctx, "test@nps.fr", "wrong")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("want ErrInvalidCredentials, got %v", err)
	}
}

func TestAuthenticateUnknownEmail(t *testing.T) {
	db := newTestDB(t)
	s := &auth.Store{DB: db}
	ctx := context.Background()

	_, err := s.Authenticate(ctx, "unknown@nps.fr", "pass")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("want ErrInvalidCredentials, got %v", err)
	}
}

func TestRegisterEmailTaken(t *testing.T) {
	db := newTestDB(t)
	s := &auth.Store{DB: db}
	ctx := context.Background()

	s.Register(ctx, "dup@nps.fr", "pass", "First")
	_, err := s.Register(ctx, "dup@nps.fr", "pass2", "Second")
	if !errors.Is(err, auth.ErrEmailTaken) {
		t.Errorf("want ErrEmailTaken, got %v", err)
	}
}

func TestEmailNormalized(t *testing.T) {
	db := newTestDB(t)
	s := &auth.Store{DB: db}
	ctx := context.Background()

	s.Register(ctx, "  USER@EXAMPLE.ORG  ", "pass", "Test User")
	_, err := s.Authenticate(ctx, "user@example.org", "pass")
	if err != nil {
		t.Errorf("email normalisé: %v", err)
	}
}

func TestGetByID(t *testing.T) {
	db := newTestDB(t)
	s := &auth.Store{DB: db}
	ctx := context.Background()

	u, _ := s.Register(ctx, "get@nps.fr", "pass", "GetUser")
	u2, err := s.GetByID(ctx, u.ID)
	if err != nil || u2 == nil {
		t.Fatalf("GetByID: %v", err)
	}
	if u2.Email != "get@nps.fr" {
		t.Errorf("GetByID: email mismatch %s", u2.Email)
	}
}

func TestGetByIDNotFound(t *testing.T) {
	db := newTestDB(t)
	s := &auth.Store{DB: db}
	ctx := context.Background()

	u, _ := s.GetByID(ctx, "inexistant")
	if u != nil {
		t.Error("GetByID non-existent should return nil")
	}
}

func TestAddRemoveRole(t *testing.T) {
	db := newTestDB(t)
	s := &auth.Store{DB: db}
	ctx := context.Background()

	u, _ := s.Register(ctx, "role@nps.fr", "pass", "RoleUser")
	if err := s.AddRole(ctx, u.ID, "admin"); err != nil {
		t.Fatalf("AddRole: %v", err)
	}
	u2, _ := s.GetByID(ctx, u.ID)
	hasAdmin := false
	for _, r := range u2.Roles {
		if r == "admin" {
			hasAdmin = true
		}
	}
	if !hasAdmin {
		t.Error("user should have admin role")
	}

	s.RemoveRole(ctx, u.ID, "admin")
	u3, _ := s.GetByID(ctx, u.ID)
	for _, r := range u3.Roles {
		if r == "admin" {
			t.Error("admin role should be removed")
		}
	}
}

func TestMailerNilNocrash(t *testing.T) {
	db := newTestDB(t)
	s := &auth.Store{DB: db, Mailer: nil}
	ctx := context.Background()

	_, err := s.Register(ctx, "nomailer@nps.fr", "pass", "NoMailer")
	if err != nil {
		t.Fatalf("Register with nil Mailer: %v", err)
	}
}
