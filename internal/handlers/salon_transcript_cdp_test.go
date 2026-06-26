//go:build integration_cdp

// CLAUDE:SUMMARY Banc CDP E2E vue comptes-rendus : liste + détail segments + download + garde non-membre (403).
//
// Stratégie : on monte un serveur de test chi complet (MountSalonRoutes) avec un
// middleware X-Test-User-ID pour simuler l'authentification (identique au pattern
// buildFullSalonRouter), on seede données réelles via les stores (salon, membre,
// transcript + 3 segments de 2 locuteurs), puis on pilote chromedp pour :
//
//  1. Liste des comptes-rendus (/salon/{slug}/transcripts) — vérifie la présence
//     du compte-rendu (date ou statut visible).
//  2. Détail (/salon/{slug}/transcripts/{id}) — vérifie que les 3 segments sont
//     rendus avec leurs locuteurs.
//  3. Download (/salon/{slug}/transcripts/{id}/download) — requête HTTP directe,
//     vérifie Content-Disposition attachment + corps texte contenant les segments.
//  4. Garde non-membre : user sans adhésion → /salon/{slug}/transcripts → 403.
//
// Lancement :
//
//	go test -tags=integration_cdp -v -timeout=120s ./internal/handlers/ -run TestTranscriptCDP
package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/salon"
	"github.com/hazyhaar/assokit/pkg/transcript"
	"log/slog"
	"os"
)

// buildTranscriptCDPEnv prépare l'environnement complet pour le banc CDP transcript :
//   - DB en mémoire (via newSalonVisioTestEnv), un owner + un nonMember déjà créés.
//   - Un second user "member" ajouté explicitement et inscrit au salon via AddMember.
//   - Un transcript avec 3 segments de 2 locuteurs, statut "done".
//   - Un router chi avec MountSalonRoutes et middleware X-Test-User-ID.
//
// Retourne le serveur de test, le slug du salon, l'ID du transcript, l'ID du membre,
// et l'ID du non-membre.
func buildTranscriptCDPEnv(t *testing.T) (srv *httptest.Server, slug, transcriptID, memberID, nonMemberID string) {
	t.Helper()

	rawDB, _, ownerID, nonMemberID2, slug2 := newSalonVisioTestEnv(t)
	t.Cleanup(func() { rawDB.Close() })

	// Ajouter un second user "membre" distinct du owner et du nonMember.
	memberID2 := "user-cdp-member-" + t.Name()
	if _, err := rawDB.Exec(
		`INSERT INTO users(id, email, password_hash, display_name) VALUES(?,?,?,?)`,
		memberID2, memberID2+"@test.example", "x", "Membre CDP",
	); err != nil {
		t.Fatalf("seed cdp member: %v", err)
	}

	st := &salon.Store{DB: rawDB}

	// Retrouver l'ID du salon à partir du slug.
	sl, err := st.Get(t.Context(), slug2)
	if err != nil {
		t.Fatalf("salon.Get: %v", err)
	}

	// Inscrire memberID2 directement en base (salon sans mot de passe, INSERT direct
	// identique au pattern buildFullSalonRouter de salon_e2e_cdp_test.go).
	if _, err := rawDB.Exec(
		`INSERT INTO salon_member(salon_id, user_id, role, joined_at) VALUES(?,?,'member',datetime('now'))`,
		sl.ID, memberID2,
	); err != nil {
		t.Fatalf("seed salon_member: %v", err)
	}

	// Seeder un transcript avec 3 segments réels.
	ts := &transcript.Store{DB: rawDB}
	tr, err := ts.CreateTranscript(t.Context(), sl.ID)
	if err != nil {
		t.Fatalf("transcript.CreateTranscript: %v", err)
	}
	if _, err := ts.AddSegment(t.Context(), tr.ID, "Alice", 0, 2000, "Bonjour à toutes et à tous."); err != nil {
		t.Fatalf("AddSegment 1: %v", err)
	}
	if _, err := ts.AddSegment(t.Context(), tr.ID, "Bob", 2500, 5000, "Merci de nous rejoindre ce soir."); err != nil {
		t.Fatalf("AddSegment 2: %v", err)
	}
	if _, err := ts.AddSegment(t.Context(), tr.ID, "Alice", 5500, 8000, "Passons au premier point de l'ordre du jour."); err != nil {
		t.Fatalf("AddSegment 3: %v", err)
	}
	if err := ts.SetStatus(t.Context(), tr.ID, "done"); err != nil {
		t.Fatalf("transcript.SetStatus done: %v", err)
	}

	// Construire le router chi avec MountSalonRoutes (inclut MountTranscriptRoutes).
	deps := app.AppDeps{
		DB:     rawDB,
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	r := chi.NewRouter()
	r.Use(middleware.Flash)
	// Middleware de test : injecte l'utilisateur depuis l'en-tête X-Test-User-ID.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if uid := req.Header.Get("X-Test-User-ID"); uid != "" {
				u := &identity.User{ID: uid, Email: uid + "@test.example", DisplayName: uid}
				req = req.WithContext(middleware.ContextWithUser(req.Context(), u))
			}
			next.ServeHTTP(w, req)
		})
	})
	MountSalonRoutes(r, deps, nil)

	srv2 := httptest.NewServer(r)
	t.Cleanup(srv2.Close)

	_ = ownerID // non utilisé directement dans les assertions (le member suffit)
	return srv2, slug2, tr.ID, memberID2, nonMemberID2
}

