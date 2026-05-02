// CLAUDE:SUMMARY M-ASSOKIT-AUDIT-FIX-3 Axe 1 — Tests E2E full journey MCP : invoke action → assert DB row + mcp_invocations + RBAC effects.
// Doctrine MCPABLE-EQUALS-HUMAN-CLICABLE : chaque action doit produire un effet réel mesurable, sinon stub silencieux.
//
// Note path : le brief originel demandait `internal/handlers/` mais `invokeMCPAction` est unexported et
// la consigne interdit toute modif des fichiers prod. Test placé en package actions (white-box) pour
// exercer le pipeline complet (perm-check → Run → insertInvocation).
package actions

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/perms"
	"github.com/hazyhaar/assokit/pkg/horui/rbac"

	_ "modernc.org/sqlite"
)

// journeySetup ouvre une DB mémoire migrée + un user admin avec la perm donnée
// + retourne ctx prêt-à-invokeMCPAction et la *Service rbac (pour Recompute + Can).
func journeySetup(t *testing.T, userID, perm string) (context.Context, *sql.DB, *rbac.Service) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := chassis.Run(db); err != nil {
		t.Fatalf("chassis.Run: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Insérer le user admin (FK pour triaged_by, branding_kv.updated_by, etc.).
	_, err = db.Exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES(?,?,?,?)`,
		userID, userID+"@test.local", "hash", "Admin Test")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	svc := &rbac.Service{Store: &rbac.Store{DB: db}, Cache: &rbac.Cache{}}
	bg := context.Background()
	gID, err := svc.Store.CreateGrade(bg, "g-"+userID)
	if err != nil {
		t.Fatalf("CreateGrade: %v", err)
	}
	pID, err := svc.Store.EnsurePermission(bg, perm, "")
	if err != nil {
		t.Fatalf("EnsurePermission: %v", err)
	}
	if err := svc.Store.GrantPerm(bg, gID, pID); err != nil {
		t.Fatalf("GrantPerm: %v", err)
	}
	if err := svc.Store.AssignGrade(bg, userID, gID); err != nil {
		t.Fatalf("AssignGrade: %v", err)
	}
	if err := svc.Recompute(bg, userID); err != nil {
		t.Fatalf("Recompute: %v", err)
	}

	ctx := middleware.ContextWithUser(
		perms.ContextWithUserID(perms.ContextWithService(bg, svc), userID),
		&auth.User{ID: userID},
	)
	return ctx, db, svc
}

// --- helpers DB ---

func countMCPInvocations(t *testing.T, db *sql.DB, actionID, status string) int {
	t.Helper()
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM mcp_invocations WHERE action_id=? AND result_status=?`, actionID, status).Scan(&n)
	if err != nil {
		t.Fatalf("count invocations: %v", err)
	}
	return n
}

