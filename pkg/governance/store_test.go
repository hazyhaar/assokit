package governance_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/governance"
	"github.com/hazyhaar/assokit/pkg/membership"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func mkUser(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO users(id, email, password_hash, display_name, is_active) VALUES(?,?,?,?,?)`,
		id, id+"@example.org", "x", id, 1,
	)
	if err != nil {
		t.Fatalf("mkUser %s: %v", id, err)
	}
}

// mkActiveMember crée une adhésion ACTIVE réelle (via membership.Store) couvrant la
// date du jour, pour un utilisateur existant. Données par les Stores, jamais
// hardcodées en base.
func mkActiveMember(t *testing.T, db *sql.DB, userID string) {
	t.Helper()
	if _, err := (&membership.Store{DB: db}).Create(context.Background(), membership.Membership{
		UserID:      userID,
		PeriodStart: "2020-01-01",
		PeriodEnd:   "2099-12-31",
		AmountCents: 1000,
		Status:      "active",
	}); err != nil {
		t.Fatalf("mkActiveMember %s: %v", userID, err)
	}
}

func TestCreateRoundTrip(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()

	s := &governance.Store{DB: db}
	id, err := s.Create(ctx, governance.Assembly{
		Name:        "Assemblée générale ordinaire 2026",
		Agenda:      "Rapport moral, comptes, renouvellement du bureau.",
		ScheduledAt: "2026-09-12 18:00",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Assemblée générale ordinaire 2026" || got.Status != "drafting" || got.ScheduledAt != "2026-09-12 18:00" {
		t.Fatalf("assemblée inattendue: %+v", got)
	}
	if got.Agenda == "" {
		t.Fatal("ordre du jour vide")
	}
}

func TestCreateValidation(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}

	for _, tc := range []struct {
		name string
		asm  governance.Assembly
		want error
	}{
		{"nom vide", governance.Assembly{Agenda: "x"}, governance.ErrNameManquant},
		{"statut invalide", governance.Assembly{Name: "AG", Status: "bogus"}, governance.ErrStatutInvalide},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Create(ctx, tc.asm)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestListAll(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}

	if _, err := s.Create(ctx, governance.Assembly{Name: "AG 1"}); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	if _, err := s.Create(ctx, governance.Assembly{Name: "AG 2"}); err != nil {
		t.Fatalf("create 2: %v", err)
	}
	list, err := s.ListAll(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("attendu 2 assemblées, obtenu %d", len(list))
	}
}

func TestGetByIDUnknown(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	if _, err := (&governance.Store{DB: db}).GetByID(context.Background(), "inconnu"); !errors.Is(err, governance.ErrAssemblyNotFound) {
		t.Fatalf("want ErrAssemblyNotFound, got %v", err)
	}
}

func TestMarkConvokedTransition(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}

	id, err := s.Create(ctx, governance.Assembly{Name: "AG"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.MarkConvoked(ctx, id); err != nil {
		t.Fatalf("mark convoked: %v", err)
	}
	got, _ := s.GetByID(ctx, id)
	if got.Status != "convoked" {
		t.Fatalf("statut attendu convoked, obtenu %s", got.Status)
	}
	// Second appel : déjà convoquée, plus en brouillon → refus.
	if err := s.MarkConvoked(ctx, id); !errors.Is(err, governance.ErrPasConvocable) {
		t.Fatalf("seconde convocation: want ErrPasConvocable, got %v", err)
	}
	// Assemblée inconnue → ErrAssemblyNotFound.
	if err := s.MarkConvoked(ctx, "inconnu"); !errors.Is(err, governance.ErrAssemblyNotFound) {
		t.Fatalf("convoke inconnu: want ErrAssemblyNotFound, got %v", err)
	}
}

func TestRecordConvocationFailsLoudOnUnknownUser(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}
	id, err := s.Create(ctx, governance.Assembly{Name: "AG"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// FK user_id inconnu → échec fail-loud.
	if err := s.RecordConvocation(ctx, id, "user-inexistant"); err == nil {
		t.Fatal("un utilisateur inconnu doit faire échouer la trace (FK)")
	}
	// Assemblée inconnue → échec fail-loud.
	mkUser(t, db, "alice")
	if err := s.RecordConvocation(ctx, "assemblee-inexistante", "alice"); err == nil {
		t.Fatal("une assemblée inconnue doit faire échouer la trace (FK)")
	}
	// Cas nominal.
	if err := s.RecordConvocation(ctx, id, "alice"); err != nil {
		t.Fatalf("record nominal: %v", err)
	}
}

// TestSignAttendanceIdempotent : deux émargements du même membre → une seule ligne,
// pas d'erreur dure, même ID retourné.
func TestSignAttendanceIdempotent(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}

	mkUser(t, db, "alice")
	asmID, err := s.Create(ctx, governance.Assembly{Name: "AG"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.MarkConvoked(ctx, asmID); err != nil {
		t.Fatalf("convoke: %v", err)
	}

	id1, err := s.SignAttendance(ctx, asmID, "alice", "manual")
	if err != nil {
		t.Fatalf("sign 1: %v", err)
	}
	id2, err := s.SignAttendance(ctx, asmID, "alice", "manual")
	if err != nil {
		t.Fatalf("sign 2 (idempotent attendu): %v", err)
	}
	if id1 != id2 {
		t.Fatalf("idempotence: 2e émargement doit retourner le même ID (%s != %s)", id1, id2)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM attendance WHERE assembly_id=? AND member_user_id=?`, asmID, "alice").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("attendu 1 ligne d'émargement, obtenu %d", n)
	}
}

