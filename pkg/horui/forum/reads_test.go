// CLAUDE:SUMMARY Tests état non-lu forum : MarkRead idempotent + UnreadSet/UnreadByCategory
// bascule (horodatages explicites, anti-flaky).
package forum

import (
	"context"
	"database/sql"
	"testing"
)

// openReadsTestDB = nodes (via openForumTestDB) + node_reads.
func openReadsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openForumTestDB(t)
	if _, err := db.Exec(`CREATE TABLE node_reads (
		user_id TEXT NOT NULL, node_id TEXT NOT NULL,
		last_read_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (user_id, node_id)
	);`); err != nil {
		t.Fatalf("node_reads schema: %v", err)
	}
	return db
}

func insNode(t *testing.T, db *sql.DB, id, parent, slug, createdAt string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO nodes(id,parent_id,slug,type,title,created_at) VALUES(?,?,?,'post',?,?)`,
		id, parent, slug, slug, createdAt); err != nil {
		t.Fatalf("insert node %s: %v", id, err)
	}
}

func TestMarkRead_Idempotent(t *testing.T) {
	db := openReadsTestDB(t)
	ctx := context.Background()
	insNode(t, db, "q1", "cat1", "q1", "2020-01-01 00:00:00")

	// Pré-poser une lecture ancienne pour prouver l'avancement.
	if _, err := db.Exec(`INSERT INTO node_reads(user_id,node_id,last_read_at) VALUES('u1','q1','2000-01-01 00:00:00')`); err != nil {
		t.Fatalf("seed read: %v", err)
	}
	if err := MarkRead(ctx, db, "u1", "q1"); err != nil {
		t.Fatalf("MarkRead 1: %v", err)
	}
	if err := MarkRead(ctx, db, "u1", "q1"); err != nil {
		t.Fatalf("MarkRead 2: %v", err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM node_reads WHERE user_id='u1' AND node_id='q1'`).Scan(&n)
	if n != 1 {
		t.Fatalf("idempotence: attendu 1 ligne, got %d", n)
	}
	var last string
	db.QueryRow(`SELECT last_read_at FROM node_reads WHERE user_id='u1' AND node_id='q1'`).Scan(&last)
	if last <= "2000-01-01 00:00:00" {
		t.Fatalf("last_read_at non avancé: %q", last)
	}
}

func TestUnreadSet_Toggle(t *testing.T) {
	db := openReadsTestDB(t)
	ctx := context.Background()
	insNode(t, db, "q1", "cat1", "q1", "2020-01-01 00:00:00")
	insNode(t, db, "m1", "q1", "m1", "2020-01-01 00:00:00") // message ancien

	// Jamais lu → non-lu.
	got, err := UnreadSet(ctx, db, "u1", []string{"q1"})
	if err != nil {
		t.Fatalf("UnreadSet 1: %v", err)
	}
	if !got["q1"] {
		t.Fatal("jamais lu doit être non-lu")
	}

	// Lecture à maintenant → message ancien (2020) < last_read → lu.
	if err := MarkRead(ctx, db, "u1", "q1"); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	got, _ = UnreadSet(ctx, db, "u1", []string{"q1"})
	if got["q1"] {
		t.Fatal("après lecture, sans nouveau message, doit être lu")
	}

	// Nouveau message daté dans le futur → redevient non-lu.
	insNode(t, db, "m2", "q1", "m2", "2030-01-01 00:00:00")
	got, _ = UnreadSet(ctx, db, "u1", []string{"q1"})
	if !got["q1"] {
		t.Fatal("nouveau message postérieur à la lecture doit rendre non-lu")
	}
}

func TestUnreadByCategory_CountsQuestions(t *testing.T) {
	db := openReadsTestDB(t)
	ctx := context.Background()
	insNode(t, db, "cat1", "forum", "cat1", "2020-01-01 00:00:00")
	insNode(t, db, "q1", "cat1", "q1", "2020-01-01 00:00:00")
	insNode(t, db, "q2", "cat1", "q2", "2020-01-01 00:00:00")
	insNode(t, db, "b1", "q1", "b1", "2020-01-01 00:00:00") // branche de q1
	insNode(t, db, "m1", "b1", "m1", "2020-01-01 00:00:00") // message sous la branche → q1 non-lue
	insNode(t, db, "b2", "q2", "b2", "2020-01-01 00:00:00") // q2 a une branche mais 0 message → pas de nouveau

	got, err := UnreadByCategory(ctx, db, "u1", []string{"cat1"})
	if err != nil {
		t.Fatalf("UnreadByCategory: %v", err)
	}
	if got["cat1"] != 1 {
		t.Fatalf("cat1 doit compter 1 question non-lue (q1), got %d", got["cat1"])
	}

	// Anonyme → map vide.
	anon, _ := UnreadByCategory(ctx, db, "", []string{"cat1"})
	if len(anon) != 0 {
		t.Fatalf("anonyme doit rendre map vide, got %v", anon)
	}
}

// TestUnreadQuestions_Toggle : une question est non-lue si un message sous une de ses
// branches est plus récent que la lecture (node_reads) de cette branche.
func TestUnreadQuestions_Toggle(t *testing.T) {
	db := openReadsTestDB(t)
	ctx := context.Background()
	insNode(t, db, "q1", "cat1", "q1", "2020-01-01 00:00:00")
	insNode(t, db, "b1", "q1", "b1", "2020-01-01 00:00:00")
	insNode(t, db, "m1", "b1", "m1", "2020-01-01 00:00:00") // message ancien sous la branche

	got, err := UnreadQuestions(ctx, db, "u1", []string{"q1"})
	if err != nil {
		t.Fatalf("UnreadQuestions: %v", err)
	}
	if !got["q1"] {
		t.Fatal("jamais lu doit être non-lu")
	}
	// Lire la BRANCHE à maintenant → message ancien (2020) < lecture → lu.
	if err := MarkRead(ctx, db, "u1", "b1"); err != nil {
		t.Fatalf("MarkRead branche: %v", err)
	}
	got, _ = UnreadQuestions(ctx, db, "u1", []string{"q1"})
	if got["q1"] {
		t.Fatal("après lecture de la branche, sans nouveau message, doit être lu")
	}
	// Nouveau message futur sous la branche → redevient non-lu.
	insNode(t, db, "m2", "b1", "m2", "2030-01-01 00:00:00")
	got, _ = UnreadQuestions(ctx, db, "u1", []string{"q1"})
	if !got["q1"] {
		t.Fatal("nouveau message postérieur à la lecture de la branche doit rendre non-lu")
	}
}
