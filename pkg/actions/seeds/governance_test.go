package seeds_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/internal/mailer"
	"github.com/hazyhaar/assokit/pkg/actions/seeds"
	"github.com/hazyhaar/assokit/pkg/governance"
	"github.com/hazyhaar/assokit/pkg/membership"
)

func newGovernanceDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// mkActiveMember crée un compte réel et lui attache une adhésion ACTIVE en base de
// test (aucun placeholder hardcodé : ce sont des lignes créées par les Stores réels).
func mkActiveMember(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO users(id,email,password_hash,display_name,is_active) VALUES(?,?,?,?,1)`,
		id, id+"@example.org", "x", id); err != nil {
		t.Fatalf("insert user %s: %v", id, err)
	}
	if _, err := (&membership.Store{DB: db}).Create(context.Background(), membership.Membership{
		UserID:      id,
		PeriodStart: "2026-01-01",
		PeriodEnd:   "2026-12-31",
		AmountCents: 2500,
		Status:      "active",
	}); err != nil {
		t.Fatalf("create membership %s: %v", id, err)
	}
}

// TestConvokeEnqueuesOnePerActiveMember : convoquer une assemblée doit faire passer
// son statut à convoked, mettre en file un courriel par adhérent ACTIF réel, et
// tracer chaque convocation. Données de test = lignes créées par les Stores, jamais
// des placeholders dans le code applicatif.
func TestConvokeEnqueuesOnePerActiveMember(t *testing.T) {
	db := newGovernanceDB(t)
	defer db.Close()
	ctx := context.Background()

	const n = 3
	for i := 0; i < n; i++ {
		mkActiveMember(t, db, "member-"+string(rune('a'+i)))
	}
	// Un membre non-actif (adhésion pending) : ne doit PAS être convoqué.
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash,display_name,is_active) VALUES('inactif','inactif@example.org','x','Inactif',1)`); err != nil {
		t.Fatalf("insert inactif: %v", err)
	}
	if _, err := (&membership.Store{DB: db}).Create(ctx, membership.Membership{
		UserID: "inactif", PeriodStart: "2026-01-01", PeriodEnd: "2026-12-31", Status: "pending",
	}); err != nil {
		t.Fatalf("create pending: %v", err)
	}

	deps := app.AppDeps{DB: db, Mailer: &mailer.Mailer{DB: db}}

	// Créer l'assemblée via l'action MCP (parité du chemin réel).
	createAct := actionByID(t, "governance.create_assembly")
	res, err := createAct.Run(ctx, deps, json.RawMessage(`{"name":"AG ordinaire 2026","agenda":"Comptes et bureau","scheduled_at":"2026-09-12 18:00"}`))
	if err != nil {
		t.Fatalf("create_assembly: %v", err)
	}
	data, _ := res.Data.(map[string]string)
	assemblyID := data["id"]
	if assemblyID == "" {
		t.Fatalf("id assemblée vide: %+v", res)
	}

	// Convoquer via l'action MCP.
	convokeAct := actionByID(t, "governance.convoke")
	if _, err := convokeAct.Run(ctx, deps, json.RawMessage(`{"assembly_id":"`+assemblyID+`"}`)); err != nil {
		t.Fatalf("convoke: %v", err)
	}

	// L'assemblée est passée à convoked.
	var status string
	if err := db.QueryRow(`SELECT status FROM assemblies WHERE id=?`, assemblyID).Scan(&status); err != nil {
		t.Fatalf("scan status: %v", err)
	}
	if status != "convoked" {
		t.Fatalf("statut attendu convoked, obtenu %s", status)
	}

	// N courriels en file (pending), un par adhérent actif — l'inactif exclu.
	var pending int
	if err := db.QueryRow(`SELECT COUNT(*) FROM email_outbox WHERE status='pending'`).Scan(&pending); err != nil {
		t.Fatalf("scan outbox: %v", err)
	}
	if pending != n {
		t.Fatalf("attendu %d courriels pending, obtenu %d", n, pending)
	}

	// N traces de convocation.
	var convocations int
	if err := db.QueryRow(`SELECT COUNT(*) FROM convocations WHERE assembly_id=?`, assemblyID).Scan(&convocations); err != nil {
		t.Fatalf("scan convocations: %v", err)
	}
	if convocations != n {
		t.Fatalf("attendu %d convocations, obtenu %d", n, convocations)
	}

	// Le sujet et le destinataire sont réels (issus du compte), pas fabriqués.
	var to, subject string
	if err := db.QueryRow(`SELECT to_addr, subject FROM email_outbox ORDER BY to_addr LIMIT 1`).Scan(&to, &subject); err != nil {
		t.Fatalf("scan first email: %v", err)
	}
	if to != "member-a@example.org" {
		t.Fatalf("destinataire inattendu: %s", to)
	}
	if subject != "Convocation : AG ordinaire 2026" {
		t.Fatalf("sujet inattendu: %s", subject)
	}
}

