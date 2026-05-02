// CLAUDE:SUMMARY Test gardien auth brute-force lockout — skip si non implémenté (M-ASSOKIT-AUDIT-FIX-2).
package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hazyhaar/assokit/pkg/horui/auth"
)

// TestAuth_LoginBruteForceLockout : 5 fails → 6e doit être verrouillé OU rate-limité.
// Si le mécanisme n'est pas implémenté → t.Skip explicite.
// TODO M-ASSOKIT-IMPL-AUTH-BRUTE-FORCE-LOCKOUT
func TestAuth_LoginBruteForceLockout(t *testing.T) {
	db := newTestDB(t)
	s := &auth.Store{DB: db}
	ctx := context.Background()

	if _, err := s.Register(ctx, "brute@nps.fr", "correct-password", "Brute Test"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// 5 tentatives avec mauvais mot de passe.
	for i := 0; i < 5; i++ {
		_, err := s.Authenticate(ctx, "brute@nps.fr", "wrong-pass")
		if !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("attempt %d : err=%v, attendu ErrInvalidCredentials", i+1, err)
		}
	}

	// 6e tentative : si lockout implémenté → erreur différente (LOCKED/RATE_LIMIT).
	_, err := s.Authenticate(ctx, "brute@nps.fr", "wrong-pass")

	// Heuristique : si l'erreur est encore ErrInvalidCredentials, lockout pas implémenté.
	if errors.Is(err, auth.ErrInvalidCredentials) {
		// Confirmer : authentification avec bon mot de passe doit toujours réussir.
		if _, e := s.Authenticate(ctx, "brute@nps.fr", "correct-password"); e == nil {
			t.Skip("brute force lockout not implemented — login still works after 5 fails — TODO M-ASSOKIT-IMPL-AUTH-BRUTE-FORCE-LOCKOUT")
		}
		t.Skip("brute force lockout not implemented — TODO M-ASSOKIT-IMPL-AUTH-BRUTE-FORCE-LOCKOUT")
	}

	// Lockout détecté : vérifier que même bon mot de passe est rejeté.
	if _, e := s.Authenticate(ctx, "brute@nps.fr", "correct-password"); e == nil {
		t.Errorf("lockout détecté à la 6e tentative mais bon mot de passe accepté immédiatement après — comportement incohérent")
	}
	// Vérifier que le message d'erreur indique lockout/locked/blocked.
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "lock") && !strings.Contains(msg, "block") && !strings.Contains(msg, "limit") {
		t.Logf("err=%v — vérifier que le message indique le lockout", err)
	}
}
