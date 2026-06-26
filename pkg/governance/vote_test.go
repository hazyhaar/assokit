package governance_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hazyhaar/assokit/pkg/governance"
	"github.com/hazyhaar/assokit/pkg/membership"
)

// convokedAssembly crée une AG et la convoque, prête à recevoir des résolutions.
func convokedAssembly(t *testing.T, s *governance.Store) string {
	t.Helper()
	ctx := context.Background()
	id, err := s.Create(ctx, governance.Assembly{Name: "AG vote"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.MarkConvoked(ctx, id); err != nil {
		t.Fatalf("convoke: %v", err)
	}
	return id
}

func TestOpenResolutionRequiresConvokedAssembly(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}

	// AG en brouillon (non convoquée) → rejet.
	draft, err := s.Create(ctx, governance.Assembly{Name: "brouillon"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.OpenResolution(ctx, draft, "Approuver les comptes ?"); !errors.Is(err, governance.ErrAssembleeNonConvoquee) {
		t.Fatalf("want ErrAssembleeNonConvoquee, got %v", err)
	}

	// Libellé vide → rejet, même sur une AG convoquée.
	conv := convokedAssembly(t, s)
	if _, err := s.OpenResolution(ctx, conv, "   "); !errors.Is(err, governance.ErrResolutionLabelManquant) {
		t.Fatalf("want ErrResolutionLabelManquant, got %v", err)
	}

	// AG convoquée + libellé → succès.
	rid, err := s.OpenResolution(ctx, conv, "Approuver les comptes ?")
	if err != nil {
		t.Fatalf("open resolution: %v", err)
	}
	res, err := s.GetResolution(ctx, rid)
	if err != nil {
		t.Fatalf("get resolution: %v", err)
	}
	if res.Status != "open" || res.Label != "Approuver les comptes ?" {
		t.Fatalf("résolution inattendue: %+v", res)
	}
}

func TestCastVoteNominalAndTally(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}
	members := &membership.Store{DB: db}

	conv := convokedAssembly(t, s)
	rid, err := s.OpenResolution(ctx, conv, "Renouveler le bureau ?")
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Trois adhérents actifs réels (Stores réels, jamais hardcodés).
	for _, id := range []string{"alice", "bob", "carol"} {
		mkUser(t, db, id)
		mkActiveMember(t, db, id)
	}

	if _, err := s.CastVoteWithChecker(ctx, members, rid, "alice", "pour"); err != nil {
		t.Fatalf("vote alice: %v", err)
	}
	if _, err := s.CastVoteWithChecker(ctx, members, rid, "bob", "contre"); err != nil {
		t.Fatalf("vote bob: %v", err)
	}
	if _, err := s.CastVoteWithChecker(ctx, members, rid, "carol", "abstention"); err != nil {
		t.Fatalf("vote carol: %v", err)
	}

	tally, err := s.Tally(ctx, rid)
	if err != nil {
		t.Fatalf("tally: %v", err)
	}
	if tally.Pour != 1 || tally.Contre != 1 || tally.Abstention != 1 || tally.Total != 3 {
		t.Fatalf("tally inattendu: %+v", tally)
	}
	t.Logf("TALLY nominal: pour=%d contre=%d abstention=%d total=%d",
		tally.Pour, tally.Contre, tally.Abstention, tally.Total)
}

func TestCastVoteDoubleVoteRejected(t *testing.T) {
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
		t.Fatalf("premier vote: %v", err)
	}
	// Second vote du même votant → ErrDejaVote (jamais d'écrasement).
	if _, err := s.CastVoteWithChecker(ctx, members, rid, "alice", "contre"); !errors.Is(err, governance.ErrDejaVote) {
		t.Fatalf("double vote: want ErrDejaVote, got %v", err)
	}
	// Le bulletin initial est intact (pour=1, total=1).
	tally, _ := s.Tally(ctx, rid)
	if tally.Pour != 1 || tally.Total != 1 {
		t.Fatalf("le double vote a altéré le dépouillement: %+v", tally)
	}
	t.Logf("DOUBLE VOTE rejeté (ErrDejaVote), bulletin initial préservé: %+v", tally)
}

func TestCastVoteAfterCloseRejected(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}
	members := &membership.Store{DB: db}

	conv := convokedAssembly(t, s)
	rid, _ := s.OpenResolution(ctx, conv, "Question ?")
	mkUser(t, db, "alice")
	mkActiveMember(t, db, "alice")

	if err := s.CloseResolution(ctx, rid); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Seconde clôture → ErrPasCloturable.
	if err := s.CloseResolution(ctx, rid); !errors.Is(err, governance.ErrPasCloturable) {
		t.Fatalf("double clôture: want ErrPasCloturable, got %v", err)
	}
	// Vote après clôture → ErrScrutinClos.
	if _, err := s.CastVoteWithChecker(ctx, members, rid, "alice", "pour"); !errors.Is(err, governance.ErrScrutinClos) {
		t.Fatalf("vote après clôture: want ErrScrutinClos, got %v", err)
	}
	t.Logf("VOTE après clôture rejeté (ErrScrutinClos)")
}

