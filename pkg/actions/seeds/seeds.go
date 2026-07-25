// Package seeds enregistre l'ensemble des actions V1 dans le Registry.
// Appeler InitAll(reg) au démarrage du serveur pour peupler le registry.
// Après InitAll, appeler InitSalonVisio(reg, vault) pour enregistrer les
// actions room.* (qui dépendent du Vault LiveKit optionnel).
// Après InitAll, boucler reg.All() → rbac.Store.EnsurePermission pour idempotence RBAC.
package seeds

import "github.com/hazyhaar/assokit/pkg/actions"

// InitAll enregistre toutes les actions dans reg.
// Les actions salon.* (sans visio) sont enregistrées ici avec conn=nil.
// InitSalonVisio ré-injecte le connecteur LiveKit pour les actions room.* et
// le cycle de vie best-effort — elle doit être appelée après InitAll.
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
	initMessages(reg)
	initSetup(reg)
	initEvents(reg)
	initMemberships(reg)
	initNewsletter(reg)
	initTranscript(reg)
	initGDPR(reg)
	initConvention(reg)
	initGovernance(reg)
	initSocial(reg)
	// Note : initSalon est appelé depuis InitSalonVisio (qui connaît le Connector
	// LiveKit optionnel pour le cycle de vie best-effort). Si InitSalonVisio n'est
	// jamais appelé, les actions salon.* ne seront pas enregistrées — ce cas ne
	// doit pas se produire : api.go appelle toujours InitSalonVisio après InitAll.
}
