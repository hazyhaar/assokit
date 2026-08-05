//go:build integration_cdp

// CLAUDE:SUMMARY Test E2E CDP conventions de pâturage : un administrateur saisit une
// convention dans un navigateur réel (formulaire guidé), la génère (document
// imprimable), puis la relit ; l'accès nominatif apparaît au journal RGPD.
//
//	cd /devhoros/assokit
//	go test -tags=integration_cdp -v -timeout=120s ./internal/handlers/ -run TestCDPConventionJourney
package handlers

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"log/slog"
	"os"

	"github.com/chromedp/chromedp"
	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/internal/mailer"
	"github.com/hazyhaar/assokit/pkg/convention"
	"github.com/hazyhaar/assokit/pkg/gdpr"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/parcelle"
)

// TestCDPConventionJourney : login admin, saisie d'une convention via le
// formulaire guidé, génération du document, relecture, et assertion d'un accès
// nominatif au journal.
func TestCDPConventionJourney(t *testing.T) {
	chromePath := findChromium(t)
	if chromePath == "" {
		t.Skip("aucun binaire chromium trouvé — test skippé.")
	}

	dbPath := filepath.Join(t.TempDir(), "convention-cdp.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	seedRoles(t, db)

	const adminEmail = "admin@example.local"
	const adminPwd = "admin-conv-pwd-1234"
	authStore := &identity.Store{DB: db}
	admin, err := authStore.Register(context.Background(), adminEmail, adminPwd, "Admin Conv")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO user_grades(user_id, grade_id) VALUES (?, 'sys-admin'), (?, 'sys-member')`, admin.ID, admin.ID); err != nil {
		t.Fatalf("admin roles: %v", err)
	}
	preneur, err := authStore.Register(context.Background(), "preneur@example.local", "preneur-pwd-1234", "Preneur Conv")
	if err != nil {
		t.Fatalf("register preneur: %v", err)
	}
	p1, err := (&parcelle.Store{DB: db}).Create(context.Background(), parcelle.Parcelle{
		CommuneCode: "05015", Section: "AB", NumeroParcelle: "0001", SurfaceM2: 8000, StatutMad: "mise_a_disposition",
	})
	if err != nil {
		t.Fatalf("create parcelle: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	theme.Init(&theme.Branding{Name: "Assokit Test"})
	deps := app.AppDeps{
		DB:                 db,
		Logger:             logger,
		Mailer:             &mailer.Mailer{DB: db, From: adminEmail, AdminTo: adminEmail, Logger: logger},
		ConventionProvider: convention.InertProvider{},
		Config: config.Config{
			Port:         "0",
			DBPath:       dbPath,
			BaseURL:      "http://localhost",
			CookieSecret: []byte("test-cookie-secret-32bytes-padded000"),
			AdminEmail:   adminEmail,
		},
	}
	r := chi.NewRouter()
	r.Use(middleware.Flash)
	r.Use(middleware.Auth(db, deps.Config.CookieSecret))
	if err := MountRoutes(r, deps); err != nil {
		t.Fatalf("MountRoutes: %v", err)
	}
	srv := httptest.NewServer(r)
	defer srv.Close()

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer allocCancel()
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()
	ctx, cancel := context.WithTimeout(browserCtx, 60*time.Second)
	defer cancel()

	if err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/login"),
		chromedp.WaitVisible(`form input[name="email"]`, chromedp.ByQuery),
		chromedp.SendKeys(`input[name="email"]`, adminEmail, chromedp.ByQuery),
		chromedp.SendKeys(`input[name="password"]`, adminPwd, chromedp.ByQuery),
		chromedp.Submit(`form`, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
	); err != nil {
		t.Fatalf("login: %v", err)
	}

	// Saisie de la convention via le formulaire guidé (preneur + durée + parcelle).
	if err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/admin/conventions"),
		chromedp.WaitVisible(`#admin-conventions`, chromedp.ByQuery),
		chromedp.SendKeys(`input[name="preneur_id"]`, preneur.ID, chromedp.ByQuery),
		chromedp.Click(`input[name="parcelle_id"][value="`+p1+`"]`, chromedp.ByQuery),
		chromedp.Submit(`#admin-conventions form`, chromedp.ByQuery),
		// La création redirige vers le document de génération.
		chromedp.WaitVisible(`#convention-document`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("saisie + génération: %v", err)
	}

	// La convention est bien persistée et rattachée au preneur.
	convs, err := (&convention.Store{DB: db}).ListForUser(context.Background(), preneur.ID)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("1 convention attendue pour le preneur, obtenu %d", len(convs))
	}

	// L'accès nominatif (sujet = preneur) doit apparaître au journal pour l'admin.
	logs, err := (&gdpr.Store{DB: db}).ListForActor(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("ListForActor: %v", err)
	}
	found := false
	for _, l := range logs {
		if l.SubjectKind == gdpr.SubjectUser && l.SubjectID == preneur.ID && l.Action == gdpr.ActionView {
			found = true
		}
	}
	if !found {
		t.Fatalf("la création/génération de convention doit journaliser un accès au preneur : %+v", logs)
	}
	t.Logf("convention créée et générée via navigateur ; %d accès journalisés", len(logs))
}