func TestCastVoteInactiveOrNonMemberRejected(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}
	members := &membership.Store{DB: db}

	conv := convokedAssembly(t, s)
	rid, _ := s.OpenResolution(ctx, conv, "Question ?")

	// Non-membre : utilisateur existant sans adhésion active.
	mkUser(t, db, "dave")
	if _, err := s.CastVoteWithChecker(ctx, members, rid, "dave", "pour"); !errors.Is(err, governance.ErrVotantInactif) {
		t.Fatalf("vote non-membre: want ErrVotantInactif, got %v", err)
	}

	// Inactif : adhésion réelle mais expirée, donc CurrentStatus != active.
	mkUser(t, db, "erin")
	if _, err := (&membership.Store{DB: db}).Create(ctx, membership.Membership{
		UserID:      "erin",
		PeriodStart: "2020-01-01",
		PeriodEnd:   "2020-12-31",
		AmountCents: 1000,
		Status:      "expired",
	}); err != nil {
		t.Fatalf("create adhésion expirée: %v", err)
	}
	if _, err := s.CastVoteWithChecker(ctx, members, rid, "erin", "pour"); !errors.Is(err, governance.ErrVotantInactif) {
		t.Fatalf("vote inactif: want ErrVotantInactif, got %v", err)
	}
	t.Logf("VOTE non-membre et inactif rejetés (ErrVotantInactif)")
}

func TestCastVoteInvalidChoiceRejected(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}
	members := &membership.Store{DB: db}

	conv := convokedAssembly(t, s)
	rid, _ := s.OpenResolution(ctx, conv, "Question ?")
	mkUser(t, db, "alice")
	mkActiveMember(t, db, "alice")

	if _, err := s.CastVoteWithChecker(ctx, members, rid, "alice", "blanc"); !errors.Is(err, governance.ErrChoixInvalide) {
		t.Fatalf("choix invalide: want ErrChoixInvalide, got %v", err)
	}
	t.Logf("CHOIX invalide rejeté (ErrChoixInvalide)")
}

// TestCastVoteActiveButOutOfPeriodRejected prouve que la voie de vote unique
// (CastVoteWithChecker) rejette un adhérent au statut 'active' mais dont la période est
// échue : la garde est CurrentStatus (statut ET période), exactement la définition sur
// laquelle la liste déroulante de vote du handler est alignée. Aucun bulletin n'est
// enregistré pour un votant hors période.
func TestCastVoteActiveButOutOfPeriodRejected(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}
	members := &membership.Store{DB: db}

	conv := convokedAssembly(t, s)
	rid, _ := s.OpenResolution(ctx, conv, "Question ?")

	// Adhésion au statut 'active' mais période passée → CurrentStatus != active.
	mkUser(t, db, "frank")
	if _, err := members.Create(ctx, membership.Membership{
		UserID:      "frank",
		PeriodStart: "2020-01-01",
		PeriodEnd:   "2020-12-31",
		AmountCents: 1000,
		Status:      "active",
	}); err != nil {
		t.Fatalf("create adhésion active hors période: %v", err)
	}
	if _, err := s.CastVoteWithChecker(ctx, members, rid, "frank", "pour"); !errors.Is(err, governance.ErrVotantInactif) {
		t.Fatalf("vote actif hors période: want ErrVotantInactif, got %v", err)
	}
	tally, _ := s.Tally(ctx, rid)
	if tally.Total != 0 {
		t.Fatalf("un bulletin a été enregistré pour un votant hors période: %+v", tally)
	}
	t.Logf("VOTE actif hors période rejeté (ErrVotantInactif), aucun bulletin enregistré")
}

func TestCastVoteOnUnknownResolutionRejected(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &governance.Store{DB: db}
	members := &membership.Store{DB: db}
	mkUser(t, db, "alice")
	mkActiveMember(t, db, "alice")

	// Résolution inexistante → ErrScrutinClos (un scrutin inconnu n'est pas ouvert).
	if _, err := s.CastVoteWithChecker(ctx, members, "resolution-inconnue", "alice", "pour"); !errors.Is(err, governance.ErrScrutinClos) {
		t.Fatalf("résolution inconnue: want ErrScrutinClos, got %v", err)
	}
}
