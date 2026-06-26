package gdpr

import (
	"context"
	"database/sql"
	"testing"

	"github.com/hazyhaar/assokit/internal/chassis"
	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return &Store{DB: db}
}

// TestLogAndListBySubject vérifie l'append puis la lecture filtrée par sujet.
func TestLogAndListBySubject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, tc := range []AccessLog{
		{SubjectKind: SubjectParcelle, SubjectID: "p1", ActorID: "boris", Action: ActionView},
		{SubjectKind: SubjectParcelle, SubjectID: "p1", ActorID: "alice", Action: ActionView},
		{SubjectKind: SubjectParcelle, SubjectID: "p2", ActorID: "boris", Action: ActionView},
	} {
		if _, err := s.Log(ctx, tc); err != nil {
			t.Fatalf("Log: %v", err)
		}
	}

	got, err := s.ListBySubject(ctx, SubjectParcelle, "p1")
	if err != nil {
		t.Fatalf("ListBySubject: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("attendu 2 accès sur p1, obtenu %d", len(got))
	}
}

// TestListForActor vérifie la lecture filtrée par acteur.
func TestListForActor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, _ = s.Log(ctx, AccessLog{SubjectKind: SubjectUser, SubjectID: "u1", ActorID: "boris", Action: ActionView})
	_, _ = s.Log(ctx, AccessLog{SubjectKind: SubjectUser, SubjectID: "u2", ActorID: "boris", Action: ActionExport})
	_, _ = s.Log(ctx, AccessLog{SubjectKind: SubjectUser, SubjectID: "u3", ActorID: "alice", Action: ActionView})

	got, err := s.ListForActor(ctx, "boris")
	if err != nil {
		t.Fatalf("ListForActor: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("attendu 2 accès par boris, obtenu %d", len(got))
	}
}

// TestLogRejectsMissingFields vérifie qu'un champ requis manquant est rejeté
// (fail-loud), sans écrire de ligne incomplète.
func TestLogRejectsMissingFields(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Log(context.Background(), AccessLog{SubjectKind: SubjectUser, SubjectID: "", ActorID: "boris", Action: ActionView}); err == nil {
		t.Fatal("subject_id manquant : Log doit échouer")
	}
}