// TestRecordMandateRejets : les trois rejets typés (principal==proxy, mandant non
// actif, doublon).
func TestRecordMandateRejets(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}
	members := &membership.Store{DB: db}

	mkUser(t, db, "alice")
	mkUser(t, db, "bob")
	mkUser(t, db, "carol") // non adhérent
	mkActiveMember(t, db, "alice")
	mkActiveMember(t, db, "bob")

	asmID, err := s.Create(ctx, governance.Assembly{Name: "AG"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.MarkConvoked(ctx, asmID); err != nil {
		t.Fatalf("convoke: %v", err)
	}

	// Rejet 1 : principal == proxy.
	if _, err := s.RecordMandate(ctx, members, asmID, "alice", "alice"); !errors.Is(err, governance.ErrMandatSurSoi) {
		t.Fatalf("want ErrMandatSurSoi, got %v", err)
	}
	// Rejet 2 : mandant non adhérent actif (carol).
	if _, err := s.RecordMandate(ctx, members, asmID, "carol", "alice"); !errors.Is(err, governance.ErrMandantInactif) {
		t.Fatalf("want ErrMandantInactif, got %v", err)
	}
	// Rejet 2bis : mandataire non adhérent actif (carol comme proxy).
	if _, err := s.RecordMandate(ctx, members, asmID, "alice", "carol"); !errors.Is(err, governance.ErrMandataireInactif) {
		t.Fatalf("want ErrMandataireInactif, got %v", err)
	}
	// Cas nominal : alice mandate bob.
	if _, err := s.RecordMandate(ctx, members, asmID, "alice", "bob"); err != nil {
		t.Fatalf("mandat nominal: %v", err)
	}
	// Rejet 3 : doublon (alice a déjà un mandat sur cette AG).
	if _, err := s.RecordMandate(ctx, members, asmID, "alice", "bob"); !errors.Is(err, governance.ErrMandatExistant) {
		t.Fatalf("want ErrMandatExistant, got %v", err)
	}
}

// TestGardeAGNonConvoquee : ni émargement ni mandat ne sont acceptés tant que l'AG
// n'est pas convoquée (drafting refusé ; cancelled refusé).
func TestGardeAGNonConvoquee(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}
	members := &membership.Store{DB: db}

	mkUser(t, db, "alice")
	mkUser(t, db, "bob")
	mkActiveMember(t, db, "alice")
	mkActiveMember(t, db, "bob")

	// AG en brouillon (drafting) : émargement refusé.
	draftID, err := s.Create(ctx, governance.Assembly{Name: "AG brouillon"})
	if err != nil {
		t.Fatalf("create drafting: %v", err)
	}
	if _, err := s.SignAttendance(ctx, draftID, "alice", "manual"); !errors.Is(err, governance.ErrAssembleeNonConvoquee) {
		t.Fatalf("émargement sur AG drafting: want ErrAssembleeNonConvoquee, got %v", err)
	}
	// Mandat sur la même AG en brouillon : refusé aussi.
	if _, err := s.RecordMandate(ctx, members, draftID, "alice", "bob"); !errors.Is(err, governance.ErrAssembleeNonConvoquee) {
		t.Fatalf("mandat sur AG drafting: want ErrAssembleeNonConvoquee, got %v", err)
	}

	// AG annulée (cancelled) : mandat refusé.
	cancelID, err := s.Create(ctx, governance.Assembly{Name: "AG annulée", Status: "cancelled"})
	if err != nil {
		t.Fatalf("create cancelled: %v", err)
	}
	if _, err := s.RecordMandate(ctx, members, cancelID, "alice", "bob"); !errors.Is(err, governance.ErrAssembleeNonConvoquee) {
		t.Fatalf("mandat sur AG cancelled: want ErrAssembleeNonConvoquee, got %v", err)
	}
}

