// CLAUDE:SUMMARY registry — V0Fields() définit les 25 champs branding, FieldsBySection() groupe par section, ValidateField() + IBAN modulo 97.
package adminpanel

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode"
)

// Field décrit un champ de branding administrable.
type Field struct {
	Key         string
	Section     string
	Order       int
	Label       string
	Hint        string
	Kind        string // text,longtext,url,iban,bic,file,color,int
	Placeholder string
	Required    bool
	MaxBytes    int
	MimeAllow   []string
}

// V0Fields retourne les 25 champs du panel V0.
func V0Fields() []Field {
	return []Field{
		// IDENTITE
		{Key: "identite.nom_asso", Section: "identite", Order: 1, Label: "Nom complet de l'association", Hint: "Tel qu'écrit dans les statuts", Kind: "text", Required: true},
		{Key: "identite.sigle", Section: "identite", Order: 2, Label: "Sigle ou nom court", Hint: "Si différent du nom complet", Kind: "text"},
		{Key: "identite.date_creation", Section: "identite", Order: 3, Label: "Date de création", Hint: "JJ/MM/AAAA", Kind: "text", Required: true},
		{Key: "identite.rna", Section: "identite", Order: 4, Label: "Numéro RNA", Hint: "Le W suivi de 9 chiffres, sur le récépissé de déclaration en préfecture", Kind: "text", Required: true},
		{Key: "identite.siret", Section: "identite", Order: 5, Label: "Numéro SIRET (si vous en avez un)", Hint: "14 chiffres", Kind: "text"},
		{Key: "identite.siege_adresse", Section: "identite", Order: 6, Label: "Adresse du siège social", Hint: "Numéro, rue, code postal, ville", Kind: "longtext", Required: true},
		{Key: "identite.president_nom", Section: "identite", Order: 7, Label: "Nom du président", Hint: "Nom et prénom", Kind: "text", Required: true},
		{Key: "identite.contact_email", Section: "identite", Order: 8, Label: "Email de contact public", Hint: "Sera affiché sur le site", Kind: "text", Required: true},
		{Key: "identite.contact_telephone", Section: "identite", Order: 9, Label: "Téléphone (optionnel)", Hint: "Si vous voulez l'afficher publiquement", Kind: "text"},

		// PRESENTATION
		{Key: "presentation.slogan", Section: "presentation", Order: 1, Label: "Slogan de l'asso", Hint: "Une phrase courte", Kind: "text", Required: true},
		{Key: "presentation.accroche_home", Section: "presentation", Order: 2, Label: "Texte d'accroche page d'accueil", Hint: "3 à 5 lignes", Kind: "longtext", Required: true},
		{Key: "presentation.logo_svg", Section: "presentation", Order: 3, Label: "Logo (fichier SVG)", Hint: "Fichier .svg vectoriel, fond transparent recommandé", Kind: "file", Required: true, MimeAllow: []string{"image/svg+xml"}, MaxBytes: 1_000_000},
		{Key: "presentation.favicon_ico", Section: "presentation", Order: 4, Label: "Favicon", Kind: "file", MimeAllow: []string{"image/x-icon", "image/vnd.microsoft.icon", "image/png"}, MaxBytes: 100_000},
		{Key: "presentation.og_image", Section: "presentation", Order: 5, Label: "Image partage réseaux sociaux (1200×630)", Kind: "file", MimeAllow: []string{"image/png", "image/jpeg"}, MaxBytes: 2_000_000},
		{Key: "presentation.couleur_principale", Section: "presentation", Order: 6, Label: "Couleur principale du site", Hint: "Ex #00897b", Kind: "color", Required: true},

		// HELLOASSO
		{Key: "helloasso.url_don_ponctuel", Section: "helloasso", Order: 1, Label: "URL HelloAsso pour les dons ponctuels", Hint: "Copiez l'URL de la page campagne", Kind: "url", Required: true},
		{Key: "helloasso.url_adhesion", Section: "helloasso", Order: 2, Label: "URL HelloAsso pour les adhésions", Kind: "url"},
		{Key: "helloasso.url_don_mensuel", Section: "helloasso", Order: 3, Label: "URL HelloAsso pour les dons mensuels", Kind: "url"},
		{Key: "helloasso.argumentaire", Section: "helloasso", Order: 4, Label: "Pourquoi soutenir l'asso ?", Hint: "3 à 5 lignes pour convaincre", Kind: "longtext", Required: true},
		{Key: "helloasso.paliers_suggeres", Section: "helloasso", Order: 5, Label: "Paliers de don suggérés", Hint: "Ex '10,30,50,100'", Kind: "text"},

		// VIREMENT
		{Key: "virement.iban", Section: "virement", Order: 1, Label: "IBAN du compte", Hint: "Sur votre RIB, commence par FR...", Kind: "iban", Required: true},
		{Key: "virement.bic", Section: "virement", Order: 2, Label: "BIC (code SWIFT)", Hint: "8 ou 11 caractères, ex CRLYFRPP", Kind: "bic", Required: true},
		{Key: "virement.titulaire_compte", Section: "virement", Order: 3, Label: "Nom du titulaire du compte", Hint: "Tel qu'écrit sur le RIB", Kind: "text", Required: true},
		{Key: "virement.banque_nom", Section: "virement", Order: 4, Label: "Nom et ville de la banque", Hint: "Ex 'Crédit Lyonnais — Strasbourg Centre'", Kind: "text", Required: true},
		{Key: "virement.rib_pdf", Section: "virement", Order: 5, Label: "RIB en PDF (optionnel)", Kind: "file", MimeAllow: []string{"application/pdf"}, MaxBytes: 1_000_000},
	}
}

