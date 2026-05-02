package adminpanel

import (
	"testing"
)

// TestRegistry_V0FieldsCount vérifie que V0Fields retourne exactement 25 champs.
func TestRegistry_V0FieldsCount(t *testing.T) {
	fields := V0Fields()
	if len(fields) != 25 {
		t.Errorf("V0Fields : attendu 25 champs, got %d", len(fields))
	}
}

// TestRegistry_IBANValidation : IBAN valide → nil, invalide → erreur.
func TestRegistry_IBANValidation(t *testing.T) {
	ibanField := Field{Key: "virement.iban", Kind: "iban", Required: true}

	// IBAN français valide (exemple de test — conforme modulo 97)
	validIBAN := "FR7630006000011234567890189"
	if err := ValidateField(ibanField, validIBAN); err != nil {
		t.Errorf("IBAN valide %q attendu nil, got %v", validIBAN, err)
	}

	// IBAN invalide
	invalidIBAN := "FR0000000000000000000000000"
	if err := ValidateField(ibanField, invalidIBAN); err == nil {
		t.Errorf("IBAN invalide %q attendu erreur, got nil", invalidIBAN)
	}

	// Valeur vide = OK (pas de validation)
	if err := ValidateField(ibanField, ""); err != nil {
		t.Errorf("IBAN vide attendu nil, got %v", err)
	}
}

// TestRegistry_AllRequiredFieldsHaveHint vérifie que tous les champs Required ont un Hint non vide.
func TestRegistry_AllRequiredFieldsHaveHint(t *testing.T) {
	for _, f := range V0Fields() {
		if f.Required && f.Hint == "" {
			t.Errorf("champ %q required mais Hint vide", f.Key)
		}
	}
}
