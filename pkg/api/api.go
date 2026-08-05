// Package api expose la surface publique stable d'assokit.
package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"expvar"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/bootstrap"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/internal/handlers"
	"github.com/hazyhaar/assokit/internal/mailer"
	"github.com/hazyhaar/assokit/internal/transcription"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/connectors"
	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	"github.com/hazyhaar/assokit/pkg/connectors/helloasso"
	"github.com/hazyhaar/assokit/pkg/connectors/livekit"
	"github.com/hazyhaar/assokit/pkg/connectors/webhooks"
	"github.com/hazyhaar/assokit/pkg/convention"
	"github.com/hazyhaar/assokit/pkg/corpus"
	"github.com/hazyhaar/assokit/pkg/eventsink"
	appMiddleware "github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
	"github.com/hazyhaar/assokit/pkg/rbac"
	"github.com/hazyhaar/assokit/pkg/salon"
	"github.com/hazyhaar/assokit/pkg/signupprofile"
	transcriptpkg "github.com/hazyhaar/assokit/pkg/transcript"
	"github.com/hazyhaar/assokit/static"

	_ "modernc.org/sqlite"
)

// MetierTab, GrantableGrade et ProfileGrant sont réexportés pour la bordure
// (instances consommatrices ne peuvent pas importer internal/app).
type (
	MetierTab      = app.MetierTab
	GrantableGrade = app.GrantableGrade
	ProfileGrant   = app.ProfileGrant
	AccountCard    = app.AccountCard
)

// DashGroup et DashLink sont réexportés pour la bordure : une instance
// consommatrice (ex. GAFP) ne peut pas importer internal/webui/views, mais peut
// construire ses groupes de tableau de bord via ces alias et les injecter par
// Options.AdminDashboardGroups.
type (
	DashGroup = views.DashGroup
	DashLink  = views.DashLink
)

// App représente une instance assokit configurée et prête à servir.
type App struct {
	opts          Options
	db            *sql.DB
	srv           *http.Server
	handler       http.Handler
	ml            *mailer.Mailer
	logger        *slog.Logger
	connectorReg  *connectors.Registry
	connectorLife *connectors.Lifecycle
}