// TestComputeQuorum : N adhérents actifs réels + 1 non actif, certains émargés, un
// mandat ; vérifie l'exclusion du non-actif, le décompte et le non-double-comptage
// d'un adhérent présent ET représenté.
func TestComputeQuorum(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}
	members := &membership.Store{DB: db}

	// 4 adhérents actifs réels + 1 non actif.
	active := []string{"alice", "bob", "carol", "dora"}
	for _, u := range active {
		mkUser(t, db, u)
		mkActiveMember(t, db, u)
	}
	mkUser(t, db, "eve") // non actif (aucune adhésion active)

	asmID, err := s.Create(ctx, governance.Assembly{Name: "AG"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.MarkConvoked(ctx, asmID); err != nil {
		t.Fatalf("convoke: %v", err)
	}

	// Population éligible résolue par l'appelant (adhérents actifs).
	msAll, err := members.ListAll(ctx, "active")
	if err != nil {
		t.Fatalf("listall: %v", err)
	}
	var eligibleIDs []string
	seen := map[string]struct{}{}
	for _, m := range msAll {
		if _, ok := seen[m.UserID]; ok {
			continue
		}
		seen[m.UserID] = struct{}{}
		eligibleIDs = append(eligibleIDs, m.UserID)
	}

	// alice et bob émargent. eve émarge aussi mais n'est PAS éligible → exclue.
	for _, u := range []string{"alice", "bob", "eve"} {
		if _, err := s.SignAttendance(ctx, asmID, u, "manual"); err != nil {
			t.Fatalf("sign %s: %v", u, err)
		}
	}
	// carol (éligible, absente) mandate bob (éligible, présent) → carol représentée.
	if _, err := s.RecordMandate(ctx, members, asmID, "carol", "bob"); err != nil {
		t.Fatalf("mandat carol→bob: %v", err)
	}
	// alice (présente) mandate dora (absente) : alice est déjà présente → ce mandat
	// ne doit PAS faire compter alice deux fois (mandataire dora absent de surcroît).
	if _, err := s.RecordMandate(ctx, members, asmID, "alice", "dora"); err != nil {
		t.Fatalf("mandat alice→dora: %v", err)
	}

	q, err := s.ComputeQuorum(ctx, asmID, eligibleIDs, 0.5)
	if err != nil {
		t.Fatalf("quorum: %v", err)
	}
	t.Logf("quorum counts: eligible=%d present=%d represented=%d attending=%d reached=%v",
		q.Eligible, q.Present, q.Represented, q.Attending, q.Reached)

	if q.Eligible != 4 {
		t.Fatalf("Eligible attendu 4 (eve non actif exclu), obtenu %d", q.Eligible)
	}
	// Présents éligibles : alice, bob (eve exclue).
	if q.Present != 2 {
		t.Fatalf("Present attendu 2, obtenu %d", q.Present)
	}
	// Représentés : carol (mandant éligible absent, mandataire bob présent éligible).
	// Le mandat alice→dora ne compte pas (alice déjà présente ; dora absente).
	if q.Represented != 1 {
		t.Fatalf("Represented attendu 1, obtenu %d", q.Represented)
	}
	// Attending sans double comptage : alice + bob + carol = 3.
	if q.Attending != 3 {
		t.Fatalf("Attending attendu 3 (sans double comptage), obtenu %d", q.Attending)
	}
	// ceil(4*0.5)=2 ; 3 >= 2 → atteint.
	if !q.Reached {
		t.Fatalf("Reached attendu true (3 >= 2)")
	}
}

// TestComputeQuorumEmptyAFP : une AFP sans adhérent éligible ne peut jamais atteindre
// le quorum, quel que soit le threshold — un corps électoral vide ne délibère pas.
// Sans ce garde, needed=ceil(0*threshold)=0 et Attending(0)>=0 rendrait Reached vrai.
func TestComputeQuorumEmptyAFP(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}

	asmID, err := s.Create(ctx, governance.Assembly{Name: "AG vide"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.MarkConvoked(ctx, asmID); err != nil {
		t.Fatalf("convoke: %v", err)
	}

	for _, threshold := range []float64{0, 0.5, 1} {
		q, err := s.ComputeQuorum(ctx, asmID, nil, threshold)
		if err != nil {
			t.Fatalf("quorum threshold=%v: %v", threshold, err)
		}
		if q.Eligible != 0 {
			t.Fatalf("Eligible attendu 0, obtenu %d", q.Eligible)
		}
		if q.Reached {
			t.Fatalf("Reached attendu false sur AFP vide (threshold=%v), obtenu true", threshold)
		}
	}
}
