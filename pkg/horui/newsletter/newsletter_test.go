package newsletter_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/horui/newsletter"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func mkUser(t *testing.T, db *sql.DB, id, email string, active int) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO users(id, email, password_hash, display_name, is_active) VALUES(?,?,?,?,?)`,
		id, email, "x", email, active,
	)
	if err != nil {
		t.Fatalf("mkUser %s: %v", id, err)
	}
}

func TestCreateListAndGet(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &newsletter.Store{DB: db}

	id, err := s.Create(ctx, newsletter.Newsletter{
		Subject:   "Bulletin de mai",
		BodyMD:    "Bonjour **à tous**.",
		CreatedBy: "admin",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Subject != "Bulletin de mai" {
		t.Fatalf("subject = %q", got.Subject)
	}
	if got.BodyHTML == "" || got.BodyHTML == got.BodyMD {
		t.Fatalf("body_html non rendu: %q", got.BodyHTML)
	}
	if got.SentAt.Valid {
		t.Fatalf("nouvelle diffusion ne doit pas être marquée envoyée")
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("list = %+v, want 1 entrée id=%s", list, id)
	}
}

func TestCreateValidation(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &newsletter.Store{DB: db}

	if _, err := s.Create(ctx, newsletter.Newsletter{Subject: "", BodyMD: "x"}); !errors.Is(err, newsletter.ErrEmptySubject) {
		t.Fatalf("sujet vide: err=%v, want ErrEmptySubject", err)
	}
	if _, err := s.Create(ctx, newsletter.Newsletter{Subject: "s", BodyMD: "   "}); !errors.Is(err, newsletter.ErrEmptyBody) {
		t.Fatalf("corps vide: err=%v, want ErrEmptyBody", err)
	}
}

func TestActiveMemberEmails(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "alice", "alice@example.org", 1)
	mkUser(t, db, "bob", "bob@example.org", 1)
	mkUser(t, db, "ghost", "ghost@example.org", 0) // inactif
	s := &newsletter.Store{DB: db}

	emails, err := s.ActiveMemberEmails(ctx)
	if err != nil {
		t.Fatalf("emails: %v", err)
	}
	if len(emails) != 2 {
		t.Fatalf("emails = %v, want 2 (que les actifs)", emails)
	}
	for _, e := range emails {
		if e == "ghost@example.org" {
			t.Fatalf("inactif inclus: %v", emails)
		}
	}
}

func TestMarkSent(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &newsletter.Store{DB: db}

	id, err := s.Create(ctx, newsletter.Newsletter{Subject: "s", BodyMD: "b"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.MarkSent(ctx, id, 42); err != nil {
		t.Fatalf("marksent: %v", err)
	}
	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.SentAt.Valid {
		t.Fatalf("sent_at non posé après MarkSent")
	}
	if got.RecipientsCount != 42 {
		t.Fatalf("recipients_count = %d, want 42", got.RecipientsCount)
	}

	if err := s.MarkSent(ctx, "inconnu", 1); !errors.Is(err, newsletter.ErrNotFound) {
		t.Fatalf("marksent id inconnu: err=%v, want ErrNotFound", err)
	}
}

// TestActiveMembershipEmails : seuls les comptes actifs avec une adhésion 'active'
// sont retournés (audience « adhérents à jour »), distincts.
func TestActiveMembershipEmails(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	s := &newsletter.Store{DB: db}

	// 3 comptes actifs : a (adhésion active x2), b (adhésion expirée), c (aucune).
	for _, id := range []string{"a", "b", "c"} {
		if _, err := db.Exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES(?,?,?,?)`,
			id, id+"@example.org", "x", id); err != nil {
			t.Fatal(err)
		}
	}
	mk := func(id, uid, status string) {
		if _, err := db.Exec(`INSERT INTO memberships(id,user_id,period_start,period_end,status) VALUES(?,?,?,?,?)`,
			id, uid, "2026-01-01", "2026-12-31", status); err != nil {
			t.Fatal(err)
		}
	}
	mk("m1", "a", "active")
	mk("m2", "a", "active") // doublon → 'a' compté une fois
	mk("m3", "b", "expired")

	emails, err := s.ActiveMembershipEmails(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(emails) != 1 || emails[0] != "a@example.org" {
		t.Fatalf("ActiveMembershipEmails = %v, attendu [a@example.org]", emails)
	}

	// ActiveMemberEmails (audience 'all') retourne bien les 3 comptes actifs.
	all, _ := s.ActiveMemberEmails(context.Background())
	if len(all) != 3 {
		t.Fatalf("ActiveMemberEmails = %d, attendu 3", len(all))
	}
}
