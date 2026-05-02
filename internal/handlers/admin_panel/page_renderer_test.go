// CLAUDE:SUMMARY Tests page_renderer — fallback file→branding, XSS escape, charte/legal/a-propos rendus (M-ASSOKIT-ADMIN-PANEL-V0.1).
package adminpanel

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/pkg/horui/branding"
)

func openPageRendererDB(t *testing.T) *sql.DB {
	t.Helper()
	branding.InvalidateAll() // évite la pollution du cache singleton entre tests
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close(); branding.InvalidateAll() })
	if _, err := db.Exec(`
		CREATE TABLE branding_kv (
			key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '',
			value_type TEXT NOT NULL DEFAULT 'text',
			file_path TEXT NOT NULL DEFAULT '', file_mime TEXT NOT NULL DEFAULT '',
			file_size INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_by TEXT NOT NULL DEFAULT ''
		);
	`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

// TestPageRenderer_CharteFromBrandingKV : charte.body markdown rendu en HTML.
func TestPageRenderer_CharteFromBrandingKV(t *testing.T) {
	db := openPageRendererDB(t)
	db.Exec(`INSERT INTO branding_kv(key, value, value_type) VALUES('charte.body', '# Notre charte\n\nUn **texte** important.', 'longtext')`) //nolint:errcheck

	page, err := buildPageData(context.Background(), db, "charte", "")
	if err != nil {
		t.Fatalf("buildPageData: %v", err)
	}
	if !strings.Contains(page.BodyHTML, "<h1") {
		t.Errorf("titre H1 absent : %s", page.BodyHTML)
	}
	if !strings.Contains(page.BodyHTML, "<strong>") {
		t.Errorf("gras absent : %s", page.BodyHTML)
	}
	if page.Title != "Charte / valeurs" {
		t.Errorf("title = %q", page.Title)
	}
}

// TestPageRenderer_CharteXSSEscaped : <script> dans markdown → escapé.
func TestPageRenderer_CharteXSSEscaped(t *testing.T) {
	db := openPageRendererDB(t)
	body := "Texte normal puis <script>alert('XSS')</script> fin."
	db.Exec(`INSERT INTO branding_kv(key, value) VALUES('charte.body', ?)`, body) //nolint:errcheck

	page, err := buildPageData(context.Background(), db, "charte", "")
	if err != nil {
		t.Fatalf("buildPageData: %v", err)
	}
	if strings.Contains(page.BodyHTML, "<script>") {
		t.Errorf("script tag non escapé (XSS risk) : %s", page.BodyHTML)
	}
}

// TestPageRenderer_FallbackFromBrandingToFile : pas de KV → lit fichier .md.
func TestPageRenderer_FallbackFromBrandingToFile(t *testing.T) {
	db := openPageRendererDB(t)
	tmpDir := t.TempDir()
	pagesDir := filepath.Join(tmpDir, "pages")
	if err := os.MkdirAll(pagesDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pagesDir, "charte.md"), []byte("# Charte fichier"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	page, err := buildPageData(context.Background(), db, "charte", tmpDir)
	if err != nil {
		t.Fatalf("buildPageData: %v", err)
	}
	if !strings.Contains(page.BodyHTML, "Charte fichier") {
		t.Errorf("fallback fichier non lu : %s", page.BodyHTML)
	}
}

// TestPageRenderer_NotFoundIfNeitherKVNorFile : ni KV ni fichier → errPageNotFound.
func TestPageRenderer_NotFoundIfNeitherKVNorFile(t *testing.T) {
	db := openPageRendererDB(t)
	_, err := buildPageData(context.Background(), db, "charte", t.TempDir())
	if err == nil {
		t.Error("attendu erreur, got nil")
	}
}

// TestPageRenderer_MentionsLegalesFromKV : legal.* keys → HTML structuré.
func TestPageRenderer_MentionsLegalesFromKV(t *testing.T) {
	db := openPageRendererDB(t)
	db.Exec(`INSERT INTO branding_kv(key, value) VALUES('legal.mentions_editeur', 'Nonpossumus, 1 rue X, Paris')`)            //nolint:errcheck
	db.Exec(`INSERT INTO branding_kv(key, value) VALUES('legal.mentions_hebergeur', 'OVHcloud Roubaix')`)                     //nolint:errcheck
	db.Exec(`INSERT INTO branding_kv(key, value) VALUES('legal.contact_dpo', 'dpo@nonpossumus.eu')`)                          //nolint:errcheck
	db.Exec(`INSERT INTO branding_kv(key, value) VALUES('legal.statuts_pdf', '/uploads/abc-statuts.pdf')`)                    //nolint:errcheck

	page, err := buildPageData(context.Background(), db, "mentions-legales", "")
	if err != nil {
		t.Fatalf("buildPageData: %v", err)
	}
	for _, want := range []string{"Éditeur", "Nonpossumus", "Hébergeur", "OVHcloud", "RGPD", "dpo@nonpossumus.eu", "Télécharger les statuts"} {
		if !strings.Contains(page.BodyHTML, want) {
			t.Errorf("mention %q absente du HTML : %s", want, page.BodyHTML)
		}
	}
}

// TestPageRenderer_MentionsLegalesEditeurEscaped : <img> dans editeur → escapé.
func TestPageRenderer_MentionsLegalesEditeurEscaped(t *testing.T) {
	db := openPageRendererDB(t)
	db.Exec(`INSERT INTO branding_kv(key, value) VALUES('legal.mentions_editeur', 'Asso <img src=x onerror=alert(1)>')`) //nolint:errcheck
	db.Exec(`INSERT INTO branding_kv(key, value) VALUES('legal.mentions_hebergeur', 'OVHcloud')`)                       //nolint:errcheck

	page, err := buildPageData(context.Background(), db, "mentions-legales", "")
	if err != nil {
		t.Fatalf("buildPageData: %v", err)
	}
	if strings.Contains(page.BodyHTML, "<img src=x") {
		t.Errorf("img tag non escapé : %s", page.BodyHTML)
	}
	if !strings.Contains(page.BodyHTML, "&lt;img") {
		t.Errorf("img attendu en entité escapée : %s", page.BodyHTML)
	}
}

// TestPageRenderer_AProposFromKV : quisommesnous.* keys → histoire + missions + bureau.
func TestPageRenderer_AProposFromKV(t *testing.T) {
	db := openPageRendererDB(t)
	for k, v := range map[string]string{
		"quisommesnous.histoire":          "Notre asso a été fondée en 2025.",
		"quisommesnous.mission_1":         "Soutenir les lanceurs d'alerte",
		"quisommesnous.mission_2":         "Mettre en réseau journalistes",
		"quisommesnous.president_bio":     "Boris LUTZ",
		"quisommesnous.bureau_tresorier":  "Alice",
		"quisommesnous.bureau_secretaire": "Bob",
	} {
		db.Exec(`INSERT INTO branding_kv(key, value) VALUES(?, ?)`, k, v) //nolint:errcheck
	}
	page, err := buildPageData(context.Background(), db, "a-propos", "")
	if err != nil {
		t.Fatalf("buildPageData: %v", err)
	}
	for _, want := range []string{"histoire", "Notre asso", "missions", "Soutenir les lanceurs", "Boris LUTZ", "Trésorier", "Alice", "Secrétaire"} {
		if !strings.Contains(page.BodyHTML, want) {
			t.Errorf("contenu %q absent : %s", want, page.BodyHTML[:min(len(page.BodyHTML), 800)])
		}
	}
}

// TestPageRenderer_UnknownSlugReturnsNotFound : slug invalide → errPageNotFound.
func TestPageRenderer_UnknownSlugReturnsNotFound(t *testing.T) {
	db := openPageRendererDB(t)
	_, err := buildPageData(context.Background(), db, "page-inconnue", "")
	if err == nil {
		t.Error("attendu errPageNotFound")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
