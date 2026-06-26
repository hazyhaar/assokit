// CLAUDE:SUMMARY Adaptateur SalonStore → pkg/salon.Store (côté consommateur, découplage).
package transcription

import (
	"context"

	"github.com/hazyhaar/assokit/pkg/salon"
)

// SalonStoreAdapter adapte *salon.Store à l'interface SalonStore.
// Défini côté consommateur (internal/) pour que pkg/connectors/livekit
// et internal/transcription n'importent pas pkg/salon.
type SalonStoreAdapter struct {
	S *salon.Store
}

func (a *SalonStoreAdapter) ShouldTranscribe(ctx context.Context, slug string) (bool, error) {
	return a.S.ShouldTranscribe(ctx, slug)
}

func (a *SalonStoreAdapter) GetSalonID(ctx context.Context, slug string) (string, error) {
	sl, err := a.S.Get(ctx, slug)
	if err != nil {
		return "", err
	}
	return sl.ID, nil
}
