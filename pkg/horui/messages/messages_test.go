package messages_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/messaging"
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

func TestSendAndThread(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "alice", "alice@example.org", 1)
	mkUser(t, db, "bob", "bob@example.org", 1)
	s := &messaging.Store{DB: db}

	if _, err := s.Send(ctx, "alice", "bob", "salut **bob**"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, err := s.Send(ctx, "bob", "alice", "salut alice"); err != nil {
		t.Fatalf("send back: %v", err)
	}

	thread, err := s.Thread(ctx, "alice", "bob", 0)
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	if len(thread) != 2 {
		t.Fatalf("thread len = %d, want 2", len(thread))
	}
	// Ordre chronologique : alice→bob d'abord.
	if thread[0].SenderID != "alice" || thread[1].SenderID != "bob" {
		t.Fatalf("ordre fil inattendu: %s, %s", thread[0].SenderID, thread[1].SenderID)
	}
	// body_html rendu depuis markdown.
	if thread[0].BodyHTML == "" || thread[0].BodyHTML == thread[0].BodyMD {
		t.Fatalf("body_html non rendu: %q", thread[0].BodyHTML)
	}
}

func TestInboxUnreadAndMarkRead(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "alice", "alice@example.org", 1)
	mkUser(t, db, "bob", "bob@example.org", 1)
	s := &messaging.Store{DB: db}

	_, _ = s.Send(ctx, "bob", "alice", "msg 1")
	_, _ = s.Send(ctx, "bob", "alice", "msg 2")

	inbox, err := s.Inbox(ctx, "alice")
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if len(inbox) != 1 {
		t.Fatalf("inbox len = %d, want 1", len(inbox))
	}
	if inbox[0].WithUserID != "bob" || inbox[0].Unread != 2 {
		t.Fatalf("inbox: with=%s unread=%d (want bob/2)", inbox[0].WithUserID, inbox[0].Unread)
	}

	n, err := s.MarkRead(ctx, "alice", "bob")
	if err != nil || n != 2 {
		t.Fatalf("markread n=%d err=%v (want 2)", n, err)
	}
	inbox, _ = s.Inbox(ctx, "alice")
	if inbox[0].Unread != 0 {
		t.Fatalf("unread après markread = %d, want 0", inbox[0].Unread)
	}
}

func TestSendValidation(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "alice", "alice@example.org", 1)
	mkUser(t, db, "ghost", "ghost@example.org", 0) // inactif
	s := &messaging.Store{DB: db}

	if _, err := s.Send(ctx, "alice", "alice", "moi-même"); !errors.Is(err, messaging.ErrSelf) {
		t.Fatalf("self: err=%v, want ErrSelf", err)
	}
	if _, err := s.Send(ctx, "alice", "bob", "   "); !errors.Is(err, messaging.ErrEmptyBody) {
		t.Fatalf("empty: err=%v, want ErrEmptyBody", err)
	}
	if _, err := s.Send(ctx, "alice", "nope", "coucou"); !errors.Is(err, messaging.ErrRecipientInvalid) {
		t.Fatalf("missing recipient: err=%v, want ErrRecipientInvalid", err)
	}
	if _, err := s.Send(ctx, "alice", "ghost", "coucou"); !errors.Is(err, messaging.ErrRecipientInvalid) {
		t.Fatalf("inactive recipient: err=%v, want ErrRecipientInvalid", err)
	}
}
