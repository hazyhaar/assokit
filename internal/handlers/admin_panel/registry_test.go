package adminpanel

import (
	"testing"
)

// TestRegistry_V0FieldsCount vérifie 25 V0 + 14 V0.1 = 39 champs.
func TestRegistry_V0FieldsCount(t *testing.T) {
	fields := V0Fields()
	if len(fields) != 40 {
		t.Errorf("V0Fields : attendu 40 champs (25 V0 + 15 V0.1 : 5 legal + 1 charte + 9 quisommesnous), got %d", len(fields))
	}
}

// TestRegistry_V01FieldsPresent : V0.1 a ajouté 5 legal + 1 charte + 8 quisommesnous.
func TestRegistry_V01FieldsPresent(t *testing.T) {
	bySection := FieldsBySection(V0Fields())
	cases := map[string]int{
		"legal":         5,
		"charte":        1,
		"quisommesnous": 9, // 8 + photo_equipe = 9
	}
	for section, want := range cases {
		got := len(bySection[section])
		if got != want {
			t.Errorf("section %q : %d champs, attendu %d", section, got, want)
		}
	}
	// V0 sections preserved
	for _, sec := range []string{"identite", "presentation", "helloasso", "virement"} {
		if len(bySection[sec]) == 0 {
			t.Errorf("section V0 %q absente après extension V0.1", sec)
		}
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
