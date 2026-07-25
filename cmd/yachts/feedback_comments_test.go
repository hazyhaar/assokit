package main

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestLogger(_ *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// feedbacksSchema est le schéma minimal de la table feedbacks (issu de
// internal/chassis/migrations/v2_feedbacks.sql) pour les tests unitaires.
// On ne dépend pas du runner de migration core pour ce test d'unité.
const feedbacksSchema = `
CREATE TABLE IF NOT EXISTS feedbacks (
  id         TEXT PRIMARY KEY,
  page_url   TEXT NOT NULL,
  page_title TEXT NOT NULL DEFAULT '',
  message    TEXT NOT NULL,
  ip_hash    TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT '',
  locale     TEXT NOT NULL DEFAULT '',
  status     TEXT NOT NULL DEFAULT 'pending',
  admin_note TEXT NOT NULL DEFAULT '',
  triaged_by TEXT,
  triaged_at TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  collected_at TEXT
) STRICT;
`

func newTestCoreDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite",
		filepath.Join(t.TempDir(), "core_test.db")+
			"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open core db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(feedbacksSchema); err != nil {
		t.Fatalf("create feedbacks table: %v", err)
	}
	return db
}

func TestFeedbackComments_Empty(t *testing.T) {
	db := newTestCoreDB(t)
	h := &feedbackCommentsHandler{db: db, appName: "yachts", logger: newTestLogger(t)}

	req := httptest.NewRequest(http.MethodGet, "/feedback/comments", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var comments []remoteComment
	if err := json.NewDecoder(rec.Body).Decode(&comments); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(comments) != 0 {
		t.Fatalf("expected 0 comments, got %d", len(comments))
	}
}

func TestFeedbackComments_TwoRows(t *testing.T) {
	db := newTestCoreDB(t)

	_, err := db.Exec(`
		INSERT INTO feedbacks(id, page_url, message, created_at)
		VALUES
		  ('fb1', 'https://example.com/page1', 'Premier commentaire', '2026-06-01 10:00:00'),
		  ('fb2', 'https://example.com/page2', 'Deuxième commentaire', '2026-06-02 12:00:00')
	`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	h := &feedbackCommentsHandler{db: db, appName: "yachts", logger: newTestLogger(t)}
	req := httptest.NewRequest(http.MethodGet, "/feedback/comments", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var comments []remoteComment
	if err := json.NewDecoder(rec.Body).Decode(&comments); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(comments))
	}

	// Vérifie le mapping vers le contrat RemoteComment.
	c := comments[0]
	if c.ID != "fb1" {
		t.Errorf("id[0] = %q, want fb1", c.ID)
	}
	if c.Text != "Premier commentaire" {
		t.Errorf("text[0] = %q", c.Text)
	}
	if c.PageURL != "https://example.com/page1" {
		t.Errorf("page_url[0] = %q", c.PageURL)
	}
	if c.ScreenshotPath != "" {
		t.Errorf("screenshot_path[0] must be empty, got %q", c.ScreenshotPath)
	}
	if c.AppName != "yachts" {
		t.Errorf("app_name[0] = %q, want yachts", c.AppName)
	}
	if c.CreatedAt == 0 {
		t.Error("created_at[0] must be non-zero epoch")
	}

	// Décodable par la struct horos55 feedback_pull.RemoteComment (même schéma).
	raw, _ := json.Marshal(comments)
	type fp struct {
		ID             string `json:"id"`
		Text           string `json:"text"`
		PageURL        string `json:"page_url"`
		ScreenshotPath string `json:"screenshot_path"`
		AppName        string `json:"app_name"`
		CreatedAt      int64  `json:"created_at"`
	}
	var fpComments []fp
	if err := json.Unmarshal(raw, &fpComments); err != nil {
		t.Fatalf("round-trip vers struct feedback_pull: %v", err)
	}
	if len(fpComments) != 2 {
		t.Fatalf("round-trip: got %d", len(fpComments))
	}
}
