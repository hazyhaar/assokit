// CLAUDE:SUMMARY Tests gardiens PKCE S256+plain (M-ASSOKIT-DCR-5).
package oauth

import (
	"errors"
	"testing"
)

// TestPKCE_S256_VerifierMatches : verifier connu → challenge connu → OK.
func TestPKCE_S256_VerifierMatches(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := ChallengeFromVerifierS256(verifier)
	if err := VerifyPKCE(verifier, challenge, "S256"); err != nil {
		t.Errorf("S256 valid match err = %v, attendu nil", err)
	}
}

// TestPKCE_S256_VerifierMismatch : autre verifier → ErrPKCEMismatch.
func TestPKCE_S256_VerifierMismatch(t *testing.T) {
	challenge := ChallengeFromVerifierS256("verifier-1")
	err := VerifyPKCE("verifier-2-attaquant", challenge, "S256")
	if !errors.Is(err, ErrPKCEMismatch) {
		t.Errorf("err = %v, attendu ErrPKCEMismatch", err)
	}
}

// TestPKCE_PlainMatches : plain method, verifier == challenge → OK.
func TestPKCE_PlainMatches(t *testing.T) {
	if err := VerifyPKCE("abc", "abc", "plain"); err != nil {
		t.Errorf("plain match err = %v", err)
	}
}

// TestPKCE_PlainMismatch : plain, verifier != challenge → erreur.
func TestPKCE_PlainMismatch(t *testing.T) {
	err := VerifyPKCE("abc", "xyz", "plain")
	if !errors.Is(err, ErrPKCEMismatch) {
		t.Errorf("err = %v, attendu ErrPKCEMismatch", err)
	}
}

// TestPKCE_UnsupportedMethod : random method → ErrPKCEMethodUnsupported.
func TestPKCE_UnsupportedMethod(t *testing.T) {
	err := VerifyPKCE("v", "c", "magic")
	if !errors.Is(err, ErrPKCEMethodUnsupported) {
		t.Errorf("err = %v, attendu ErrPKCEMethodUnsupported", err)
	}
}

// TestPKCE_EmptyChallengeRejected : si challenge vide stocké → ErrPKCEMissingChallenge.
func TestPKCE_EmptyChallengeRejected(t *testing.T) {
	err := VerifyPKCE("v", "", "S256")
	if !errors.Is(err, ErrPKCEMissingChallenge) {
		t.Errorf("err = %v, attendu ErrPKCEMissingChallenge", err)
	}
}

// TestPKCE_EmptyVerifierRejected : verifier vide → ErrPKCEMissingVerifier.
func TestPKCE_EmptyVerifierRejected(t *testing.T) {
	err := VerifyPKCE("", "challenge-stocké", "S256")
	if !errors.Is(err, ErrPKCEMissingVerifier) {
		t.Errorf("err = %v, attendu ErrPKCEMissingVerifier", err)
	}
}

// TestPKCE_DefaultMethodIsS256 : method vide (défaut RFC 7636) → S256.
func TestPKCE_DefaultMethodIsS256(t *testing.T) {
	verifier := "abc"
	challenge := ChallengeFromVerifierS256(verifier)
	if err := VerifyPKCE(verifier, challenge, ""); err != nil {
		t.Errorf("default method err = %v, attendu nil (S256 par défaut)", err)
	}
}

// TestPKCE_RFC7636VectorExample : vecteur de test officiel RFC 7636 Appendix B.
// verifier="dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
// challenge="E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
func TestPKCE_RFC7636VectorExample(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	expectedChallenge := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	got := ChallengeFromVerifierS256(verifier)
	if got != expectedChallenge {
		t.Errorf("RFC 7636 vector mismatch : got %q, want %q", got, expectedChallenge)
	}
	if err := VerifyPKCE(verifier, expectedChallenge, "S256"); err != nil {
		t.Errorf("RFC 7636 vector verify err = %v", err)
	}
}
