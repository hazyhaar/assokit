// CLAUDE:SUMMARY Adaptateurs vers pkg/transcript.Store : TranscriptStore (launcher),
// BatchStore (batch), RetentionStore (retention TTL).
package transcription

import (
	"context"
	"time"

	"github.com/hazyhaar/assokit/pkg/transcript"
)

// TranscriptStoreAdapter adapte *transcript.Store à l'interface TranscriptStore.
type TranscriptStoreAdapter struct {
	S *transcript.Store
}

func (a *TranscriptStoreAdapter) CreateTranscript(ctx context.Context, salonID string) (TranscriptResult, error) {
	tr, err := a.S.CreateTranscript(ctx, salonID)
	if err != nil {
		return TranscriptResult{}, err
	}
	return TranscriptResult{ID: tr.ID}, nil
}

func (a *TranscriptStoreAdapter) SetStatus(ctx context.Context, id, status string) error {
	return a.S.SetStatus(ctx, id, status)
}

// BatchStoreAdapter adapte *transcript.Store à l'interface BatchStore.
type BatchStoreAdapter struct {
	S *transcript.Store
}

func (a *BatchStoreAdapter) SetStatus(ctx context.Context, id, status string) error {
	return a.S.SetStatus(ctx, id, status)
}

// AddSegment absorbe la valeur de retour *transcript.Segment (non nécessaire côté batch).
func (a *BatchStoreAdapter) AddSegment(ctx context.Context, transcriptID, speaker string, t0ms, t1ms int64, text string) error {
	_, err := a.S.AddSegment(ctx, transcriptID, speaker, t0ms, t1ms, text)
	return err
}

// RetentionStoreAdapter adapte *transcript.Store à l'interface RetentionStore.
type RetentionStoreAdapter struct {
	S *transcript.Store
}

func (a *RetentionStoreAdapter) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	return a.S.DeleteOlderThan(ctx, cutoff)
}
