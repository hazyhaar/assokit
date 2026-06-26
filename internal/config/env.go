package config

import "strings"

// Config regroupe la configuration technique (non-branding) de l'application.
type Config struct {
	Port                   string
	DBPath                 string
	BaseURL                string
	AdminEmail             string
	ContactEmail           string
	CookieSecret           []byte
	HelloassoDonURL        string
	HelloassoCotisationURL string
	BankIBAN               string

	// Config OAuth/social — injectée à la bordure (le core ne lit pas l'env).
	OAuthSigningKey    []byte // vide → fallback CookieSecret
	OAuthAllowInsecure bool   // autorise issuer http:// (dev)
	GoogleClientID     string // vide → login Google désactivé
	GoogleClientSecret string

	// BrandingUploadDir : répertoire d'upload des assets de branding admin.
	BrandingUploadDir string

	// Prod : mode production (injecté à la bordure). Influe sur les diagnostics
	// Setup (ex : un COOKIE_SECRET aléatoire est dégradé en prod).
	Prod bool

	// MailerConfigured : un transport mail (SMTP ou Resend) est configuré à la
	// bordure. Diagnostiqué par le menu Setup (le core ne lit pas l'env).
	MailerConfigured bool

	// ConnectorsEnabled : une MasterKey est fournie → Vault des connecteurs actif
	// (HelloAsso…). Diagnostiqué par le menu Setup.
	ConnectorsEnabled bool

	// TrustProxyHeaders : si true, les en-têtes X-Forwarded-For / X-Real-IP sont
	// honorés pour déterminer l'IP cliente (rate-limits, anti-bruteforce).
	// FALSE par défaut : sans reverse-proxy de confiance, ces en-têtes sont
	// spoofables et contourneraient tous les rate-limits → on s'en tient à
	// RemoteAddr. À activer UNIQUEMENT derrière un proxy fiable qui réécrit XFF.
	TrustProxyHeaders bool
}

// CookieSecure indique si les cookies (session, CSRF) doivent porter l'attribut
// Secure. Dérivé une fois de BaseURL : une instance servie en https:// doit
// émettre des cookies Secure, sans quoi ils transitent en clair sur un downgrade
// HTTP (rejeu de session). Source unique pour tous les sites d'appel, qui
// codaient auparavant le flag en dur de façon incohérente.
func (c Config) CookieSecure() bool {
	return strings.HasPrefix(c.BaseURL, "https://")
}
