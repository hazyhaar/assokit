// CLAUDE:SUMMARY scope_labels.go — mapping scope/perm → libellé naturel français pour page consent (M-ASSOKIT-DCR-4).
// CLAUDE:WARN Tout nouveau scope ajouté au Registry actions doit avoir une row ici, sinon fallback "Action technique : <scope>" apparaît à l'utilisateur (closed-world).
package pages

// ScopeLabelsFR : map scope → libellé naturel FR pour page consent.
// Cible UX : non-tech, grandma. Pas de jargon "endpoint", "scope", "OAuth".
var ScopeLabelsFR = map[string]string{
	// Feedback
	"feedback.create":  "Créer un nouveau feedback en votre nom",
	"feedback.list":    "Lire les feedbacks publiés sur le site",
	"feedback.triage":  "Triater les feedbacks (modérateur)",
	"feedback.delete":  "Supprimer un feedback (modérateur)",

	// Forum
	"forum.post.create":      "Publier un message sur le forum en votre nom",
	"forum.post.edit_self":   "Modifier vos propres messages forum",
	"forum.post.delete_self": "Supprimer vos propres messages forum",
	"forum.post.delete":      "Supprimer un message du forum (modérateur)",
	"forum.thread.lock":      "Verrouiller un fil de discussion (modérateur)",
	"forum.user.warn":        "Avertir un utilisateur (modérateur)",
	"forum.user.timeout":     "Suspendre temporairement un utilisateur (modérateur)",

	// Users / RBAC
	"users.list":         "Voir la liste des membres",
	"users.role_assign":  "Attribuer un rôle à un membre (admin)",
	"users.deactivate":   "Désactiver un compte (admin)",
	"rbac.grade.create":  "Créer un nouveau rôle (admin)",
	"rbac.grade.delete":  "Supprimer un rôle (admin)",
	"rbac.perm.grant":    "Accorder une permission à un rôle (admin)",
	"rbac.perm.revoke":   "Retirer une permission à un rôle (admin)",

	// Pages / Branding
	"pages.update":  "Modifier le contenu des pages du site (admin)",
	"branding.read": "Voir la configuration du site",
	"branding.set":  "Modifier l'apparence du site (admin)",
	"branding.write": "Modifier l'apparence du site (admin)",

	// Profil / Account
	"profile.edit_self":   "Modifier votre propre profil",
	"profile.avatar_upload": "Mettre à jour votre photo de profil",
	"account.delete_self": "Supprimer votre compte définitivement (RGPD)",

	// Signup / Mailer
	"signup.activate":     "Activer une nouvelle inscription (admin)",
	"mailer.outbox.list":  "Voir la liste des emails envoyés (admin)",
	"mailer.outbox.retry": "Réessayer l'envoi d'un email (admin)",
	"mailer.outbox.cancel": "Annuler l'envoi d'un email en attente (admin)",

	// Donations / Connectors
	"donations.list":   "Voir la liste des dons (admin)",
	"donations.export": "Exporter les dons en CSV (admin)",
	"connectors.list":  "Voir la liste des intégrations tierces (admin)",
	"connectors.configure": "Configurer une intégration tierce (admin)",

	// Standard OIDC
	"openid":  "Vous identifier sur le site",
	"profile": "Voir votre nom et email",
	"email":   "Voir votre adresse email",
	"mcp":     "Utiliser les outils du site via Claude/MCP",
}

// LibelleScope retourne le libellé FR du scope, ou un fallback explicit si absent.
// Closed-world : un scope non mappé apparaîtra comme "Action technique : <scope>"
// pour que l'utilisateur ne voit jamais une chaîne vide ou opaque.
func LibelleScope(scope string) string {
	if lib, ok := ScopeLabelsFR[scope]; ok {
		return lib
	}
	return "Action technique : " + scope
}

// LibellesScope mappe une slice de scopes en libellés.
func LibellesScope(scopes []string) []string {
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = LibelleScope(s)
	}
	return out
}
