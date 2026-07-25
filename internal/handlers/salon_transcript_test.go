// CLAUDE:SUMMARY Tests T5 — handlers comptes-rendus : garde membre (403), cross-salon (404), download.
package handlers

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/salon"
	"github.com/hazyhaar/assokit/pkg/transcript"
)

// buildTranscriptRouter construit un router chi avec les routes transcript.
func buildTranscriptRouter(deps app.AppDeps) *chi.Mux {
	r := chi.NewRouter()
	MountTranscriptRoutes(r, deps)
	return r
}

// newTranscriptTestEnv prépare un DB en mémoire avec deux salons, un membre, un transcript.
// Retourne : db, deps, memberID, nonMemberID, slug du salon cible, ID du transcript,
// et slug + transcriptID d'un AUTRE salon (pour le test cross-salon).
func newTranscriptTestEnv(t *testing.T) (
	deps app.AppDeps,
	memberID, nonMemberID string,
	slug, transcriptID string,
	otherSlug, otherTranscriptID string,
) {
	t.Helper()
	rawDB := newTestDB(t)
	seedRoles(t, rawDB)

	deps = app.AppDeps{
		DB:     rawDB,
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	memberID = "user-member-" + t.Name()
	nonMemberID = "user-nonmember-" + t.Name()
	for _, uid := range []string{memberID, nonMemberID} {
		if _, err := rawDB.Exec(
			`INSERT INTO users(id, email, password_hash, display_name) VALUES(?,?,?,?)`,
			uid, uid+"@test.example", "x", "Test User",
		); err != nil {
			t.Fatalf("seed user %s: %v", uid, err)
		}
	}

	st := &salon.Store{DB: rawDB}

	// Salon principal dont memberID est membre.
	sl, err := st.Create(t.Context(), "Salon Principal", memberID, "", false)
	if err != nil {
		t.Fatalf("salon.Create: %v", err)
	}
	slug = sl.Slug

	// Transcript pour ce salon.
	ts := &transcript.Store{DB: rawDB}
	tr, err := ts.CreateTranscript(t.Context(), sl.ID)
	if err != nil {
		t.Fatalf("transcript.CreateTranscript: %v", err)
	}
	if err := ts.SetStatus(t.Context(), tr.ID, "done"); err != nil {
		t.Fatalf("transcript.SetStatus: %v", err)
	}
	_, _ = ts.AddSegment(t.Context(), tr.ID, "Alice", 0, 1500, "Bonjour tout le monde.")
	_, _ = ts.AddSegment(t.Context(), tr.ID, "Bob", 2000, 4000, "Bonjour Alice.")
	transcriptID = tr.ID

	// Autre salon avec son propre transcript (pour le test cross-salon).
	sl2, err := st.Create(t.Context(), "Salon Autre", memberID, "", false)
	if err != nil {
		t.Fatalf("salon2.Create: %v", err)
	}
	otherSlug = sl2.Slug
	tr2, err := ts.CreateTranscript(t.Context(), sl2.ID)
	if err != nil {
		t.Fatalf("transcript2.Create: %v", err)
	}
	otherTranscriptID = tr2.ID

	return deps, memberID, nonMemberID, slug, transcriptID, otherSlug, otherTranscriptID
}

func TestTranscript_List_Membre_200(t *testing.T) {
	deps, memberID, _, slug, _, _, _ := newTranscriptTestEnv(t)
	r := buildTranscriptRouter(deps)

	req := httptest.NewRequest(http.MethodGet, "/salon/"+slug+"/transcripts", nil)
	req = injectUser(req, &identity.User{ID: memberID})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("membre : attendu 200, got %d", w.Code)
	}
}

func TestTranscript_List_NonMembre_403(t *testing.T) {
	deps, _, nonMemberID, slug, _, _, _ := newTranscriptTestEnv(t)
	r := buildTranscriptRouter(deps)

	req := httptest.NewRequest(http.MethodGet, "/salon/"+slug+"/transcripts", nil)
	req = injectUser(req, &identity.User{ID: nonMemberID})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("non-membre : attendu 403, got %d", w.Code)
	}
}

func TestTranscript_Detail_CrossSalon_404(t *testing.T) {
	// Le memberID est membre du salon principal ET du salon autre (car il les a créés).
	// On tente de lire otherTranscriptID via le slug du salon principal → 404.
	deps, memberID, _, slug, _, _, otherTranscriptID := newTranscriptTestEnv(t)
	r := buildTranscriptRouter(deps)

	req := httptest.NewRequest(http.MethodGet, "/salon/"+slug+"/transcripts/"+otherTranscriptID, nil)
	req = injectUser(req, &identity.User{ID: memberID})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("cross-salon : attendu 404, got %d", w.Code)
	}
}

func TestTranscript_Detail_Membre_200(t *testing.T) {
	deps, memberID, _, slug, transcriptID, _, _ := newTranscriptTestEnv(t)
	r := buildTranscriptRouter(deps)

	req := httptest.NewRequest(http.MethodGet, "/salon/"+slug+"/transcripts/"+transcriptID, nil)
	req = injectUser(req, &identity.User{ID: memberID})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("détail membre : attendu 200, got %d", w.Code)
	}
	body, _ := io.ReadAll(w.Body)
	if !strings.Contains(string(body), "Alice") {
		t.Error("détail : segments locuteur 'Alice' attendus dans la réponse")
	}
}

func TestTranscript_Download_AttachmentHeader(t *testing.T) {
	deps, memberID, _, slug, transcriptID, _, _ := newTranscriptTestEnv(t)
	r := buildTranscriptRouter(deps)

	req := httptest.NewRequest(http.MethodGet, "/salon/"+slug+"/transcripts/"+transcriptID+"/download", nil)
	req = injectUser(req, &identity.User{ID: memberID})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("download : attendu 200, got %d", w.Code)
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") {
		t.Errorf("download : Content-Disposition attachment attendu, got %q", cd)
	}
	body := w.Body.String()
	if !strings.Contains(body, "[Alice]") || !strings.Contains(body, "Bonjour tout le monde.") {
		t.Errorf("download : segments attendus dans le corps, got %q", body)
	}
}

