package oauth_test

import (
	"context"
	"testing"
	"time"

	"github.com/hazyhaar/assokit/internal/oauth"
)

// TestPurgeStaleClients : un client DCR périmé sans token actif est supprimé ;
// un client avec token vivant OU un owner_user_id est préservé.
func TestPurgeStaleClients(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	ctx := context.Background()
	now := time.Now()
	old := now.Add(-60 * 24 * time.Hour).UTC().Format(time.RFC3339) // > TTL (30j)

	// c_stale : DCR, vieux, aucun token → doit être purgé.
	if _, err := db.Exec(`INSERT INTO oauth_clients(client_id, owner_user_id, created_at) VALUES('c_stale','',?)`, old); err != nil {
		t.Fatal(err)
	}
	// c_active : DCR, vieux, MAIS token vivant → préservé.
	if _, err := db.Exec(`INSERT INTO oauth_clients(client_id, owner_user_id, created_at) VALUES('c_active','',?)`, old); err != nil {
		t.Fatal(err)
	}
	future := now.Add(time.Hour).UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO oauth_tokens(id, client_id, user_id, expires_at) VALUES('t1','c_active','u1',?)`, future); err != nil {
		t.Fatal(err)
	}
	// c_owned : vieux mais owner humain → préservé.
	if _, err := db.Exec(`INSERT INTO oauth_clients(client_id, owner_user_id, created_at) VALUES('c_owned','u2',?)`, old); err != nil {
		t.Fatal(err)
	}

	n, err := oauth.PurgeStaleClients(ctx, db, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purge a supprimé %d clients, attendu 1", n)
	}
	for _, id := range []string{"c_active", "c_owned"} {
		var x string
		if err := db.QueryRow(`SELECT client_id FROM oauth_clients WHERE client_id=?`, id).Scan(&x); err != nil {
			t.Fatalf("%s a été purgé à tort: %v", id, err)
		}
	}
	var x string
	if err := db.QueryRow(`SELECT client_id FROM oauth_clients WHERE client_id='c_stale'`).Scan(&x); err == nil {
		t.Fatal("c_stale aurait dû être purgé")
	}
}