// Options configure une nouvelle App.
type Options struct {
	// DBPath : chemin SQLite (créé/migré au New). ":memory:" accepté pour tests.
	DBPath string

	// BaseURL : URL publique du site (ex: "https://my-association.org").
	BaseURL string

	// Port : port HTTP d'écoute (ex: "8080").
	Port string

	// BindHost : interface d'écoute (ex: "127.0.0.1" pour le loopback derrière un
	// reverse-proxy). Vide = toutes les interfaces (défaut historique).
	BindHost string

	// BrandingFS : système de fichiers contenant branding.toml + pages/*.md + assets.
	BrandingFS fs.FS

	// AdminEmail / AdminPassword : compte admin bootstrap (idempotent si users>0).
	AdminEmail    string
	AdminPassword string

	// ContactEmail : affiché sur le site / destinataire contact form. Défaut = AdminEmail.
	ContactEmail string

	// CookieSecret : 32+ bytes pour sessions ; généré aléatoire si vide (warn log).
	CookieSecret []byte

	// Prod : si true, une configuration absente/dangereuse est fatale au lieu
	// d'être contournée silencieusement (ex : CookieSecret vide → erreur, pas
	// de secret aléatoire qui invalide les sessions au restart). La bordure
	// (cmd/assokit) décide du mode ; le core ne lit jamais l'environnement.
	Prod bool

	// MasterKey : clé de chiffrement du Vault des connecteurs (HelloAsso…).
	// Vide → Vault désactivé, connecteurs inactifs. Injecté à la bordure
	// (le core ne lit aucune variable d'environnement).
	MasterKey string

	// OAuthSigningKey : clé de signature des tokens OAuth (hex/raw). Vide →
	// fallback CookieSecret (tokens invalidés au restart, warn).
	OAuthSigningKey string

	// OAuthAllowInsecure : autorise un issuer http:// (dev/local).
	OAuthAllowInsecure bool

	// GoogleClientID / GoogleClientSecret : login social Google (optionnel).
	// ID vide → route Google non montée.
	GoogleClientID     string
	GoogleClientSecret string

	// BrandingUploadDir : répertoire d'upload des assets admin. Défaut "./uploads".
	BrandingUploadDir string

	// TrustProxyHeaders : honorer X-Forwarded-For / X-Real-IP (rate-limits).
	// FALSE par défaut — n'activer que derrière un reverse-proxy de confiance.
	TrustProxyHeaders bool

	// EventSink : sortie d'événements injectée par la bordure. Si non nil, prend
	// le pas sur WebhookURL (permet à un build pôle d'injecter un adaptateur bus
	// ledger horos55). Si nil et WebhookURL non vide → HTTPSink ; sinon NopSink.
	EventSink eventsink.Sink

	// WebhookURL / WebhookSecret : webhook HTTP générique (publiable) recevant les
	// événements métier. Secret optionnel → signature HMAC X-Assokit-Signature.
	WebhookURL    string
	WebhookSecret string

	// SMTP / Resend : optionnels ; si vides, mailer en mode "outbox enqueue only".
	SMTPHost     string
	SMTPUser     string
	SMTPPass     string
	SMTPPort     int
	SMTPFrom     string
	SMTPAdminTo  string
	ResendAPIKey string

	// Logger optionnel ; slog.Default() si nil.
	Logger *slog.Logger

	// LogLevel optionnel : niveau de log appliqué au handler créé si Logger==nil.
	// Permet à l'opérateur de passer Debug en investigation sans recompiler
	// (ex: ASSOKIT_LOG_LEVEL=debug). Défaut Info.
	// Ignoré si Logger non-nil (le caller contrôle son propre niveau).
	LogLevel slog.Level

	// RegisterActions : hook de bordure optionnel. Reçoit le registre d'actions
	// après les seeds core, pour qu'un produit consommateur (ex. GAFP) injecte
	// ses actions métier — chacune obtenant route HTTP admin + outil MCP +
	// permission RBAC automatiques (LLM-parity), sans modifier le core. Une
	// erreur (ex. ID en doublon avec une action core) est fatale au New.
	RegisterActions func(*actions.Registry) error

	// RegisterRoutes : hook de bordure optionnel. Reçoit le chi.Router complet
	// APRÈS le montage des routes core, pour qu'un produit consommateur (ex.
	// l'instance verticale yachts) monte ses propres routes HTTP publiques
	// (front-office marketplace) sans modifier ni forker le core. Distinct de
	// RegisterActions (actions admin/MCP/RBAC) : ici routes FO server-rendered.
	// Une erreur est fatale au New.
	RegisterRoutes func(chi.Router) error

	// ConventionProvider : fournisseur des clauses d'une convention de pâturage,
	// injecté à la bordure. Nil → fournisseur par défaut inerte
	// (convention.InertProvider, aucune clause fabriquée). Une instance plateforme
	// injecte ici un adaptateur qui appelle le service de clauses réel (horos55)
	// en désérialisant un contrat JSON neutre vers []convention.Clause. Le core ne
	// connaît que l'interface, jamais le producteur : assokit reste import-clean.
	ConventionProvider convention.ClauseProvider

	// CorpusProvider : fournisseur de recherche dans un corpus documentaire externe
	// (ex. corpus de droit rural afp_droit), injecté à la bordure. Nil → fournisseur
	// par défaut inerte (corpus.InertProvider, aucun résultat fabriqué). Une instance
	// plateforme injecte ici un adaptateur qui interroge le moteur réel (siftrag) en
	// lecture pure. Le core ne connaît que l'interface corpus.Provider : assokit reste
	// import-clean (aucun couplage au moteur).
	CorpusProvider corpus.Provider

	// SignupProfiles : catalogue des profils d'inscription proposés sur /participer,
	// injecté à la bordure. Vide → catalogue générique par défaut
	// (defaultSignupProfiles : adherent, benevole, don, partenaire, tous grade
	// sys-member). Le core ne CHOISIT jamais ses profils : une instance affine le set,
	// les labels, les champs extra et le grade RBAC assigné via cette option. Chaque
	// GradeID référencé doit exister dans la table RBAC de l'instance, sinon api.New
	// échoue au boot (fail-loud, aucun repli silencieux).
	SignupProfiles []signupprofile.Profile

	// MetierTabs : liste blanche des onglets d'espace membre par grade métier.
	// Injectée à la bordure ; vide par défaut (aucune garde métier, requireAuth seul).
	// Le core ne connaît aucun nom de grade en dur : seuls les onglets déclarés ici
	// activent requireMetierGrade et le filtrage UI.
	MetierTabs []app.MetierTab

	// ProfileGrant : configuration d'octroi de profils métier (grade de gouvernance
	// + catalogue requestable). Injectée à la bordure ; vide par défaut. Le core ne
	// connaît aucun nom de grade en dur.
	ProfileGrant app.ProfileGrant

	// DisabledModules : slugs des modules d'espace membre du socle à désactiver pour
	// cette instance (ex. "conventions"). Vide/nil par défaut → tous les modules
	// actifs (rétro-compatible). Quand un module est désactivé, ses routes ne sont
	// pas montées (404 naturel) et ses cartes/liens ne sont pas rendus ; une instance
	// verticale (ex. GAFP) fournit alors sa propre vue à la même place. Le core reste
	// tenant-agnostic : seul un slug de module générique est accepté, jamais une
	// identité d'instance. Un slug inconnu du catalogue socle est fatal au New
	// (fail-loud, aucune désactivation silencieuse). Modules reconnus : cf.
	// handlers.KnownModuleSlugs.
	DisabledModules []string

	// SeedGrades : hook de bordure optionnel, appelé APRÈS les migrations (la table
	// grades existe alors) et AVANT la validation des profils d'inscription, pour
	// qu'un consommateur (ex. GAFP) enregistre ses grades RBAC personnalisés. Sans
	// cette fenêtre, un SignupProfile référençant un grade custom ferait échouer le
	// boot (validateProfileGrades fail-loud) faute de pouvoir seeder ce grade. Une
	// erreur du hook est fatale au New (la db est refermée). Nil → aucun seed custom.
	SeedGrades func(*sql.DB) error

	// AdminDashboardGroups : groupes de liens supplémentaires ajoutés au tableau de
	// bord /admin, injectés à la bordure. Vide par défaut → seuls les groupes socle
	// (génériques du kit) s'affichent. Le core reste tenant-agnostic : il ne connaît
	// aucune sous-page propre à une instance ; une instance verticale (ex. GAFP)
	// place ici son groupe « Registre foncier » (→ /admin/registre) sans modifier le
	// core. Les liens injectés portent, s'il le faut, une permission RBAC fine (champ
	// Perm) et sont soumis au même filtrage par utilisateur que les liens socle.
	AdminDashboardGroups []views.DashGroup

	// ElevageExtraCards : cartes supplémentaires injectées dans le hub d'espace
	// élevage (/account/elevage), miroir d'AdminDashboardGroups pour l'espace
	// membre. Vide par défaut → seules les cartes socle (entretien, problème,
	// export PAC) s'affichent. Une instance verticale (ex. GAFP) y relie ses
	// sous-pages métier (questions parcellaires, télédéclaration PAC, écarts
	// acte/PAC) sans que le core ne connaisse jamais ces routes.
	ElevageExtraCards []AccountCard

	// CSPExtra : sources additionnelles autorisées par directive Content-Security-Policy,
	// injectées à la bordure d'instance. Valeur zéro (défaut) → CSP durcie inchangée,
	// octet-pour-octet. Le core reste durci par défaut ; une instance qui charge une
	// ressource externe légitime (ex. GAFP : MapLibre CDN + tuiles IGN pour la carte
	// foncière) ouvre ici le cran nécessaire, sans que le core n'élargisse jamais sa
	// propre CSP.
	CSPExtra appMiddleware.CSPExtra
}

