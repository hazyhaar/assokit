package seeds_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/actions/seeds"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
)

func findMsgAction(t *testing.T, id string) actions.Action {
	t.Helper()
	reg := actions.NewRegistry()
	seeds.InitAll(reg)
	for _, a := range reg.All() {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("action %q absente du registry", id)
	return actions.Action{}
}

// TestMessagesActions_RegisteredAndUsable : les actions de messagerie sont dans
// le registry (donc exposées en MCP — LLM-parité) et l'action send persiste un
// message lisible via thread, depuis l'utilisateur courant du contexte.
func TestMessagesActions_RegisteredAndUsable(t *testing.T) {
	for _, id := range []string{"messages.send", "messages.inbox", "messages.thread", "messages.mark_read"} {
		findMsgAction(t, id) // panique le test si absent
	}

	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatal(err)
	}
	for _, u := range []struct{ id, email string }{{"alice", "alice@example.org"}, {"bob", "bob@example.org"}} {
		if _, err := db.Exec(`INSERT INTO users(id,email,password_hash,display_name,is_active) VALUES(?,?,?,?,1)`,
			u.id, u.email, "x", u.email); err != nil {
			t.Fatal(err)
		}
	}
	deps := app.AppDeps{DB: db}
	ctx := middleware.ContextWithUser(context.Background(), &identity.User{ID: "alice"})

	send := findMsgAction(t, "messages.send")
	res, err := send.Run(ctx, deps, json.RawMessage(`{"recipient_id":"bob","body":"coucou bob"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("send: status=%s err=%v", res.Status, err)
	}

	thread := findMsgAction(t, "messages.thread")
	res, err = thread.Run(ctx, deps, json.RawMessage(`{"with_user_id":"bob"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("thread: status=%s err=%v", res.Status, err)
	}
	msgs, ok := res.Data.([]map[string]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("thread data inattendu: %#v", res.Data)
	}
	if msgs[0]["from_me"] != true {
		t.Fatalf("from_me attendu true pour alice")
	}
}
