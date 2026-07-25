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
	"github.com/hazyhaar/assokit/pkg/connectors/social"
)

func findSocialAction(t *testing.T, id string) actions.Action {
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

// TestSocialReconcileAction_ResolvesOrphan : l'action social.reconcile est dans le
// registry (donc exposée en MCP — LLM-parité) et résout une diffusion orpheline
// 'publishing' en la tranchant 'sent', via Store.ResolvePublishing.
func TestSocialReconcileAction_ResolvesOrphan(t *testing.T) {
	findSocialAction(t, "social.reconcile") // panique le test si absente

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
	store := social.NewStore(db)
	postID, err := store.Enqueue(ctx, social.SocialPost{
		Content:        "Diffusion orpheline à réconcilier",
		Networks:       []string{"facebook"},
		IdempotencyKey: "reconcile-action",
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := store.ClaimPublish(ctx, postID, "facebook"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	deps := app.AppDeps{DB: db}
	act := findSocialAction(t, "social.reconcile")
	params, _ := json.Marshal(map[string]string{"post_id": postID, "network": "facebook", "outcome": "sent"})
	res, err := act.Run(ctx, deps, params)
	if err != nil || res.Status != "ok" {
		t.Fatalf("reconcile: status=%s err=%v", res.Status, err)
	}

	status, err := store.PublishStatus(ctx, postID, "facebook")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != "sent" {
		t.Fatalf("status = %q, attendu sent après réconciliation", status)
	}
}

// TestSocialReconcileAction_InvalidOutcome : un outcome hors {sent,failed} remonte une
// erreur explicite, sans muter le journal.
func TestSocialReconcileAction_InvalidOutcome(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatal(err)
	}

	deps := app.AppDeps{DB: db}
	act := findSocialAction(t, "social.reconcile")
	// outcome hors enum : rejeté par la validation de schéma OU par le Store. On invoque
	// Run directement (le schéma est appliqué par la couche d'exécution, pas par Run) :
	// la garde de ResolvePublishing doit renvoyer un statut error.
	params, _ := json.Marshal(map[string]string{"post_id": "p", "network": "facebook", "outcome": "bidon"})
	res, err := act.Run(context.Background(), deps, params)
	if res.Status != "error" || err == nil {
		t.Fatalf("attendu status=error + err non nil, got status=%s err=%v", res.Status, err)
	}
}