// New crée une App, ouvre la DB, applique les migrations, charge le branding.
// Renvoie une erreur typée pour chaque échec (DB, migration, bootstrap, branding).
func New(opts Options) (*App, error) {
	logger := opts.Logger
	if logger == nil {
		level := opts.LogLevel
		if level == 0 {
			level = slog.LevelInfo
		}
		logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	}

	// 1. DB open + ping (pragmas FK/WAL/busy_timeout/synchronous posés par openDB).
	db, err := openDB(opts.DBPath)
	if err != nil {
		return nil, fmt.Errorf("api.New: open db: %w", err)
	}

	// 2. Migrations.
	if err := chassis.Run(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("api.New: migrations: %w", err)
	}

	// 2bis. Seed des grades RBAC personnalisés (bordure). Fenêtre dédiée entre les
	// migrations (table grades créée) et la validation des profils d'inscription
	// (validateProfileGrades, fail-loud) : un consommateur enregistre ici ses grades
	// custom pour qu'un SignupProfile puisse les référencer sans planter au boot.
	if opts.SeedGrades != nil {
		if err := opts.SeedGrades(db); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("api.New: seed grades: %w", err)
		}
	}

	// 3. Admin bootstrap (idempotent, skip si AdminEmail vide).
	if opts.AdminEmail != "" {
		if err := bootstrap.BootstrapAdmin(db, opts.AdminEmail, opts.AdminPassword, logger); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("api.New: bootstrap admin: %w", err)
		}
	}

	// 3bis. Permissions forum (idempotent, indépendant de l'admin) : accorde
	// l'écriture sur le forum aux rôles admin et member, sans quoi la route
	// POST /forum/{slug}/reply renvoie 403 à tout le monde (table roles et
	// node_permissions vides au bootstrap initial).
	if err := bootstrap.BootstrapForumPerms(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("api.New: bootstrap forum perms: %w", err)
	}

	// Modèle pur question→branche→messages : chaque question porte une branche
	// "Général" (idempotent, ne touche que les questions sans branche).
	if err := bootstrap.BootstrapForumBranches(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("api.New: bootstrap forum branches: %w", err)
	}

	// 4. CookieSecret.
	secret := opts.CookieSecret
	if len(secret) == 0 {
		if opts.Prod {
			_ = db.Close()
			return nil, fmt.Errorf("api.New: CookieSecret requis en mode prod (un secret aléatoire invaliderait les sessions à chaque restart)")
		}
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("api.New: cookie secret rand: %w", err)
		}
		logger.Warn("CookieSecret non fourni — secret aléatoire généré : sessions invalides au prochain restart")
	} else if len(secret) < 32 {
		_ = db.Close()
		return nil, fmt.Errorf("api.New: CookieSecret trop court (%d bytes, minimum 32 requis)", len(secret))
	}

	// 5. Mailer init.
	from := opts.SMTPFrom
	if from == "" {
		from = opts.AdminEmail
	}
	adminTo := opts.SMTPAdminTo
	if adminTo == "" {
		adminTo = opts.AdminEmail
	}
	ml := &mailer.Mailer{
		DB:       db,
		APIKey:   opts.ResendAPIKey,
		SMTPHost: opts.SMTPHost,
		SMTPPort: opts.SMTPPort,
		SMTPUser: opts.SMTPUser,
		SMTPPass: opts.SMTPPass,
		From:     from,
		AdminTo:  adminTo,
		Logger:   logger,
	}
	if opts.SMTPHost == "" && opts.ResendAPIKey == "" {
		logger.Info("mailer: SMTP et Resend non configurés — mode outbox enqueue only")
	}

	// 6. Branding.
	if opts.BrandingFS != nil {
		b, err := theme.LoadFromFS(opts.BrandingFS, "branding.toml")
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("api.New: branding: %w", err)
		}
		theme.Init(b)
		if err := bootstrap.SeedBrandingPages(context.Background(), db, opts.BrandingFS, logger); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("api.New: seed branding pages: %w", err)
		}
	} else {
		theme.Init(&theme.Branding{Name: "Assokit Default"})
	}

	// 7. AppDeps assemblage.
	contactEmail := opts.ContactEmail
	if contactEmail == "" {
		contactEmail = opts.AdminEmail
	}

	// Catalogue des profils d'inscription : injecté à la bordure, ou repli sur le
	// set générique par défaut. Chaque grade référencé doit déjà exister dans la
	// table RBAC (seedée par les migrations exécutées plus haut) — fail-loud sinon,
	// pour qu'aucun signup ne puisse assigner un grade fantôme.
	signupProfiles := opts.SignupProfiles
	if len(signupProfiles) == 0 {
		signupProfiles = defaultSignupProfiles()
	}
	if err := validateProfileGrades(db, signupProfiles); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("api.New: profils d'inscription: %w", err)
	}
	if err := validateProfileGrant(opts.ProfileGrant); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("api.New: octroi de profil: %w", err)
	}

	// Modules d'espace membre désactivés (bordure). Chaque slug doit désigner un
	// module reconnu du socle — fail-loud sinon, pour qu'une faute de frappe ne
	// désactive jamais rien en silence. Le core ne connaît que des slugs génériques.
	if err := handlers.ValidateDisabledModules(opts.DisabledModules); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("api.New: modules désactivés: %w", err)
	}
	disabledModules := make(map[string]bool, len(opts.DisabledModules))
	for _, s := range opts.DisabledModules {
		disabledModules[s] = true
	}
	if len(disabledModules) > 0 {
		logger.Info("modules d'espace membre désactivés", "modules", opts.DisabledModules)
	}

	deps := app.AppDeps{
		DB:     db,
		Logger: logger,
		Config: config.Config{
			Port:               opts.Port,
			DBPath:             opts.DBPath,
			BaseURL:            opts.BaseURL,
			AdminEmail:         opts.AdminEmail,
			ContactEmail:       contactEmail,
			CookieSecret:       secret,
			OAuthSigningKey:    []byte(opts.OAuthSigningKey),
			OAuthAllowInsecure: opts.OAuthAllowInsecure,
			GoogleClientID:     opts.GoogleClientID,
			GoogleClientSecret: opts.GoogleClientSecret,
			BrandingUploadDir:  opts.BrandingUploadDir,
			TrustProxyHeaders:  opts.TrustProxyHeaders,
			Prod:               opts.Prod,
			MailerConfigured:   opts.SMTPHost != "" || opts.ResendAPIKey != "",
			ConnectorsEnabled:  opts.MasterKey != "",
		},
		Mailer:             ml,
		BrandingFS:         opts.BrandingFS,
		Profils:            signupProfiles,
		MetierTabs:         opts.MetierTabs,
		ProfileGrant:       opts.ProfileGrant,
		DisabledModules:    disabledModules,
		ElevageExtraCards:  opts.ElevageExtraCards,
		EventSink:          chooseEventSink(opts, logger),
		ConventionProvider: chooseConventionProvider(opts),
		CorpusProvider:     chooseCorpusProvider(opts),
	}
	// Service RBAC : UNIQUE instance applicative, partagée par Can() (admin + MCP)
	// et par les actions de mutation de grade/permission. Indispensable pour que
	// l'invalidation du cache L1 et le recalcul des permissions effectives soient
	// cohérents (sinon une révocation ne retire pas l'accès — audit 2026-06-13).
	deps.RBAC = &rbac.Service{
		Store:  &rbac.Store{DB: db},
		Cache:  &rbac.Cache{},
		Logger: logger,
	}

	// 7b. Connectors : init Registry + register HelloAsso si MasterKey fournie.
	// (Si master key absente, Vault non créé → connectors restent inutilisables ;
	// la bordure injecte opts.MasterKey pour activer.)
	connectorReg := connectors.NewRegistry()
	var connectorLife *connectors.Lifecycle
	// Vault créé UNE SEULE FOIS et réutilisé (webhook receiver + admin routes) —
	// dédup de l'ancien double-create. nil si MasterKey absente/invalide.
	var connectorVault *assets.Vault
	if mk := opts.MasterKey; mk != "" {
		vault, vErr := assets.NewVault(db, mk)
		if vErr != nil {
			logger.Error("connectors: master key invalide, connectors désactivés", "err", vErr.Error())
		} else {
			connectorVault = vault
			// Launcher transcription : déclenchement du bot transcriber via webhook LiveKit.
			// Construit AVANT le connecteur LiveKit pour injecter le hook OnRoomEvent.
			// Le Launcher lit ses creds LiveKit depuis le Vault à la construction.
			var lkURL, lkKey, lkSecret string
			_ = vault.Use(context.Background(), "livekit", "server_url", func(v string) error { lkURL = v; return nil })
			_ = vault.Use(context.Background(), "livekit", "api_key", func(v string) error { lkKey = v; return nil })
			_ = vault.Use(context.Background(), "livekit", "api_secret", func(v string) error { lkSecret = v; return nil })

			txLauncher := transcription.NewLauncher(
				&transcription.SalonStoreAdapter{S: &salon.Store{DB: db}},
				&transcription.TranscriptStoreAdapter{S: &transcriptpkg.Store{DB: db}},
				logger,
			)
			txLauncher.LiveKitURL = lkURL
			txLauncher.LiveKitAPIKey = lkKey
			txLauncher.LiveKitAPISecret = lkSecret
			// Moteur batch : après la fin du bot, whisper-cli transcrit les WAV,
			// persiste les segments et purge les WAV (lot T4). Sans ce branchement,
			// le launcher marquerait "done" sans produire de compte-rendu.
			txLauncher.Batch = transcription.NewBatcher(
				&transcription.BatchStoreAdapter{S: &transcriptpkg.Store{DB: db}}, logger)
			// Rétention TTL : purge périodique des transcrits expirés (lot T4).
			go transcription.NewRetentionWorker(
				&transcription.RetentionStoreAdapter{S: &transcriptpkg.Store{DB: db}}, logger).
				RunWorker(context.Background())

			// Connecteur LiveKit avec hook OnRoomEvent → déclenche le launcher.
			// Enregistré dans le Registry pour apparaître dans l'admin connecteurs.
			lkConnector := livekit.NewWithHook(vault, txLauncher.OnRoomEvent)
			if err := connectorReg.Register(lkConnector); err != nil {
				logger.Error("connectors: register livekit", "err", err.Error())
			}
			if err := connectorReg.Register(helloasso.New(vault)); err != nil {
				logger.Error("connectors: register helloasso", "err", err.Error())
			}
			connectorLife = &connectors.Lifecycle{
				Registry:     connectorReg,
				DB:           db,
				Logger:       logger,
				PingInterval: 5 * time.Minute,
			}
			// StartAll non-fatal : un connector down ne casse pas le boot du site.
			startCtx, startCancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := connectorLife.StartAll(startCtx); err != nil {
				logger.Warn("connectors: StartAll partial", "err", err.Error())
			}
			startCancel()

			// Webhook receiver : route publique /webhooks/{provider}.
			// HelloAsso : HMAC-SHA256 via Vault.
			// LiveKit : JWT HS256 ; le receiver générique est court-circuité (SignatureConfig
			// avec HeaderName "Authorization" et un extractor minimal) ; la vérification JWT
			// réelle est faite dans lkConnector.HandleWebhook (cf. webhook_verify.go).
			// Pour que le HMAC générique ne rejette pas LiveKit, on utilise un extractor
			// qui transmet l'Authorization JWT dans la Signature stockée, et on bypasse
			// la vérif HMAC en configurant un secret sentinelle "livekit-jwt" dans le Vault
			// (ops : SET livekit/webhook_signing_secret = "livekit-jwt").
			// Alternative propre (future) : refactorer WebhookHandler pour accepter un
			// vérificateur custom par provider.
			whStore := &webhooks.Store{DB: db}
			whConfigs := map[string]handlers.SignatureConfig{
				"helloasso": {
					HeaderName:   helloasso.WebhookSignatureHeader,
					ExtractEvent: helloasso.ExtractWebhookEventID,
				},
				"livekit": {
					HeaderName:   "Authorization",
					ExtractEvent: livekit.ExtractWebhookEventID,
				},
			}
			deps.WebhookReceiver = handlers.WebhookHandler(deps, whStore, vault, whConfigs)

			// Drainer webhook : lancé en goroutine au boot. Dispatche via Registry.
			// Précédemment non câblé → events s'accumulaient sans dispatch (corrigé ici).
			whWorker := &webhooks.Worker{
				Store:    whStore,
				Registry: connectorReg,
				Logger:   logger,
			}
			go whWorker.RunDrainer(context.Background()) // T3b: câblage drainer au boot
		}
	} else {
		logger.Info("connectors: ASSOKIT_MASTER_KEY absent — Vault désactivé, connectors inactifs")
	}

	// 8. chi.Router + middlewares + routes.
	// RequestID monté EN PREMIER : tous les slogs en aval peuvent récupérer req_id via ctx.
	r := chi.NewRouter()
	r.Use(appMiddleware.RequestID)
	r.Use(appMiddleware.AccessLog(logger))
	r.Use(appMiddleware.SecurityHeadersWith(opts.CSPExtra))
	r.Use(chiMiddleware.Recoverer)
	r.Use(appMiddleware.CSRF(secret, deps.Config.CookieSecure()))
	r.Use(appMiddleware.Auth(db, secret))
	r.Use(appMiddleware.Flash)
	r.Use(appMiddleware.HTMX)

	// Sondes d'orchestration (LB/systemd) et métriques. GET sans effet de bord :
	// la chaîne Auth/CSRF ne les rejette pas (Auth n'exige rien en GET, CSRF ne
	// contrôle que les méthodes mutantes). /readyz vérifie la DB.
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if err := db.PingContext(req.Context()); err != nil {
			http.Error(w, "db indisponible", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	r.Handle("/debug/vars", expvar.Handler())
	if err := handlers.MountRoutes(r, deps, handlers.WithExtraActions(opts.RegisterActions), handlers.WithLiveKitVault(connectorVault), handlers.WithAdminDashboardGroups(opts.AdminDashboardGroups)); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("api.New: mount routes: %w", err)
	}
	// Admin connectors (override mission 019de9fa) : routes câblées ici car
	// reg/life/vault sont en scope local (pas dans AppDeps). connectorVault est
	// le Vault déjà créé en 7b (réutilisé, pas re-créé).
	handlers.MountAdminConnectorsRoutes(r, deps, connectorReg, connectorLife, connectorVault)
	// Visio de salon (L2a) : GET /salon/{slug}/visio, gardée par appartenance + visio_enabled.
	// Cf. internal/handlers/salon_visio.go. Bypass de sécurité retiré (L2a).
	handlers.MountSalonVisioRoute(r, deps, connectorVault)
	// Hook de bordure : routes FO additionnelles (instance verticale).
	if opts.RegisterRoutes != nil {
		if err := opts.RegisterRoutes(r); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("api.New: register routes: %w", err)
		}
	}
	// Fichiers uploadés (branding admin : statuts, photo équipe…) servis sur disque.
	// Monté AVANT /static/* (plus spécifique) : page_renderer émet des liens
	// /static/uploads/<fichier> qui restaient morts (404) faute de ce service.
	uploadBase := opts.BrandingUploadDir
	if uploadBase == "" {
		uploadBase = "./uploads"
	}
	uploadsFS := http.Dir(filepath.Join(uploadBase, "uploads"))
	r.Handle("/static/uploads/*", http.StripPrefix("/static/uploads/", uploadsSafeHandler(uploadsFS)))
	r.Handle("/static/*", http.StripPrefix("/static", http.FileServer(http.FS(static.FS))))

	return &App{
		opts:          opts,
		db:            db,
		handler:       r,
		ml:            ml,
		logger:        logger,
		connectorReg:  connectorReg,
		connectorLife: connectorLife,
	}, nil
}

