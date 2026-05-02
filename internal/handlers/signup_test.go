// CLAUDE:SUMMARY Tests gardiens signup RGPD : ip_hash stocké pas IP brute, grep statique slog (M-ASSOKIT-FIX-RGPD-SIGNUPS-IP-HASH-ALIGNMENT-WITH-FEEDBACK-DOCTRINE).
package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// TestSignup_StoresIPHashNotRawIP vérifie que createMember stocke ip_hash et jamais l'IP brute.
func TestSignup_StoresIPHashNotRawIP(t *testing.T) {
	db := newTestDB(t)
	seedRoles(t, db)

	remoteAddr := "192.0.2.42:12345"
	rawIP := "192.0.2.42"
	secret := []byte("test-cookie-secret")

	_, err := createMember(context.Background(), db, "rgpd@test.com", "Test RGPD", "adherent", "{}", remoteAddr, secret)
	if err != nil {
		t.Fatalf("createMember: %v", err)
	}

	// Vérifier que la colonne ip_hash est remplie
	var ipHash string
	if err := db.QueryRow(`SELECT ip_hash FROM signups WHERE email='rgpd@test.com'`).Scan(&ipHash); err != nil {
		t.Fatalf("SELECT ip_hash: %v", err)
	}
	if ipHash == "" {
		t.Error("ip_hash vide : attendu hash non vide")
	}

	// Vérifier que le hash correspond bien à SHA256(IP || secret)
	h := sha256.New()
	h.Write([]byte(rawIP))
	h.Write(secret)
	expected := hex.EncodeToString(h.Sum(nil))
	if ipHash != expected {
		t.Errorf("ip_hash incorrect : attendu %q, got %q", expected, ipHash)
	}

	// Vérifier que l'IP brute n'est pas stockée nulle part dans la DB signups
	var allValues string
	db.QueryRow( //nolint:errcheck
		`SELECT COALESCE(ip_hash,'') || COALESCE(email,'') || COALESCE(display_name,'') || COALESCE(fields_json,'') FROM signups WHERE email='rgpd@test.com'`,
	).Scan(&allValues) //nolint:errcheck
	if strings.Contains(allValues, rawIP) {
		t.Errorf("IP brute %q trouvée dans les données signups DB : violation RGPD", rawIP)
	}

	// Vérifier absence de colonne 'ip' dans la table
	rows, err := db.Query(`PRAGMA table_info(signups)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, colType, notNull, dflt string
		var pk int
		rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk) //nolint:errcheck
		if name == "ip" {
			t.Error("colonne 'ip' (brute) encore présente dans la table signups : migration manquante")
		}
	}
}

// TestSignup_IPNeverLoggedRaw vérifie statiquement que signup.go ne logue pas d'IP brute.
func TestSignup_IPNeverLoggedRaw(t *testing.T) {
	src, err := os.ReadFile("signup.go")
	if err != nil {
		t.Fatalf("lecture signup.go: %v", err)
	}
	content := string(src)

	// Interdit : slog avec mention d'ip non hashée
	forbidden := []string{
		"slog.Info.*[Ii]p",
		"RemoteAddr",
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Ignorer les commentaires
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		for _, pattern := range forbidden {
			// Vérification simple par sous-chaîne (pas regex, pour lisibilité)
			keyword := strings.Split(pattern, ".*")[0]
			// Pour RemoteAddr : autoriser net.SplitHostPort(remoteAddr) mais pas slog/logger avec RemoteAddr
			if keyword == "RemoteAddr" {
				if strings.Contains(line, "RemoteAddr") && (strings.Contains(line, "slog.") || strings.Contains(line, "logger.") || strings.Contains(line, "log.")) {
					t.Errorf("signup.go ligne %d : IP brute loguée via slog/logger (%q)", i+1, strings.TrimSpace(line))
				}
			}
		}
	}
}
