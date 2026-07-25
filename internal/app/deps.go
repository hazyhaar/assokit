package app

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/pkg/convention"
	"github.com/hazyhaar/assokit/pkg/corpus"
	"github.com/hazyhaar/assokit/pkg/eventsink"
	"github.com/hazyhaar/assokit/pkg/rbac"
	"github.com/hazyhaar/assokit/pkg/signupprofile"
)

// Mailer interface stub (sera défini dans pkg/mailer au LOT2)
type Mailer interface {
	Enqueue(ctx context.Context, to string, subject string, textBody string, htmlBody string) error
}

// AppDeps regroupe toutes les dépendances transverses nécessaires au démarrage.
type AppDeps struct {
	DB         *sql.DB
	Logger     *slog.Logger
	Config     config.Config
	Mailer     Mailer
	BrandingFS fs.FS
	Profils    []signupprofile.Profile

	// MetierTabs : liste blanche des onglets d'espace membre par grade métier,
	// injectée à la bordure (api.Options). Vide par défaut : aucune garde métier
	// supplémentaire, comportement historique (requireAuth seul).
	MetierTabs []MetierTab

	// ProfileGrant : configuration d'octroi de profils métier (gouvernance + grades
	// requestables), injectée à la bordure. Vide par défaut : pas de parcours demande/
	// octroi actif.
	ProfileGrant ProfileGrant

	// DisabledModules : ensemble des slugs de module d'espace membre désactivés pour
	// l'instance, injecté à la bordure (api.Options.DisabledModules). Nil/vide par
	// défaut → tous les modules socle actifs (comportement historique). Une instance
	// verticale (ex. GAFP) désactive ici un module socle (« conventions ») pour le
	// remplacer par sa propre vue : les routes du module ne sont pas montées (404
	// naturel) et ses cartes/liens ne sont pas rendus. Le core reste tenant-agnostic :
	// il ne connaît que des slugs génériques, aucune identité d'instance.
	DisabledModules map[string]bool

	// ElevageExtraCards : cartes supplémentaires injectées dans le hub d'espace
	// élevage (/account/elevage), fournies par la bordure (ex. GAFP relie ici ses
	// sous-pages questions parcellaires / télédéclaration PAC / divergence). Vide
	// par défaut → seules les cartes socle (entretien/problème/export PAC)
	// s'affichent. Le core reste tenant-agnostic : il ne connaît aucune route
	// propre à une instance verticale.
	ElevageExtraCards []AccountCard

	// EventSink : sortie des événements métier (nouveau membre, feedback, post,
	// paiement…). Jamais nil après api.New (NopSink par défaut). La bordure
	// injecte HTTPSink (webhook générique) ou un adaptateur bus ledger horos55
	// (mode pôle) sans coupler le core à horos55.
	EventSink eventsink.Sink

	// WebhookReceiver : handler /webhooks/{provider} câblé depuis api.New si
	// ASSOKIT_MASTER_KEY+Vault disponibles. Nil sinon → la route renvoie 503 explicite
	// (HelloAsso peut détecter via leur retry pattern).
	WebhookReceiver func(http.ResponseWriter, *http.Request)

	// RBAC : service d'autorisation partagé (Store + cache L1). UNIQUE instance
	// applicative — c'est celle qui sert Can() sur les chemins admin et MCP. Toute
	// mutation de grade/permission DOIT passer par ce Service (pas un Store nu), sinon
	// le cache L1 vivant reste périmé et une révocation ne retire pas l'accès
	// (régression auditée 2026-06-13). Nil possible dans un montage de test minimal.
	RBAC *rbac.Service

	// ConventionProvider : fournisseur des clauses d'une convention de pâturage.
	// Jamais nil après api.New (InertProvider par défaut, qui ne fabrique aucune
	// clause). La bordure d'une instance plateforme injecte un adaptateur qui
	// appelle le service de clauses réel (côté horos55), sans coupler le core à
	// horos55 : assokit ne connaît que l'interface convention.ClauseProvider.
	ConventionProvider convention.ClauseProvider

	// CorpusProvider : fournisseur de recherche dans un corpus documentaire externe
	// (ex. corpus de droit rural afp_droit). Jamais nil après api.New (InertProvider
	// par défaut, qui ne fabrique aucun résultat). La bordure d'une instance plateforme
	// injecte un adaptateur qui interroge le moteur réel (siftrag, côté horos55) en
	// lecture pure, sans coupler le core à horos55 : assokit ne connaît que l'interface
	// corpus.Provider.
	CorpusProvider corpus.Provider
}