// uploadsSafeHandler sert les fichiers uploadés en forçant le téléchargement des
// formats à HTML actif (SVG, XML, HTML) : un SVG peut embarquer <script>/on*-handlers
// et deviendrait un XSS stocké s'il était rendu inline. Content-Disposition:attachment
// fait télécharger le fichier au lieu de l'exécuter. Les images raster (png/jpg/…)
// restent servies inline car référencées en <img>. Durcissement audité 2026-06-13.
//
// La couche safeFileSystem ajoute une protection explicite contre le path traversal :
// toute requête dont le path nettoie vers un élément contenant ".." ou "/" absolu
// est rejetée (404), indépendamment du nettoyage interne de http.FileServer.
func uploadsSafeHandler(dir http.FileSystem) http.Handler {
	fs := http.FileServer(safeFileSystem{root: dir})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lower := strings.ToLower(r.URL.Path)
		for _, ext := range []string{".svg", ".svgz", ".html", ".htm", ".xml", ".xhtml"} {
			if strings.HasSuffix(lower, ext) {
				w.Header().Set("Content-Disposition", "attachment")
				break
			}
		}
		fs.ServeHTTP(w, r)
	})
}

// safeFileSystem wrapper http.FileSystem qui rejette tout accès contenant ".."
// dans le path nettoyé, même si http.Dir le protège déjà en interne. Defense-in-depth.
type safeFileSystem struct {
	root http.FileSystem
}