// TestConvokeAtomicity prouve l'invariant « AG convoked ⟺ traces de convocation
// complètes » : la transition drafting→convoked et les N inserts de convocations
// sont écrits dans UNE seule transaction. Un échec à mi-parcours (ici : violation de
// clé étrangère sur la 2e convocation, assembly_id inexistant) doit faire échouer le
// commit et laisser zéro état partiel — l'AG reste drafting, zéro trace. C'est la
// promotion en régression du point aveugle initial (UPDATE + boucle hors tx).
func TestConvokeAtomicity(t *testing.T) {
	db := newGovernanceDB(t)
	defer db.Close()
	ctx := context.Background()

	mkActiveMember(t, db, "member-a")
	mkActiveMember(t, db, "member-b")

	gov := &governance.Store{DB: db}
	assemblyID, err := gov.Create(ctx, governance.Assembly{Name: "AG atomique"})
	if err != nil {
		t.Fatalf("create assembly: %v", err)
	}

	// Reproduit la section critique de Convoke, mais force un échec à mi-parcours :
	// la 2e trace référence une assemblée inexistante → la FK convocations.assembly_id
	// fait échouer l'insert, donc le commit n'aura jamais lieu.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := gov.MarkConvokedTx(ctx, tx, assemblyID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("MarkConvokedTx: %v", err)
	}
	if err := gov.RecordConvocationTx(ctx, tx, assemblyID, "member-a"); err != nil {
		_ = tx.Rollback()
		t.Fatalf("RecordConvocationTx #1: %v", err)
	}
	// Échec injecté : assembly_id inconnu → violation FK, l'écriture doit échouer.
	if err := gov.RecordConvocationTx(ctx, tx, "assembly-inexistante", "member-b"); err == nil {
		_ = tx.Rollback()
		t.Fatal("RecordConvocationTx #2 aurait dû échouer (FK assembly_id inexistant)")
	}
	// La section critique a échoué à mi-parcours → rollback, comme dans Convoke.
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// Invariant : aucun état partiel committé.
	var status string
	if err := db.QueryRow(`SELECT status FROM assemblies WHERE id=?`, assemblyID).Scan(&status); err != nil {
		t.Fatalf("scan status: %v", err)
	}
	if status != "drafting" {
		t.Fatalf("après échec, statut attendu drafting, obtenu %s", status)
	}
	var traces int
	if err := db.QueryRow(`SELECT COUNT(*) FROM convocations WHERE assembly_id=?`, assemblyID).Scan(&traces); err != nil {
		t.Fatalf("scan convocations: %v", err)
	}
	if traces != 0 {
		t.Fatalf("après échec, attendu 0 trace, obtenu %d", traces)
	}

	// Contre-épreuve : le chemin nominal (Convoke réel) committe atomiquement la
	// transition ET exactement N traces (= adhérents actifs), pas un sous-ensemble.
	deps := app.AppDeps{DB: db, Mailer: &mailer.Mailer{DB: db}}
	sent, err := seeds.Convoke(ctx, deps, assemblyID)
	if err != nil {
		t.Fatalf("Convoke nominal: %v", err)
	}
	if sent != 2 {
		t.Fatalf("attendu 2 convocations, obtenu %d", sent)
	}
	if err := db.QueryRow(`SELECT status FROM assemblies WHERE id=?`, assemblyID).Scan(&status); err != nil {
		t.Fatalf("scan status nominal: %v", err)
	}
	if status != "convoked" {
		t.Fatalf("après Convoke nominal, statut attendu convoked, obtenu %s", status)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM convocations WHERE assembly_id=?`, assemblyID).Scan(&traces); err != nil {
		t.Fatalf("scan convocations nominal: %v", err)
	}
	if traces != 2 {
		t.Fatalf("après Convoke nominal, attendu 2 traces, obtenu %d", traces)
	}
}

// TestConvokeNoActiveMembers : sans adhérent actif, aucun courriel n'est envoyé
// (état vide assumé) et aucune erreur n'est levée. L'assemblée passe convoked.
func TestConvokeNoActiveMembers(t *testing.T) {
	db := newGovernanceDB(t)
	defer db.Close()
	ctx := context.Background()
	deps := app.AppDeps{DB: db, Mailer: &mailer.Mailer{DB: db}}

	createAct := actionByID(t, "governance.create_assembly")
	res, err := createAct.Run(ctx, deps, json.RawMessage(`{"name":"AG vide"}`))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	assemblyID := res.Data.(map[string]string)["id"]

	convokeAct := actionByID(t, "governance.convoke")
	if _, err := convokeAct.Run(ctx, deps, json.RawMessage(`{"assembly_id":"`+assemblyID+`"}`)); err != nil {
		t.Fatalf("convoke: %v", err)
	}

	var pending int
	if err := db.QueryRow(`SELECT COUNT(*) FROM email_outbox WHERE status='pending'`).Scan(&pending); err != nil {
		t.Fatalf("scan outbox: %v", err)
	}
	if pending != 0 {
		t.Fatalf("attendu 0 courriel, obtenu %d", pending)
	}
}

// TestCheckQuorumActionResolvesEligibles : l'action governance.check_quorum résout
// les éligibles via membership.Store (adhérents actifs réels) PUIS appelle
// ComputeQuorum. Un membre non actif est exclu d'Eligible ; un adhérent présent ET
// représenté n'est compté qu'une fois dans Attending.
func TestCheckQuorumActionResolvesEligibles(t *testing.T) {
	db := newGovernanceDB(t)
	defer db.Close()
	ctx := context.Background()
	deps := app.AppDeps{DB: db, Mailer: &mailer.Mailer{DB: db}}

	// 4 adhérents actifs réels.
	for _, u := range []string{"alice", "bob", "carol", "dora"} {
		mkActiveMember(t, db, u)
	}
	// 1 utilisateur sans adhésion active → non éligible.
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash,display_name,is_active) VALUES('eve','eve@example.org','x','Eve',1)`); err != nil {
		t.Fatalf("insert eve: %v", err)
	}

	gov := &governance.Store{DB: db}
	asmID, err := gov.Create(ctx, governance.Assembly{Name: "AG quorum"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// L'émargement et le mandat exigent une AG convoquée (garde d'état V2b).
	if err := gov.MarkConvoked(ctx, asmID); err != nil {
		t.Fatalf("convoke: %v", err)
	}

	// Émargements : alice, bob (éligibles) + eve (non éligible, ignorée au décompte).
	signAct := actionByID(t, "governance.sign_attendance")
	for _, u := range []string{"alice", "bob", "eve"} {
		if _, err := signAct.Run(ctx, deps, json.RawMessage(`{"assembly_id":"`+asmID+`","member_user_id":"`+u+`"}`)); err != nil {
			t.Fatalf("sign %s: %v", u, err)
		}
	}
	// Idempotence via l'action : ré-émarger alice ne crée pas de doublon.
	if _, err := signAct.Run(ctx, deps, json.RawMessage(`{"assembly_id":"`+asmID+`","member_user_id":"alice"}`)); err != nil {
		t.Fatalf("sign alice (idempotent): %v", err)
	}

	// Mandats : carol (absente) → bob (présent) ⇒ carol représentée ;
	//           alice (présente) → dora (absente) ⇒ ne double-compte pas alice.
	mandateAct := actionByID(t, "governance.record_mandate")
	if _, err := mandateAct.Run(ctx, deps, json.RawMessage(`{"assembly_id":"`+asmID+`","principal_user_id":"carol","proxy_user_id":"bob"}`)); err != nil {
		t.Fatalf("mandat carol→bob: %v", err)
	}
	if _, err := mandateAct.Run(ctx, deps, json.RawMessage(`{"assembly_id":"`+asmID+`","principal_user_id":"alice","proxy_user_id":"dora"}`)); err != nil {
		t.Fatalf("mandat alice→dora: %v", err)
	}

	checkAct := actionByID(t, "governance.check_quorum")
	res, err := checkAct.Run(ctx, deps, json.RawMessage(`{"assembly_id":"`+asmID+`"}`))
	if err != nil {
		t.Fatalf("check_quorum: %v", err)
	}
	q, ok := res.Data.(governance.QuorumResult)
	if !ok {
		t.Fatalf("Data inattendu: %T", res.Data)
	}
	t.Logf("action check_quorum: eligible=%d present=%d represented=%d attending=%d reached=%v",
		q.Eligible, q.Present, q.Represented, q.Attending, q.Reached)
	if q.Eligible != 4 {
		t.Fatalf("Eligible attendu 4 (eve exclue), obtenu %d", q.Eligible)
	}
	if q.Present != 2 {
		t.Fatalf("Present attendu 2, obtenu %d", q.Present)
	}
	if q.Represented != 1 {
		t.Fatalf("Represented attendu 1, obtenu %d", q.Represented)
	}
	if q.Attending != 3 {
		t.Fatalf("Attending attendu 3 (sans double comptage), obtenu %d", q.Attending)
	}
	if !q.Reached {
		t.Fatalf("Reached attendu true")
	}
}

// TestGovernanceVoteActionsFullCycle exerce les actions V2c via le registry réel
// (parité du chemin MCP/HTTP) : ouverture d'une résolution, trois bulletins
// distincts, dépouillement exact, double vote rejeté, puis clôture verrouillant le
// scrutin. Données par les Stores réels (membership), jamais hardcodées.
func TestGovernanceVoteActionsFullCycle(t *testing.T) {
	db := newGovernanceDB(t)
	defer db.Close()
	ctx := context.Background()
	deps := app.AppDeps{DB: db, Mailer: &mailer.Mailer{DB: db}}

	for _, id := range []string{"alice", "bob", "carol"} {
		mkActiveMember(t, db, id)
	}

	// AG convoquée (chemin réel : create_assembly + convoke).
	createRes, err := actionByID(t, "governance.create_assembly").Run(ctx, deps,
		json.RawMessage(`{"name":"AG vote 2026"}`))
	if err != nil {
		t.Fatalf("create_assembly: %v", err)
	}
	asmID := createRes.Data.(map[string]string)["id"]
	if _, err := actionByID(t, "governance.convoke").Run(ctx, deps,
		json.RawMessage(`{"assembly_id":"`+asmID+`"}`)); err != nil {
		t.Fatalf("convoke: %v", err)
	}

	// Ouvrir une résolution.
	openRes, err := actionByID(t, "governance.open_resolution").Run(ctx, deps,
		json.RawMessage(`{"assembly_id":"`+asmID+`","label":"Approuver les comptes ?"}`))
	if err != nil {
		t.Fatalf("open_resolution: %v", err)
	}
	rid := openRes.Data.(map[string]string)["id"]

	cast := actionByID(t, "governance.cast_vote")
	for _, tc := range []struct{ voter, choice string }{
		{"alice", "pour"}, {"bob", "contre"}, {"carol", "abstention"},
	} {
		if _, err := cast.Run(ctx, deps,
			json.RawMessage(`{"resolution_id":"`+rid+`","voter_user_id":"`+tc.voter+`","choice":"`+tc.choice+`"}`)); err != nil {
			t.Fatalf("cast_vote %s: %v", tc.voter, err)
		}
	}

	// Double vote → rejet (status error, jamais d'écrasement).
	dbl, err := cast.Run(ctx, deps,
		json.RawMessage(`{"resolution_id":"`+rid+`","voter_user_id":"alice","choice":"contre"}`))
	if err == nil || dbl.Status != "error" {
		t.Fatalf("double vote: attendu rejet, obtenu status=%s err=%v", dbl.Status, err)
	}

	// Dépouillement exact.
	tallyRes, err := actionByID(t, "governance.tally_resolution").Run(ctx, deps,
		json.RawMessage(`{"resolution_id":"`+rid+`"}`))
	if err != nil {
		t.Fatalf("tally_resolution: %v", err)
	}
	tally := tallyRes.Data.(governance.Tally)
	t.Logf("action tally: pour=%d contre=%d abstention=%d total=%d",
		tally.Pour, tally.Contre, tally.Abstention, tally.Total)
	if tally.Pour != 1 || tally.Contre != 1 || tally.Abstention != 1 || tally.Total != 3 {
		t.Fatalf("dépouillement inattendu: %+v", tally)
	}

	// Clôturer, puis un vote après clôture est rejeté.
	if _, err := actionByID(t, "governance.close_resolution").Run(ctx, deps,
		json.RawMessage(`{"resolution_id":"`+rid+`"}`)); err != nil {
		t.Fatalf("close_resolution: %v", err)
	}
	post, err := cast.Run(ctx, deps,
		json.RawMessage(`{"resolution_id":"`+rid+`","voter_user_id":"bob","choice":"pour"}`))
	if err == nil || post.Status != "error" {
		t.Fatalf("vote après clôture: attendu rejet, obtenu status=%s err=%v", post.Status, err)
	}
}
