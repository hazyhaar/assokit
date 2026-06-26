// CLAUDE:SUMMARY Diagnostic de configuration (menu Setup) : Status(deps) -> []Item par fonction (configuré/manquant/partiel) + lien d'édition DB-safe. Source unique UI + action registry.
package setup

import (
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
)

// State est l'état de configuration d'une fonction.
type State string

const (
	StateOK      State = "ok"      // configuré et opérationnel
	StateMissing State = "missing" // non configuré
	StatePartial State = "partial" // partiellement configuré / mode dégradé
)

// Item décrit l'état de configuration d'une fonction du kit.
type Item struct {
	Key      string // identifiant stable
	Label    string // libellé humain
	State    State
	Required bool   // false = optionnel (un manque n'est pas bloquant)
	Hint     string // ce qu'il faut renseigner (variable d'env ou action)
	EditURL  string // écran d'édition DB-safe si la config est stockée en DB, sinon ""
}

// Status calcule l'état de chaque fonction à partir de la configuration injectée
// à la bordure (Config), du branding chargé et de la DB. Lecture seule : les
// secrets d'environnement ne sont jamais modifiables ici (doctrine bordure) ;
// seules les configs stockées en DB exposent un EditURL vers leur éditeur.
func Status(deps app.AppDeps) []Item {
	cfg := deps.Config
	items := make([]Item, 0, 8)

	// 1. URL publique — requise (issuer OAuth, liens emails).
	items = append(items, boolItem(Item{
		Key: "base_url", Label: "URL publique du site", Required: true,
		Hint: "Variable d'environnement BASE_URL (ex: https://ma-communaute.org)",
	}, cfg.BaseURL != ""))

	// 2. Sécurité des sessions — COOKIE_SECRET. En prod il est fatal s'il manque
	//    (cf. api.New), donc présent ici ; en dev un secret aléatoire est toléré.
	sessions := Item{
		Key: "session_security", Label: "Sécurité des sessions", Required: true,
		Hint: "COOKIE_SECRET (64 hex) en production ; sinon secret aléatoire (sessions perdues au restart)",
	}
	if cfg.Prod {
		sessions.State = StateOK
	} else {
		sessions.State = StatePartial
	}
	items = append(items, sessions)

	// 3. Email — SMTP/Resend. Sans, le mailer reste en outbox seul.
	items = append(items, boolItem(Item{
		Key: "email", Label: "Envoi d'emails", Required: false,
		Hint: "SMTP_HOST/SMTP_USER/SMTP_PASS (ou un serveur horosmail via SMTP) ou RESEND_API_KEY ; sinon emails en file d'attente non envoyés",
	}, cfg.MailerConfigured))

	// 4. Connecteurs / dons HelloAsso — DB-éditable (clés via Vault).
	connectors := boolItem(Item{
		Key: "connectors_helloasso", Label: "Connecteurs (dons HelloAsso…)", Required: false,
		Hint:    "ASSOKIT_MASTER_KEY pour activer le Vault, puis renseigner les clés du connecteur",
		EditURL: "/admin/connectors",
	}, cfg.ConnectorsEnabled)
	items = append(items, connectors)

	// 4b. Visioconférence LiveKit — optionnel, configurée via le Vault des
	//     connecteurs (donc nécessite MasterKey active). DB-éditable.
	items = append(items, boolItem(Item{
		Key: "visio_livekit", Label: "Visioconférence LiveKit (optionnel)", Required: false,
		Hint:    "Activer le Vault (ASSOKIT_MASTER_KEY) puis renseigner server_url/api_key/api_secret du serveur LiveKit voisin",
		EditURL: "/admin/connectors",
	}, cfg.ConnectorsEnabled))

	// 5. Login social Google — optionnel.
	items = append(items, boolItem(Item{
		Key: "social_login_google", Label: "Login Google (optionnel)", Required: false,
		Hint: "GOOGLE_CLIENT_ID et GOOGLE_CLIENT_SECRET",
	}, cfg.GoogleClientID != ""))

	// 6. Branding — DB-éditable via le panel admin.
	branding := Item{
		Key: "branding", Label: "Identité / branding", Required: true,
		Hint:    "Charger un branding.toml ou renseigner le nom de l'organisation",
		EditURL: "/admin/panel",
	}
	switch {
	case deps.BrandingFS != nil && theme.Brand().Name != "":
		branding.State = StateOK
	case theme.Brand().Name != "":
		branding.State = StatePartial
	default:
		branding.State = StateMissing
	}
	items = append(items, branding)

	// 7. Compte administrateur — requis.
	items = append(items, boolItem(Item{
		Key: "admin_account", Label: "Compte administrateur", Required: true,
		Hint: "ADMIN_EMAIL + ADMIN_PASSWORD au démarrage (compte admin bootstrap)",
	}, hasAdmin(deps)))

	return items
}

// boolItem fixe l'état d'un item selon un booléen "configuré".
func boolItem(it Item, ok bool) Item {
	if ok {
		it.State = StateOK
	} else {
		it.State = StateMissing
	}
	return it
}

// hasAdmin retourne true s'il existe au moins un utilisateur avec le rôle admin.
func hasAdmin(deps app.AppDeps) bool {
	if deps.DB == nil {
		return false
	}
	var n int
	if err := deps.DB.QueryRow(`SELECT COUNT(*) FROM user_grades ug JOIN grades g ON g.id = ug.grade_id WHERE g.name = 'admin'`).Scan(&n); err != nil {
		return false
	}
	return n > 0
}