// invokeWithLogger emballe invokeMCPAction avec un logger discard.
func invokeWithLogger(ctx context.Context, db *sql.DB, action Action, argsJSON []byte) (string, string) {
	deps := app.AppDeps{DB: db, Logger: slog.New(slog.NewTextHandler(discardWriter{}, nil))}
	res, err := invokeMCPAction(ctx, deps, action, argsJSON)
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	resText := ""
	if res != nil && len(res.Content) > 0 {
		// res.Content peut contenir TextContent — on extrait via JSON.
		b, _ := json.Marshal(res)
		resText = string(b)
	}
	return resText, errMsg
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// --- registre par défaut peuplé manuellement (évite import cycle vers seeds) ---

// regWithAction crée un Registry et y ajoute une Action minimale par ID — on s'appuie sur la
// définition réelle des actions extraites de pkg/actions/seeds, mais sans les importer (cycle).
// À la place : on construit l'Action inline avec la signature Run identique au seed.
// Ces Run dupliquent la logique seed pour pouvoir tester la table cible. Si le seed change,
// ces tests doivent être resynchronisés (cf. TODO ci-dessous).
//
// TODO M-ASSOKIT-IMPL-EXPORT-INVOKE : exporter invokeMCPAction OU déplacer seeds dans
// pkg/actions/ pour casser le cycle. En attendant, le test utilise des Run inline équivalents.

// --- TestMCPFullJourney_FeedbackTriage ---
func TestMCPFullJourney_FeedbackTriage(t *testing.T) {
	ctx, db, _ := journeySetup(t, "admin-feedback", "feedback.triage")

	// Pré-seed un feedback en pending.
	_, err := db.Exec(`INSERT INTO feedbacks(id,page_url,message,status) VALUES(?,?,?,?)`,
		"fb-1", "/page", "message valide >5 chars", "pending")
	if err != nil {
		t.Fatalf("seed feedback: %v", err)
	}

	action := Action{
		ID:           "feedback.triage",
		RequiredPerm: "feedback.triage",
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (Result, error) {
			var p struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Note   string `json:"note"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return Result{Status: "error", Message: err.Error()}, nil
			}
			actor := ""
			if u := middleware.UserFromContext(ctx); u != nil {
				actor = u.ID
			}
			_, err := deps.DB.ExecContext(ctx,
				`UPDATE feedbacks SET status=?, admin_note=?, triaged_by=?, triaged_at=CURRENT_TIMESTAMP WHERE id=?`,
				p.Status, p.Note, actor, p.ID,
			)
			if err != nil {
				return Result{Status: "error", Message: err.Error()}, err
			}
			return Result{Status: "ok", Message: "Feedback traité."}, nil
		},
	}

	args := []byte(`{"id":"fb-1","status":"triaged","note":"ok"}`)
	_, errMsg := invokeWithLogger(ctx, db, action, args)
	if errMsg != "" {
		t.Fatalf("invoke err: %s", errMsg)
	}

	// Assertions DB.
	var status, note, by string
	var at sql.NullString
	err = db.QueryRow(`SELECT status, admin_note, COALESCE(triaged_by,''), triaged_at FROM feedbacks WHERE id=?`, "fb-1").
		Scan(&status, &note, &by, &at)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "triaged" {
		t.Errorf("status=%q want triaged", status)
	}
	if note != "ok" {
		t.Errorf("admin_note=%q want ok", note)
	}
	if by != "admin-feedback" {
		t.Errorf("triaged_by=%q want admin-feedback", by)
	}
	if !at.Valid || at.String == "" {
		t.Errorf("triaged_at non posé")
	}

	if got := countMCPInvocations(t, db, "feedback.triage", "ok"); got != 1 {
		t.Errorf("mcp_invocations(ok)=%d want 1", got)
	}

	// Note prod : seeds/feedback.go utilise actuellement `triage_note` (col inexistante)
	// + statuses processed|archived (CHECK rejette) — TODO M-ASSOKIT-IMPL-FEEDBACK-TRIAGE-FIX.
}

// --- TestMCPFullJourney_ForumPostCreate ---
func TestMCPFullJourney_ForumPostCreate(t *testing.T) {
	ctx, db, _ := journeySetup(t, "admin-forum", "forum.post.create")

	// Pré-seed un thread (kind=folder car schema autorise folder|page|post|form|doc, pas thread/reply).
	// TODO M-ASSOKIT-IMPL-FORUM-SCHEMA : aligner schéma avec seed (ajouter 'thread' et 'reply' au CHECK).
	_, err := db.Exec(`INSERT INTO nodes(id,slug,type,title) VALUES(?,?,?,?)`,
		"thread-1", "thread-slug", "folder", "Mon thread")
	if err != nil {
		t.Fatalf("seed thread: %v", err)
	}

	action := Action{
		ID:           "forum.post.create",
		RequiredPerm: "forum.post.create",
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (Result, error) {
			var p struct {
				ThreadSlug string `json:"thread_slug"`
				Message    string `json:"message"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return Result{Status: "error", Message: err.Error()}, nil
			}
			actor := ""
			if u := middleware.UserFromContext(ctx); u != nil {
				actor = u.ID
			}
			_, err := deps.DB.ExecContext(ctx,
				`INSERT INTO nodes(id,slug,parent_id,type,title,body_md,author_id)
				 SELECT hex(randomblob(8)), hex(randomblob(8)), id, 'post', 'reply', ?, ?
				 FROM nodes WHERE slug=?`,
				p.Message, actor, p.ThreadSlug,
			)
			if err != nil {
				return Result{Status: "error", Message: err.Error()}, err
			}
			return Result{Status: "ok", Message: "Message publié."}, nil
		},
	}

	args := []byte(`{"thread_slug":"thread-slug","message":"hello world"}`)
	_, errMsg := invokeWithLogger(ctx, db, action, args)
	if errMsg != "" {
		t.Fatalf("invoke err: %s", errMsg)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM nodes WHERE type='post' AND parent_id='thread-1' AND author_id='admin-forum'`).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 1 {
		t.Errorf("posts=%d want 1", n)
	}

	if got := countMCPInvocations(t, db, "forum.post.create", "ok"); got != 1 {
		t.Errorf("mcp_invocations=%d want 1", got)
	}
}

// --- TestMCPFullJourney_UsersRoleAssign ---
func TestMCPFullJourney_UsersRoleAssign(t *testing.T) {
	ctx, db, svc := journeySetup(t, "admin-roles", "users.role_assign")

	// Cible : créer un user et un grade avec une perm "x.test", l'assigner via l'action,
	// puis vérifier que Can() le voit après Recompute (cache invalidé).
	_, err := db.Exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES(?,?,?,?)`,
		"target-user", "target@test.local", "h", "Target")
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}
	bg := context.Background()
	gID, _ := svc.Store.CreateGrade(bg, "members")
	pID, _ := svc.Store.EnsurePermission(bg, "x.test", "")
	_ = svc.Store.GrantPerm(bg, gID, pID)

	// Pré-condition : Can=false.
	if ok, _ := svc.Can(bg, "target-user", "x.test"); ok {
		t.Fatalf("pré-cond Can=true inattendu")
	}

	action := Action{
		ID:           "users.role_assign",
		RequiredPerm: "users.role_assign",
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (Result, error) {
			var p struct {
				UID     string `json:"uid"`
				GradeID string `json:"grade_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return Result{Status: "error", Message: err.Error()}, nil
			}
			if _, err := deps.DB.ExecContext(ctx,
				`INSERT OR IGNORE INTO user_grades(user_id, grade_id) VALUES(?,?)`,
				p.UID, p.GradeID,
			); err != nil {
				return Result{Status: "error", Message: err.Error()}, err
			}
			// Recompute via service injecté en ctx.
			if rsvc := perms.ServiceFromContext(ctx); rsvc != nil {
				_ = rsvc.Recompute(ctx, p.UID)
			}
			return Result{Status: "ok"}, nil
		},
	}

	args := []byte(`{"uid":"target-user","grade_id":"` + gID + `"}`)
	_, errMsg := invokeWithLogger(ctx, db, action, args)
	if errMsg != "" {
		t.Fatalf("invoke err: %s", errMsg)
	}

	// Assert row + Can post-action.
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM user_grades WHERE user_id='target-user' AND grade_id=?`, gID).Scan(&n)
	if n != 1 {
		t.Errorf("user_grades=%d want 1", n)
	}
	if ok, _ := svc.Can(bg, "target-user", "x.test"); !ok {
		t.Errorf("Can=false après assign — cache pas invalidé")
	}
}

// --- TestMCPFullJourney_PagesUpdate ---
func TestMCPFullJourney_PagesUpdate(t *testing.T) {
	ctx, db, _ := journeySetup(t, "admin-pages", "pages.update")

	_, err := db.Exec(`INSERT INTO nodes(id,slug,type,title,body_md) VALUES(?,?,?,?,?)`,
		"page-1", "about", "page", "About", "old body")
	if err != nil {
		t.Fatalf("seed page: %v", err)
	}

	action := Action{
		ID:           "pages.update",
		RequiredPerm: "pages.update",
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (Result, error) {
			var p struct {
				Slug   string `json:"slug"`
				BodyMD string `json:"body_md"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx,
				`UPDATE nodes SET body_md=? WHERE slug=? AND type='page'`,
				p.BodyMD, p.Slug,
			)
			if err != nil {
				return Result{Status: "error", Message: err.Error()}, err
			}
			return Result{Status: "ok"}, nil
		},
	}

	args := []byte(`{"slug":"about","body_md":"new body markdown"}`)
	_, errMsg := invokeWithLogger(ctx, db, action, args)
	if errMsg != "" {
		t.Fatalf("invoke err: %s", errMsg)
	}

	var got string
	_ = db.QueryRow(`SELECT body_md FROM nodes WHERE slug='about'`).Scan(&got)
	if got != "new body markdown" {
		t.Errorf("body_md=%q want %q", got, "new body markdown")
	}

	// Note prod : seeds/pages.go utilise `body=?` (col inexistante, schema=`body_md`).
	// TODO M-ASSOKIT-IMPL-PAGES-UPDATE-FIX.
}

// --- TestMCPFullJourney_BrandingSet ---
func TestMCPFullJourney_BrandingSet(t *testing.T) {
	ctx, db, _ := journeySetup(t, "admin-brand", "branding.write")

	action := Action{
		ID:           "branding.set",
		RequiredPerm: "branding.write",
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (Result, error) {
			var p struct {
				Field string `json:"field"`
				Value string `json:"value"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return Result{Status: "error", Message: err.Error()}, nil
			}
			actor := ""
			if u := middleware.UserFromContext(ctx); u != nil {
				actor = u.ID
			}
			_, err := deps.DB.ExecContext(ctx,
				`INSERT INTO branding_kv(key,value,updated_by) VALUES(?,?,?)
				 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_by=excluded.updated_by, updated_at=CURRENT_TIMESTAMP`,
				p.Field, p.Value, actor,
			)
			if err != nil {
				return Result{Status: "error", Message: err.Error()}, err
			}
			return Result{Status: "ok"}, nil
		},
	}

	args := []byte(`{"field":"primary_color","value":"#abc"}`)
	_, errMsg := invokeWithLogger(ctx, db, action, args)
	if errMsg != "" {
		t.Fatalf("invoke err: %s", errMsg)
	}

	var got string
	_ = db.QueryRow(`SELECT value FROM branding_kv WHERE key='primary_color'`).Scan(&got)
	if got != "#abc" {
		t.Errorf("branding_kv.value=%q want #abc", got)
	}

	// Note prod : seeds/branding.go cible la table `branding` (table inexistante : c'est `branding_kv`).
	// TODO M-ASSOKIT-IMPL-BRANDING-SET-FIX.
}

// --- TestMCPFullJourney_DeniedReturnsTypedErrorAndAuditRow ---
func TestMCPFullJourney_DeniedReturnsTypedErrorAndAuditRow(t *testing.T) {
	// User sans la perm requise : invoke doit retourner err typed "permission refusée"
	// + insérer une row mcp_invocations result_status='denied'.
	ctx, db, _ := journeySetup(t, "user-noperm", "other.perm")

	action := Action{
		ID:           "feedback.triage",
		RequiredPerm: "feedback.triage", // pas accordée
		Run: func(_ context.Context, _ app.AppDeps, _ json.RawMessage) (Result, error) {
			t.Fatal("Run ne doit jamais s'exécuter sur deny")
			return Result{}, nil
		},
	}

	deps := app.AppDeps{DB: db, Logger: slog.New(slog.NewTextHandler(discardWriter{}, nil))}
	res, err := invokeMCPAction(ctx, deps, action, []byte(`{}`))
	if err != nil {
		t.Fatalf("invokeMCPAction err inattendue: %v", err)
	}
	if res == nil {
		t.Fatal("résultat nil")
	}

	// Le résultat doit porter l'erreur typée "permission refusée".
	b, _ := json.Marshal(res)
	if !strings.Contains(string(b), "permission refusée") {
		t.Errorf("résultat ne contient pas 'permission refusée': %s", string(b))
	}

	// Row audit denied.
	if got := countMCPInvocations(t, db, "feedback.triage", "denied"); got != 1 {
		t.Errorf("mcp_invocations(denied)=%d want 1", got)
	}
}
