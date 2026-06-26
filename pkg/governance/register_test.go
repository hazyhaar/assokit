package governance_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hazyhaar/assokit/pkg/governance"
	"github.com/hazyhaar/assokit/pkg/membership"
)

func TestRecordDeliberationNominalFiguresTally(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}
	members := &membership.Store{DB: db}

	conv := convokedAssembly(t, s)
	rid, err := s.OpenResolution(ctx, conv, "Approuver les comptes ?")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, id := range []string{"alice", "bob", "carol"} {
		mkUser(t, db, id)
		mkActiveMember(t, db, id)
	}
	if _, err := s.CastVoteWithChecker(ctx, members, rid, "alice", "pour"); err != nil {
		t.Fatalf("vote alice: %v", err)
	}
	if _, err := s.CastVoteWithChecker(ctx, members, rid, "bob", "pour"); err != nil {
		t.Fatalf("vote bob: %v", err)
	}
	if _, err := s.CastVoteWithChecker(ctx, members, rid, "carol", "contre"); err != nil {
		t.Fatalf("vote carol: %v", err)
	}
	if err := s.CloseResolution(ctx, rid); err != nil {
		t.Fatalf("close: %v", err)
	}

	delibID, err := s.RecordDeliberation(ctx, rid)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if delibID == "" {
		t.Fatal("identifiant de délibération vide")
	}

	register, err := s.ListRegister(ctx, conv)
	if err != nil {
		t.Fatalf("list register: %v", err)
	}
	if len(register) != 1 {
		t.Fatalf("attendu 1 délibération, obtenu %d", len(register))
	}
	d := register[0]
	if d.Pour != 2 || d.Contre != 1 || d.Abstention != 0 || d.Total != 3 {
		t.Fatalf("tally figé inattendu: %+v", d)
	}
	if d.Label != "Approuver les comptes ?" || d.ResolutionID != rid || d.AssemblyID != conv {
		t.Fatalf("délibération inattendue: %+v", d)
	}
	t.Logf("TALLY FIGÉ exact: pour=%d contre=%d abstention=%d total=%d (label=%q)",
		d.Pour, d.Contre, d.Abstention, d.Total, d.Label)
}

func TestRecordDeliberationDoubleRejected(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}

	conv := convokedAssembly(t, s)
	rid, _ := s.OpenResolution(ctx, conv, "Question ?")
	if err := s.CloseResolution(ctx, rid); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := s.RecordDeliberation(ctx, rid); err != nil {
		t.Fatalf("première consignation: %v", err)
	}
	// Seconde consignation de la même résolution → ErrDejaConsigne, registre inchangé.
	if _, err := s.RecordDeliberation(ctx, rid); !errors.Is(err, governance.ErrDejaConsigne) {
		t.Fatalf("double consignation: want ErrDejaConsigne, got %v", err)
	}
	register, _ := s.ListRegister(ctx, conv)
	if len(register) != 1 {
		t.Fatalf("le registre a été altéré par la double consignation: %d entrées", len(register))
	}
	t.Logf("DOUBLE CONSIGNATION rejetée (ErrDejaConsigne), registre inchangé (1 entrée)")
}

func TestRecordDeliberationOpenResolutionRejected(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}

	conv := convokedAssembly(t, s)
	rid, _ := s.OpenResolution(ctx, conv, "Question ?")
	// Résolution OUVERTE (non close) → ErrScrutinNonClos.
	if _, err := s.RecordDeliberation(ctx, rid); !errors.Is(err, governance.ErrScrutinNonClos) {
		t.Fatalf("résolution ouverte: want ErrScrutinNonClos, got %v", err)
	}
	// Résolution inexistante → ErrScrutinNonClos (un scrutin inconnu n'est pas clos).
	if _, err := s.RecordDeliberation(ctx, "resolution-inconnue"); !errors.Is(err, governance.ErrScrutinNonClos) {
		t.Fatalf("résolution inconnue: want ErrScrutinNonClos, got %v", err)
	}
	register, _ := s.ListRegister(ctx, conv)
	if len(register) != 0 {
		t.Fatalf("aucune consignation ne devait avoir lieu, obtenu %d", len(register))
	}
	t.Logf("CONSIGNATION d'une résolution OUVERTE rejetée (ErrScrutinNonClos)")
}

// TestDeliberationImmutable prouve que la délibération consignée est figée : un vote
// ne peut plus changer après clôture, et la valeur stockée ne bouge pas après une
// tentative de re-consignation. Les colonnes figées restent celles du moment du record.
func TestDeliberationImmutable(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}
	members := &membership.Store{DB: db}

	conv := convokedAssembly(t, s)
	rid, _ := s.OpenResolution(ctx, conv, "Question ?")
	mkUser(t, db, "alice")
	mkActiveMember(t, db, "alice")
	if _, err := s.CastVoteWithChecker(ctx, members, rid, "alice", "pour"); err != nil {
		t.Fatalf("vote: %v", err)
	}
	if err := s.CloseResolution(ctx, rid); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := s.RecordDeliberation(ctx, rid); err != nil {
		t.Fatalf("record: %v", err)
	}

	before, _ := s.ListRegister(ctx, conv)
	// Tentative de re-consignation (rejetée) : ne doit pas altérer la valeur figée.
	_, _ = s.RecordDeliberation(ctx, rid)
	after, _ := s.ListRegister(ctx, conv)

	if len(before) != 1 || len(after) != 1 {
		t.Fatalf("registre attendu 1 entrée stable, before=%d after=%d", len(before), len(after))
	}
	b, a := before[0], after[0]
	if b.ID != a.ID || b.Pour != a.Pour || b.Contre != a.Contre || b.Abstention != a.Abstention || b.Total != a.Total {
		t.Fatalf("colonne figée altérée: before=%+v after=%+v", b, a)
	}
	if a.Pour != 1 || a.Total != 1 {
		t.Fatalf("valeur figée inattendue: %+v", a)
	}
	t.Logf("IMMUABILITÉ: délibération figée (id=%s pour=%d total=%d) inchangée après re-consignation", a.ID, a.Pour, a.Total)
}

