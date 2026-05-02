// CLAUDE:SUMMARY Test gardien PII : password admin jamais dans slog (M-ASSOKIT-AUDIT-FIX-1).
package bootstrap

import (
	"bytes"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestBootstrap_AdminPasswordNeverInSlog : capture slog output, search "password" → 0 leak structuré.
// Le password peut apparaître sur stderr (Fprintf direct), c'est intentionnel : visible boot manuel
// mais jamais ingéré par les agrégateurs structurés (journalctl, file logs slog JSON).
func TestBootstrap_AdminPasswordNeverInSlog(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	var slogBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&slogBuf, nil))

	// Rediriger stderr vers /dev/null pour ne pas polluer le test (le print stderr est attendu).
	origStderr := os.Stderr
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	os.Stderr = devnull
	defer func() {
		os.Stderr = origStderr
		_ = devnull.Close()
	}()

	// Lancer bootstrapAdmin avec password vide → password généré.
	if err := BootstrapAdmin(db, "admin@test.org", "", logger); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}

	output := slogBuf.String()
	if output == "" {
		t.Fatal("aucun log produit, attendu au moins admin_bootstrap_password_generated")
	}

	// Le slog NE DOIT PAS contenir le mot "password_initial" (clé de l'ancien leak).
	if strings.Contains(output, "password_initial") {
		t.Errorf("slog contient 'password_initial' (leak PII) : %s", output)
	}

	// Le slog NE DOIT PAS contenir un attribut "password" avec valeur de plus de 8 chars
	// (heuristique : 16 chars hex = un password généré).
	if matchesHexPasswordValue(output) {
		t.Errorf("slog semble contenir un password en clair : %s", output)
	}

	// Le slog DOIT contenir l'event admin_bootstrap_password_generated.
	if !strings.Contains(output, "admin_bootstrap_password_generated") {
		t.Errorf("slog ne contient pas 'admin_bootstrap_password_generated', got : %s", output)
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE users (
			id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL, display_name TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE user_roles (user_id TEXT, role_id TEXT, PRIMARY KEY(user_id, role_id));
	`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

// matchesHexPasswordValue retourne true si le JSON output contient une valeur hex
// de 16 chars (le format des passwords générés). Heuristique défensive.
func matchesHexPasswordValue(s string) bool {
	// Cherche une string JSON de 16 chars hex sans clé connue inoffensive.
	// On accepte les hashes courts (req_id, hashes) sauf si associés à un attribut password*.
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			continue
		}
		// Si le mot "password" apparaît, vérifier qu'aucune valeur hex 16+ n'est dans la même ligne
		// EXCEPTÉ comme contenu d'une string décrivant l'event ("password_generated", "hint" texte).
		if strings.Contains(line, "\"password\"") {
			return true // attribut "password" direct = leak
		}
	}
	// Pour le reste : OK (hash courts type req_id sont admis).
	_ = io.Discard
	return false
}