func (s safeFileSystem) Open(name string) (http.File, error) {
	cleaned := filepath.Clean(name)
	if strings.Contains(cleaned, "..") {
		return nil, os.ErrNotExist
	}
	return s.root.Open(name)
}

// ListenAndServe démarre le serveur HTTP. Bloquant. Honore ctx.Done() avec
// graceful shutdown timeout 10s.
func (a *App) ListenAndServe(ctx context.Context) error {
	// Mailer worker en background, s'arrête avec ctx.
	go a.ml.RunWorker(ctx)

	port := a.opts.Port
	if port == "" {
		port = "8080"
	}
	// Timeouts complets : ReadHeaderTimeout seul ne couvre ni la lecture du corps,
	// ni l'écriture de la réponse (slow-read), ni les connexions keep-alive idle.
	// Sans WriteTimeout/IdleTimeout, des connexions lentes ou oisives accaparent
	// goroutines et descripteurs (DoS Slowloris-like). Audit 2026-06-13.
	// Adresse d'écoute : loopback si BindHost est posé (derrière reverse-proxy),
	// sinon toutes les interfaces (défaut historique).
	addr := ":" + port
	if a.opts.BindHost != "" {
		addr = a.opts.BindHost + ":" + port
	}
	a.srv = &http.Server{
		Addr:              addr,
		Handler:           a.handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	srvErr := make(chan error, 1)
	go func() {
		if err := a.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			srvErr <- err
		}
	}()

	select {
	case err := <-srvErr:
		return err
	case <-ctx.Done():
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.srv.Shutdown(shutCtx)
}

// Handler renvoie le chi.Router complet — utilisable en httptest.NewServer(app.Handler()).
func (a *App) Handler() http.Handler {
	return a.handler
}

// Shutdown arrête proprement le serveur HTTP, stoppe les connectors et ferme la DB.
func (a *App) Shutdown(ctx context.Context) error {
	if a.connectorLife != nil {
		a.connectorLife.StopAll(ctx)
	}
	if a.srv != nil {
		if err := a.srv.Shutdown(ctx); err != nil {
			return err
		}
	}
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

// chooseEventSink sélectionne la sortie d'événements selon Options : adaptateur
// injecté par la bordure (mode pôle) en priorité, sinon webhook HTTP générique
// si une URL est fournie, sinon NopSink (aucune sortie). Jamais nil.
func chooseEventSink(opts Options, logger *slog.Logger) eventsink.Sink {
	switch {
	case opts.EventSink != nil:
		return opts.EventSink
	case opts.WebhookURL != "":
		logger.Info("eventsink: webhook HTTP générique activé", "url", opts.WebhookURL)
		return eventsink.HTTPSink{URL: opts.WebhookURL, Secret: opts.WebhookSecret}
	default:
		return eventsink.NopSink{}
	}
}

// chooseConventionProvider retourne le fournisseur de clauses injecté par la
// bordure, ou l'implémentation par défaut inerte (aucune clause fabriquée) si
// aucun n'est fourni. Jamais nil.
func chooseConventionProvider(opts Options) convention.ClauseProvider {
	if opts.ConventionProvider != nil {
		return opts.ConventionProvider
	}
	return convention.InertProvider{}
}

// chooseCorpusProvider retourne le fournisseur de recherche de corpus injecté par
// la bordure, ou l'implémentation par défaut inerte (aucun résultat fabriqué) si
// aucun n'est fourni. Jamais nil.
func chooseCorpusProvider(opts Options) corpus.Provider {
	if opts.CorpusProvider != nil {
		return opts.CorpusProvider
	}
	return corpus.InertProvider{}
}

// defaultSignupProfiles retourne le catalogue générique d'une communauté à
// membres, servi quand la bordure n'injecte aucun catalogue. Set, labels, champs
// extra et grade (sys-member) identiques au comportement historique.
func defaultSignupProfiles() []signupprofile.Profile {
	return []signupprofile.Profile{
		{ID: "adherent", Label: "Adhérent", Subtitle: "Rejoindre la communauté"},
		{ID: "benevole", Label: "Bénévole", Subtitle: "S'impliquer"},
		{
			ID:       "don",
			Label:    "Donateur",
			Subtitle: "Soutenir",
			Extra:    []signupprofile.ExtraField{{Name: "telephone", Label: "Téléphone", Type: "tel"}},
		},
		{
			ID:       "partenaire",
			Label:    "Partenaire",
			Subtitle: "Collaboration",
			Extra:    []signupprofile.ExtraField{{Name: "structure", Label: "Structure / organisation", Type: "text", Required: true}},
		},
	}
}

// validateProfileGrant vérifie l'invariant d'instance : le grade de gouvernance
// ne doit pas figurer parmi les grades requestables (auto-octroi interdit par design).
func validateProfileGrant(pg app.ProfileGrant) error {
	if pg.GovernanceGradeID == "" && len(pg.Requestable) == 0 {
		return nil
	}
	for _, g := range pg.Requestable {
		if g.ID == pg.GovernanceGradeID {
			return fmt.Errorf("le grade de gouvernance %q ne peut figurer parmi les grades requestables", pg.GovernanceGradeID)
		}
	}
	return nil
}

// validateProfileGrades vérifie que chaque grade RBAC référencé par le catalogue
// existe dans la table grades. Échoue (fail-loud) au premier grade absent, en le
// nommant avec le profil fautif — aucun signup ne doit pouvoir assigner un grade
// qui n'a pas été seedé.
func validateProfileGrades(db *sql.DB, catalog []signupprofile.Profile) error {
	for _, p := range catalog {
		grade := p.Grade()
		var exists int
		err := db.QueryRow(`SELECT 1 FROM grades WHERE id = ?`, grade).Scan(&exists)
		if err == sql.ErrNoRows {
			return fmt.Errorf("profil %q référence le grade RBAC %q absent de la table grades (seed manquant)", p.ID, grade)
		}
		if err != nil {
			return fmt.Errorf("vérification du grade %q (profil %q): %w", grade, p.ID, err)
		}
	}
	return nil
}