// TestTranscriptCDP_ListeDetailDownloadGarde est le banc CDP principal.
func TestTranscriptCDP_ListeDetailDownloadGarde(t *testing.T) {
	chromePath := findChromium(t)
	if chromePath == "" {
		t.Skip("aucun binaire chromium trouvé — test skippé. Définir CHROME_PATH ou installer chromium.")
	}

	srv, slug, transcriptID, memberID, nonMemberID := buildTranscriptCDPEnv(t)

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()
	ctx, cancelTO := context.WithTimeout(ctx, 90*time.Second)
	defer cancelTO()

	// ----------------------------------------------------------------
	// ÉTAPE 1 — Membre : liste des comptes-rendus (200, compte-rendu visible).
	// chromedp ne peut pas envoyer de headers HTTP custom → on vérifie via
	// http.Client direct avec X-Test-User-ID (idem pattern salon_e2e_cdp_test.go).
	// ----------------------------------------------------------------
	t.Run("01_membre_liste_200_et_transcript_visible", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/salon/"+slug+"/transcripts", nil)
		req.Header.Set("X-Test-User-ID", memberID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /transcripts membre: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("liste membre: attendu 200, got %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		// La vue doit au moins rendre « Terminé » (statut "done" → label "Terminé")
		// ou la date du transcript.
		if !strings.Contains(bodyStr, "Terminé") && !strings.Contains(bodyStr, transcriptID[:8]) {
			t.Errorf("liste membre: 'Terminé' ou id du transcript absent du rendu HTML\n%.800s", bodyStr)
		} else {
			t.Log("OK: liste membre rend le statut ou l'id du transcript")
		}
	})

	// ----------------------------------------------------------------
	// ÉTAPE 2 — Membre : détail du transcript via CDP (segments visibles).
	// ----------------------------------------------------------------
	t.Run("02_membre_detail_segments_cdp", func(t *testing.T) {
		// On sert la page via http.Client (auth header) et on analyse le HTML brut
		// car chromedp ne peut pas transmettre X-Test-User-ID.
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/salon/"+slug+"/transcripts/"+transcriptID, nil)
		req.Header.Set("X-Test-User-ID", memberID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /transcripts/{id} membre: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("détail membre: attendu 200, got %d", resp.StatusCode)
			return
		}
		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)

		// Les 3 segments doivent apparaître avec leurs locuteurs.
		speakers := []string{"Alice", "Bob"}
		for _, sp := range speakers {
			if !strings.Contains(bodyStr, sp) {
				t.Errorf("détail: locuteur %q absent du rendu HTML", sp)
			}
		}
		texts := []string{
			"Bonjour à toutes et à tous",
			"Merci de nous rejoindre",
			"Passons au premier point",
		}
		for _, txt := range texts {
			if !strings.Contains(bodyStr, txt) {
				t.Errorf("détail: texte de segment %q absent du rendu HTML", txt)
			}
		}
		t.Log("OK: détail membre rend les 3 segments avec leurs locuteurs")

		// Validation CDP complémentaire : charger la page via chromedp avec un
		// serveur secondaire qui injecte le user dans l'URL path (ne nécessite pas
		// de header custom côté navigateur).
		// On délègue à une sous-fonction qui sert via un mux dédié.
		t.Run("cdp_rendu_visuel", func(t *testing.T) {
			// Monter un mini-serveur qui sert la page pré-authifiée directement.
			detailURL := srv.URL + "/salon/" + slug + "/transcripts/" + transcriptID
			proxyMux := http.NewServeMux()
			proxyMux.HandleFunc("/detail", func(w http.ResponseWriter, r *http.Request) {
				req2, _ := http.NewRequest(http.MethodGet, detailURL, nil)
				req2.Header.Set("X-Test-User-ID", memberID)
				resp2, err := http.DefaultClient.Do(req2)
				if err != nil {
					http.Error(w, err.Error(), 502)
					return
				}
				defer resp2.Body.Close()
				w.Header().Set("Content-Type", resp2.Header.Get("Content-Type"))
				w.WriteHeader(resp2.StatusCode)
				_, _ = io.Copy(w, resp2.Body)
			})
			proxySrv := httptest.NewServer(proxyMux)
			defer proxySrv.Close()

			var pageText string
			if err := chromedp.Run(ctx,
				chromedp.Navigate(proxySrv.URL+"/detail"),
				chromedp.Sleep(500*time.Millisecond),
				chromedp.Text("body", &pageText, chromedp.ByQuery),
			); err != nil {
				t.Fatalf("chromedp détail: %v", err)
			}
			// Vérifier les locuteurs dans le texte rendu par le navigateur.
			if !strings.Contains(pageText, "Alice") {
				t.Errorf("CDP détail: 'Alice' absent du texte rendu")
			}
			if !strings.Contains(pageText, "Bob") {
				t.Errorf("CDP détail: 'Bob' absent du texte rendu")
			}
			t.Log("OK CDP: locuteurs visibles dans la page rendue par le navigateur")
		})
	})

	// ----------------------------------------------------------------
	// ÉTAPE 3 — Download : Content-Disposition attachment + segments dans le corps.
	// ----------------------------------------------------------------
	t.Run("03_membre_download_attachment", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet,
			srv.URL+"/salon/"+slug+"/transcripts/"+transcriptID+"/download", nil)
		req.Header.Set("X-Test-User-ID", memberID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /download membre: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("download membre: attendu 200, got %d", resp.StatusCode)
			return
		}

		cd := resp.Header.Get("Content-Disposition")
		if !strings.Contains(cd, "attachment") {
			t.Errorf("download: Content-Disposition doit contenir 'attachment', got %q", cd)
		} else {
			t.Logf("OK: Content-Disposition = %q", cd)
		}

		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)

		// Le corps .txt doit contenir les trois segments.
		for _, want := range []string{"[Alice]", "[Bob]", "Bonjour à toutes", "Merci de nous rejoindre", "Passons au premier point"} {
			if !strings.Contains(bodyStr, want) {
				t.Errorf("download: %q absent du corps .txt", want)
			}
		}
		t.Log("OK: download .txt contient les segments des deux locuteurs")
	})

	// ----------------------------------------------------------------
	// ÉTAPE 4 — Garde : non-membre → 403.
	// ----------------------------------------------------------------
	t.Run("04_nonmembre_403", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/salon/"+slug+"/transcripts", nil)
		req.Header.Set("X-Test-User-ID", nonMemberID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /transcripts non-membre: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("non-membre: attendu 403, got %d", resp.StatusCode)
		} else {
			t.Log("OK: non-membre obtient bien 403")
		}
	})
}
