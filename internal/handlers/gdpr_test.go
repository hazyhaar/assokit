package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"log/slog"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions/seeds"
	"github.com/hazyhaar/assokit/pkg/gdpr"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/messaging"
	"github.com/hazyhaar/assokit/pkg/parcelle"
)

func mkUserH(t *testing.T, deps app.AppDeps, id string) {
	t.Helper()
	_, err := deps.DB.Exec(
		`INSERT INTO users(id, email, password_hash, display_name, is_active) VALUES(?,?,?,?,?)`,
		id, id+"@example.org", "x", id, 1,
	)
	if err != nil {
		t.Fatalf("mkUser %s: %v", id, err)
	}
}

// TestDataExport_MemberOnlyOwnData : l'export ne restitue QUE les données du
// membre courant. Deux membres détiennent chacun une parcelle ; l'export d'Alice
// ne contient pas le droit de Boris, et inversement.
func TestDataExport_MemberOnlyOwnData(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	mkUserH(t, deps, "alice")
	mkUserH(t, deps, "boris")

	pStore := &parcelle.Store{DB: db}
	pAlice, err := pStore.Create(context.Background(), parcelle.Parcelle{CommuneCode: "05015", Section: "AB", NumeroParcelle: "1"})
	if err != nil {
		t.Fatalf("create parcelle alice: %v", err)
	}
	pBoris, err := pStore.Create(context.Background(), parcelle.Parcelle{CommuneCode: "05015", Section: "AB", NumeroParcelle: "2"})
	if err != nil {
		t.Fatalf("create parcelle boris: %v", err)
	}
	if _, err := pStore.AddDroit(context.Background(), pAlice, "alice", "pleine_propriete"); err != nil {
		t.Fatalf("droit alice: %v", err)
	}
	if _, err := pStore.AddDroit(context.Background(), pBoris, "boris", "occupant"); err != nil {
		t.Fatalf("droit boris: %v", err)
	}
	mStore := &messaging.Store{DB: db}
	if _, err := mStore.Send(context.Background(), "alice", "boris", "bonjour"); err != nil {
		t.Fatalf("message: %v", err)
	}

	data, err := seeds.ExportMemberData(context.Background(), deps, "alice")
	if err != nil {
		t.Fatalf("ExportMemberData: %v", err)
	}
	if len(data.Droits) != 1 || data.Droits[0].UserID != "alice" {
		t.Fatalf("export d'Alice doit contenir uniquement son droit, obtenu %+v", data.Droits)
	}
	if len(data.Parcelles) != 1 || data.Parcelles[0].ID != pAlice {
		t.Fatalf("export d'Alice doit contenir uniquement sa parcelle, obtenu %+v", data.Parcelles)
	}
	// Le message échangé apparaît côté Alice (expéditrice).
	if len(data.Messages) != 1 {
		t.Fatalf("export d'Alice doit contenir son message, obtenu %d", len(data.Messages))
	}

	// Vérification HTTP : la route renvoie du JSON et journalise un export.
	r := httptest.NewRequest(http.MethodGet, "/account/data-download", nil)
	r = r.WithContext(middleware.ContextWithUser(r.Context(), &identity.User{ID: "alice"}))
	w := httptest.NewRecorder()
	handleDataDownload(deps).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("data-download: code %d", w.Code)
	}
	var out seeds.MemberDataExport
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if out.UserID != "alice" {
		t.Fatalf("export user_id attendu alice, obtenu %s", out.UserID)
	}

	logs, err := (&gdpr.Store{DB: db}).ListForActor(context.Background(), "alice")
	if err != nil {
		t.Fatalf("ListForActor: %v", err)
	}
	foundExport := false
	for _, l := range logs {
		if l.Action == gdpr.ActionExport {
			foundExport = true
		}
	}
	if !foundExport {
		t.Fatal("l'export doit apparaître au journal (action export)")
	}
}

// TestCadastreAccess_AppearsInJournal : un accès admin au registre parcellaire
// (consultation) apparaît bien au journal des accès nominatifs. Preuve de la
// rétro-câblage de la couche RGPD sur le cadastre existant.
func TestCadastreAccess_AppearsInJournal(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	r := httptest.NewRequest(http.MethodGet, "/admin/parcelles", nil)
	r = adminUserReq(r)
	w := httptest.NewRecorder()
	handleAdminParcelles(deps).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("admin parcelles GET : code %d", w.Code)
	}

	logs, err := (&gdpr.Store{DB: db}).ListForActor(context.Background(), "boris")
	if err != nil {
		t.Fatalf("ListForActor: %v", err)
	}
	if len(logs) == 0 {
		t.Fatal("la consultation admin du cadastre doit apparaître au journal")
	}
	if logs[0].SubjectKind != gdpr.SubjectParcelle || logs[0].Action != gdpr.ActionView {
		t.Fatalf("entrée de journal inattendue : %+v", logs[0])
	}
}

// TestDataPolicy_RendersFromConsts : la page de politique se rend depuis les
// const Go et expose les libellés du catalogue de conservation.
func TestDataPolicy_RendersFromConsts(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	r := httptest.NewRequest(http.MethodGet, "/donnees-personnelles", nil)
	w := httptest.NewRecorder()
	handleDataPolicy(deps).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("politique : code %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Registre parcellaire") {
		t.Fatal("la page doit rendre les catégories du catalogue de conservation")
	}
}