func TestGeneratePVNominalAndImmutable(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}
	members := &membership.Store{DB: db}

	conv := convokedAssembly(t, s)
	rid, _ := s.OpenResolution(ctx, conv, "Renouveler le bureau ?")
	mkUser(t, db, "alice")
	mkActiveMember(t, db, "alice")
	if _, err := s.CastVoteWithChecker(ctx, members, rid, "alice", "pour"); err != nil {
		t.Fatalf("vote: %v", err)
	}
	if err := s.CloseResolution(ctx, rid); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := s.RecordDeliberation(ctx, rid); err != nil {
		t.Fatalf("record: %v", err)
	}

	pvID, err := s.GeneratePV(ctx, conv)
	if err != nil {
		t.Fatalf("generate PV: %v", err)
	}
	if pvID == "" {
		t.Fatal("identifiant de PV vide")
	}

	m, err := s.GetMinutes(ctx, conv)
	if err != nil {
		t.Fatalf("get minutes: %v", err)
	}
	if !strings.Contains(m.Body, "AG vote") || !strings.Contains(m.Body, "Renouveler le bureau ?") {
		t.Fatalf("corps du PV incomplet: %q", m.Body)
	}
	t.Logf("PV généré:\n%s", m.Body)

	// Seconde génération → rejet, PV immuable.
	if _, err := s.GeneratePV(ctx, conv); !errors.Is(err, governance.ErrPVDejaGenere) {
		t.Fatalf("régénération: want ErrPVDejaGenere, got %v", err)
	}
	m2, _ := s.GetMinutes(ctx, conv)
	if m2.ID != m.ID || m2.Body != m.Body {
		t.Fatal("le PV a été altéré par une tentative de régénération")
	}
	t.Logf("RÉGÉNÉRATION du PV rejetée (ErrPVDejaGenere), PV immuable")
}

// TestGeneratePVRejectedOnPendingClosedResolutions prouve qu'un PV immuable ne peut être
// figé tant qu'une résolution close n'est pas consignée au registre : deux résolutions
// closes, une seule consignée → ErrDeliberationsPendantes ; consignation de la seconde →
// génération réussie, le corps contenant les deux délibérations.
func TestGeneratePVRejectedOnPendingClosedResolutions(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}

	conv := convokedAssembly(t, s)
	rid1, err := s.OpenResolution(ctx, conv, "Approuver les comptes ?")
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	rid2, err := s.OpenResolution(ctx, conv, "Renouveler le bureau ?")
	if err != nil {
		t.Fatalf("open 2: %v", err)
	}
	if err := s.CloseResolution(ctx, rid1); err != nil {
		t.Fatalf("close 1: %v", err)
	}
	if err := s.CloseResolution(ctx, rid2); err != nil {
		t.Fatalf("close 2: %v", err)
	}

	// Une seule des deux résolutions closes est consignée.
	if _, err := s.RecordDeliberation(ctx, rid1); err != nil {
		t.Fatalf("record 1: %v", err)
	}

	// GeneratePV doit rejeter : la seconde résolution close n'est pas consignée.
	if _, err := s.GeneratePV(ctx, conv); !errors.Is(err, governance.ErrDeliberationsPendantes) {
		t.Fatalf("PV registre incomplet: want ErrDeliberationsPendantes, got %v", err)
	}
	if _, err := s.GetMinutes(ctx, conv); !errors.Is(err, governance.ErrMinutesIntrouvable) {
		t.Fatalf("aucun PV ne devait être figé tant que le registre est incomplet: got %v", err)
	}
	t.Logf("PV REJETÉ tant qu'une résolution close n'est pas consignée (ErrDeliberationsPendantes)")

	// Consignation de la seconde résolution : le registre est désormais complet.
	if _, err := s.RecordDeliberation(ctx, rid2); err != nil {
		t.Fatalf("record 2: %v", err)
	}
	pvID, err := s.GeneratePV(ctx, conv)
	if err != nil {
		t.Fatalf("generate PV après registre complet: %v", err)
	}
	if pvID == "" {
		t.Fatal("identifiant de PV vide")
	}
	m, err := s.GetMinutes(ctx, conv)
	if err != nil {
		t.Fatalf("get minutes: %v", err)
	}
	if !strings.Contains(m.Body, "Approuver les comptes ?") || !strings.Contains(m.Body, "Renouveler le bureau ?") {
		t.Fatalf("le corps du PV ne contient pas les deux délibérations: %q", m.Body)
	}
	t.Logf("PV ACCEPTÉ une fois le registre complet (2 délibérations consignées):\n%s", m.Body)
}

func TestGeneratePVRejectedOnNonConvokedAssembly(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}

	// AG en brouillon (drafting) → rejet ErrAssembleeNonConvoquee.
	draft, err := s.Create(ctx, governance.Assembly{Name: "AG brouillon"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.GeneratePV(ctx, draft); !errors.Is(err, governance.ErrAssembleeNonConvoquee) {
		t.Fatalf("PV sur AG drafting: want ErrAssembleeNonConvoquee, got %v", err)
	}
	if _, err := s.GetMinutes(ctx, draft); !errors.Is(err, governance.ErrMinutesIntrouvable) {
		t.Fatalf("aucun PV ne devait être généré: got %v", err)
	}
	t.Logf("PV sur AG drafting rejeté (ErrAssembleeNonConvoquee)")
}