// FieldsBySection groupe les champs par section, triés par Order.
func FieldsBySection(fields []Field) map[string][]Field {
	m := map[string][]Field{}
	for _, f := range fields {
		m[f.Section] = append(m[f.Section], f)
	}
	for sec := range m {
		sort.Slice(m[sec], func(i, j int) bool {
			return m[sec][i].Order < m[sec][j].Order
		})
	}
	return m
}

// ValidateField valide la valeur d'un champ selon son Kind.
func ValidateField(f Field, value string) error {
	if value == "" {
		return nil // pas de validation si vide
	}
	switch f.Kind {
	case "iban":
		return validateIBAN(value)
	case "url":
		if _, err := url.ParseRequestURI(value); err != nil {
			return fmt.Errorf("URL invalide : %w", err)
		}
	}
	return nil
}

// validateIBAN valide un IBAN via l'algorithme modulo 97.
func validateIBAN(s string) error {
	// Normaliser : supprimer espaces, mettre en majuscules
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToUpper(r)
	}, s)

	if len(s) < 4 {
		return fmt.Errorf("IBAN trop court")
	}

	// Déplacer les 4 premiers caractères en fin
	rearranged := s[4:] + s[:4]

	// Convertir les lettres en chiffres (A=10, B=11, ...)
	var numeric strings.Builder
	for _, r := range rearranged {
		if r >= 'A' && r <= 'Z' {
			numeric.WriteString(fmt.Sprintf("%d", int(r-'A'+10)))
		} else if r >= '0' && r <= '9' {
			numeric.WriteByte(byte(r))
		} else {
			return fmt.Errorf("IBAN contient un caractère invalide : %c", r)
		}
	}

	// Calcul modulo 97 en segments pour éviter les overflow
	digits := numeric.String()
	remainder := 0
	for _, ch := range digits {
		remainder = (remainder*10 + int(ch-'0')) % 97
	}

	if remainder != 1 {
		return fmt.Errorf("IBAN invalide (modulo 97 = %d, attendu 1)", remainder)
	}
	return nil
}
