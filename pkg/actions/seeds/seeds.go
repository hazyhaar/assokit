// Package seeds enregistre l'ensemble des actions V1 dans le Registry.
// Appeler InitAll(reg) au démarrage du serveur pour peupler le registry.
// Après InitAll, boucler reg.All() → rbac.Store.EnsurePermission pour idempotence RBAC.
package seeds

import "github.com/hazyhaar/assokit/pkg/actions"

// InitAll enregistre toutes les actions dans reg.
func InitAll(reg *actions.Registry) {
	initForum(reg)
	initFeedback(reg)
	initUsers(reg)
	initPages(reg)
	initBranding(reg)
	initSignup(reg)
	initMailer(reg)
	initProfile(reg)
	initAccount(reg)
	initRBAC(reg)
}
