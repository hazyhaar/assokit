package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/listing"
	"github.com/hazyhaar/assokit/pkg/listingactions"
)

// C5 : l'instance yachts câble le provider marketplace via RegisterActions —
// les 5 actions sont enregistrées dans le registry monté (HTTP admin + MCP).
func TestC5_YachtsWiresMarketplaceActions(t *testing.T) {
	silo, err := listing.LoadSilo("../../config/silos/yachts.toml")
	if err != nil {
		t.Fatalf("LoadSilo: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "yachts_wiring.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store, err := listing.NewStore(context.Background(), db, silo)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Même hook que celui passé à api.Options.RegisterActions dans run().
	reg := actions.NewRegistry()
	if err := listingactions.Register(store)(reg); err != nil {
		t.Fatalf("RegisterActions: %v", err)
	}
	for _, id := range []string{
		"listing.create", "listing.update", "listing.set_status",
		"listing.open_inquiry", "listing.search",
	} {
		if _, ok := reg.Get(id); !ok {
			t.Errorf("action %q non câblée par l'instance yachts", id)
		}
	}
}
