// Garde permanent : la sentinelle de brouillon est dupliquée (miroir) entre social
// et tiktok pour éviter un cycle d'import (tiktok importe social). Une dérive d'une
// seule des deux casserait silencieusement la détection de brouillon du journal.
// Ce test verrouille leur égalité byte-à-byte. Promu garde au sous-goal S5.
package social_test

import (
	"testing"

	"github.com/hazyhaar/assokit/pkg/connectors/social"
	"github.com/hazyhaar/assokit/pkg/connectors/social/tiktok"
)

func TestDraftSentinelMirror(t *testing.T) {
	if social.DraftPendingURL != tiktok.DraftPending {
		t.Fatalf("sentinelles de brouillon désynchronisées:\n social.DraftPendingURL = %q\n tiktok.DraftPending    = %q",
			social.DraftPendingURL, tiktok.DraftPending)
	}
}
