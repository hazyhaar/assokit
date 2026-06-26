// CLAUDE:SUMMARY Transcripts de salon : Create/SetStatus/AddSegment/GetTranscript/ListBySalon/Segments. UUIDv7 via pkg/uid. Status machine : pending→recording→transcribing→done|failed.
package transcript

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hazyhaar/assokit/pkg/uid"
)

const tsLayout = "2006-01-02 15:04:05"

// ErrNotFound est retourné quand le transcript demandé n'existe pas.
var ErrNotFound = errors.New("transcript: introuvable")

// Transcript représente l'enregistrement d'une session audio d'un salon.
type Transcript struct {
	ID        string
	SalonID   string
	StartedAt time.Time
	EndedAt   sql.NullTime
	Status    string
}

// Segment est un fragment de parole dans un transcript.
type Segment struct {
	ID           string
	TranscriptID string
	Speaker      string
	T0ms         int64
	T1ms         int64
	Text         string
	CreatedAt    time.Time
}

// Store est le dépôt des transcripts.
type Store struct {
	DB *sql.DB
}

// CreateTranscript insère un nouveau transcript en statut 'pending' et le lie au salon.
func (s *Store) CreateTranscript(ctx context.Context, salonID string) (*Transcript, error) {
	id := uid.New()
	now := time.Now().UTC().Format(tsLayout)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO transcript(id, salon_id, started_at, status) VALUES(?,?,?,'pending')`,
		id, salonID, now,
	)
	if err != nil {
		return nil, fmt.Errorf("transcript.CreateTranscript: %w", err)
	}
	t, err := time.Parse(tsLayout, now)
	if err != nil {
		t = time.Now().UTC()
	}
	return &Transcript{
		ID:        id,
		SalonID:   salonID,
		StartedAt: t,
		Status:    "pending",
	}, nil
}

// SetStatus met à jour le statut du transcript. Si le statut est 'done' ou 'failed',
// ended_at est positionné à l'heure courante.
func (s *Store) SetStatus(ctx context.Context, id, status string) error {
	now := time.Now().UTC().Format(tsLayout)
	var err error
	if status == "done" || status == "failed" {
		_, err = s.DB.ExecContext(ctx,
			`UPDATE transcript SET status=?, ended_at=? WHERE id=?`,
			status, now, id,
		)
	} else {
		_, err = s.DB.ExecContext(ctx,
			`UPDATE transcript SET status=? WHERE id=?`,
			status, id,
		)
	}
	if err != nil {
		return fmt.Errorf("transcript.SetStatus: %w", err)
	}
	return nil
}

// AddSegment insère un segment de parole dans le transcript.
func (s *Store) AddSegment(ctx context.Context, transcriptID, speaker string, t0ms, t1ms int64, text string) (*Segment, error) {
	id := uid.New()
	now := time.Now().UTC().Format(tsLayout)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO transcript_segment(id, transcript_id, speaker, t0_ms, t1_ms, text, created_at)
		 VALUES(?,?,?,?,?,?,?)`,
		id, transcriptID, speaker, t0ms, t1ms, text, now,
	)
	if err != nil {
		return nil, fmt.Errorf("transcript.AddSegment: %w", err)
	}
	createdAt, _ := time.Parse(tsLayout, now)
	return &Segment{
		ID:           id,
		TranscriptID: transcriptID,
		Speaker:      speaker,
		T0ms:         t0ms,
		T1ms:         t1ms,
		Text:         text,
		CreatedAt:    createdAt,
	}, nil
}

// GetTranscript retourne un transcript par son id.
func (s *Store) GetTranscript(ctx context.Context, id string) (*Transcript, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, salon_id, started_at, ended_at, status FROM transcript WHERE id=?`, id)
	tr, err := scanTranscript(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return tr, err
}

// ListBySalon retourne les transcripts d'un salon, du plus récent au plus ancien.
func (s *Store) ListBySalon(ctx context.Context, salonID string) ([]*Transcript, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, salon_id, started_at, ended_at, status
		 FROM transcript WHERE salon_id=? ORDER BY started_at DESC`, salonID)
	if err != nil {
		return nil, fmt.Errorf("transcript.ListBySalon: %w", err)
	}
	defer rows.Close()

	var result []*Transcript
	for rows.Next() {
		tr, err := scanTranscriptRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, tr)
	}
	return result, rows.Err()
}

// Segments retourne les segments d'un transcript, triés par t0_ms croissant.
func (s *Store) Segments(ctx context.Context, transcriptID string) ([]*Segment, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, transcript_id, speaker, t0_ms, t1_ms, text, created_at
		 FROM transcript_segment WHERE transcript_id=? ORDER BY t0_ms`, transcriptID)
	if err != nil {
		return nil, fmt.Errorf("transcript.Segments: %w", err)
	}
	defer rows.Close()

	var result []*Segment
	for rows.Next() {
		var seg Segment
		var createdAt string
		if err := rows.Scan(&seg.ID, &seg.TranscriptID, &seg.Speaker,
			&seg.T0ms, &seg.T1ms, &seg.Text, &createdAt); err != nil {
			return nil, err
		}
		seg.CreatedAt, _ = time.Parse(tsLayout, createdAt)
		result = append(result, &seg)
	}
	return result, rows.Err()
}

// DeleteOlderThan supprime les transcripts dont started_at est antérieur à cutoff.
// La contrainte FK ON DELETE CASCADE supprime automatiquement les segments associés.
// Retourne le nombre de lignes supprimées.
func (s *Store) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM transcript WHERE started_at < ?`,
		cutoff.UTC().Format(tsLayout),
	)
	if err != nil {
		return 0, fmt.Errorf("transcript.DeleteOlderThan: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("transcript.DeleteOlderThan: rows_affected: %w", err)
	}
	return int(n), nil
}

func scanTranscript(row *sql.Row) (*Transcript, error) {
	var tr Transcript
	var startedAt string
	var endedAt sql.NullString
	if err := row.Scan(&tr.ID, &tr.SalonID, &startedAt, &endedAt, &tr.Status); err != nil {
		return nil, err
	}
	tr.StartedAt, _ = time.Parse(tsLayout, startedAt)
	if endedAt.Valid {
		t, _ := time.Parse(tsLayout, endedAt.String)
		tr.EndedAt = sql.NullTime{Time: t, Valid: true}
	}
	return &tr, nil
}

func scanTranscriptRow(rows *sql.Rows) (*Transcript, error) {
	var tr Transcript
	var startedAt string
	var endedAt sql.NullString
	if err := rows.Scan(&tr.ID, &tr.SalonID, &startedAt, &endedAt, &tr.Status); err != nil {
		return nil, err
	}
	tr.StartedAt, _ = time.Parse(tsLayout, startedAt)
	if endedAt.Valid {
		t, _ := time.Parse(tsLayout, endedAt.String)
		tr.EndedAt = sql.NullTime{Time: t, Valid: true}
	}
	return &tr, nil
}
