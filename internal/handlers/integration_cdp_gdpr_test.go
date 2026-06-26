//go:build integration_cdp

// CLAUDE:SUMMARY Test E2E CDP couche RGPD : un administrateur consulte le registre
// parcellaire dans un navigateur réel, et l'accès nominatif apparaît au journal
// (data_access_log). Preuve de bout en bout du rétro-câblage RGPD sur le cadastre.
//
//	cd /devhoros/assokit
//	go test -tags=integration_cdp -v -timeout=120s ./internal/handlers/ -run TestCDPGDPRJournal
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
	"github.com/hazyhaar/assokit/pkg/gdpr"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
	"github.com/hazyhaar/assokit/pkg/identity"
)

// TestCDPGDPRJournal : login admin via navigateur, navigation /admin/parcelles,
// puis assertion qu'une entrée d'accès nominatif est apparue au journal.
func TestCDPGDPRJournal(t *testing.T) {
	chromePath := findChromium(t)
	if chromePath == "" {
		t.Skip("aucun binaire chromium trouvé — test skippé.")
	}

	dbPath := filepath.Join(t.TempDir(), "gdpr-cdp.db")
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

	const adminEmail = "admin@gdpr.local"
	const adminPwd = "admin-gdpr-pwd-1234"
	authStore := &identity.Store{DB: db}
	admin, err := authStore.Register(context.Background(), adminEmail, adminPwd, "Admin GDPR")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO user_grades(user_id, grade_id) VALUES (?, 'sys-admin'), (?, 'sys-member')`, admin.ID, admin.ID); err != nil {
		t.Fatalf("admin roles: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	theme.Init(&theme.Branding{Name: "Assokit Test"})
	deps := app.AppDeps{
		DB:     db,
		Logger: logger,
		Mailer: &mailer.Mailer{DB: db, From: adminEmail, AdminTo: adminEmail, Logger: logger},
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

	// Login admin via navigateur (établit la session cookie).
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

	// Consultation du registre parcellaire (accès nominatif).
	if err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/admin/parcelles"),
		chromedp.WaitVisible(`#admin-parcelles`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("admin parcelles nav: %v", err)
	}

	// L'accès doit apparaître au journal pour l'acteur admin.
	logs, err := (&gdpr.Store{DB: db}).ListForActor(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("ListForActor: %v", err)
	}
	if len(logs) == 0 {
		t.Fatal("la consultation admin du cadastre via navigateur doit apparaître au journal")
	}
	found := false
	for _, l := range logs {
		if l.SubjectKind == gdpr.SubjectParcelle && l.Action == gdpr.ActionView {
			found = true
		}
	}
	if !found {
		t.Fatalf("aucune entrée de consultation parcellaire trouvée : %+v", logs)
	}
	t.Logf("journal RGPD : %d accès enregistrés pour l'admin via le navigateur", len(logs))
}
